import Foundation
import Testing
@testable import DebridKit

private final class SearchFixtureBundle: NSObject {}
private enum SearchFixtures {
    static func account(id: String = "fixture-account", name: String = "Main", kind: String = "torbox") throws -> AccountDTO {
        let url = try #require(Bundle(for:SearchFixtureBundle.self).url(forResource:"contract",withExtension:"json",subdirectory:"ios-api"))
        let root = try #require(JSONSerialization.jsonObject(with:Data(contentsOf:url)) as? [String:Any])
        var account = try #require(root["account"] as? [String:Any])
        account["id"] = id; account["name"] = name; account["kind"] = kind
        return try DebridAPIClient.decoder().decode(AccountDTO.self,from:JSONSerialization.data(withJSONObject:account))
    }
}
struct TransferSearchIndexTests {
    @Test func combinedUnicodeNameCategoryAccountAndScopeFilters() throws {
        let a = TransferSummary(try TransferFixtures.torrent(overrides:["id":"a","name":"Café Ocean","category":"Documentaries"]))
        let b = TransferSummary(try TransferFixtures.torrent(status:"completed",overrides:["id":"b","name":"Ocean","category":"Audio","account_id":"archive"]))
        let index = TransferSearchIndex(items:[a,b],accounts:[try SearchFixtures.account()],locale:Locale(identifier:"en_US"))
        var search = TransferSearch(); search.query = "CAFE TORBOX"; search.scope = .active; search.category = .named("Documentaries"); search.accountID = "fixture-account"
        #expect(index.results(for:search).map(\.id) == ["a"])
        search.accountID = "archive"; #expect(index.results(for:search).isEmpty)
        search = TransferSearch(); search.scope = .completed; #expect(index.results(for:search).map(\.id) == ["b"])
    }
    @Test func uncategorizedAndNamedUncategorizedAreDifferentAndUnknownNeedsAttention() throws {
        let a = TransferSummary(try TransferFixtures.torrent(status:"future_state",overrides:["id":"a","category":NSNull()]))
        let b = TransferSummary(try TransferFixtures.torrent(overrides:["id":"b","category":"Uncategorized"]))
        let index = TransferSearchIndex(items:[a,b],accounts:[])
        var search = TransferSearch(); search.category = .uncategorized; search.scope = .attention
        #expect(index.results(for:search).map(\.id) == ["a"])
        search.scope = .all; search.category = .named("Uncategorized")
        #expect(index.results(for:search).map(\.id) == ["b"])
        search.query = "  \n  "; #expect(index.results(for:search).count == 1)
        let empty = TransferSummary(try TransferFixtures.torrent(overrides:["category":""]))
        var uncategorized = TransferSearch(); uncategorized.query = "Uncategorized"
        #expect(TransferSearchIndex(items:[empty],accounts:[]).results(for:uncategorized).count == 1)
    }
    @Test func providerSpellingHashAndLargeSnapshotHaveStableResults() throws {
        let account = try SearchFixtures.account(kind:"realdebrid")
        let values = try (0..<2000).map { index in TransferSummary(try TransferFixtures.torrent(overrides:["id":String(index),"name":index % 2 == 0 ? "Open Ocean" : "Field Notes"])) }
        let index = TransferSearchIndex(items:values,accounts:[account])
        var search = TransferSearch(); search.query = "ocean real debrid"
        #expect(index.results(for:search).count == 1000)
        search.query = String(repeating:"0",count:40)
        #expect(index.results(for:search).count == 2000)
    }
}

private actor MemorySearchPreferences: TransferPreferenceStoring {
    var saved: [String:TransferSearch] = [:]
    var hold = false
    var pending: CheckedContinuation<TransferSearch?,Never>?
    let reads = AsyncStream<Void>.makeStream()
    let writes = AsyncStream<TransferSearch>.makeStream()
    func load(for profile: ServerProfile) async throws -> TransferSearch? {
        reads.continuation.yield(())
        if hold { return await withCheckedContinuation { pending = $0 } }
        return saved[profile.id]
    }
    func save(_ search: TransferSearch,for profile:ServerProfile) { saved[profile.id] = search; writes.continuation.yield(search) }
    func setHold() { hold = true }
    func release(_ value: TransferSearch?) { pending?.resume(returning:value); pending = nil }
    func seed(_ search: TransferSearch,profile:ServerProfile) { saved[profile.id] = search }
}
private struct SearchListingFixture: TransferListing {
    func torrents() async throws -> [TorrentDTO] { [try TransferFixtures.torrent()] }
}
@MainActor @Suite(.timeLimit(.minutes(1))) struct TransferSearchModelTests {
    private func profile() throws -> ServerProfile { try ServerProfile(server:ServerAddress("https://fixture.example.test")) }
    @Test func restoringSearchDoesNotOverwriteTypingWhileStorageWaits() async throws {
        let preferences = MemorySearchPreferences(), profile = try profile()
        await preferences.setHold()
        let model = TransferListModel(profile:profile,api:SearchListingFixture(),preferences:preferences)
        var reads = await preferences.reads.stream.makeAsyncIterator()
        let task = Task { await model.restoreSearchIfNeeded() }; _ = await reads.next()
        model.setQuery("typed query")
        var saved = TransferSearch(); saved.query = "old query"
        await preferences.release(saved); await task.value
        #expect(model.search.query == "typed query")
    }
    @Test func restoresAndFiltersNewSnapshotsThenFlushesScopedPreferences() async throws {
        let preferences = MemorySearchPreferences(), profile = try profile()
        var search = TransferSearch(); search.query = "ocean"; search.category = .named("Documentaries")
        await preferences.seed(search,profile:profile)
        let model = TransferListModel(profile:profile,api:SearchListingFixture(),preferences:preferences)
        await model.restoreSearchIfNeeded(); await model.reload()
        #expect(model.search == search && model.visibleItems.count == 1)
        model.search.scope = .completed; #expect(model.visibleItems.isEmpty)
        var writes = await preferences.writes.stream.makeAsyncIterator()
        model.flushSearchPreferences()
        #expect(await writes.next()?.scope == .completed)
        model.setQuery(String(repeating:"a",count:10000)); #expect(model.search.query.unicodeScalars.count == 256)
    }
}

struct ProtectedSearchPreferenceTests {
    private func profile(_ name: String) throws -> ServerProfile { try ServerProfile(server:ServerAddress("https://"+name+".example.test")) }
    @Test func atomicProtectedRoundtripAndCrossServerEnvelopeBinding() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("debrid-search-"+UUID().uuidString)
        defer { try? FileManager.default.removeItem(at:directory) }
        let store = ProtectedTransferPreferences(directory:directory), a = try profile("a"), b = try profile("b")
        var search = TransferSearch(); search.query = "Private filename"; search.category = .uncategorized
        try await store.save(search,for:a)
        #expect(try await store.load(for:a) == search)
        #expect(try await store.load(for:b) == nil)
        let file = directory.appendingPathComponent(a.id+".json")
        let attributes = try FileManager.default.attributesOfItem(atPath:file.path)
        let protection = attributes[.protectionKey]
        let protectionName = (protection as? FileProtectionType)?.rawValue ?? (protection as? String)
        #if targetEnvironment(simulator)
        // Simulator may omit this attribute entirely; it cannot certify device data protection.
        if protection != nil { #expect(protectionName == FileProtectionType.complete.rawValue) }
        #else
        #expect(try #require(protectionName) == FileProtectionType.complete.rawValue)
        #endif
        try FileManager.default.copyItem(at:file,to:directory.appendingPathComponent(b.id+".json"))
        await #expect(throws:SearchPreferenceError.corrupt) { try await store.load(for:b) }
    }
    @Test func corruptOversizeAndInvalidWritesCannotReplaceExistingSearch() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("debrid-search-"+UUID().uuidString)
        defer { try? FileManager.default.removeItem(at:directory) }
        let store = ProtectedTransferPreferences(directory:directory), a = try profile("a")
        var search = TransferSearch(); search.query = "saved"
        try await store.save(search,for:a)
        var invalid = search; invalid.query = String(repeating:"x",count:257)
        await #expect(throws:SearchPreferenceError.invalid) { try await store.save(invalid,for:a) }
        #expect(try await store.load(for:a) == search)
        let file = directory.appendingPathComponent(a.id+".json")
        try Data(repeating:0,count:9000).write(to:file)
        await #expect(throws:SearchPreferenceError.corrupt) { try await store.load(for:a) }
        try Data("broken".utf8).write(to:file)
        await #expect(throws:SearchPreferenceError.corrupt) { try await store.load(for:a) }
    }
    @Test func profileRetentionIsBoundedWithoutTouchingUnrelatedFiles() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("debrid-search-"+UUID().uuidString)
        defer { try? FileManager.default.removeItem(at:directory) }
        let store = ProtectedTransferPreferences(directory:directory)
        for i in 0..<17 { try await store.save(TransferSearch(),for:profile("p"+String(i))) }
        let unrelated = directory.appendingPathComponent("keep.txt"); try Data("owned fixture".utf8).write(to:unrelated)
        try await store.save(TransferSearch(),for:profile("latest"))
        let files = try FileManager.default.contentsOfDirectory(at:directory,includingPropertiesForKeys:nil)
        #expect(files.filter { $0.pathExtension == "json" }.count == 16)
        #expect(FileManager.default.fileExists(atPath:unrelated.path))
    }
}
