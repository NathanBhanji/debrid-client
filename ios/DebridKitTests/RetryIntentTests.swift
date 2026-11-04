import Foundation
import Testing
@testable import DebridKit

@Suite(.timeLimit(.minutes(1))) struct RetryIntentTests {
    private let server=URL(string:"https://retry.example.test/proxy")!
    private let revision=String(repeating:"a",count:64)
    private func review(kind:RetryResourceKind = .download,id:String = "failed-file",parent:String = "parent")->RetryReviewDTO {
        RetryReviewDTO(kind:kind.rawValue,resourceId:id,torrentId:parent,revision:revision,allowed:true,waitForSelection:true)
    }
    @Test func savedIntentSurvivesReclaimWithoutFreshRejectionAuthority() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer {try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory)
        let claim=try await store.claimRetry(server:server,kind:.download,resourceID:"failed-file",torrentID:"parent",name:"Sample file")
        let prepared=try await store.prepareRetry(claim.id,token:claim.token,review:review())
        let nonce=try #require(prepared.rejectionToken)
        await #expect(throws:RetryIntentError.unresolved) {_=try await store.prepareRetry(claim.id,token:claim.token,review:RetryReviewDTO(kind:"download",resourceId:"failed-file",torrentId:"parent",revision:String(repeating:"b",count:64),allowed:true,waitForSelection:true))}
        try await store.releaseRetry(claim.id,token:claim.token)
        let restored=try await store.claimRetry(server:server,kind:.download,resourceID:"failed-file",torrentID:"parent",name:"Changed display name")
        #expect(restored.name=="Sample file" && restored.intent?.key==prepared.intent.key)
        let replay=try await store.prepareRetry(restored.id,token:restored.token,review:review())
        #expect(!replay.wasCreated && replay.intent.key==prepared.intent.key)
        await #expect(throws:RetryIntentError.unresolved) {try await store.rejectRetry(restored.id,token:restored.token,key:prepared.intent.key,freshToken:nonce)}
        try await store.releaseRetry(restored.id,token:restored.token)
        let rows=try await store.savedRetries();#expect(rows.count==1 && rows[0].torrentID=="parent")
        await #expect(throws:RetryIntentError.parentChanged) {_=try await store.claimRetry(server:server,kind:.download,resourceID:"failed-file",torrentID:"different-parent",name:"Changed")}
    }
    @Test func acceptanceMustMatchCapturedParentAndCanDescribeRemovedParent() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer {try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory),claim=try await store.claimRetry(server:server,kind:.download,resourceID:"failed-file",torrentID:"parent",name:"Sample")
        let prepared=try await store.prepareRetry(claim.id,token:claim.token,review:review())
        let wrong=RetryAcceptanceDTO(kind:"download",resourceId:"failed-file",torrentId:"other",acceptedAt:Date(),torrentExists:true)
        await #expect(throws:RetryIntentError.parentChanged) {try await store.acceptRetry(claim.id,token:claim.token,key:prepared.intent.key,receipt:wrong)}
        #expect(try await store.retryClaim(claim.id,token:claim.token).intent?.phase == .prepared)
        let correct=RetryAcceptanceDTO(kind:"download",resourceId:"failed-file",torrentId:"parent",acceptedAt:Date(),torrentExists:false)
        try await store.acceptRetry(claim.id,token:claim.token,key:prepared.intent.key,receipt:correct)
        let accepted=try #require(try await store.retryClaim(claim.id,token:claim.token).intent)
        #expect(accepted.phase == .accepted && accepted.torrentExists==false)
        try await store.releaseRetry(claim.id,token:claim.token)
        #expect(try await store.savedRetries().isEmpty)
    }
    @Test func versionFourSelectionMigratesAndVersionFiveMissingRetryFieldIsPreserved() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer {try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory)
        let selection=try await store.claimFileSelection(server:server,transferID:"parent",name:"Selection")
        let prepared=try await store.prepareFileSelection(selection.id,token:selection.token,revision:revision,fileIDs:["provider-file"])
        try await store.releaseFileSelection(selection.id,token:selection.token)
        let file=directory.appendingPathComponent("inbox.json")
        var old=try #require(JSONSerialization.jsonObject(with:Data(contentsOf:file)) as? [String:Any])
        old["version"]=4;old.removeValue(forKey:"retries")
        try JSONSerialization.data(withJSONObject:old).write(to:file)
        #expect(try await store.savedFileSelections().count==1)
        let restored=try await store.claimFileSelection(server:server,transferID:"parent",name:"Selection")
        #expect(restored.intent?.id==prepared.intent.id)
        try await store.releaseFileSelection(restored.id,token:restored.token)
        var upgraded=try #require(JSONSerialization.jsonObject(with:Data(contentsOf:file)) as? [String:Any])
        #expect(upgraded["version"] as? Int==5 && upgraded["retries"] is [Any])
        upgraded.removeValue(forKey:"retries")
        let corrupt=try JSONSerialization.data(withJSONObject:upgraded);try corrupt.write(to:file)
        await #expect(throws:PendingInboxError.corrupt) {_=try await store.snapshot()}
        #expect(try Data(contentsOf:file)==corrupt)
    }
    @Test func unresolvedRetriesAreNeverEvictedByTheQuota() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer {try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory)
        for number in 0..<PendingInbox.itemLimit {
            let id="parent-\(number)",claim=try await store.claimRetry(server:server,kind:.torrent,resourceID:id,torrentID:id,name:"Sample")
            _=try await store.prepareRetry(claim.id,token:claim.token,review:review(kind:.torrent,id:id,parent:id))
            try await store.releaseRetry(claim.id,token:claim.token)
        }
        await #expect(throws:PendingInboxError.full) {_=try await store.claimRetry(server:server,kind:.torrent,resourceID:"extra",torrentID:"extra",name:"Extra")}
        #expect(try await store.savedRetries().count==PendingInbox.itemLimit)
    }
}
