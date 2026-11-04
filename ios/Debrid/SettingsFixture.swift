#if DEBUG
import DebridKit
import SwiftUI

private actor SettingsFixtureAPI: SettingsReviewAPI, OrganizationPreviewAPI {
    nonisolated let serverURL = URL(string: "https://debrid.example.test")!
    private let mode: String
    private var revision = String(repeating: "a", count: 64)
    private var value = SettingsDTO(torrentDefaults: TorrentSettingsDTO(excludeRegex: "\\.sample\\.", finishedAction: "keep", downloadRetries: 3, torrentRetries: 1, unpack: true), categories: ["Documentaries", "Audio"], unpackMaxDepth: 1, organize: OrganizeSettingsDTO(enabled: false))
    init(mode: String) { self.mode = mode }
    func health() async throws -> HealthDTO { HealthDTO(ok: true, version: "fixture", capabilities: mode == "unsupported" ? [] : ["native_settings_revision_v1", "manual_file_selection_v1"]) }
    func settingsReview() async throws -> SettingsReviewDTO {
        if mode == "offline" { throw APIError.connection(.notConnectedToInternet) }
        return SettingsReviewDTO(settings: value, revision: revision)
    }
    func updateSettings(_ settings: SettingsDTO, revision expected: String) async throws -> SettingsReviewDTO {
        if mode == "conflict", revision == String(repeating: "a", count: 64) {
            value = SettingsDTO(torrentDefaults: value.torrentDefaults, categories: ["Audio", "Documentaries", "On server"], unpackMaxDepth: 1, organize: value.organize)
            revision = String(repeating: "b", count: 64); throw APIError.http(status: 412)
        }
        guard expected == revision else { throw APIError.http(status: 412) }
        value = settings; revision = String(repeating: "c", count: 64)
        if mode == "unknown" { throw APIError.connection(.timedOut) }
        return SettingsReviewDTO(settings: value, revision: revision)
    }
    func previewOrganization(name: String, movieTemplate: String, tvTemplate: String) async throws -> OrganizationPreviewDTO {
        try JSONDecoder().decode(OrganizationPreviewDTO.self, from: Data(#"{"path":"The Open Ocean (2024)","organized":true}"#.utf8))
    }
}
struct SettingsFixtureRoot: View {
    let mode: String
    @State private var server: AuthenticatedServer?
    @State private var api: SettingsFixtureAPI?
    @State private var providers: ProviderAccountsModel?
    @State private var providerAPI: ProviderFixtureAPI?
    @State private var tab = 2
    var body: some View {
        Group { if let server, let api, let providers, let providerAPI {
                TabView(selection: $tab) {
                    Tab("Transfers", systemImage: "arrow.down.circle", value: 0) { LiveTransfersPreview() }
                    Tab("Providers", systemImage: "externaldrive.connected.to.line.below", value: 1) { LiveProviders(server: server, model: providers, editingAPI: providerAPI) }
                    Tab("Settings", systemImage: "gearshape", value: 2) { LiveSettings(server: server, api: api) }
                }.tint(DebridStyle.tint)
            } else { ProgressView("Preparing sample settings…") } }
            .preferredColorScheme(mode == "light" ? .light : .dark)
            .task {
                guard server == nil else { return }
                if let profile = try? ServerProfile(server: ServerAddress("https://debrid.example.test")), let credential = try? APICredential.session("fixture_unused") {
                    let connection = AuthenticatedServer(profile: profile, username: "Sample User", credential: credential)
                    let provider = ProviderFixtureAPI(mode: "normal")
                    providers = try? ProviderAccountsModel(server: connection, api: provider)
                    providerAPI = provider; server = connection; api = SettingsFixtureAPI(mode: mode)
                }
            }
    }
}
#endif
