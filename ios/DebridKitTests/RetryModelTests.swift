import Foundation
import Testing
@testable import DebridKit

@MainActor private final class RetryConnectionFlag {var current=true}

private actor RetryTestAPI:RetryAPI {
    enum Behavior {case success,timeoutAccepted,offline,rejected,mismatch,unauthorized,wrongParent}
    nonisolated let serverURL:URL
    let store:PendingInbox
    var behavior:Behavior = .success
    var supports=true,hold=false,healthHold=false
    var reviewParent="parent"
    private(set) var keys:[UUID]=[]
    private(set) var revisions:[String]=[]
    private(set) var receiptChecks=0
    private var accepted:[UUID:RetryAcceptanceDTO]=[:]
    init(serverURL:URL,store:PendingInbox) {self.serverURL=serverURL;self.store=store}
    func configure(_ value:Behavior,hold:Bool = false) {behavior=value;self.hold=hold}
    func setHealthHold(_ value:Bool) {healthHold=value}
    func setSupport(_ value:Bool) {supports=value}
    func setParent(_ value:String) {reviewParent=value}
    func release() {hold=false}
    func health() async throws -> HealthDTO {while healthHold {try await Task.sleep(for:.milliseconds(10))};return HealthDTO(ok:true,version:"fixture",capabilities:supports ? ["native_retry_v1"] : [])}
    func retryReview(kind:RetryResourceKind,id:String) async throws -> RetryReviewDTO {
        RetryReviewDTO(kind:kind.rawValue,resourceId:id,torrentId:reviewParent,revision:String(repeating:"a",count:64),allowed:true,waitForSelection:true)
    }
    func requestRetry(kind:RetryResourceKind,id:String,key:UUID,revision:String) async throws -> RetryAcceptanceDTO {
        keys.append(key);revisions.append(revision)
        #expect(try await store.savedRetries().contains {$0.resourceID==id && $0.torrentID=="parent"})
        while hold {try await Task.sleep(for:.milliseconds(10))}
        switch behavior {
        case .offline:throw APIError.connection(.notConnectedToInternet)
        case .rejected:throw APIError.http(status:409)
        case .mismatch:throw SubmissionFailure.keyMismatch
        case .unauthorized:throw APIError.unauthorized
        case .success,.timeoutAccepted,.wrongParent:break
        }
        let receipt=RetryAcceptanceDTO(kind:kind.rawValue,resourceId:id,torrentId:behavior == .wrongParent ? "other" : "parent",acceptedAt:Date(),torrentExists:true)
        accepted[key]=receipt
        if behavior == .timeoutAccepted {throw APIError.connection(.timedOut)}
        return receipt
    }
    func retryAcceptance(kind:RetryResourceKind,id:String,key:UUID,revision:String) async throws -> RetryAcceptanceDTO? {
        receiptChecks+=1
        if behavior == .mismatch {throw SubmissionFailure.keyMismatch}
        return accepted[key]
    }
}
@MainActor @Suite(.timeLimit(.minutes(1))) struct RetryModelTests {
    private struct Fixture {
        let directory:URL,store:PendingInbox,server:AuthenticatedServer,api:RetryTestAPI,model:RetryModel
    }
    private func fixture(kind:RetryResourceKind = .download,isConnectionCurrent:@escaping @MainActor @Sendable ()->Bool = {true}) throws -> Fixture {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent("debrid-retry-"+UUID().uuidString)
        let store=try PendingInbox(directory:directory),server=AuthenticatedServer(profile:try ServerProfile(server:ServerAddress("https://retry.example.test/proxy")),username:"Fixture",credential:try .session("fixture_unused"))
        let api=RetryTestAPI(serverURL:server.profile.serverURL,store:store)
        let model=try RetryModel(server:server,kind:kind,resourceID:kind == .torrent ? "parent" : "failed-file",torrentID:"parent",name:"Sample",api:api,store:store,isConnectionCurrent:isConnectionCurrent)
        return Fixture(directory:directory,store:store,server:server,api:api,model:model)
    }
    private func waitForPost(_ api:RetryTestAPI) async throws {for _ in 0..<100 {if await !api.keys.isEmpty {return};try await Task.sleep(for:.milliseconds(10))};Issue.record("retry not dispatched")}
    @Test func explicitConfirmationAndReopenCheckNeverCreateAnotherMutation() async throws {
        let f=try fixture(kind:.torrent);defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();#expect(f.model.phase == .review && f.model.waitForSelection);#expect(await f.api.keys.isEmpty)
        await f.api.configure(.timeoutAccepted);await f.model.confirm()
        #expect(f.model.phase == .uncertain && f.model.intent?.phase == .prepared)
        f.model.stop();await f.model.waitForRelease()
        let reopened=try RetryModel(server:f.server,kind:.torrent,resourceID:"parent",torrentID:"parent",name:"Changed",api:f.api,store:f.store)
        await reopened.open()
        #expect(reopened.phase == .accepted(true) && reopened.waitForSelection && reopened.name=="Sample")
        let sent=await f.api.keys,checked=await f.api.receiptChecks
        #expect(sent.count==1 && checked==1)
        reopened.stop();await reopened.waitForRelease();#expect(try await f.store.savedRetries().isEmpty)
        await reopened.open();#expect(reopened.phase == .accepted(true));#expect(await f.api.keys.count==1)
        reopened.stop();await reopened.waitForRelease()
    }
    @Test func missingReceiptAndConflictOnRetryKeepOriginalKey() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();await f.api.configure(.offline);await f.model.confirm()
        let original=try #require(f.model.intent?.key)
        await f.model.refresh();#expect(f.model.phase == .uncertain && !f.model.canConfirm)
        await f.api.configure(.rejected);await f.model.retry()
        #expect(f.model.phase == .uncertain && f.model.intent?.key==original)
        #expect(await f.api.keys==[original,original]);#expect(await f.api.revisions==[String(repeating:"a",count:64),String(repeating:"a",count:64)])
        f.model.stop();await f.model.waitForRelease();#expect(try await f.store.savedRetries().count==1)
    }
    @Test func onlyFreshDefiniteRejectionCanBeReviewedWithNewKey() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();await f.api.configure(.rejected);await f.model.confirm()
        #expect(f.model.phase == .rejected);let rejected=try #require(f.model.intent?.key)
        await f.model.refresh();#expect(f.model.canConfirm)
        await f.api.configure(.success);await f.model.confirm()
        #expect(f.model.phase == .accepted(true) && f.model.intent?.key != rejected)
        f.model.stop();await f.model.waitForRelease()
    }
    @Test func capturedParentPreventsReviewAndAcceptanceRebinding() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.api.setParent("other");await f.model.open();#expect(!f.model.canConfirm && f.model.phase == .unavailable)
        #expect(await f.api.keys.isEmpty)
        await f.api.setParent("parent");await f.model.refresh();await f.api.configure(.wrongParent);await f.model.confirm()
        #expect(f.model.phase == .uncertain && f.model.intent?.torrentID=="parent")
        await f.model.refresh();#expect(f.model.phase == .uncertain && !f.model.canConfirm)
        f.model.stop();await f.model.waitForRelease();#expect(try await f.store.savedRetries().first?.torrentID=="parent")
    }
    @Test func capabilityRecheckedBeforePrepareAndSubmission() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();await f.api.setSupport(false);await f.model.confirm()
        #expect(f.model.phase == .requiresUpdate && f.model.intent==nil);#expect(await f.api.keys.isEmpty)
        f.model.stop();await f.model.waitForRelease();#expect(try await f.store.savedRetries().isEmpty)
    }
    @Test func canceledRequestKeepsIntentAndLateUnauthorizedCannotRejectConnection() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();await f.api.configure(.success,hold:true)
        let sending=Task {await f.model.confirm()};try await waitForPost(f.api);sending.cancel();await sending.value
        #expect(f.model.phase == .uncertain && !f.model.isBusy)
        f.model.stop();await f.model.waitForRelease();#expect(try await f.store.savedRetries().count==1)
        let g=try fixture();defer {try? FileManager.default.removeItem(at:g.directory)}
        await g.model.open();await g.api.configure(.unauthorized,hold:true)
        let late=Task {await g.model.confirm()};try await waitForPost(g.api)
        g.model.stop();await g.model.waitForRelease();await g.api.release();await late.value
        #expect(!g.model.requiresSignIn)
    }
    @Test func doubleConfirmationUsesOnePersistedRequest() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();await f.api.configure(.success,hold:true)
        let first=Task {await f.model.confirm()};try await waitForPost(f.api)
        await f.model.confirm();#expect(await f.api.keys.count==1)
        await f.api.release();await first.value;#expect(f.model.phase == .accepted(true))
        f.model.stop();await f.model.waitForRelease()
    }
    @Test func changedConnectionDuringReadinessNeverPreparesOrPostsRetry() async throws {
        let flag=RetryConnectionFlag(),f=try fixture(isConnectionCurrent:{flag.current})
        defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();await f.api.setHealthHold(true)
        let sending=Task {await f.model.confirm()}
        for _ in 0..<100 {if f.model.isBusy {break};try await Task.sleep(for:.milliseconds(10))}
        flag.current=false;await f.api.setHealthHold(false);await sending.value
        #expect(await f.api.keys.isEmpty);#expect(f.model.intent==nil && !f.model.canConfirm && !f.model.requiresSignIn)
        f.model.stop();await f.model.waitForRelease();#expect(try await f.store.savedRetries().isEmpty)
    }
    @Test func mismatchRemainsBlockedAcrossModelRecreation() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();await f.api.configure(.mismatch);await f.model.confirm()
        let key=try #require(f.model.intent?.key);#expect(f.model.phase == .blocked)
        f.model.stop();await f.model.waitForRelease()
        let reopened=try RetryModel(server:f.server,kind:.download,resourceID:"failed-file",torrentID:"parent",name:"Sample",api:f.api,store:f.store)
        await reopened.open();await reopened.retry()
        #expect(reopened.phase == .blocked && reopened.intent?.key==key);#expect(await f.api.keys.count==1)
        reopened.stop();await reopened.waitForRelease()
    }
}
