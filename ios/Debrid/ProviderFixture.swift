#if DEBUG
import DebridKit
import SwiftUI

private actor ProviderFixtureAPI: ProviderAccountAPI {
    nonisolated let serverURL = URL(string: "https://debrid.example.test")!
    let mode: String
    init(mode: String) { self.mode = mode }
    private func decode<T: Decodable>(_ type: T.Type, _ object: Any) throws -> T {
        let decoder = JSONDecoder(); decoder.keyDecodingStrategy = .convertFromSnakeCase; decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(type, from: JSONSerialization.data(withJSONObject: object))
    }
    func accounts() async throws -> [AccountDTO] {
        if mode == "offline" { throw APIError.connection(.notConnectedToInternet) }
        if mode == "empty" { return [] }
        return try decode([AccountDTO].self, [
            ["id":"fixture-torbox", "kind":"torbox", "name":"Home library", "enabled":true, "is_default":true, "created_at":"2026-01-01T00:00:00Z", "updated_at":"2026-01-01T00:00:00Z"],
            ["id":"fixture-rd", "kind":"realdebrid", "name":"Real-Debrid", "enabled":true, "is_default":false, "created_at":"2026-01-01T00:00:00Z", "updated_at":"2026-01-01T00:00:00Z"],
            ["id":"fixture-premiumize", "kind":"premiumize", "name":"Travel backup", "enabled":false, "is_default":false, "created_at":"2026-01-01T00:00:00Z", "updated_at":"2026-01-01T00:00:00Z"]
        ])
    }
    func account(id: String) async throws -> AccountDTO {
        guard let row = try await accounts().first(where: { $0.id == id }) else { throw APIError.http(status: 404) }
        return row
    }
    func checkAccount(id: String) async throws -> ProviderUserDTO {
        if mode == "failure" { throw APIError.http(status: 422) }
        return try decode(ProviderUserDTO.self, ["username":"ocean.library", "premium":true, "plan":"Pro", "expires_at":"2027-04-18T00:00:00Z"])
    }
}
struct ProviderFixtureRoot: View {
    let mode: String
    @State private var model: ProviderAccountsModel?
    @State private var server: AuthenticatedServer?
    @State private var tab = 1
    var body: some View {
        Group {
            if let model, let server {
                TabView(selection: $tab) {
                    Tab("Transfers", systemImage: "arrow.down.circle", value: 0) { LiveTransfersPreview() }
                    Tab("Providers", systemImage: "externaldrive.connected.to.line.below", value: 1) { LiveProviders(server: server, model: model) }
                    Tab("Settings", systemImage: "gearshape", value: 2) { ConnectionSettings(server: server, auth: nil) }
                }.tint(DebridStyle.tint)
            } else { ProgressView("Preparing sample accounts…") }
        }.preferredColorScheme(mode == "light" ? .light : .dark)
        .task {
            guard model == nil else { return }
            do {
                let api = ProviderFixtureAPI(mode: mode)
                let value = AuthenticatedServer(profile: try ServerProfile(server: ServerAddress(api.serverURL.absoluteString)), username: "Sample User", credential: try .session("fixture_unused"))
                model = try ProviderAccountsModel(server: value, api: api); server = value
            } catch { assertionFailure("Invalid provider fixture") }
        }
    }
}
#endif
