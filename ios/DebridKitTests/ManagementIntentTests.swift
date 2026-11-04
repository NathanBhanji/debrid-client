import Foundation
import Testing
@testable import DebridKit

@Suite(.timeLimit(.minutes(1))) struct ManagementIntentTests {
    private let server=URL(string:"https://management.example.test/proxy")!
    private let revision=String(repeating:"a",count:64)
    @Test func immutableRemovalSurvivesReclaimWithoutFreshRejectionAuthority() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString);defer{try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory)
        let first=try await store.claimManagement(server:server,kind:.remove,resourceID:"parent",name:"Original")
        let prepared=try await store.prepareManagement(first.id,token:first.token,resourceID:"parent",revision:revision,options:RemovalOptions(deleteFiles:true))
        #expect(prepared.wasCreated)
        let nonce=try #require(prepared.rejectionToken)
        try await store.releaseManagement(first.id,token:first.token)
        let reopened=try await store.claimManagement(server:server,kind:.remove,resourceID:"parent",name:"Changed")
        #expect(reopened.intent?.key==prepared.intent.key && reopened.name=="Original" && reopened.intent?.options==RemovalOptions(deleteFiles:true))
        await #expect(throws:ManagementIntentError.unresolved) {try await store.rejectManagement(reopened.id,token:reopened.token,key:prepared.intent.key,freshToken:nonce)}
        await #expect(throws:ManagementIntentError.unresolved) {_=try await store.prepareManagement(reopened.id,token:reopened.token,resourceID:"parent",revision:revision,options:RemovalOptions())}
        try await store.releaseManagement(reopened.id,token:reopened.token)
        await #expect(throws:ManagementIntentError.unresolved) {_=try await store.claimManagement(server:server,kind:.cancel,resourceID:"parent",name:"Other action")}
        #expect(try await store.savedManagement().count==1)
    }
    @Test func cancellationReceiptMustMatchAndRemovalAbsenceIsSeparateProof() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString);defer{try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory)
        let claim=try await store.claimManagement(server:server,kind:.cancel,resourceID:"parent",name:"Original")
        let prepared=try await store.prepareManagement(claim.id,token:claim.token,resourceID:"parent",revision:revision,options:nil)
        await #expect(throws:ManagementIntentError.invalid) {try await store.acceptCancellation(claim.id,token:claim.token,key:prepared.intent.key,receipt:CancellationAcceptanceDTO(torrentId:"other",acceptedAt:Date(),torrentExists:true))}
        try await store.acceptCancellation(claim.id,token:claim.token,key:prepared.intent.key,receipt:CancellationAcceptanceDTO(torrentId:"parent",acceptedAt:Date(),torrentExists:false))
        try await store.releaseManagement(claim.id,token:claim.token)
        let remove=try await store.claimManagement(server:server,kind:.remove,resourceID:"parent",name:"Original")
        let intent=try await store.prepareManagement(remove.id,token:remove.token,resourceID:"parent",revision:revision,options:RemovalOptions())
        try await store.completeRemoval(remove.id,token:remove.token,key:intent.intent.key,confirmed:false)
        #expect(try await store.managementClaim(remove.id,token:remove.token).intent?.outcome == .absenceObserved)
    }
    @Test func versionFivePreservesRetryAndVersionSixRequiresManagementArray() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString);defer{try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory)
        let retry=try await store.claimRetry(server:server,kind:.torrent,resourceID:"parent",torrentID:"parent",name:"Retry")
        let prepared=try await store.prepareRetry(retry.id,token:retry.token,review:RetryReviewDTO(kind:"torrent",resourceId:"parent",torrentId:"parent",revision:revision,allowed:true,waitForSelection:true))
        try await store.releaseRetry(retry.id,token:retry.token)
        let file=directory.appendingPathComponent("inbox.json")
        var legacy=try #require(JSONSerialization.jsonObject(with:Data(contentsOf:file)) as? [String:Any]);legacy["version"]=5;legacy.removeValue(forKey:"management")
        try JSONSerialization.data(withJSONObject:legacy).write(to:file)
        #expect(try await store.savedRetries().count==1)
        let restored=try await store.claimRetry(server:server,kind:.torrent,resourceID:"parent",torrentID:"parent",name:"Retry")
        #expect(restored.intent?.key==prepared.intent.key)
        try await store.releaseRetry(restored.id,token:restored.token)
        var upgraded=try #require(JSONSerialization.jsonObject(with:Data(contentsOf:file)) as? [String:Any]);#expect(upgraded["version"] as? Int==6)
        upgraded.removeValue(forKey:"management");let corrupt=try JSONSerialization.data(withJSONObject:upgraded);try corrupt.write(to:file)
        await #expect(throws:PendingInboxError.corrupt) {_=try await store.savedManagement()}
        #expect(try Data(contentsOf:file)==corrupt)
    }
    @Test func uncertainActionsAreNotEvictedForCapacity() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString);defer{try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory)
        for number in 0..<PendingInbox.itemLimit {
            let id="parent-\(number)",claim=try await store.claimManagement(server:server,kind:.cancel,resourceID:id,name:"Sample")
            _=try await store.prepareManagement(claim.id,token:claim.token,resourceID:id,revision:revision,options:nil)
            try await store.releaseManagement(claim.id,token:claim.token)
        }
        await #expect(throws:PendingInboxError.full) {_=try await store.claimManagement(server:server,kind:.cancel,resourceID:"extra",name:"Extra")}
        #expect(try await store.savedManagement().count==PendingInbox.itemLimit)
    }
}
