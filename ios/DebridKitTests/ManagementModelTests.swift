import Foundation
import Testing
@testable import DebridKit

@MainActor private final class ManagementConnectionFlag {var current=true}
private actor ManagementTestAPI:ManagementAPI {
    enum Behavior {case success,timeoutAccepted,offline,precondition,unauthorized,wrongID}
    struct Removal:Equatable,Sendable {let revision:String;let options:RemovalOptions}
    nonisolated let serverURL:URL
    nonisolated let isPreview=false
    let store:PendingInbox
    var behavior:Behavior = .success
    var supports=true,removed=false,missingRoute=false,hold=false,healthHold=false
    var revision=String(repeating:"a",count:64)
    private var waiter:CheckedContinuation<Void,Never>?
    private var receipts:[UUID:CancellationAcceptanceDTO]=[:]
    private(set) var keys:[UUID]=[]
    private(set) var removals:[Removal]=[]
    private(set) var receiptChecks=0,reviewChecks=0,healthCalls=0
    init(server:URL,store:PendingInbox){serverURL=server;self.store=store}
    func configure(_ behavior:Behavior,hold:Bool = false){self.behavior=behavior;self.hold=hold}
    func setSupport(_ value:Bool){supports=value}
    func setRevision(_ value:String){revision=value}
    func setMissingRoute(_ value:Bool){missingRoute=value}
    func setHealthHold(_ value:Bool){healthHold=value}
    func release(){hold=false;waiter?.resume();waiter=nil}
    func health() async throws -> HealthDTO {
        healthCalls+=1
        while healthHold {try await Task.sleep(for:.milliseconds(10))}
        return HealthDTO(ok:true,version:"fixture",capabilities:supports ? ["native_cancellation_v1","native_removal_precondition_v1"]:[])
    }
    func cancellationReview(id:String) async throws -> CancellationReviewDTO {CancellationReviewDTO(torrentId:id,revision:revision,allowed:true,providerMayContinue:true)}
    func requestCancellation(id:String,key:UUID,revision:String) async throws -> CancellationAcceptanceDTO {
        keys.append(key)
        #expect(try await store.savedManagement().contains {$0.resourceID==id && $0.kind == .cancel})
        if hold {await withCheckedContinuation {waiter=$0}}
        switch behavior {
        case .offline:throw APIError.connection(.notConnectedToInternet)
        case .precondition:throw APIError.http(status:409)
        case .unauthorized:throw APIError.unauthorized
        default:break
        }
        let receipt=CancellationAcceptanceDTO(torrentId:behavior == .wrongID ? "other":id,acceptedAt:Date(),torrentExists:true);receipts[key]=receipt
        if behavior == .timeoutAccepted{throw APIError.connection(.timedOut)}
        return receipt
    }
    func cancellationAcceptance(id:String,key:UUID,revision:String) async throws -> CancellationAcceptanceDTO? {receiptChecks+=1;return receipts[key]}
    func removalReview(id:String) async throws -> RemovalReviewDTO {
        reviewChecks+=1
        if missingRoute {throw APIError.http(status:404)}
        return RemovalReviewDTO(torrentId:id,exists:!removed,revision:removed ? nil:revision,providerCopyKnown:!removed)
    }
    func removeTransfer(id:String,revision:String,options:RemovalOptions) async throws {
        removals.append(Removal(revision:revision,options:options))
        #expect(try await store.savedManagement().contains {$0.resourceID==id && $0.kind == .remove})
        if hold {await withCheckedContinuation {waiter=$0}}
        if revision != self.revision || behavior == .precondition {throw APIError.http(status:412)}
        switch behavior {
        case .offline:throw APIError.connection(.notConnectedToInternet)
        case .unauthorized:throw APIError.unauthorized
        default:break
        }
        removed=true
        if behavior == .timeoutAccepted {throw APIError.connection(.timedOut)}
    }
}
@MainActor @Suite(.timeLimit(.minutes(1))) struct ManagementModelTests {
    private struct Fixture {let directory:URL,store:PendingInbox,server:AuthenticatedServer,api:ManagementTestAPI,model:ManagementModel}
    private func withFixture(_ kind:ManagementAction,isCurrent:@escaping @MainActor @Sendable ()->Bool = {true},body:(Fixture) async throws -> Void) async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent("debrid-management-"+UUID().uuidString)
        let store=try PendingInbox(directory:directory),server=AuthenticatedServer(profile:try ServerProfile(server:ServerAddress("https://management.example.test/proxy")),username:"Fixture",credential:try .session("fixture_unused"))
        let api=ManagementTestAPI(server:server.profile.serverURL,store:store)
        let model=try ManagementModel(server:server,kind:kind,resourceID:"parent",name:"Original",api:api,store:store,isConnectionCurrent:isCurrent)
        let fixture=Fixture(directory:directory,store:store,server:server,api:api,model:model)
        do {try await body(fixture);model.stop();await model.waitForRelease();try? FileManager.default.removeItem(at:directory)}
        catch {model.stop();await model.waitForRelease();try? FileManager.default.removeItem(at:directory);throw error}
    }
    private func waitFor(_ condition:() async -> Bool) async throws {for _ in 0..<100 {if await condition(){return};try await Task.sleep(for:.milliseconds(10))};Issue.record("fixture operation did not reach barrier")}
    @Test func cancellationNeedsConfirmAndReopenOnlyChecksOriginalReceipt() async throws {
        try await withFixture(.cancel){f in
            await f.model.open();let initial=await f.api.keys;#expect(f.model.canConfirm && initial.isEmpty)
            await f.api.configure(.timeoutAccepted);await f.model.confirm()
            #expect(f.model.phase == .uncertain)
            let key=try #require(f.model.intent?.key)
            f.model.stop();await f.model.waitForRelease()
            let reopened=try ManagementModel(server:f.server,kind:.cancel,resourceID:"parent",name:"Changed",api:f.api,store:f.store)
            await reopened.open()
            if case .accepted(.cancellation(_,true))=reopened.phase {}else{Issue.record("saved cancellation receipt not accepted")}
            let sent=await f.api.keys,checks=await f.api.receiptChecks
            #expect(reopened.name=="Original" && sent==[key] && checks==1)
            reopened.stop();await reopened.waitForRelease()
        }
    }
    @Test func removalDefaultsSafeAndLostResponseNeverClaimsCleanupConfirmed() async throws {
        try await withFixture(.remove){f in
            await f.model.open();#expect(f.model.options==RemovalOptions())
            f.model.setDeleteFiles(true);await f.api.configure(.timeoutAccepted);await f.model.confirm()
            #expect(f.model.phase == .uncertain && f.model.options==RemovalOptions(deleteFiles:true))
            f.model.setDeleteFiles(false);#expect(f.model.options.deleteFiles)
            f.model.stop();await f.model.waitForRelease()
            let reopened=try ManagementModel(server:f.server,kind:.remove,resourceID:"parent",name:"Changed",api:f.api,store:f.store)
            await reopened.open()
            #expect(reopened.phase == .accepted(.absenceObserved))
            #expect(await f.api.removals.count==1)
            reopened.stop();await reopened.waitForRelease()
        }
    }
    @Test func unknownRemovalKeepsOptionsAndBlocksChangedIncarnation() async throws {
        try await withFixture(.remove){f in
            await f.model.open();f.model.setDeleteProvider(true)
            await f.api.configure(.offline);await f.model.confirm()
            let key=try #require(f.model.intent?.key)
            await f.model.refresh();#expect(f.model.phase == .uncertain && f.model.canRetry)
            await f.api.configure(.precondition);await f.model.retry()
            #expect(f.model.phase == .blocked && !f.model.canRetry && f.model.intent?.key==key)
            let sent=await f.api.removals;#expect(sent.count==2 && sent[0]==sent[1] && sent[0].options==RemovalOptions(deleteFromProvider:true))
            f.model.stop();await f.model.waitForRelease()
            let reopened=try ManagementModel(server:f.server,kind:.remove,resourceID:"parent",name:"Changed",api:f.api,store:f.store)
            await reopened.open();#expect(reopened.phase == .blocked && !reopened.canConfirm && !reopened.canRetry)
            reopened.stop();await reopened.waitForRelease()
        }
    }
    @Test func route404CannotBecomeAnAbsenceResult() async throws {
        try await withFixture(.remove){f in
            await f.model.open();await f.api.configure(.offline);await f.model.confirm()
            await f.api.setMissingRoute(true);await f.model.refresh()
            #expect(f.model.phase == .uncertain && f.model.intent?.phase == .prepared)
            #expect(await f.api.removals.count==1)
            await f.api.setMissingRoute(false);await f.api.setRevision(String(repeating:"b",count:64));await f.model.refresh()
            #expect(f.model.phase == .blocked && !f.model.canRetry)
        }
    }
    @Test func definiteFreshRejectionAllowsNewReviewAndKey() async throws {
        try await withFixture(.remove){f in
            await f.model.open();await f.api.configure(.precondition);await f.model.confirm()
            #expect(f.model.phase == .rejected)
            let key=f.model.intent?.key
            await f.api.configure(.success);await f.model.refresh();await f.model.confirm()
            #expect(f.model.phase == .accepted(.removalConfirmed) && f.model.intent?.key != key)
            f.model.stop();await f.model.waitForRelease();await f.model.open()
            let sent=await f.api.removals
            #expect(f.model.phase == .accepted(.removalConfirmed) && sent.count==2)
        }
    }
    @Test func capabilityAndConnectionChangesPreventPreparationOrMutation() async throws {
        let flag=ManagementConnectionFlag()
        try await withFixture(.remove,isCurrent:{flag.current}){f in
            await f.api.setSupport(false);await f.model.open();#expect(f.model.phase == .requiresUpdate)
            await f.api.setSupport(true);await f.model.refresh()
            await f.api.setHealthHold(true)
            let request=Task{await f.model.confirm()}
            try await waitFor {await f.api.healthCalls>=3}
            flag.current=false;await f.api.setHealthHold(false);await request.value
            let sent=await f.api.removals
            #expect(sent.isEmpty && f.model.intent==nil)
            #expect(try await f.store.savedManagement().isEmpty)
        }
    }
    @Test func doubleTapAndLateUnauthorizedCannotAffectNewConnection() async throws {
        try await withFixture(.cancel){f in
            await f.model.open();await f.api.configure(.unauthorized,hold:true)
            let request=Task{await f.model.confirm()}
            try await waitFor {await !f.api.keys.isEmpty}
            await f.model.confirm();#expect(await f.api.keys.count==1)
            f.model.stop();request.cancel();await f.api.release();await request.value;await f.model.waitForRelease()
            let saved=try await f.store.savedManagement()
            #expect(!f.model.requiresSignIn && saved.count==1)
        }
    }
    @Test func wrongReceiptCannotMarkCancellationAccepted() async throws {
        try await withFixture(.cancel){f in
            await f.model.open();await f.api.configure(.wrongID);await f.model.confirm()
            #expect(f.model.phase == .uncertain && f.model.intent?.phase == .prepared)
        }
    }
}
