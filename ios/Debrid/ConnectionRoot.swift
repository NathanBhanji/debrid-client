import DebridKit
import SwiftUI

struct ConnectionRoot: View {
    @State private var model = ServerConnectionModel()
    @State private var checkTask: Task<Void, Never>?
    @State private var restored = false
    @State private var savedID: String?
    @State private var savingID: String?
    @State private var saveRevision = 0
    @State private var storageError: String?
    @State private var store: SessionStore?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    DebridWordmark().padding(.vertical, 12)
                    Text("Your downloads, within reach.").font(.title2.weight(.semibold))
                    Text("Connect to your self-hosted Debrid server. Downloads stay on your server.")
                        .foregroundStyle(.secondary)
                }.listRowBackground(Color.clear)
                Section {
                    TextField("https://debrid.example.com", text: $model.address)
                        .keyboardType(.URL).textContentType(.URL)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                        .accessibilityIdentifier("server-address")
                        .onSubmit { check() }
                    if model.phase == .checking {
                        HStack { ProgressView(); Text("Checking connection…"); Spacer(); Button("Cancel") { cancel() }.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint) }
                    } else {
                        Button("Check connection", systemImage: "network") { check() }
                            .buttonStyle(.borderless).foregroundStyle(DebridStyle.tint)
                            .disabled(model.address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || store == nil)
                            .accessibilityIdentifier("check-connection")
                    }
                } header: { Text("Server address") } footer: {
                    Text("Use the full HTTPS address, including any path prefix. A tailnet-only server needs Tailscale access on this iPhone.")
                }
                switch model.phase {
                case .failed(let message):
                    Section { Label(message, systemImage: "exclamationmark.triangle").foregroundStyle(.secondary).accessibilityIdentifier("connection-error") }
                case .discovered(let server):
                    Section("Server found") {
                        Label("Debrid \(server.version)", systemImage: "checkmark.circle.fill")
                        Text(server.profile.serverURL.absoluteString).font(.footnote).textSelection(.enabled)
                        loginSummary(server)
                        Button(savedID == server.profile.id ? "Server saved" : "Save server", systemImage: savedID == server.profile.id ? "checkmark" : "bookmark") {
                            Task { await save(server.profile) }
                        }.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint)
                            .disabled(savedID == server.profile.id || savingID != nil)
                    }
                default: EmptyView()
                }
                if let storageError {
                    Section { Label(storageError, systemImage: "lock.trianglebadge.exclamationmark").foregroundStyle(.secondary) }
                }
            }
            .modifier(DebridListStyle())
            .navigationTitle("Connect")
            .tint(DebridStyle.tint)
            .task {
                guard !restored else { return }
                restored = true
                if let group = Bundle.main.object(forInfoDictionaryKey: "DebridKeychainAccessGroup") as? String,
                   let secrets = try? KeychainSecretStorage(accessGroup: group) {
                    store = SessionStore(secrets: secrets)
                }
                guard let store else { storageError = "Secure storage is unavailable. Reopen the app and try again."; return }
                let inputRevision = model.addressRevision
                do {
                    if let profile = try await store.selectedProfile() {
                        model.restoreAddress(profile.serverURL.absoluteString, expectedRevision: inputRevision)
                    }
                }
                catch { storageError = "Saved server information could not be read. Your credentials have not been changed." }
            }
            .onChange(of: model.address) { checkTask?.cancel(); savedID = nil; savingID = nil; saveRevision &+= 1 }
            .onDisappear { cancel() }
        }
    }

    @ViewBuilder private func loginSummary(_ server: DiscoveredServer) -> some View {
        switch server.login {
        case .pocketID:
            Label("Pocket ID is available", systemImage: "person.badge.key")
        case .upgradeRequired:
            Text("Pocket ID is configured. Update the server to enable secure iPhone sign-in.").foregroundStyle(.secondary)
        case .webSetupRequired:
            Text("Finish authentication setup in the server’s web interface, then check again.").foregroundStyle(.secondary)
        case .passwordOnly:
            Text("This server uses password sign-in. Use its web client for now. Pocket ID requires an administrator to change the server’s authentication setup.").foregroundStyle(.secondary)
        case .unsupported:
            Text("This authentication method is not supported. Check the server configuration.").foregroundStyle(.secondary)
        }
    }
    private func check() {
        guard store != nil else { return }
        checkTask?.cancel()
        savedID = nil
        saveRevision &+= 1
        savingID = nil
        checkTask = Task { await model.check() }
    }
    private func cancel() { checkTask?.cancel(); checkTask = nil; model.invalidate(); saveRevision &+= 1; savingID = nil }
    private func save(_ profile: ServerProfile) async {
        guard let store else { return }
        guard case .discovered(let current) = model.phase, current.profile.id == profile.id else { return }
        saveRevision &+= 1
        let operation = saveRevision
        savingID = profile.id
        defer { if operation == saveRevision { savingID = nil } }
        do {
            try await store.saveProfile(profile)
            guard operation == saveRevision else { return }
            try await store.select(profile.id)
            guard operation == saveRevision else { return }
            savedID = profile.id; storageError = nil
        } catch {
            guard operation == saveRevision else { return }
            storageError = "The server could not be saved. Try again; existing credentials have not been changed."
        }
    }
}
