import Foundation
import Testing
@testable import DebridKit

private final class SubmissionTestBundle:NSObject {}
private func submissionWire<T:Decodable>(_ key:String,as type:T.Type,changes:[String:Any] = [:]) throws -> T {
    let url=try #require(Bundle(for:SubmissionTestBundle.self).url(forResource:"contract",withExtension:"json",subdirectory:"ios-api"))
    let root=try #require(JSONSerialization.jsonObject(with:Data(contentsOf:url)) as? [String:Any])
    var value=try #require(root[key] as? [String:Any]);for (key,change) in changes { value[key]=change }
    return try DebridAPIClient.decoder().decode(type,from:JSONSerialization.data(withJSONObject:value))
}
private actor SubmissionTestAPI:TransferSubmissionAPI {
    enum Behavior:Sendable { case success, timeoutAccepted, offline, duplicate, validation, mismatch }
    nonisolated let serverURL:URL
    let store:PendingInbox
    var healthy=true,supports=true,hold=false
    var customSettings:SettingsDTO?
    var behavior:Behavior = .success
    private(set) var keys:[UUID]=[]
    private(set) var options:[SubmissionOptions]=[]
    private(set) var checks=0
    private var accepted:[UUID:TorrentDTO]=[:]
    init(serverURL:URL,store:PendingInbox) { self.serverURL=serverURL;self.store=store }
    func configure(_ behavior:Behavior,hold:Bool = false) { self.behavior=behavior;self.hold=hold }
    func setReadiness(healthy:Bool = true,supports:Bool = true) { self.healthy=healthy;self.supports=supports }
    func release() { hold=false }
    func health() async throws -> HealthDTO { HealthDTO(ok:healthy,version:"0.2.3",capabilities:supports ? ["torrent_submissions_v1","multipart_torrent_settings_v1","manual_file_selection_v1"] : []) }
    func accounts() async throws -> [AccountDTO] { [try submissionWire("account",as:AccountDTO.self)] }
    func setSettings(_ value:SettingsDTO) {customSettings=value}
    func settings() async throws -> SettingsDTO { if let customSettings{return customSettings};return try submissionWire("settings",as:SettingsDTO.self) }
    func submit(_ payload:SubmissionPayload,options:SubmissionOptions,key:UUID) async throws -> TorrentDTO {
        keys.append(key);self.options.append(options)
        let records=try await store.snapshot()
        #expect(records.contains { $0.submission?.key==key && $0.submission?.phase == .prepared })
        while hold { try await Task.sleep(for:.milliseconds(20)) }
        switch behavior {
        case .offline:throw APIError.connection(.notConnectedToInternet)
        case .duplicate:throw SubmissionFailure.activeTorrent("existing-transfer")
        case .validation:throw APIError.http(status:422)
        case .mismatch:throw SubmissionFailure.keyMismatch
        case .success,.timeoutAccepted:break
        }
        let hash:String
        switch payload { case .magnet(let magnet):hash=magnet.infoHash;case .torrent(let file):hash=file.summary.infoHash }
        let torrent:TorrentDTO=try submissionWire("torrent",as:TorrentDTO.self,changes:["id":"accepted-transfer","hash":hash,"account_id":options.accountID])
        accepted[key]=torrent
        if behavior == .timeoutAccepted { behavior = .success;throw APIError.connection(.timedOut) }
        return torrent
    }
    func submissionReceipt(key:UUID) async throws -> SubmissionReceiptDTO? {
        checks+=1;guard let value=accepted[key] else { return nil }
        return SubmissionReceiptDTO(torrentId:value.id,acceptedAt:Date(timeIntervalSince1970:1),torrentExists:true)
    }
}
@MainActor @Suite(.timeLimit(.minutes(1))) struct TransferAddTests {
    private let raw="magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=Fixture%2bOriginal&tr=https%3a%2f%2ffixture.test%2fa%3ftoken%3dprivate"
    private struct Fixture {
        let directory:URL,store:PendingInbox,api:SubmissionTestAPI,server:AuthenticatedServer,claim:PendingClaim,model:TransferAddModel
    }
    private func fixture(target:String? = nil) async throws -> Fixture {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent("debrid-submission-"+UUID().uuidString)
        let store=try PendingInbox(directory:directory),profile=try ServerProfile(server:ServerAddress("https://submit.example.test/proxy"))
        let server=AuthenticatedServer(profile:profile,username:"Fixture",credential:try .session("fixture_unused"))
        let id=try await store.enqueue(MagnetLink(raw),source:.paste,target:target.map { try ServerAddress($0) }).id
        let claim=try await store.claim(id),api=SubmissionTestAPI(serverURL:profile.serverURL,store:store)
        let model=try TransferAddModel(claim:claim,server:server,store:store,api:api)
        return Fixture(directory:directory,store:store,api:api,server:server,claim:claim,model:model)
    }
    private func waitForPostCount(_ api:SubmissionTestAPI,_ count:Int) async throws {
        for _ in 0..<300 { if await api.keys.count>=count { return };try await Task.sleep(for:.milliseconds(10)) }
        Issue.record("Submission fixture never reached expected POST count")
    }
    private func waitForPost(_ api:SubmissionTestAPI) async throws {
        for _ in 0..<300 { if await !api.keys.isEmpty { return };try await Task.sleep(for:.milliseconds(10)) }
        Issue.record("Submission fixture never reached POST")
    }
    @Test func reviewNeverPostsAndSubmissionIsDurableBeforeExactlyOneDispatch() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        await f.model.load();#expect(f.model.canSubmit);#expect(await f.api.keys.isEmpty)
        f.model.category="Documentaries";await f.api.configure(.success,hold:true)
        let first=Task { await f.model.submit() };try await waitForPost(f.api)
        await f.model.submit();#expect(await f.api.keys.count==1)
        await f.api.release();await first.value
        #expect(f.model.phase == .accepted("accepted-transfer",true) && f.model.durableAcceptance)
        let saved=try #require(try await f.store.snapshot().first?.submission)
        #expect(saved.phase == .accepted && saved.options.category=="Documentaries")
        #expect(try await f.store.snapshot().first?.magnet?.rawURI==raw)
        #expect(!String(reflecting:saved).contains("fixture.test"))
    }
    @Test func lostResponseRestoresByReceiptWithoutPostingAgain() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        await f.model.load();await f.api.configure(.timeoutAccepted);await f.model.submit()
        #expect(f.model.phase == .uncertain && !f.model.canDiscard)
        let reopened=try TransferAddModel(claim:f.claim,server:f.server,store:f.store,api:f.api);await reopened.load()
        #expect(reopened.phase == .accepted("accepted-transfer",true) && reopened.durableAcceptance)
        #expect(await f.api.keys.count==1)
    }
    @Test func unknownReceiptNeverCreatesKeyAndExplicitRetryKeepsExactIntent() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        await f.model.load();await f.api.configure(.offline);await f.model.submit()
        let first=try #require(try await f.store.snapshot().first?.submission)
        let reopened=try TransferAddModel(claim:f.claim,server:f.server,store:f.store,api:f.api);await reopened.load()
        #expect(reopened.phase == .uncertain);#expect(await f.api.keys.count==1)
        reopened.accountID="different-account";reopened.category="Changed";reopened.unpack = !first.options.settings.unpack
        await f.api.configure(.success);await reopened.retrySameSubmission()
        let keys=await f.api.keys,options=await f.api.options
        #expect(keys.count==2 && keys[0]==keys[1])
        #expect(options.count==2 && options[0]==options[1] && options[1]==first.options)
    }
    @Test func cancelledBeforeActionNeverPreparesOrPostsAndCancelledSendRemainsRecoverable() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        await f.model.load()
        let before=Task { await f.model.submit() };before.cancel();await before.value
        #expect(try await f.store.snapshot().first?.submission==nil);#expect(await f.api.keys.isEmpty)
        await f.api.configure(.success,hold:true);let sending=Task { await f.model.submit() };try await waitForPost(f.api)
        sending.cancel();await sending.value;await f.api.release()
        #expect(f.model.phase == .uncertain && !f.model.isBusy)
        #expect(try await f.store.snapshot().first?.submission?.phase == .prepared)
        await #expect(throws:PendingInboxError.submissionPending) { try await f.store.discard(f.claim.item.id,token:f.claim.token) }
    }
    @Test func chooseAfterAddingPersistsHoldBeforeSubmission() async throws {
        let f=try await fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.load();f.model.filePolicy = .choose
        #expect(f.model.reviewedSettings?.waitForSelection == true && f.model.reviewedSettings?.manualFiles == nil)
        await f.api.configure(.offline);await f.model.submit()
        #expect(f.model.phase == .uncertain)
        #expect(try await f.store.snapshot().first?.submission?.options.settings.waitForSelection == true)
        #expect(await f.api.options.first?.settings.waitForSelection == true)
    }
    @Test func freshRejectionCanBeReviewedButRetryOfUncertainAttemptCannotRetireIt() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        await f.model.load();await f.api.configure(.validation);await f.model.submit()
        #expect(f.model.phase == .rejected && f.model.canDiscard)
        let rejected=try #require(f.model.attempt?.key);f.model.editRejectedOptions()
        f.model.unpack = !f.model.unpack;f.model.category="Revised category"
        let revised=try #require(f.model.reviewedSettings)
        await f.api.configure(.offline,hold:true)
        let sending=Task { await f.model.submit() };try await waitForPostCount(f.api,2)
        #expect(f.model.visibleSettings==revised && f.model.visibleOptions?.category=="Revised category")
        await f.api.release();await sending.value;let unknown=try #require(f.model.attempt?.key)
        #expect(unknown != rejected && f.model.phase == .uncertain)
        await f.api.configure(.validation);await f.model.retrySameSubmission()
        #expect(f.model.phase == .uncertain && f.model.attempt?.key==unknown)
        #expect(try await f.store.snapshot().first?.submission?.phase == .prepared)
    }
    @Test func rejectedAttemptCannotBypassMissingOrFalseHealthReadiness() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        await f.model.load();await f.api.configure(.duplicate);await f.model.submit()
        #expect(f.model.phase == .duplicate("existing-transfer"))
        await f.api.setReadiness(supports:false);await f.model.load();f.model.editRejectedOptions();await f.model.submit()
        #expect(f.model.phase == .requiresUpdate && !f.model.canSubmit);#expect(await f.api.keys.count==1)
        await f.api.setReadiness(healthy:false);await f.model.load();f.model.editRejectedOptions();await f.model.submit()
        #expect(!f.model.canSubmit && !f.model.capabilityReady);#expect(await f.api.keys.count==1)
    }
    @Test func mismatchStaysBlockedAfterModelRecreationAndCannotDiscard() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        await f.model.load();await f.api.configure(.mismatch);await f.model.submit()
        let reopened=try TransferAddModel(claim:f.claim,server:f.server,store:f.store,api:f.api);await reopened.load();await reopened.retrySameSubmission();await reopened.reconcile()
        #expect(reopened.phase == .mismatched && !reopened.canDiscard);#expect(await f.api.keys.count==1)
        await #expect(throws:PendingInboxError.submissionPending) { try await f.store.discard(f.claim.item.id,token:f.claim.token) }
    }
    @Test func targetMismatchRequiresExplicitConfirmationAndUnknownAttemptRejectsRebinding() async throws {
        let f=try await fixture(target:"https://previous.example.test");defer { try? FileManager.default.removeItem(at:f.directory) }
        await f.model.load();#expect(f.model.needsTargetConfirmation && !f.model.canSubmit)
        await f.model.submit();#expect(await f.api.keys.isEmpty)
        f.model.confirmedTargetChange=true;await f.api.configure(.offline);await f.model.submit()
        let otherProfile=try ServerProfile(server:ServerAddress("https://another.example.test"))
        let otherServer=AuthenticatedServer(profile:otherProfile,username:"Fixture",credential:try .session("fixture_unused")),api=SubmissionTestAPI(serverURL:otherProfile.serverURL,store:f.store)
        let other=try TransferAddModel(claim:f.claim,server:otherServer,store:f.store,api:api);await other.load();await other.retrySameSubmission()
        #expect(other.phase == .wrongServer(f.server.profile.serverURL));#expect(await api.keys.isEmpty)
    }
    @Test func oneUseRejectionNonceAndAcceptedAttemptsSurviveLeaseOwnerChanges() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        let defaults:SettingsDTO=try submissionWire("settings",as:SettingsDTO.self)
        let options=try SubmissionOptions(accountID:"fixture-account",category:"",settings:defaults.torrentDefaults),server=try ServerAddress(f.server.profile.serverURL.absoluteString)
        let first=try await f.store.prepareSubmission(f.claim.item.id,token:f.claim.token,server:server,options:options,confirmedTargetChange:false)
        let second=try await f.store.prepareSubmission(f.claim.item.id,token:f.claim.token,server:server,options:options,confirmedTargetChange:false)
        #expect(first.wasCreated && !second.wasCreated && first.attempt.key==second.attempt.key && second.rejectionToken==nil)
        let nonce=try #require(first.rejectionToken)
        try await f.store.recordSubmissionRejected(f.claim.item.id,token:f.claim.token,key:first.attempt.key,freshToken:nonce,reason:.options)
        await #expect(throws:PendingInboxError.submissionPending) { try await f.store.recordSubmissionRejected(f.claim.item.id,token:f.claim.token,key:first.attempt.key,freshToken:nonce,reason:.options) }
        let new=try await f.store.prepareSubmission(f.claim.item.id,token:f.claim.token,server:server,options:options,confirmedTargetChange:false)
        #expect(new.attempt.key != first.attempt.key)
        try await f.store.release(f.claim.item.id,token:f.claim.token);let owner=try await f.store.claim(f.claim.item.id)
        await #expect(throws:PendingInboxError.lostClaim) { try await f.store.recordSubmissionAccepted(f.claim.item.id,token:f.claim.token,key:new.attempt.key,server:server,transferID:"accepted-transfer",exists:true) }
        try await f.store.recordSubmissionAccepted(f.claim.item.id,token:owner.token,key:new.attempt.key,server:server,transferID:"accepted-transfer",exists:false)
        #expect(try await f.store.snapshot().first?.submission?.torrentExists==false)
    }
    @Test func acceptedServerResponseRemainsRecoverableWhenLocalConfirmationWriteFails() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        let fault=IntakeTestStore(f.store);await fault.setAcceptanceFailing(true)
        let model=try TransferAddModel(claim:f.claim,server:f.server,store:fault,api:f.api)
        await model.load();await model.submit()
        #expect(model.phase == .accepted("accepted-transfer",true) && !model.durableAcceptance && !model.canDiscard)
        #expect(try await f.store.snapshot().first?.submission?.phase == .prepared)
        let restored=try TransferAddModel(claim:f.claim,server:f.server,store:f.store,api:f.api);await restored.load()
        #expect(restored.durableAcceptance);#expect(await f.api.keys.count==1)
    }
    @Test func perTransferPolicyOverridesAreFrozenWithoutUndoingKeepCopiesOrFileChoice() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        await f.api.setSettings(SettingsDTO(torrentDefaults:TorrentSettingsDTO(finishedAction:"remove_from_provider",downloadRetries:3,torrentRetries:1,deleteOnError:"1h",unpack:true),categories:["Caf\u{00e9}","Cafe\u{0301}"],unpackMaxDepth:1,organize:OrganizeSettingsDTO(enabled:false)))
        await f.model.load();#expect(f.model.categories.count == 2)
        f.model.keepCopies=true;f.model.filePolicy = .choose
        var draft=DownloadPolicyDraft(try #require(f.model.editableOverrides));draft.downloadRetries="7";draft.unpack=false
        f.model.applyOverrides(try draft.output())
        #expect(f.model.filePolicy == .choose && f.model.reviewedSettings?.finishedAction == "keep" && f.model.reviewedSettings?.deleteOnError == nil)
        await f.model.submit()
        let sent=try #require((await f.api.options).last)
        #expect(sent.settings.downloadRetries == 7 && !sent.settings.unpack && sent.settings.waitForSelection == true)
        f.model.applyOverrides(TorrentSettingsDTO(downloadRetries:0,torrentRetries:0,unpack:true))
        #expect(f.model.editableOverrides == nil && f.model.visibleSettings?.downloadRetries == 7)
    }
    @Test func originalTorrentCopyAndExplicitRuleOverridesAreUsedForAdd() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        let file=try fixtureTorrent(),id=try await f.store.enqueueTorrent(file,source:.file).id,claim=try await f.store.claim(id)
        let model=try TransferAddModel(claim:claim,server:f.server,store:f.store,api:f.api);await model.load()
        model.allFiles=true;model.unpack=false;model.organization = .off;model.keepCopies=true
        await model.submit();#expect(model.phase == .accepted("accepted-transfer",true))
        let options=try #require((await f.api.options).last)
        #expect(options.settings.includeRegex==nil && options.settings.manualFiles==nil && !options.settings.unpack && options.settings.organize==false && options.settings.finishedAction=="keep" && options.settings.deleteOnError==nil)
        #expect(try await f.store.loadTorrent(id,token:claim.token).data==file.data)
    }
    @Test func invalidPersistedIntentFailsBeforeOwnedOrphanCleanup() async throws {
        let f=try await fixture();defer { try? FileManager.default.removeItem(at:f.directory) }
        await f.model.load();await f.api.configure(.offline);await f.model.submit()
        let url=f.directory.appendingPathComponent("inbox.json")
        var root=try #require(JSONSerialization.jsonObject(with:Data(contentsOf:url)) as? [String:Any])
        var rows=try #require(root["items"] as? [[String:Any]]),attempt=try #require(rows[0]["submission"] as? [String:Any]),options=try #require(attempt["options"] as? [String:Any])
        options["accountID"]="invalid/account";attempt["options"]=options;rows[0]["submission"]=attempt;root["items"]=rows
        let invalid=try JSONSerialization.data(withJSONObject:root);try invalid.write(to:url)
        let orphan=f.directory.appendingPathComponent(UUID().uuidString+".torrent-payload");try Data([1,2,3]).write(to:orphan)
        await #expect(throws:PendingInboxError.corrupt) { try await f.store.snapshot() }
        #expect(try Data(contentsOf:url)==invalid && FileManager.default.fileExists(atPath:orphan.path))
    }

}
