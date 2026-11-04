#if DEBUG
import DebridKit
import SwiftUI

actor ProviderFixtureAPI: ProviderManagingAPI {
    nonisolated let serverURL = URL(string: "https://debrid.example.test")!
    private var stored: [AccountDTO]?
    let mode: String
    init(mode: String) { self.mode = mode }
    private func decode<T: Decodable>(_ type: T.Type, _ object: Any) throws -> T {
        let decoder = JSONDecoder(); decoder.keyDecodingStrategy = .convertFromSnakeCase; decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(type, from: JSONSerialization.data(withJSONObject: object))
    }
    func accounts() async throws -> [AccountDTO] {
        if let stored { return stored }
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
    func health() async throws -> HealthDTO { HealthDTO(ok: true, version: "fixture", capabilities: ["native_account_removal_drain_v1"]) }
    private func object(_ row: AccountDTO) -> [String: Any] {
        ["id":row.id,"kind":row.kind,"name":row.name,"enabled":row.enabled,"is_default":row.isDefault,"created_at":row.createdAt.ISO8601Format(),"updated_at":row.updatedAt.ISO8601Format()]
    }
    private func write(_ input: ProviderAccountWrite, id: String?) async throws -> AccountDTO {
        let data = try JSONEncoder().encode(input)
        let fields = try JSONSerialization.jsonObject(with: data) as! [String: Any]
        var rows = try await accounts().map { object($0) }
        let existing = rows.first { $0["id"] as? String == id }
        var row = existing ?? ["id":UUID().uuidString.lowercased(),"created_at":"2026-01-01T00:00:00Z","enabled":true,"is_default":rows.isEmpty]
        if let name = fields["name"] { row["name"] = name }
        if let kind = fields["kind"] { row["kind"] = kind }
        if let enabled = fields["enabled"] { row["enabled"] = enabled }
        if fields["set_default"] as? Bool == true {
            for index in rows.indices { rows[index]["is_default"] = false }
            row["is_default"] = true
        }
        row["updated_at"] = Date().ISO8601Format()
        rows.removeAll { $0["id"] as? String == id }; rows.append(row)
        stored = try decode([AccountDTO].self, rows)
        if mode == "mutation-unknown" { throw APIError.connection(.timedOut) }
        return try decode(AccountDTO.self, row)
    }
    func addAccount(_ input: ProviderAccountWrite) async throws -> AccountDTO { try await write(input, id: nil) }
    func updateAccount(id: String, input: ProviderAccountWrite) async throws -> AccountDTO { try await write(input, id: id) }
    func removeAccount(id: String, includingTransfers: Bool) async throws {
        stored = try await accounts().filter { $0.id != id }
        if mode == "mutation-unknown" { throw APIError.connection(.timedOut) }
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
    @State private var api: ProviderFixtureAPI?
    var body: some View {
        Group {
            if let model, let server, let api {
                TabView(selection: $tab) {
                    Tab("Transfers", systemImage: "arrow.down.circle", value: 0) { LiveTransfersPreview() }
                    Tab("Providers", systemImage: "externaldrive.connected.to.line.below", value: 1) { LiveProviders(server: server, model: model, editingAPI: api) }
                    Tab("Settings", systemImage: "gearshape", value: 2) { ConnectionSettings(server: server, auth: nil) }
                }.tint(DebridStyle.tint)
            } else { ProgressView("Preparing sample accounts…") }
        }.preferredColorScheme(mode == "light" ? .light : .dark)
        .task {
            guard model == nil else { return }
            do {
                let api = ProviderFixtureAPI(mode: mode)
                let value = AuthenticatedServer(profile: try ServerProfile(server: ServerAddress(api.serverURL.absoluteString)), username: "Sample User", credential: try .session("fixture_unused"))
                model = try ProviderAccountsModel(server: value, api: api); server = value; self.api = api
            } catch { assertionFailure("Invalid provider fixture") }
        }
    }
}
#endif
