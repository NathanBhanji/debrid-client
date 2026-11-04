import Foundation
import Testing
@testable import DebridKit

private func settingsFixture(categories: [String] = ["Original"], organization: Bool = false) -> SettingsDTO {
    SettingsDTO(torrentDefaults: TorrentSettingsDTO(manualFiles: ["preserved-id"], finishedAction: "keep", downloadRetries: 3, torrentRetries: 1, unpack: true), categories: categories, unpackMaxDepth: 1, organize: OrganizeSettingsDTO(enabled: organization))
}
private actor RuntimeTestAPI: SettingsReviewAPI, OrganizationPreviewAPI {
    nonisolated let serverURL = URL(string: "https://settings.example.test")!
    var view = SettingsReviewDTO(settings: settingsFixture(), revision: String(repeating: "a", count: 64))
    var supported = true
    var conflict: SettingsDTO?
    var error: APIError?
    var readError: APIError?
    var hold = false
    var waiting: CheckedContinuation<Void, Never>?
    var waiter: CheckedContinuation<Void, Never>?
    private(set) var revisions: [String] = []
    func configure(conflict: SettingsDTO? = nil, error: APIError? = nil, readError: APIError? = nil, hold: Bool = false, supported: Bool = true) { self.conflict = conflict; self.error = error; self.readError = readError; self.hold = hold; self.supported = supported }
    func setView(_ view: SettingsReviewDTO) { self.view = view }
    func waitUntilHeld() async { if waiting != nil { return }; await withCheckedContinuation { waiter = $0 } }
    func release() { waiting?.resume(); waiting = nil; hold = false }
    private func barrier() async { if hold { await withCheckedContinuation { waiting = $0; waiter?.resume(); waiter = nil } } }
    func health() async throws -> HealthDTO { await barrier(); return HealthDTO(ok: true, version: "fixture", capabilities: supported ? ["native_settings_revision_v1", "manual_file_selection_v1"] : []) }
    func settingsReview() async throws -> SettingsReviewDTO { if let readError { throw readError }; return view }
    func updateSettings(_ settings: SettingsDTO, revision: String) async throws -> SettingsReviewDTO {
        revisions.append(revision); await barrier()
        if let conflict { view = SettingsReviewDTO(settings: conflict, revision: String(repeating: "b", count: 64)); self.conflict = nil; throw APIError.http(status: 412) }
        if error == .http(status: 422) || error == .unauthorized { throw error! }
        view = SettingsReviewDTO(settings: settings, revision: String(repeating: "c", count: 64))
        if let error { throw error }; return view
    }
    func previewOrganization(name: String, movieTemplate: String, tvTemplate: String) async throws -> OrganizationPreviewDTO { await barrier(); return try JSONDecoder().decode(OrganizationPreviewDTO.self, from: Data(#"{"path":"Preview folder","organized":true}"#.utf8)) }
}
@MainActor @Suite(.timeLimit(.minutes(1))) struct RuntimeSettingsTests {
    private func fixture() throws -> (RuntimeSettingsModel, RuntimeTestAPI) {
        let api = RuntimeTestAPI(); let server = AuthenticatedServer(profile: try ServerProfile(server: ServerAddress(api.serverURL.absoluteString)), username: "Fixture", credential: try .session("unused"))
        return (try RuntimeSettingsModel(server: server, api: api), api)
    }
    @Test func readNeverWritesAndUnrelatedSavePreservesPolicyFieldsAndByteDistinctCategories() async throws {
        let (model,api) = try fixture(); let categories = ["Caf\u{00e9}", "Cafe\u{0301}"]
        await api.setView(SettingsReviewDTO(settings: settingsFixture(categories: categories), revision: String(repeating: "a", count: 64)))
        await model.load(); #expect(!model.isDirty && !model.canReview); #expect(await api.revisions.isEmpty)
        model.draft.unpackMaxDepth = 2; await model.save()
        #expect(model.phase == .saved && model.draft.categories.count == 2)
        let saved = await api.view.settings
        #expect(Set((saved.categories ?? []).map { Data($0.utf8) }) == Set(categories.map { Data($0.utf8) }))
        #expect(saved.torrentDefaults.manualFiles == ["preserved-id"] && saved.torrentDefaults.organize == nil)
    }
    @Test func independentSectionsMergeWithoutAutomaticSave() async throws {
        let (model,api) = try fixture(); await model.load(); model.draft.categories.append("Mine")
        await api.configure(conflict: settingsFixture(organization: true)); await model.save()
        #expect(model.phase == .conflict && model.conflicts.isEmpty && model.draft.categories.contains("Mine"))
        model.mergeForReview(); #expect(model.phase == .editing && model.draft.organizationEnabled && model.draft.categories.contains("Mine") && model.canReview)
        #expect(await api.revisions.count == 1); await model.save(); #expect(await api.revisions == [String(repeating:"a",count:64),String(repeating:"b",count:64)])
    }
    @Test func competingCategoryBytesRequireExplicitChoice() async throws {
        let (model,api) = try fixture(); await model.load(); model.draft.categories = ["Cafe\u{0301}"]
        await api.configure(conflict: settingsFixture(categories: ["Caf\u{00e9}"])); await model.save()
        #expect(model.conflicts == [.categories]); model.mergeForReview(); #expect(model.phase == .conflict)
        model.choices[.categories] = .mine; model.mergeForReview()
        #expect(model.phase == .editing && Data(model.draft.categories[0].utf8) == Data("Cafe\u{0301}".utf8))
        #expect(await api.revisions.count == 1)
    }
    @Test func lostResponseOnlyObservesAndDoesNotClaimAcceptance() async throws {
        let (model,api) = try fixture(); await model.load(); model.draft.categories.append("Mine")
        await api.configure(error: .connection(.timedOut)); await model.save(); #expect(model.phase == .unknown)
        await model.save(); await model.checkLatest(); #expect(model.phase == .conflict)
        model.mergeForReview(); #expect(!model.isDirty && model.phase == .editing); #expect(await api.revisions.count == 1)
        #expect(model.message?.contains("No save was repeated") == true)
    }
    @Test func resumedInitialLoadRemainsRetryableAfterOldRequestDrains() async throws {
        let (model,api) = try fixture(); await api.configure(hold: true)
        let load = Task { await model.load() }; await api.waitUntilHeld()
        model.stop(); model.start(); await model.load(); await api.release(); await load.value
        #expect(model.phase == .failed && model.baseline == nil && !model.isBusy)
        await model.load(); #expect(model.phase == .editing && model.baseline != nil)
    }
    @Test func duplicateSaveAndStoppedLateUnauthorizedCannotAffectSession() async throws {
        let (model,api) = try fixture(); await model.load(); model.draft.categories.append("Mine"); await api.configure(error: .unauthorized, hold: true)
        let task = Task { await model.save() }; await api.waitUntilHeld(); await model.save()
        model.stop(); await api.release(); await task.value
        #expect(model.phase == .unknown && !model.requiresSignIn); #expect(await api.revisions.count == 1)
    }
    @Test func failedConflictRefreshDoesNotPresentStaleStateAsCurrent() async throws {
        let (model,api) = try fixture(); await model.load(); model.draft.categories.append("Mine")
        await api.configure(conflict: settingsFixture(categories: ["Server"])); await model.save(); #expect(model.latest != nil)
        await api.configure(readError: .connection(.notConnectedToInternet)); await model.checkLatest()
        #expect(model.latest == nil && model.message?.contains("could not be loaded") == true)
    }
    @Test func editedTemplateInvalidatesDelayedPreviewAndValidationIsLocal() async throws {
        let (model,api) = try fixture(); await model.load(); await api.configure(hold: true)
        let preview = Task { await model.preview(name: "Sample.Movie.2024") }; await api.waitUntilHeld()
        model.draft.movieTemplate = "Different/{title}"; model.invalidatePreview(); await api.release(); await preview.value
        #expect(model.previewPath == nil)
        model.draft.policy.downloadRetries = "-1"; #expect(!model.canReview)
        model.draft.policy.downloadRetries = "3"; model.draft.categories = ["../escape"]; #expect(!model.canReview)
    }
    @Test func unsupportedServerCannotSave() async throws {
        let (model,api) = try fixture(); await api.configure(supported: false); await model.load()
        #expect(model.phase == .unsupported && model.baseline == nil); await model.save(); #expect(await api.revisions.isEmpty)
    }
}
