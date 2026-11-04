#if DEBUG
import DebridKit
import SwiftUI

private struct RecoveryDiscoveryFixture: ServerDiscoveryChecking {
    let mode: String
    func inspect(_ profile: ServerProfile) async throws -> DiscoveredServer {
        switch mode {
        case "offline": throw APIError.connection(.notConnectedToInternet)
        case "dns": throw APIError.connection(.cannotFindHost)
        case "tls": throw APIError.connection(.serverCertificateUntrusted)
        case "limited": throw APIError.rateLimited(retryAfter: 90)
        case "server": throw APIError.http(status: 503)
        default: return DiscoveredServer(profile: profile, version: "0.2.8", issuer: nil, login: .pocketID)
        }
    }
}
private actor RecoveryTransfersFixture: TransferListing {
    var failed = false
    func torrents() async throws -> [TorrentDTO] {
        if failed { throw APIError.connection(.notConnectedToInternet) }
        return try await LiveTransfersPreviewAPI(state: .filled).torrents()
    }
    func fail() { failed = true }
}
struct RecoveryFixtureRoot: View {
    let mode: String
    @State private var diagnostics: ConnectionDiagnosticsModel?
    @State private var transfers: TransferListModel?
    var body: some View {
        Group {
            if mode == "stale", let transfers, let view = try? LiveTransfers(recoveryModel: transfers) { view }
            else if let diagnostics { NavigationStack { ConnectionDiagnostics(model: diagnostics, preview: true) } }
            else { ProgressView("Preparing sample…") }
        }.preferredColorScheme(mode == "light" ? .light : .dark)
            .task {
                guard diagnostics == nil, transfers == nil, let profile = try? ServerProfile(server: ServerAddress("https://private.example.test")) else { return }
                if mode == "stale" {
                    let api = RecoveryTransfersFixture()
                    // Use one fixture instance for both the confirmed snapshot and later failure.
                    let live = TransferListModel(profile: profile, api: api)
                    await live.reload(); await api.fail(); await live.reload(); transfers = live
                } else { diagnostics = ConnectionDiagnosticsModel(profile: profile, discovery: RecoveryDiscoveryFixture(mode: mode)) }
            }
    }
}
#endif
