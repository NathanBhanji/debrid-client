import Foundation
import Testing
@testable import DebridKit

private func editorAccount(name: String = "Home", updated: String = "2026-01-01T00:00:00Z", id: String = "account-one") throws -> AccountDTO {
    try DebridAPIClient.decoder().decode(AccountDTO.self, from: JSONSerialization.data(withJSONObject: ["id":id,"kind":"torbox","name":name,"enabled":true,"is_default":false,"created_at":"2026-01-01T00:00:00Z","updated_at":updated]))
}
private actor EditorAPI: ProviderManagingAPI {
    nonisolated let serverURL = URL(string: "https://editor.example.test")!
    var row: AccountDTO?
    var failure: APIError?
    var cap = true
    var heldRead = false
    var pendingRead: CheckedContinuation<Void, Never>?
    var readWaiter: CheckedContinuation<Void, Never>?
    private(set) var writes = 0
    private(set) var forces: [Bool] = []
    init() throws { row = try editorAccount() }
    func configure(failure: APIError? = nil, cap: Bool = true, row: AccountDTO? = nil) { self.failure = failure; self.cap = cap; if let row { self.row = row } }
    func health() async throws -> HealthDTO { HealthDTO(ok:true,version:"fixture",capabilities:cap ? ["native_account_removal_drain_v1"] : []) }
    func accounts() async throws -> [AccountDTO] { row.map { [$0] } ?? [] }
    func holdRead() { heldRead = true }
    func waitForRead() async { if pendingRead != nil { return }; await withCheckedContinuation { readWaiter = $0 } }
    func releaseRead() { pendingRead?.resume(); pendingRead = nil; heldRead = false }
    func account(id: String) async throws -> AccountDTO {
        if heldRead { await withCheckedContinuation { pendingRead = $0; readWaiter?.resume(); readWaiter = nil } }
        try Task.checkCancellation()
        guard let row else { throw APIError.http(status:404) }; return row }
    func checkAccount(id: String) async throws -> ProviderUserDTO { throw APIError.invalidResponse }
    func addAccount(_ input: ProviderAccountWrite) async throws -> AccountDTO {
        writes += 1; let result = try editorAccount(name:input.name ?? ""); row = result
        if let failure { throw failure }; return result
    }
    func updateAccount(id: String, input: ProviderAccountWrite) async throws -> AccountDTO { try await addAccount(input) }
    func removeAccount(id: String, includingTransfers: Bool) async throws { writes += 1; forces.append(includingTransfers); row = nil; if let failure { throw failure } }
}
@MainActor struct ProviderEditingTests {
    private func fixture(edit: Bool = false) async throws -> (ProviderEditorModel,EditorAPI) {
        let api = try EditorAPI()
        let server = AuthenticatedServer(profile:try ServerProfile(server:ServerAddress(api.serverURL.absoluteString)),username:"Fixture",credential:try .session("unused"))
        return (try ProviderEditorModel(server:server,original:edit ? await api.row : nil,api:api),api)
    }
    @Test func writeOnlyCredentialEncodingAndReflection() throws {
        let input=ProviderAccountWrite(kind:"torbox",name:"Home",apiKey:"fixture-sensitive")
        let object=try #require(JSONSerialization.jsonObject(with:JSONEncoder().encode(input)) as? [String:Any])
        #expect((object["credentials"] as? [String:String]) == ["api_key":"fixture-sensitive"])
        #expect(!String(reflecting:input).contains("fixture-sensitive") && Mirror(reflecting:input).children.isEmpty)
        let noSecret=try #require(JSONSerialization.jsonObject(with:JSONEncoder().encode(ProviderAccountWrite(name:"Rename"))) as? [String:Any])
        #expect(noSecret["credentials"] == nil && noSecret["enabled"] == nil)
    }
    @Test func createValidatesAndClearsSecretBeforeReturning() async throws {
        let (model,api)=try await fixture(); #expect(!model.canSave)
        model.name="Home"; model.secret="fixture-key"; #expect(model.canSave)
        await model.save(); #expect(model.phase == .saved && model.secret.isEmpty && !model.canSave)
        await model.save(); #expect(await api.writes == 1)
        #expect(Mirror(reflecting:model).children.isEmpty)
    }
    @Test func unknownCreateOnlyObservesNeverResubmits() async throws {
        let (model,api)=try await fixture(); model.name="Home"; model.secret="fixture-key"
        await api.configure(failure:.connection(.timedOut)); await model.save()
        #expect(model.phase == .unknown && model.secret.isEmpty)
        await model.save(); await model.checkResult()
        #expect(await api.writes == 1 && model.phase == .observed && model.observedAccount?.name == "Home")
    }
    @Test func editedAccountBlocksWriteAndCredentialsRemainUnchangedByDefault() async throws {
        let (model,api)=try await fixture(edit:true); model.name="Renamed"
        #expect(!model.replaceCredentials && model.secret.isEmpty)
        await api.configure(row:try editorAccount(updated:"2026-01-01T00:00:01Z")); await model.save()
        #expect(model.phase == .editing && model.message?.contains("changed") == true)
        #expect(await api.writes == 0)
    }
    @Test func forceRequiresCapabilityAndNeverHappensByDefault() async throws {
        let (model,api)=try await fixture(edit:true); #expect(!model.includingTransfers)
        model.includingTransfers=true; await api.configure(cap:false); await model.loadCapabilities(); await model.remove()
        #expect(await api.writes == 0)
        await api.configure(); await model.loadCapabilities(); await model.remove()
        #expect(model.phase == .removed); #expect(await api.forces == [true])
    }
    @Test func stoppedActionsClearCredentialsAndCannotSend() async throws {
        let (model,api)=try await fixture(); model.name="Home"; model.secret="fixture-key"; model.stop()
        await model.save(); #expect(model.secret.isEmpty); #expect(await api.writes == 0)
    }
    @Test func queuedDoubleTapThenStopCannotSubmitAfterHeldPreflight() async throws {
        let (model,api)=try await fixture(edit:true);model.name="Renamed";await api.holdRead()
        let first=Task { await model.save() };await api.waitForRead()
        await model.save();first.cancel();model.stop();await api.releaseRead();await first.value
        #expect(await api.writes == 0);#expect(model.phase == .unknown && model.secret.isEmpty)
    }
    @Test func uncertainRemovalAbsenceIsObservationOnly() async throws {
        let (model,api)=try await fixture(edit:true); await api.configure(failure:.connection(.timedOut)); await model.remove()
        #expect(model.phase == .unknown); await model.checkResult()
        #expect(model.phase == .observed && model.observedAccount == nil && model.message?.contains("could not be confirmed") == true)
        #expect(await api.forces == [false])
    }
}
