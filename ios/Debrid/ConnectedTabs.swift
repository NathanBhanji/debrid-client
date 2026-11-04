import DebridKit
import SwiftUI

struct ConnectedTabs: View {
    let server: AuthenticatedServer
    let auth: SessionCoordinator
    @Environment(\.magnetIntake) private var intake
    @State private var tab = 0
    var body: some View {
        TabView(selection: $tab) {
            Tab("Transfers", systemImage: "arrow.down.circle", value: 0) { LiveTransfers(server: server, auth: auth) }
            Tab("Providers", systemImage: "externaldrive.connected.to.line.below", value: 1) { LiveProviders(server: server, auth: auth) }
            Tab("Settings", systemImage: "gearshape", value: 2) { LiveSettings(server: server, auth: auth) }
        }.tint(DebridStyle.tint)
        .onChange(of: intake?.navigationRequest?.id) {
            if intake?.navigationRequest?.serverURL == server.profile.serverURL { tab = 0 }
        }
    }
}

struct ConnectionSettings: View {
    let server: AuthenticatedServer
    let auth: SessionCoordinator?
    @State private var confirmingSignOut = false
    private let embedded: Bool
    init(server: AuthenticatedServer, auth: SessionCoordinator?, embedded: Bool = false) { self.server = server; self.auth = auth; self.embedded = embedded }
    var body: some View {
        if embedded { content } else { NavigationStack { content } }
    }
    private var content: some View {
            List {
                Section {
                    if auth == nil { Label("Preview · Sample server", systemImage: "viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                    DebridWordmark().padding(.vertical, 8)
                    Text("Native on your phone. Downloads on your server.")
                        .font(.subheadline).foregroundStyle(DebridStyle.secondary)
                }.listRowBackground(Color.clear)
                Section("Server") {
                    Text(server.profile.serverURL.absoluteString).textSelection(.enabled)
                    Label("If your server uses Tailscale, connect this iPhone to its tailnet.", systemImage: "network")
                        .font(.footnote).foregroundStyle(DebridStyle.secondary)
                }
                Section("Sign-in") {
                    LabeledContent("Pocket ID", value: TransferSearchIndex.bounded(server.username, limit: 256))
                        .accessibilityElement(children: .ignore).accessibilityLabel("Pocket ID")
                        .accessibilityValue(TransferSearchIndex.bounded(server.username, limit: 256)).accessibilityIdentifier("settings-identity")
                    if let auth {
                        Button("Sign out on this iPhone", role: .destructive) { confirmingSignOut = true }
                            .buttonStyle(.borderless).foregroundStyle(Color("DangerInk"))
                            .disabled(auth.isBusy).accessibilityIdentifier("settings-sign-out")
                    }
                }
                Section("Help") {
                    NavigationLink("Connection help") { ConnectionHelpDestination(server: server, auth: auth) }.accessibilityIdentifier("connection-help")
                }
                Section("About") {
                    LabeledContent("Debrid", value: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0.1")
                }
            }.modifier(DebridListStyle()).navigationTitle("Settings")
            .confirmationDialog("Sign out on this iPhone?", isPresented: $confirmingSignOut, titleVisibility: .visible) {
                Button("Sign out", role: .destructive) { Task { await auth?.signOut() } }
                Button("Keep signed in", role: .cancel) {}
            } message: { Text("Downloads continue on your server. Saved links stay on this iPhone.") }
    }
}
