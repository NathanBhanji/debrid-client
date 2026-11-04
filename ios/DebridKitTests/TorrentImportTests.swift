import Foundation
import Testing
@testable import DebridKit

private final class TorrentFixtureBundle {}
struct TorrentFixture: Decodable, Sendable {
    let name:String, data:Data, native_accepted:Bool, server_accepted:Bool
    let hash:String?, display_name:String?, size:Int64, files:Int
}
func torrentFixtures() throws -> [TorrentFixture] {
    let url = try #require(Bundle(for:TorrentFixtureBundle.self).url(forResource:"torrents",withExtension:"json",subdirectory:"ios-api"))
    return try JSONDecoder().decode([TorrentFixture].self,from:Data(contentsOf:url))
}
func fixtureTorrent() throws -> TorrentFile { try TorrentFile(#require(torrentFixtures().first?.data)) }
@Suite(.timeLimit(.minutes(1))) struct TorrentImportTests {
    private func directory() -> URL { FileManager.default.temporaryDirectory.appendingPathComponent("debrid-torrent-"+UUID().uuidString) }
    @Test func actualBackendCorpusAndExactBytes() async throws {
        for fixture in try torrentFixtures() {
            if fixture.native_accepted {
                let file = try await TorrentFile.parse(fixture.data)
                #expect(fixture.server_accepted,Comment(rawValue:fixture.name))
                #expect(file.data == fixture.data && file.summary.infoHash == fixture.hash,Comment(rawValue:fixture.name))
                #expect(file.summary.totalBytes == fixture.size && file.summary.fileCount == fixture.files,Comment(rawValue:fixture.name))
                #expect(file.summary.displayName == fixture.display_name,Comment(rawValue:fixture.name))
                #expect(!String(reflecting:file).contains("Fixture Ocean"))
            } else { #expect(throws:TorrentFileError.self,Comment(rawValue:fixture.name)) { try TorrentFile(fixture.data) } }
        }
    }
    @Test func malformedBoundsAndCancelledParse() async throws {
        #expect(throws:TorrentFileError.tooLarge) { try TorrentFile(Data(count:TorrentFile.byteLimit+1)) }
        for raw in ["", "d", "de", String(repeating:"l",count:34)+String(repeating:"e",count:34), "d4:infoi9223372036854775808ee", "d4:infod1:zi0e1:ai0eee"] {
            #expect(throws:TorrentFileError.self) { try TorrentFile(Data(raw.utf8)) }
        }
        let task = Task { try await TorrentFile.parse(fixtureTorrent().data) }; task.cancel()
        await #expect(throws:CancellationError.self) { try await task.value }
    }
    @Test func securityScopedLoaderCopiesOriginalAndRefusesSymlinksDirectoriesAndOversize() async throws {
        let root = directory(); try FileManager.default.createDirectory(at:root,withIntermediateDirectories:true); defer { try? FileManager.default.removeItem(at:root) }
        let file = try fixtureTorrent(), url = root.appendingPathComponent("Fixture.torrent")
        try file.data.write(to:url)
        #expect(try await TorrentDocument.load(url) == file)
        #expect(try Data(contentsOf:url) == file.data)
        let symlink = root.appendingPathComponent("alias.torrent"); try FileManager.default.createSymbolicLink(at:symlink,withDestinationURL:url)
        await #expect(throws:TorrentFileError.self) { try await TorrentDocument.load(symlink) }
        await #expect(throws:TorrentFileError.self) { try await TorrentDocument.load(root) }
        try Data(count:TorrentFile.byteLimit+1).write(to:url)
        await #expect(throws:TorrentFileError.tooLarge) { try await TorrentDocument.load(url) }
    }
    @Test func protectedBlobIsIndependentOfOriginalAndLeaseCheckedAndDeduplicated() async throws {
        let root = directory(); defer { try? FileManager.default.removeItem(at:root) }
        let store = try PendingInbox(directory:root), file = try fixtureTorrent()
        let first = try await store.enqueueTorrent(file,source:.file)
        let duplicate = try await store.enqueueTorrent(file,source:.file)
        #expect(first.id == duplicate.id && duplicate.wasDuplicate)
        let other = try await store.enqueueTorrent(file,source:.file,target:ServerAddress("https://other.example.test"))
        #expect(other.id != first.id)
        let snapshot = try await store.snapshot()
        #expect(snapshot.count == 2 && snapshot.first?.torrent == file.summary && snapshot.first?.magnet == nil)
        let manifest = try Data(contentsOf:root.appendingPathComponent("inbox.json"))
        #expect(!String(decoding:manifest,as:UTF8.self).contains("token=fixture"))
        let claim = try await store.claim(first.id)
        #expect(try await store.loadTorrent(first.id,token:claim.token) == file)
        await #expect(throws:PendingInboxError.lostClaim) { try await store.loadTorrent(first.id,token:UUID()) }
        let payload = root.appendingPathComponent(first.id.uuidString+".torrent-payload")
        let attrs = try FileManager.default.attributesOfItem(atPath:payload.path)
        #expect((attrs[.posixPermissions] as? NSNumber)?.intValue == 0o600)
        let protection = (attrs[.protectionKey] as? FileProtectionType)?.rawValue ?? attrs[.protectionKey] as? String
        #if targetEnvironment(simulator)
        if let protection { #expect(protection == FileProtectionType.complete.rawValue) }
        #else
        #expect(try #require(protection) == FileProtectionType.complete.rawValue)
        #endif
        try await store.discard(first.id,token:claim.token)
        #expect(!FileManager.default.fileExists(atPath:payload.path))
    }
    @Test func payloadQuotaDoesNotEvictAndDiscardRecoversSpace() async throws {
        let root = directory(); defer { try? FileManager.default.removeItem(at:root) }
        let store = try PendingInbox(directory:root), base = try fixtureTorrent().data
        let padding = Data(repeating:120,count:TorrentFile.byteLimit-4096)
        var data = Data(base.dropLast()); data.append(Data("2:zz\(padding.count):".utf8)); data.append(padding); data.append(101)
        let file = try TorrentFile(data)
        var ids: [UUID] = []
        for index in 0..<4 { ids.append(try await store.enqueueTorrent(file,source:.file,target:ServerAddress("https://server\(index).example.test")).id) }
        await #expect(throws:PendingInboxError.full) { try await store.enqueueTorrent(file,source:.file) }
        #expect(try await store.snapshot().count == 4)
        try await store.discard(try #require(ids.first)); _ = try await store.enqueueTorrent(file,source:.file)
        #expect(try await store.snapshot().count == 4)
    }
    @Test func digestMismatchPreservesPayloadAndUnknownVersionPreservesOrphans() async throws {
        let root = directory(); defer { try? FileManager.default.removeItem(at:root) }
        let store = try PendingInbox(directory:root), file = try fixtureTorrent(), id = try await store.enqueueTorrent(file,source:.file).id
        let claim = try await store.claim(id), payload = root.appendingPathComponent(id.uuidString+".torrent-payload")
        var changed = file.data; changed[changed.count-5] ^= 1; try changed.write(to:payload)
        await #expect(throws:PendingInboxError.corrupt) { try await store.loadTorrent(id,token:claim.token) }
        await #expect(throws:PendingInboxError.corrupt) { try await store.enqueueTorrent(file,source:.file) }
        #expect(try Data(contentsOf:payload) == changed)
        let manifestURL = root.appendingPathComponent("inbox.json")
        var manifest = try #require(JSONSerialization.jsonObject(with:Data(contentsOf:manifestURL)) as? [String:Any]); manifest["version"] = 99
        try JSONSerialization.data(withJSONObject:manifest).write(to:manifestURL)
        let orphan = root.appendingPathComponent(UUID().uuidString+".torrent-payload"); try Data([1]).write(to:orphan)
        await #expect(throws:PendingInboxError.unsupportedVersion) { try await store.snapshot() }
        #expect(FileManager.default.fileExists(atPath:orphan.path))
    }
    @Test func v1MagnetMigrationRetainsExactInputAndClaimAndCleansOnlyOwnedOrphans() async throws {
        let root = directory(); defer { try? FileManager.default.removeItem(at:root) }
        let store = try PendingInbox(directory:root), raw = "magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&tr=https%3a%2f%2ffixture.test%2fa%3ftoken%3dprivate"
        let id = try await store.enqueue(MagnetLink(raw),source:.url).id, claim = try await store.claim(id)
        let manifestURL = root.appendingPathComponent("inbox.json")
        var manifest = try #require(JSONSerialization.jsonObject(with:Data(contentsOf:manifestURL)) as? [String:Any]); manifest["version"] = 1
        try JSONSerialization.data(withJSONObject:manifest).write(to:manifestURL)
        let orphan = root.appendingPathComponent(UUID().uuidString+".torrent-payload"), unrelated = root.appendingPathComponent("unrelated.torrent-payload")
        try Data([1]).write(to:orphan); try Data([2]).write(to:unrelated)
        #expect(try await store.snapshot().first?.magnet?.rawURI == raw)
        _ = try await store.renew(id,token:claim.token)
        let migrated = try #require(JSONSerialization.jsonObject(with:Data(contentsOf:manifestURL)) as? [String:Any])
        #expect(migrated["version"] as? Int == 5)
        #expect(!FileManager.default.fileExists(atPath:orphan.path) && FileManager.default.fileExists(atPath:unrelated.path))
    }
    @Test func explicitPasteValidatesWholeBatchAndPreservesRawEncoding() async throws {
        let raw = "magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=Fixture%2bOcean&tr=https%3a%2f%2ffixture.test%2fa"
        let links = try await MagnetPaste.parse("  "+raw+"\r\n"+raw+"\n")
        #expect(links.count == 2 && links.allSatisfy { $0.rawURI == raw })
        await #expect(throws:MagnetError.self) { try await MagnetPaste.parse(raw+"\nnot a magnet") }
        await #expect(throws:MagnetPasteError.tooMany) { try await MagnetPaste.parse(Array(repeating:raw,count:17).joined(separator:"\n")) }
    }
    @MainActor @Test func failedPasteDoesNotPartiallySaveAndDuplicateBatchDoesNotMultiplyRows() async throws {
        let root = directory(); defer { try? FileManager.default.removeItem(at:root) }
        let store = try PendingInbox(directory:root), model = MagnetIntakeCoordinator(inbox:store)
        let raw = "magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=Fixture"
        #expect(await !model.paste(raw+"\nbad")); #expect(try await store.snapshot().isEmpty)
        #expect(await model.paste(raw+"\n"+raw)); #expect(model.items.count == 1 && model.items.first?.source == .paste)
    }
    @MainActor @Test func cancelledImportReleasesBusyStateAndSubsequentImportCanComplete() async throws {
        let root = directory(); defer { try? FileManager.default.removeItem(at:root) }
        let store = try PendingInbox(directory:root), file = try fixtureTorrent(), gate = ImportLoaderGate(file)
        let model = MagnetIntakeCoordinator(openInbox:{ store },loadDocument:{ try await gate.load($0) })
        let task = Task { await model.importDocument(root.appendingPathComponent("first.torrent")) }
        for _ in 0..<300 { if await gate.started { break }; try await Task.sleep(for:.milliseconds(10)) }
        #expect(model.isImporting); task.cancel(); await task.value
        #expect(!model.isImporting && model.items.isEmpty && model.intakeMessage == nil)
        await model.importDocument(root.appendingPathComponent("second.torrent"))
        #expect(!model.isImporting && model.items.first?.torrent == file.summary)
    }

}

private actor ImportLoaderGate {
    let file:TorrentFile
    private(set) var started = false
    init(_ file:TorrentFile) { self.file = file }
    func load(_ url:URL) async throws -> TorrentFile {
        if !started { started = true; try await Task.sleep(for:.seconds(120)) }
        return file
    }
}
