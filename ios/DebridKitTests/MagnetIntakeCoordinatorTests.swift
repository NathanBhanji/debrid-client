import Foundation
import Testing
@testable import DebridKit

private actor IntakeTestStore: PendingInboxAccess {
    enum Point { case enqueue, claim, snapshot }
    let base: PendingInbox
    var failing = false
    private var point: Point?
    private var continuation: CheckedContinuation<Void,Never>?
    private(set) var paused = false
    init(_ base:PendingInbox) { self.base = base }
    func setFailing(_ value:Bool) { failing = value }
    func arm(_ point:Point) { self.point = point; paused = false }
    private func pause(_ point:Point) async {
        if self.point == point { self.point = nil; paused = true; await withCheckedContinuation { continuation = $0 } }
    }
    func resume() { continuation?.resume(); continuation = nil }
    func snapshot() async throws -> [PendingMagnet] { let result = try await base.snapshot(); await pause(.snapshot); return result }
    func enqueue(_ magnet:MagnetLink,source:PendingSource,target:ServerAddress?) async throws -> PendingReceipt {
        await pause(.enqueue)
        if failing { throw NSError(domain:"fixture",code:1,userInfo:[NSLocalizedDescriptionKey:"private-fixture-body"]) }
        return try await base.enqueue(magnet,source:source,target:target)
    }
    func claim(_ id:UUID) async throws -> PendingClaim { let result = try await base.claim(id); await pause(.claim); return result }
    func renew(_ id:UUID,token:UUID) async throws -> Date { try await base.renew(id,token:token) }
    func release(_ id:UUID,token:UUID) async throws { try await base.release(id,token:token) }
    func keep(_ id:UUID) async throws { try await base.keep(id) }
    func discard(_ id:UUID,token:UUID?) async throws { try await base.discard(id,token:token) }
}
@MainActor @Suite(.timeLimit(.minutes(1))) struct MagnetIntakeCoordinatorTests {
    private func directory() -> URL { FileManager.default.temporaryDirectory.appendingPathComponent("debrid-intake-"+UUID().uuidString,isDirectory:true) }
    private func uri(_ name:String = "Fixture") -> String { "magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn="+name+"&tr=https%3a%2f%2ftracker.example.test%2fa%3ftoken%3dfixture" }
    private func wait(_ store:IntakeTestStore) async throws {
        for _ in 0..<300 { if await store.paused { return }; try await Task.sleep(for:.milliseconds(10)) }
        Issue.record("Fixture did not reach suspension point")
    }
    @Test func receivedURLIsDurableAndDuplicateSurvivesCoordinatorRestart() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let inbox = try PendingInbox(directory:dir), model = MagnetIntakeCoordinator(inbox:inbox), raw = uri()
        #expect(await model.receive(try #require(URL(string:raw))))
        let id = try #require(model.items.first?.id)
        #expect(model.items.first?.magnet.rawURI == raw && model.notice == "Link saved for review")
        _ = await model.receive(try #require(URL(string:raw)))
        #expect(model.items.count == 1 && model.items.first?.id == id && model.notice == "Link is already saved")
        let restored = MagnetIntakeCoordinator(inbox:inbox); await restored.refresh()
        #expect(restored.items.first?.magnet.rawURI == raw && restored.items.first?.id == id)
    }
    @Test func authAndOtherSchemesAreIgnoredAndInvalidMagnetNeverOpensStore() async throws {
        let model = MagnetIntakeCoordinator(openInbox:{ throw PendingInboxError.sharedContainerUnavailable })
        #expect(await !model.receive(try #require(URL(string:"dev.bhanji.debrid://auth/callback?code=fixture&state=fixture"))))
        #expect(await !model.receive(try #require(URL(string:"https://example.test"))))
        #expect(model.intakeMessage == nil && model.storageMessage == nil)
        _ = await model.receive(try #require(URL(string:"magnet:?dn=NoHash")))
        #expect(model.failedCount == 0 && model.intakeMessage?.contains("v1 info hash") == true)
    }
    @Test func multipleFailedWritesStayInMemoryUntilExplicitRetryWithoutErrorBodyLeaks() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let store = IntakeTestStore(try PendingInbox(directory:dir)), model = MagnetIntakeCoordinator(inbox:store)
        await store.setFailing(true)
        _ = await model.receive(try #require(URL(string:uri("One"))))
        _ = await model.receive(try #require(URL(string:uri("Two"))))
        #expect(model.failedCount == 2 && model.notice == nil)
        #expect(model.intakeMessage?.contains("private-fixture-body") == false)
        await store.setFailing(false); await model.retryFailed()
        #expect(model.failedCount == 0 && model.items.count == 2 && model.intakeMessage == nil)
    }
    @Test func targetServerHintIsCapturedBeforeAwaitAndNeverReboundByLogin() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let store = IntakeTestStore(try PendingInbox(directory:dir)), model = MagnetIntakeCoordinator(inbox:store)
        model.setTargetServer(URL(string:"https://first.example.test/prefix")); await store.arm(.enqueue)
        let task = Task { await model.receive(try #require(URL(string:uri()))) }; try await wait(store)
        model.setTargetServer(URL(string:"https://second.example.test")); await store.resume(); _ = try await task.value
        #expect(model.items.first?.targetServerURL?.absoluteString == "https://first.example.test/prefix")
    }
    @Test func olderRefreshCannotReplaceNewlySavedSnapshot() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let store = IntakeTestStore(try PendingInbox(directory:dir)), model = MagnetIntakeCoordinator(inbox:store)
        await store.arm(.snapshot); let old = Task { await model.refresh() }; try await wait(store)
        _ = await model.receive(try #require(URL(string:uri())))
        await store.resume(); await old.value
        #expect(model.items.count == 1 && !model.isLoading)
    }
    @Test func lateCancelledClaimIsReleasedWithoutLeavingLoadingState() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let base = try PendingInbox(directory:dir), store = IntakeTestStore(base), model = MagnetIntakeCoordinator(inbox:store)
        let id = try await base.enqueue(MagnetLink(uri()),source:.url).id
        await store.arm(.claim); let owner = UUID()
        let task = Task { await model.beginReview(id,owner:owner) }; try await wait(store)
        task.cancel(); await store.resume(); await task.value
        #expect(model.activeClaim == nil && !model.isReviewLoading)
        #expect(try await base.snapshot().first?.state == .pending)
    }
    @Test func preCancelledAndObsoleteOwnersCannotCancelOrDiscardCurrentReview() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let base = try PendingInbox(directory:dir), model = MagnetIntakeCoordinator(inbox:base), owner = UUID()
        let id = try await base.enqueue(MagnetLink(uri()),source:.url).id
        await model.beginReview(id,owner:owner)
        let token = try #require(model.activeClaim?.token), obsolete = UUID()
        let task = Task { await model.beginReview(id,owner:obsolete) }; task.cancel(); await task.value
        #expect(model.activeClaim?.token == token)
        #expect(model.endReview(owner:obsolete) == nil)
        #expect(await !model.discardReview(owner:obsolete))
        #expect(await model.discardReview(owner:owner))
        #expect(try await base.snapshot().isEmpty)
    }
    @Test func explicitKeepUnblocksExpiredReviewAndEndingReleasesLease() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let early = try PendingInbox(directory:dir,clock:{ Date(timeIntervalSince1970:0) })
        let id = try await early.enqueue(MagnetLink(uri()),source:.url).id
        let late = try PendingInbox(directory:dir,clock:{ Date(timeIntervalSince1970:PendingInbox.retentionLifetime+1) })
        let model = MagnetIntakeCoordinator(inbox:late), owner = UUID()
        await model.beginReview(id,owner:owner)
        #expect(model.reviewNeedsConfirmation && model.activeClaim == nil)
        #expect(await model.confirmKeep(id)); await model.beginReview(id,owner:owner)
        #expect(model.activeClaim?.item.id == id)
        await model.endReview(owner:owner)?.value
        #expect(try await late.snapshot().first?.state == .pending)
    }
    @Test func expiredDraftsCanBeDiscardedWithoutExtendingRetentionToRecoverQuota() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let early = try PendingInbox(directory:dir,clock:{ Date(timeIntervalSince1970:0) })
        for index in 0..<PendingInbox.itemLimit { _ = try await early.enqueue(MagnetLink(uri("Fixture\(index)")),source:.url) }
        let late = try PendingInbox(directory:dir,clock:{ Date(timeIntervalSince1970:PendingInbox.retentionLifetime+1) })
        let model = MagnetIntakeCoordinator(inbox:late); await model.refresh()
        #expect(model.items.allSatisfy { $0.state == .expired })
        _ = await model.receive(try #require(URL(string:uri("Incoming"))))
        #expect(model.failedCount == 1)
        #expect(await model.discardSaved(try #require(model.items.first?.id)))
        await model.retryFailed()
        #expect(model.failedCount == 0 && model.items.count == PendingInbox.itemLimit)
        #expect(model.items.filter { $0.state == .expired }.count == PendingInbox.itemLimit-1)
    }

    @Test func reopeningAfterFailureClearsRetryAndDiscardClearsReceipt() async throws {
        let dir = directory(); defer { try? FileManager.default.removeItem(at:dir) }
        let store = IntakeTestStore(try PendingInbox(directory:dir)), model = MagnetIntakeCoordinator(inbox:store)
        let url = try #require(URL(string:uri()))
        await store.setFailing(true); _ = await model.receive(url)
        #expect(model.failedCount == 1)
        await store.setFailing(false); _ = await model.receive(url)
        #expect(model.failedCount == 0 && model.intakeMessage == nil && model.notice != nil)
        #expect(await model.discardSaved(try #require(model.items.first?.id)))
        #expect(model.notice == nil && model.items.isEmpty)
    }

}
