import Foundation
import Observation

@Observable @MainActor public final class RuntimeSettingsModel {
    public enum Phase { case loading, editing, unsupported, failed, saved, conflict, unknown }
    public enum MergeChoice: String, CaseIterable { case mine, server }
    public private(set) var phase: Phase = .loading
    public var draft = RuntimeSettingsDraft.placeholder
    public var choices: [RuntimeSettingsSection: MergeChoice] = [:]
    public private(set) var latest: SettingsReviewDTO?
    public private(set) var baseline: SettingsReviewDTO?
    public private(set) var message: String?
    public private(set) var isBusy = false
    public private(set) var requiresSignIn = false
    public private(set) var previewPath: String?
    public private(set) var previewMessage: String?
    public let connectionID: UUID
    private let api: any SettingsReviewAPI
    private let current: @MainActor () -> Bool
    private var active = true
    private var generation = 0
    private var previewGeneration = 0
    public var isDirty: Bool { baseline.map { draft != RuntimeSettingsDraft($0.settings) } ?? false }
    public var validationMessage: String? {
        guard baseline != nil, isDirty, phase == .editing else { return nil }
        do { _ = try draft.output(); return nil }
        catch SettingsDraftError.invalidNumber { return "Use nonnegative whole numbers for size and retry limits." }
        catch SettingsDraftError.invalidCategory { return "Each category must be a plain folder name." }
        catch { return "A value is too long or unsupported. Check the settings before review." }
    }
    public var canReview: Bool { phase == .editing && !isBusy && isDirty && (try? draft.output()) != nil }
    public var conflicts: [RuntimeSettingsSection] {
        guard let baseline, let latest else { return [] }
        let base = RuntimeSettingsDraft(baseline.settings), server = RuntimeSettingsDraft(latest.settings)
        return RuntimeSettingsSection.allCases.filter { !draft.matches(base, section: $0) && !server.matches(base, section: $0) && !draft.matches(server, section: $0) }
    }
    public init(server: AuthenticatedServer, api: any SettingsReviewAPI, current: @escaping @MainActor () -> Bool = { true }) throws {
        guard server.profile.serverURL == api.serverURL else { throw APIError.invalidConfiguration }
        connectionID = server.connectionID; self.api = api; self.current = current
    }
    private func valid(_ op: Int) -> Bool { active && current() && !Task.isCancelled && generation == op }
    private func checked(_ view: SettingsReviewDTO) throws -> SettingsReviewDTO {
        let categories = view.settings.categories ?? []
        guard FileSelectionIdentity.revisionIsValid(view.revision), categories.count <= 256, Set(categories.map { Data($0.utf8) }).count == categories.count else { throw APIError.invalidResponse }
        return view
    }
    public func start() { if !Task.isCancelled && current() { active = true } }
    public func stop() {
        active = false; generation &+= 1; previewGeneration &+= 1
        if phase == .loading { phase = .failed; message = "Loading paused. Try again when connected." }
        if isBusy, phase == .editing { phase = .unknown; message = "A save may have reached the server. Check its state before saving again." }
    }
    public func load() async {
        guard active, current(), !Task.isCancelled, !isBusy, baseline == nil else { return }
        isBusy = true; phase = .loading; let op = generation
        defer { isBusy = false }
        do {
            let health = try await api.health(); guard valid(op) else { return }
            guard health.ok, health.capabilities?.contains("native_settings_revision_v1") == true else { phase = .unsupported; return }
            let view = try checked(await api.settingsReview()); guard valid(op) else { return }
            baseline = view; draft = RuntimeSettingsDraft(view.settings); phase = .editing; message = nil
        } catch { guard valid(op) else { return }; requiresSignIn = error as? APIError == .unauthorized; phase = .failed; message = ServerConnectionModel.message(for: error) }
    }
    public func save() async {
        guard active, current(), !Task.isCancelled, canReview, let baseline, let proposed = try? draft.output() else { return }
        let op = generation; isBusy = true; message = nil
        defer { isBusy = false }
        do {
            let saved = try checked(await api.updateSettings(proposed, revision: baseline.revision)); guard valid(op) else { return }
            guard saved.revision != baseline.revision else { throw APIError.invalidResponse }
            self.baseline = saved; draft = RuntimeSettingsDraft(saved.settings); phase = .saved
        } catch {
            guard valid(op) else { return }
            requiresSignIn = error as? APIError == .unauthorized
            if error as? APIError == .http(status: 412) {
                phase = .conflict; message = "Settings changed on the server. Review both versions before saving."
                _ = await readLatest(op)
            } else if error as? APIError == .http(status: 422) {
                message = "The server rejected these settings. Check filters, durations and templates. Nothing was saved."
            } else if error as? APIError == .unauthorized || error as? APIError == .forbidden {
                message = "This session cannot save settings. Sign in again."
            } else { phase = .unknown; message = "The save result is unknown. No request will be repeated automatically." }
        }
    }
    private func readLatest(_ op: Int) async -> Bool {
        latest = nil; choices = [:]
        do { let fresh = try checked(await api.settingsReview()); guard valid(op) else { return false }; latest = fresh; return true }
        catch { guard valid(op) else { return false }; requiresSignIn = error as? APIError == .unauthorized; message = "The latest settings could not be loaded. Reconnect and check again."; return false }
    }
    public func checkLatest() async {
        guard active, current(), !Task.isCancelled, !isBusy, phase == .unknown || phase == .conflict else { return }
        let op = generation; isBusy = true; defer { isBusy = false }
        guard await readLatest(op), valid(op) else { return }; phase = .conflict
        message = "These are the current server settings. Review the combined draft; no save was repeated."
    }
    public func mergeForReview() {
        guard active, current(), !isBusy, phase == .conflict, let baseline, let latest, conflicts.allSatisfy({ choices[$0] != nil }) else { return }
        let old = RuntimeSettingsDraft(baseline.settings), server = RuntimeSettingsDraft(latest.settings)
        var merged = server
        for section in RuntimeSettingsSection.allCases {
            if choices[section] == .mine || (!draft.matches(old, section: section) && (server.matches(old, section: section) || draft.matches(server, section: section))) { merged.copy(section, from: draft) }
        }
        self.baseline = latest; self.latest = nil; draft = merged; choices = [:]; phase = .editing
        message = isDirty ? "Review the merged settings, then save explicitly." : "The current server already matches this draft. No save was repeated."
    }
    public func continueEditing() { if !isBusy && phase == .saved { phase = .editing } }
    public func invalidatePreview() { previewGeneration &+= 1; previewPath = nil; previewMessage = nil }
    public func preview(name: String) async {
        guard active, current(), !Task.isCancelled, let api = api as? any OrganizationPreviewAPI, !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        guard name.utf8.count <= 2048, draft.movieTemplate.utf8.count <= 2048, draft.tvTemplate.utf8.count <= 2048 else { previewMessage = "The name or template is too long to preview."; return }
        previewGeneration &+= 1; let previewOp = previewGeneration, op = generation
        let movie = draft.movieTemplate, tv = draft.tvTemplate
        do {
            let result = try await api.previewOrganization(name: name, movieTemplate: movie, tvTemplate: tv)
            guard valid(op), previewGeneration == previewOp, draft.movieTemplate == movie, draft.tvTemplate == tv else { return }
            guard !result.path.isEmpty, result.path.utf8.count <= 4096 else { throw APIError.invalidResponse }
            previewPath = result.path; previewMessage = result.organized ? nil : "The name did not match a supported movie or episode format."
        } catch { guard valid(op), previewGeneration == previewOp else { return }; previewPath = nil; previewMessage = "Preview unavailable. Check the release name and templates."; requiresSignIn = error as? APIError == .unauthorized }
    }
}
public struct OrganizationPreviewDTO: Decodable, Sendable { public let path: String; public let organized: Bool }
public protocol OrganizationPreviewAPI: Sendable { func previewOrganization(name: String, movieTemplate: String, tvTemplate: String) async throws -> OrganizationPreviewDTO }
extension DebridAPIClient: OrganizationPreviewAPI {
    public func previewOrganization(name: String, movieTemplate: String, tvTemplate: String) async throws -> OrganizationPreviewDTO {
        guard !name.isEmpty, name.utf8.count <= 2048, movieTemplate.utf8.count <= 2048, tvTemplate.utf8.count <= 2048 else { throw APIError.invalidConfiguration }
        struct Body: Encodable {
            let name: String; let movieTemplate: String; let tvTemplate: String
            enum CodingKeys: String, CodingKey { case name; case movieTemplate = "movie_template"; case tvTemplate = "tv_template" }
        }
        return try await send(["organize", "preview"], method: "POST", body: JSONEncoder().encode(Body(name: name, movieTemplate: movieTemplate, tvTemplate: tvTemplate)), expectedStatus: 200)
    }
}
