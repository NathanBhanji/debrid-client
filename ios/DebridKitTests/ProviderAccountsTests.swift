import Foundation
import Testing
@testable import DebridKit

private func accountFixture(_ id: String = "account-one", updated: String = "2026-01-02T03:04:05Z", kind: String = "torbox") throws -> AccountDTO {
    try DebridAPIClient.decoder().decode(AccountDTO.self, from: JSONSerialization.data(withJSONObject: ["id": id, "kind": kind, "name": "Home", "enabled": true, "is_default": true, "created_at": "2026-01-01T00:00:00Z", "updated_at": updated]))
}
private actor AccountTestAPI: ProviderAccountAPI {
    nonisolated let serverURL = URL(string: "https://providers.example.test")!
    var rows: [AccountDTO]
    var error: APIError?
    var listError: APIError?
    var changed = false
    var held = false
    var checkContinuation: CheckedContinuation<Void, Never>?
    var waiter: CheckedContinuation<Void, Never>?
    private(set) var calls = 0
    init() throws { rows = [try accountFixture()] }
    func accounts() async throws -> [AccountDTO] { if let listError { throw listError }; return rows }
    func account(id: String) async throws -> AccountDTO { try #require(rows.first { $0.id == id }) }
    func checkAccount(id: String) async throws -> ProviderUserDTO {
        calls += 1
        if held { await withCheckedContinuation { checkContinuation = $0; waiter?.resume(); waiter = nil } }
        if let error { throw error }
        if changed { rows = [try accountFixture(updated: "2026-01-02T03:04:06Z")] }
        return try DebridAPIClient.decoder().decode(ProviderUserDTO.self, from: Data(#"{"premium":false}"#.utf8))
    }
    func configure(error: APIError? = nil, listError: APIError? = nil, changed: Bool = false, held: Bool = false) {
        self.error = error; self.listError = listError; self.changed = changed; self.held = held
    }
    func setRows(_ rows: [AccountDTO]) { self.rows = rows }
    func waitForCheck() async { if checkContinuation != nil { return }; await withCheckedContinuation { waiter = $0 } }
    func release() { checkContinuation?.resume(); checkContinuation = nil }
}

@MainActor struct ProviderAccountsTests {
    private func fixture() throws -> (ProviderAccountsModel, AccountTestAPI) {
        let api = try AccountTestAPI()
        let server = AuthenticatedServer(profile: try ServerProfile(server: ServerAddress(api.serverURL.absoluteString)), username: "Fixture", credential: try .session("unused"))
        return (try ProviderAccountsModel(server: server, api: api), api)
    }
    @Test func browsingNeverChecksCredentialsAndFreeUnknownFieldsRemainUnknown() async throws {
        let (model, api) = try fixture(); await model.refresh()
        #expect(model.accounts.count == 1 && model.hasLoaded && !model.isStale)
        #expect(await api.calls == 0 && model.checks.isEmpty)
        await model.check(id: "account-one")
        let result = try #require(model.checks["account-one"])
        #expect(!result.user.premium && result.user.expiresAt == nil && result.user.plan == nil)
        #expect(await api.calls == 1)
    }
    @Test func failedCheckRetainsDatedSuccessAndOtherAccounts() async throws {
        let (model, api) = try fixture()
        await api.setRows([try accountFixture(), try accountFixture("second")]); await model.refresh()
        await model.check(id: "account-one")
        let checked = model.checks["account-one"]?.checkedAt
        await api.configure(error: .http(status: 422)); await model.check(id: "account-one")
        #expect(model.checkErrors["account-one"] != nil && model.checks["account-one"]?.checkedAt == checked)
        #expect(model.accounts.count == 2 && model.checkingID == nil)
    }
    @Test func accountChangedDuringCheckCannotPublishResult() async throws {
        let (model, api) = try fixture(); await model.refresh()
        await api.configure(changed: true); await model.check(id: "account-one")
        #expect(model.checks.isEmpty && model.checkErrors["account-one"]?.contains("changed") == true)
    }
    @Test func refreshingEditedAndRemovedAccountsInvalidatesOldHealth() async throws {
        let (model, api) = try fixture(); await model.refresh(); await model.check(id: "account-one")
        await api.setRows([try accountFixture(updated: "2026-01-02T03:05:05Z")]); await model.refresh()
        #expect(model.checks.isEmpty)
        await api.setRows([]); await model.refresh()
        #expect(model.accounts.isEmpty && model.hasLoaded)
    }
    @Test func offlineAndDuplicateResponsesPreserveLastSnapshot() async throws {
        let (model, api) = try fixture(); await model.refresh()
        await api.configure(listError: .connection(.notConnectedToInternet)); await model.refresh()
        #expect(model.isStale && model.accounts.count == 1 && model.message?.contains("offline") == true)
        await api.configure(); await api.setRows([try accountFixture(), try accountFixture()]); await model.refresh()
        #expect(model.isStale && model.accounts.count == 1)
    }
    @Test func doubleTapAndStoppedLateUnauthorizedAreFenced() async throws {
        let (model, api) = try fixture(); await model.refresh(); await api.configure(error: .unauthorized, held: true)
        let task = Task { await model.check(id: "account-one") }; await api.waitForCheck()
        await model.refresh()
        await model.check(id: "account-one"); #expect(await api.calls == 1)
        model.stop(); model.start()
        await model.check(id: "account-one"); #expect(await api.calls == 1)
        model.stop(); await api.release(); await task.value
        #expect(!model.requiresSignIn && model.checks.isEmpty && model.checkErrors.isEmpty)
        #expect(model.checkingID == nil && model.isStale)
    }
    @Test func cancelledTaskAndChangedConnectionDiscardSuccess() async throws {
        let api = try AccountTestAPI()
        let server = AuthenticatedServer(profile: try ServerProfile(server: ServerAddress(api.serverURL.absoluteString)), username: "Fixture", credential: try .session("unused"))
        var current = true
        let model = try ProviderAccountsModel(server: server, api: api, isCurrent: { current })
        await model.refresh(); await api.configure(held: true)
        let task = Task { await model.check(id: "account-one") }; await api.waitForCheck()
        current = false; await api.release(); await task.value
        #expect(model.checks.isEmpty && !model.requiresSignIn)
        current = true; await api.configure(held: true)
        let cancelled = Task { await model.check(id: "account-one") }; await api.waitForCheck()
        cancelled.cancel(); await api.release(); await cancelled.value
        #expect(model.checks.isEmpty && model.checkErrors.isEmpty)
    }
    @Test func stoppedViewCannotStartQueuedActionsUntilExplicitlyResumed() async throws {
        let (model, api) = try fixture(); await model.refresh(); model.stop()
        await model.refresh(); await model.check(id: "account-one")
        #expect(await api.calls == 0 && !model.isActive)
        model.start(); await model.check(id: "account-one")
        #expect(await api.calls == 1 && model.isActive)
    }
    @Test func currentUnauthorizedRequestsSignInAndServerMismatchFails() async throws {
        let (model, api) = try fixture(); await model.refresh(); await api.configure(error: .unauthorized)
        await model.check(id: "account-one"); #expect(model.requiresSignIn)
        let other = AuthenticatedServer(profile: try ServerProfile(server: ServerAddress("https://other.example.test")), username: "Fixture", credential: try .session("unused"))
        #expect(throws: APIError.invalidConfiguration) { try ProviderAccountsModel(server: other, api: api) }
    }
}
