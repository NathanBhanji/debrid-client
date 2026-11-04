import DebridKit
import SwiftUI

struct ConnectionDiagnostics: View {
    let model: ConnectionDiagnosticsModel
    var preview = false
    @Environment(\.scenePhase) private var scenePhase
    @State private var operation: Task<Void, Never>?
    @State private var reportVisible = false
    var body: some View {
        List {
            if preview { Section { Label("Preview · Sample connection", systemImage: "viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) } }
            Section {
                Label("Check the connection", systemImage: "network").font(.title3.weight(.semibold))
                Text("Check this server’s public health and sign-in availability. Your saved session, providers and transfers are not tested or changed.").foregroundStyle(DebridStyle.secondary)
                Button(model.isBusy ? "Checking…" : "Check connection", systemImage: "arrow.clockwise") { check() }
                    .disabled(model.isBusy || operation != nil || scenePhase != .active).foregroundStyle(DebridStyle.tint).accessibilityIdentifier("diagnostics-check")
                if model.isBusy { ProgressView("Checking public server…") }
            }
            if let report = model.report {
                Section {
                    if let issue = report.issue {
                        Label(issue.title, systemImage: "exclamationmark.triangle").font(.headline).accessibilityIdentifier("diagnostics-result")
                        Text(issue.guidance).foregroundStyle(DebridStyle.secondary)
                    } else {
                        Label("Server reachable", systemImage: "checkmark.circle").font(.headline).foregroundStyle(Color("StageGreen")).accessibilityIdentifier("diagnostics-result")
                        Text(report.pocketIDAvailable ? "Pocket ID sign-in is available. This public check does not verify your saved session." : "The server responded, but native Pocket ID availability is unconfirmed.").foregroundStyle(DebridStyle.secondary)
                    }
                    LabeledContent("Checked") { Text(report.checkedAt, style: .time) }
                    Text("This is a dated check, not continuous monitoring. Debrid cannot detect Tailscale’s connection state.").font(.footnote).foregroundStyle(DebridStyle.secondary)
                }
                Section("Support report") {
                    Text("Review the exact report before sharing. It contains no server address, identity, transfers, file paths, credentials or raw logs.").font(.footnote).foregroundStyle(DebridStyle.secondary)
                    DisclosureGroup("Review report", isExpanded: $reportVisible) {
                        Text(report.text).font(.footnote.monospaced()).textSelection(.enabled).accessibilityIdentifier("diagnostics-report")
                    }
                    if reportVisible { ShareLink(item: report.text) { Label("Share redacted report", systemImage: "square.and.arrow.up") }.foregroundStyle(DebridStyle.tint).accessibilityIdentifier("diagnostics-share") }
                }
            }
            Section("If your server is unreachable") {
                Text("Check Wi-Fi or mobile data, the server address and whether your server is running. For a tailnet-only server, check Tailscale access on this iPhone.")
                Text("Saved links stay on this iPhone. A connection check never retries an uncertain transfer action; return to its review to check server state.").font(.footnote).foregroundStyle(DebridStyle.secondary)
            }
        }.modifier(DebridListStyle()).navigationTitle("Connection help").navigationBarTitleDisplayMode(.inline)
            .onChange(of: scenePhase) { if scenePhase != .active { stop() } }
            .onDisappear { stop() }
    }
    private func check() {
        guard operation == nil, !model.isBusy, scenePhase == .active else { return }
        reportVisible = false
        operation = Task { await model.check(); operation = nil }
    }
    private func stop() { operation?.cancel(); model.invalidate(); reportVisible = false }
}

struct ConnectionHelpDestination: View {
    @State private var model: ConnectionDiagnosticsModel
    init(server: AuthenticatedServer, auth: SessionCoordinator?) {
        let connection = server.connectionID, preview = auth == nil
        _model = State(initialValue: ConnectionDiagnosticsModel(profile: server.profile, current: { [weak auth] in
            guard let auth else { return preview }
            guard case .connected(let active) = auth.phase else { return false }
            return active.connectionID == connection
        }))
    }
    var body: some View { ConnectionDiagnostics(model: model) }
}
