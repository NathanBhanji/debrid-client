import Foundation
import Testing
@testable import DebridKit

private let selectionRevision=String(repeating:"a",count:64)
private func selectionResponse(_ state:String = "ready",ids:[String] = []) -> FileSelectionDTO {
    FileSelectionDTO(torrentId:"transfer",state:state,revision:selectionRevision,files:[
        FileDTO(id:"é",path:"Season 1/Episode 2.mkv",size:200,selected:false),
        FileDTO(id:"e\u{301}",path:"Season 1/Episode 10.mkv",size:300,selected:false),
        FileDTO(id:"opaque/\nID",path:"Extras/Notes.txt",size:0,selected:false)
    ],selectedIds:ids,providerSelects:true,message:"")
}
private actor SelectionTestAPI:FileSelectionAPI {
    enum Behavior {case success,timeoutAccepted,offline,rejected}
    nonisolated let serverURL:URL
    let store:PendingInbox
    var response=selectionResponse()
    var behavior:Behavior = .success
    private(set) var bodies:[[String]]=[]
    init(serverURL:URL,store:PendingInbox) {self.serverURL=serverURL;self.store=store}
    func configure(_ value:Behavior) {behavior=value}
    func health() async throws -> HealthDTO {HealthDTO(ok:true,version:"0.2.4",capabilities:["manual_file_selection_v1"])}
    func fileSelection(id:String) async throws -> FileSelectionDTO {response}
    func confirmFileSelection(id:String,revision:String,fileIDs:[String]) async throws -> FileSelectionDTO {
        let saved=try await store.savedFileSelections()
        #expect(saved.count==1 && saved.first?.phase == .prepared)
        bodies.append(fileIDs)
        switch behavior {
        case .offline:throw APIError.connection(.notConnectedToInternet)
        case .rejected:throw APIError.http(status:409)
        case .success,.timeoutAccepted:response=selectionResponse("accepted",ids:fileIDs)
        }
        if behavior == .timeoutAccepted {throw APIError.connection(.timedOut)}
        return response
    }
}
@MainActor @Suite(.timeLimit(.minutes(1))) struct FileSelectionTests {
    private struct Fixture {
        let directory:URL,store:PendingInbox,server:AuthenticatedServer,api:SelectionTestAPI,model:FileSelectionModel
    }
    private func fixture() throws -> Fixture {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent("debrid-selection-"+UUID().uuidString)
        let store=try PendingInbox(directory:directory)
        let server=AuthenticatedServer(profile:try ServerProfile(server:ServerAddress("https://selection.example.test")),username:"Fixture",credential:try .session("fixture_unused"))
        let api=SelectionTestAPI(serverURL:server.profile.serverURL,store:store)
        return Fixture(directory:directory,store:store,server:server,api:api,model:try FileSelectionModel(server:server,transferID:"transfer",name:"Fixture",api:api,store:store))
    }
    @Test func folderSearchAndOpaqueIdentityRemainIndependent() async throws {
        let review=try await FileSelectionReview.build(selectionResponse(),transferID:"transfer")
        #expect(review.filesByID.count==3)
        #expect(review.subfolders(in:Data()).map(\.name)==["Extras","Season 1"])
        let season=Data("Season 1".utf8)
        #expect(review.contents(in:season).map(\.name)==["Episode 2.mkv","Episode 10.mkv"])
        #expect(review.descendants(in:season).count==2)
        #expect(try await review.search("NOTES",in:season).map(\.providerID)==["opaque/\nID"])
        let ids=try FileSelectionIdentity.canonicalIDs(["é","e\u{301}","opaque/\nID"])
        #expect(ids.count==3)
        #expect(!FileSelectionIdentity.sameIDs(["é"],["e\u{301}"]))
        #expect(try JSONDecoder().decode([String].self,from:FileSelectionIdentity.encodedIDs(ids)).map {Data($0.utf8)}==ids.map {Data($0.utf8)})
    }
    @Test func interruptedAcceptanceReopensWithoutSendingAndResolvesByRead() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();#expect(f.model.phase == .editing && f.model.selected.isEmpty)
        f.model.toggle(Data("é".utf8));let proposal=try #require(await f.model.proposal())
        await f.api.configure(.timeoutAccepted);await f.model.confirm(proposal)
        #expect(f.model.phase == .uncertain);#expect(await f.api.bodies.count==1)
        f.model.stop();await f.model.waitForRelease()
        let reopened=try FileSelectionModel(server:f.server,transferID:"transfer",name:"Fixture",api:f.api,store:f.store)
        await reopened.open();#expect(reopened.phase == .accepted(.ours));#expect(await f.api.bodies.count==1)
        reopened.stop();await reopened.waitForRelease();#expect(try await f.store.savedFileSelections().isEmpty)
    }
    @Test func uncertainRetryKeepsExactChoiceDespiteConflictAndReadySnapshot() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();f.model.toggle(Data("e\u{301}".utf8));let proposal=try #require(await f.model.proposal())
        await f.api.configure(.offline);await f.model.confirm(proposal)
        let id=try #require(f.model.intent?.id)
        await f.model.refresh();#expect(f.model.phase == .uncertain && !f.model.canEdit)
        await f.api.configure(.rejected);await f.model.retry()
        #expect(f.model.phase == .uncertain && f.model.intent?.id==id)
        let bodies=await f.api.bodies
        #expect(bodies.count==2 && FileSelectionIdentity.sameIDs(bodies[0],bodies[1]))
        f.model.stop();await f.model.waitForRelease();#expect(try await f.store.savedFileSelections().count==1)
    }
    @Test func onlyFreshRejectionAllowsChangingFiles() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        await f.model.open();f.model.toggle(Data("é".utf8));let proposal=try #require(await f.model.proposal())
        await f.api.configure(.rejected);await f.model.confirm(proposal)
        #expect(f.model.phase == .rejected)
        await f.model.refresh();#expect(f.model.canEdit)
        f.model.clear();f.model.toggle(Data("opaque/\nID".utf8));let revised=try #require(await f.model.proposal())
        #expect(revised.hasUnknownSizes && revised.count==1)
        await f.api.configure(.success);await f.model.confirm(revised);#expect(f.model.phase == .accepted(.ours))
        f.model.stop();await f.model.waitForRelease()
    }
    @Test func legacyMigrationAndCorruptSelectionEnvelopePreserveOriginalBytes() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        _=try await f.store.snapshot()
        let file=f.directory.appendingPathComponent("inbox.json")
        for version in 1...4 {
            try JSONSerialization.data(withJSONObject:["version":version,"items":[],"fileSelections":[]]).write(to:file)
            _=try await f.store.snapshot()
            let upgraded=try #require(JSONSerialization.jsonObject(with:Data(contentsOf:file)) as? [String:Any])
            #expect(upgraded["version"] as? Int==5 && upgraded["fileSelections"] is [Any])
        }
        let corrupt=try JSONSerialization.data(withJSONObject:["version":4,"items":[]])
        try corrupt.write(to:file)
        await #expect(throws:PendingInboxError.corrupt) {_=try await f.store.snapshot()}
        #expect(try Data(contentsOf:file)==corrupt)
    }
    @Test func reclaimedLeaseNeverReceivesFreshRejectionAuthority() async throws {
        let f=try fixture();defer {try? FileManager.default.removeItem(at:f.directory)}
        let first=try await f.store.claimFileSelection(server:f.server.profile.serverURL,transferID:"transfer",name:"Fixture")
        let prepared=try await f.store.prepareFileSelection(first.id,token:first.token,revision:selectionRevision,fileIDs:["é"])
        let fresh=try #require(prepared.rejectionToken)
        try await f.store.releaseFileSelection(first.id,token:first.token)
        let next=try await f.store.claimFileSelection(server:f.server.profile.serverURL,transferID:"transfer",name:"Fixture")
        let replay=try await f.store.prepareFileSelection(next.id,token:next.token,revision:selectionRevision,fileIDs:["é"])
        #expect(!replay.wasCreated && replay.intent.id==prepared.intent.id)
        await #expect(throws:PendingInboxError.lostClaim) {try await f.store.rejectFileSelection(first.id,token:first.token,intentID:prepared.intent.id,freshToken:fresh)}
        try await f.store.releaseFileSelection(next.id,token:next.token)
    }
}
