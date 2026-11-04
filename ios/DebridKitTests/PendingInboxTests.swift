import Darwin
import Foundation
import Testing
@testable import DebridKit

private final class InboxClock: @unchecked Sendable {
    private let lock = NSLock()
    private var date = Date(timeIntervalSince1970:100_000)
    func now() -> Date { lock.withLock { date } }
    func advance(_ seconds:TimeInterval) { lock.withLock { date.addTimeInterval(seconds) } }
}
@Suite(.timeLimit(.minutes(1))) struct PendingInboxTests {
    private func directory() -> URL { FileManager.default.temporaryDirectory.appendingPathComponent("debrid-inbox-test-"+UUID().uuidString,isDirectory:true) }
    private func link(_ suffix:String = "") throws -> MagnetLink { try MagnetLink("magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+suffix) }
    @Test func roundtripRestartExactDedupeAndDistinctOptionsOrTargets() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let first = try PendingInbox(directory:dir), second = try PendingInbox(directory:dir)
        let raw = try link("&dn=My+Show&tr=https%3a%2f%2ftracker.example.test%2fa%3ftoken%3dfixture&tr=udp://tracker.example.test")
        let receipt = try await first.enqueue(raw,source:.url)
        let duplicate = try await second.enqueue(raw,source:.share)
        #expect(receipt.id == duplicate.id && duplicate.wasDuplicate)
        _ = try await first.enqueue(try link("&dn=Different"),source:.paste)
        _ = try await first.enqueue(raw,source:.url,target:ServerAddress("https://other.example.test/prefix"))
        let saved = try await second.snapshot()
        #expect(saved.count == 3 && saved[0].magnet.rawURI == raw.rawURI && saved[0].source == .url)
        #expect(saved[2].targetServerURL?.absoluteString == "https://other.example.test/prefix")
    }
    @Test func competingInstancesSaveAndClaimExactlyOnce() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let a = try PendingInbox(directory:dir), b = try PendingInbox(directory:dir), magnet = try link()
        async let first = a.enqueue(magnet,source:.share)
        async let second = b.enqueue(magnet,source:.url)
        let receipts = try await [first,second]
        #expect(receipts[0].id == receipts[1].id && receipts.filter { $0.wasDuplicate }.count == 1)
        let results = await withTaskGroup(of:Result<PendingClaim,PendingInboxError>.self) { group in
            for store in [a,b] { group.addTask {
                do { return .success(try await store.claim(receipts[0].id)) }
                catch let error as PendingInboxError { return .failure(error) }
                catch { return .failure(.unavailable) }
            } }
            var results: [Result<PendingClaim,PendingInboxError>] = []
            for await result in group { results.append(result) }; return results
        }
        #expect(results.filter { if case .success = $0 { true } else { false } }.count == 1)
        #expect(results.filter { if case .failure(.busy) = $0 { true } else { false } }.count == 1)
    }
    @Test func leasesRenewExpireAndRejectObsoleteOwners() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let clock = InboxClock(), store = try PendingInbox(directory:dir,clock:{ clock.now() })
        let id = try await store.enqueue(link(),source:.paste).id
        let first = try await store.claim(id)
        clock.advance(60); let renewed = try await store.renew(id,token:first.token)
        #expect(renewed > first.expiresAt)
        clock.advance(PendingInbox.reviewLifetime+1)
        #expect(try await store.snapshot().first?.state == .pending)
        let replacement = try await store.claim(id)
        await #expect(throws:PendingInboxError.lostClaim) { try await store.release(id,token:first.token) }
        await #expect(throws:PendingInboxError.lostClaim) { _ = try await store.renew(id,token:first.token) }
        await #expect(throws:PendingInboxError.lostClaim) { try await store.discard(id,token:first.token) }
        await #expect(throws:PendingInboxError.busy) { try await store.discard(id) }
        try await store.discard(id,token:replacement.token)
        #expect(try await store.snapshot().isEmpty)
    }
    @Test func expirationNeedsExplicitKeepAndNeverSilentlyEvicts() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let clock = InboxClock(), store = try PendingInbox(directory:dir,clock:{ clock.now() }), magnet = try link()
        let id = try await store.enqueue(magnet,source:.share).id
        clock.advance(PendingInbox.retentionLifetime)
        #expect(try await store.snapshot().first?.state == .expired)
        await #expect(throws:PendingInboxError.expired) { _ = try await store.claim(id) }
        #expect(try await store.enqueue(magnet,source:.url).wasDuplicate)
        #expect(try await store.snapshot().first?.state == .expired)
        try await store.keep(id); let claim = try await store.claim(id)
        #expect(claim.item.magnet.rawURI == magnet.rawURI); try await store.release(id,token:claim.token)
    }
    @Test func countAndByteQuotasPreserveExistingDrafts() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let store = try PendingInbox(directory:dir)
        for index in 0..<PendingInbox.itemLimit { _ = try await store.enqueue(link("&dn=Fixture\(index)"),source:.url) }
        await #expect(throws:PendingInboxError.full) { _ = try await store.enqueue(link("&dn=Overflow"),source:.url) }
        #expect(try await store.snapshot().count == PendingInbox.itemLimit)
        #expect(try await store.enqueue(link("&dn=Fixture0"),source:.share).wasDuplicate)
        let bytesDir = directory(); defer { try? FileManager.default.removeItem(at:bytesDir) }
        let byteStore = try PendingInbox(directory:bytesDir)
        for index in 0..<(PendingInbox.uriByteLimit/MagnetLink.byteLimit) {
            let prefix = try link("&dn=\(index)&x=").rawURI
            _ = try await byteStore.enqueue(MagnetLink(prefix+String(repeating:"x",count:MagnetLink.byteLimit-prefix.utf8.count)),source:.paste)
        }
        await #expect(throws:PendingInboxError.full) { _ = try await byteStore.enqueue(link("&dn=Overflow"),source:.paste) }
        #expect(try await byteStore.snapshot().count == PendingInbox.uriByteLimit/MagnetLink.byteLimit)
    }
    @Test func corruptFutureAndOversizedStateFailClosed() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let store = try PendingInbox(directory:dir), path = dir.appendingPathComponent("inbox.json")
        _ = try await store.enqueue(link(),source:.paste)
        let original = try Data(contentsOf:path)
        var future = try #require(JSONSerialization.jsonObject(with:original) as? [String:Any]); future["version"] = 99
        let futureData = try JSONSerialization.data(withJSONObject:future); try futureData.write(to:path)
        let temp = dir.appendingPathComponent(".pending-write-"+UUID().uuidString+".tmp"); try Data("preserve".utf8).write(to:temp)
        await #expect(throws:PendingInboxError.unsupportedVersion) { _ = try await store.enqueue(link("&dn=new"),source:.url) }
        #expect(try Data(contentsOf:path) == futureData && FileManager.default.fileExists(atPath:temp.path))
        let bad = Data("broken".utf8); try bad.write(to:path)
        await #expect(throws:PendingInboxError.corrupt) { _ = try await store.snapshot() }
        #expect(try Data(contentsOf:path) == bad)
        try Data(repeating:0,count:PendingInbox.stateByteLimit+1).write(to:path)
        await #expect(throws:PendingInboxError.corrupt) { _ = try await store.snapshot() }
    }
    @Test func orphanCleanupIsOwnedAndSymlinkOrFIFOStateCannotEscape() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let store = try PendingInbox(directory:dir); _ = try await store.enqueue(link(),source:.url)
        let temp = dir.appendingPathComponent(".pending-write-"+UUID().uuidString+".tmp"), unrelated = dir.appendingPathComponent("unrelated.tmp")
        try Data("partial".utf8).write(to:temp); try Data("keep".utf8).write(to:unrelated)
        _ = try await store.snapshot()
        #expect(!FileManager.default.fileExists(atPath:temp.path) && FileManager.default.fileExists(atPath:unrelated.path))
        let path = dir.appendingPathComponent("inbox.json"); try FileManager.default.removeItem(at:path)
        try FileManager.default.createSymbolicLink(at:path,withDestinationURL:unrelated)
        await #expect(throws:PendingInboxError.unavailable) { _ = try await store.snapshot() }
        #expect(try Data(contentsOf:unrelated) == Data("keep".utf8))
        try FileManager.default.removeItem(at:path)
        #expect(mkfifo(path.path,mode_t(0o600)) == 0)
        await #expect(throws:PendingInboxError.corrupt) { _ = try await store.snapshot() }
    }
    @Test func actualEntitledGroupAndCompleteProtection() async throws {
        let root = try PendingInbox.groupRoot(), dir = root.appendingPathComponent("InboxTests-"+UUID().uuidString,isDirectory:true)
        defer { try? FileManager.default.removeItem(at:dir) }
        _ = try PendingInbox.shared() // Resolve factory without touching another namespace’s drafts.
        let store = try PendingInbox(directory:dir); _ = try await store.enqueue(link(),source:.share)
        for path in [dir,dir.appendingPathComponent("inbox.json"),dir.appendingPathComponent(".inbox.lock")] {
            let value = try FileManager.default.attributesOfItem(atPath:path.path)[.protectionKey]
            let protection = (value as? FileProtectionType)?.rawValue ?? value as? String
            #if targetEnvironment(simulator)
            if value != nil { #expect(protection == FileProtectionType.complete.rawValue) }
            #else
            #expect(try #require(protection) == FileProtectionType.complete.rawValue)
            #endif
        }
        #expect(try dir.resourceValues(forKeys:[.isExcludedFromBackupKey]).isExcludedFromBackup == true)
    }
    @MainActor @Test func cancellingLockWaitReleasesResourcesWithoutChangingData() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let store = try PendingInbox(directory:dir); _ = try await store.enqueue(link(),source:.paste)
        let original = try Data(contentsOf:dir.appendingPathComponent("inbox.json"))
        let fd = Darwin.open(dir.appendingPathComponent(".inbox.lock").path,O_RDWR|O_EXLOCK|O_NONBLOCK|O_CLOEXEC)
        #expect(fd >= 0); guard fd >= 0 else { return }; defer { Darwin.close(fd) }
        let waiter = Task { try await store.snapshot() }
        try await Task.sleep(for:.milliseconds(100)); waiter.cancel()
        do { _ = try await waiter.value; Issue.record("Cancelled waiter completed") } catch { #expect(error is CancellationError) }
        #expect(try Data(contentsOf:dir.appendingPathComponent("inbox.json")) == original)
    }
    @MainActor @Test func cancelledBeforeStartCannotCreateStorageAndDescriptionsRedact() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let store = try PendingInbox(directory:dir)
        let task = Task { try await store.snapshot() }; task.cancel()
        do { _ = try await task.value; Issue.record("Cancelled operation completed") } catch { #expect(error is CancellationError) }
        #expect(!FileManager.default.fileExists(atPath:dir.path))
        let id = try await store.enqueue(link("&dn=private-fixture-passkey"),source:.paste).id, claim = try await store.claim(id)
        var reflected = ""; dump(claim,to:&reflected)
        #expect(!reflected.contains("private-fixture-passkey"))
        #expect(!String(describing:claim.item).contains("private-fixture-passkey"))
    }
}
