import DebridKit
import SwiftUI

struct ConnectionRoot: View {
    @Environment(\.magnetIntake) private var intake
    @State private var model = ServerConnectionModel()
    @State private var checkTask: Task<Void, Never>?
    @State private var restored = false
    @State private var savedID: String?
    @State private var savingID: String?
    @State private var saveRevision = 0
    @State private var storageError: String?
    @State private var store: SessionStore?
    @State private var auth: SessionCoordinator?
    @State private var authTask: Task<Void, Never>?
    @State private var showingPreviewNotice = false
    private let preview: Bool

    init() { preview = false }
    #if DEBUG
    init(discoveryPreview: any ServerDiscoveryChecking) {
        preview = true
        _model = State(initialValue: ServerConnectionModel(discovery: discoveryPreview))
    }
    #endif

    var body: some View {
        Group {
            if let auth, case .connected(let connected) = auth.phase {
                LiveTransfers(server: connected, auth: auth).id(connected.connectionID)
            } else { connectionForm }
        }
        .task { await prepare() }
         .onChange(of:activeConnection?.connectionID,initial:true) {
            guard !preview else { return }
            if let server=activeConnection,let auth {
                let profileID=server.profile.id
                intake?.setConnection(server,onUnauthorized:{ [weak auth] id in await auth?.sessionRejected(for:profileID,connectionID:id) })
            } else { intake?.setConnection(nil) }
        }
        .onChange(of: model.address) { checkTask?.cancel(); savedID = nil; savingID = nil; saveRevision &+= 1 }
        .onDisappear { cancel(); cancelAuth() }
        .alert("Preview only", isPresented: $showingPreviewNotice) {
            Button("OK", role: .cancel) {}
        } message: { Text("This sample server cannot sign in or save credentials.") }
    }

    private var activeConnection: AuthenticatedServer? {
        if let auth, case .connected(let server) = auth.phase { return server }; return nil
    }
    private var connectionForm: some View {
        NavigationStack {
            Form {
                Section {
                    VStack(alignment: .leading, spacing: 16) {
                        if preview { Label("Preview · Sample server", systemImage: "viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                        DebridWordmark()
                        Text("Your downloads, within reach.").font(.title2.weight(.semibold))
                        Text("Connect to your self-hosted Debrid server. Downloads stay on your server.")
                            .foregroundStyle(DebridStyle.secondary)
                    }.padding(.vertical, 12)
                }.listRowBackground(Color.clear).listRowSeparator(.hidden)
                Section {
                    TextField("https://debrid.example.com", text: $model.address)
                        .keyboardType(.URL).textContentType(.URL)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                        .accessibilityIdentifier("server-address")
                        .onSubmit { check() }
                        .disabled(auth?.isBusy == true)
                    if model.phase == .checking {
                        HStack { ProgressView(); Text("Checking connection…"); Spacer(); Button("Cancel") { cancel() }.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint) }
                    } else {
                        Button("Check connection", systemImage: "network") { check() }
                            .buttonStyle(.borderless).foregroundStyle(DebridStyle.tint)
                            .disabled(model.address.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || store == nil || auth?.isBusy == true)
                            .accessibilityIdentifier("check-connection")
                    }
                } header: { Text("Server address") } footer: {
                    Text("Use the full HTTPS address, including any path prefix. A tailnet-only server needs Tailscale access on this iPhone.")
                }
                switch model.phase {
                case .failed(let message):
                    Section { Label(message, systemImage: "exclamationmark.triangle").foregroundStyle(DebridStyle.secondary).accessibilityIdentifier("connection-error") }
                case .discovered(let server):
                    Section("Server found") {
                        Label("Debrid \(server.version)", systemImage: "checkmark.circle.fill")
                        Text(server.profile.serverURL.absoluteString).font(.footnote).textSelection(.enabled)
                        loginSummary(server)
                        if server.login == .pocketID {
                            Button("Continue with Pocket ID", systemImage: "person.badge.key") {
                                if preview { showingPreviewNotice = true; return }
                                guard let auth else { return }
                                authTask = Task { await auth.signIn(to: server.profile) }
                            }.buttonStyle(.glassProminent).tint(Color(red: 0.62, green: 0.17, blue: 0)).foregroundStyle(.white)
                                .disabled(auth?.isBusy == true || auth?.pendingLocalRemoval != nil)
                                .accessibilityIdentifier("pocket-id-sign-in")
                        }
                        Button(savedID == server.profile.id ? "Server saved" : "Save server", systemImage: savedID == server.profile.id ? "checkmark" : "bookmark") {
                            if preview { showingPreviewNotice = true } else { Task { await save(server.profile) } }
                        }.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint)
                            .disabled(savedID == server.profile.id || savingID != nil || auth?.isBusy == true)
                    }
                default: EmptyView()
                }
                if let auth, auth.isBusy {
                    Section {
                        HStack { ProgressView(); Text("Connecting securely…"); Spacer(); Button("Cancel") { cancelAuth() }.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint) }
                    }
                }
                if let message = auth?.message {
                    Section {
                        Label(message, systemImage: "person.crop.circle.badge.exclamationmark").foregroundStyle(DebridStyle.secondary)
                        if let auth, let pending = auth.pendingLocalRemoval {
                            Text(pending.serverURL.absoluteString).font(.footnote)
                            Button("Remove saved session", role: .destructive) { Task { await auth.removeSavedSession() } }
                                .buttonStyle(.borderless).foregroundStyle(.red).disabled(auth.isBusy)
                        } else {
                        Button("Reconnect saved server") {
                            guard let auth else { return }
                            authTask = Task { await auth.restore() }
                        }.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint).disabled(auth?.isBusy == true)
                        }
                    }
                }
                if let storageError {
                    Section { Label(storageError, systemImage: "lock.trianglebadge.exclamationmark").foregroundStyle(DebridStyle.secondary) }
                }
            }
            .modifier(DebridListStyle())
            .navigationTitle("Connect")
            .toolbar { if let intake { ToolbarItem(placement:.primaryAction) { PendingLinksButton(intake:intake) } } }
            .tint(DebridStyle.tint)

        }
    }

    private func prepare() async {
        guard !restored else { return }
        restored = true
        #if DEBUG
        if preview {
            model.address = "https://debrid.example.test"
            await model.check()
            return
        }
        #endif
        if let group = Bundle.main.object(forInfoDictionaryKey: "DebridKeychainAccessGroup") as? String,
           let secrets = try? KeychainSecretStorage(accessGroup: group) { store = SessionStore(secrets: secrets) }
        guard let store else { storageError = "Secure storage is unavailable. Reopen the app and try again."; return }
        let coordinator = SessionCoordinator(store: store, browser: SystemSignInBrowser())
        auth = coordinator
        guard !Task.isCancelled else { return }
        authTask = Task { await coordinator.restore(connection: model) }
    }
    private func cancelAuth() { authTask?.cancel(); auth?.cancel(); authTask = nil }

    @ViewBuilder private func loginSummary(_ server: DiscoveredServer) -> some View {
        switch server.login {
        case .pocketID:
            Label("Pocket ID is available", systemImage: "person.badge.key")
        case .upgradeRequired:
            Text("Pocket ID is configured. Update the server to enable secure iPhone sign-in.").foregroundStyle(DebridStyle.secondary)
        case .webSetupRequired:
            Text("Finish authentication setup in the server’s web interface, then check again.").foregroundStyle(DebridStyle.secondary)
        case .passwordOnly:
            Text("This server uses password sign-in. Use its web client for now. Pocket ID requires an administrator to change the server’s authentication setup.").foregroundStyle(DebridStyle.secondary)
        case .unsupported:
            Text("This authentication method is not supported. Check the server configuration.").foregroundStyle(DebridStyle.secondary)
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

#if DEBUG
struct SignInDiscoveryPreview: ServerDiscoveryChecking {
    func inspect(_ profile: ServerProfile) async throws -> DiscoveredServer {
        DiscoveredServer(profile: profile, version: "Preview", issuer: "https://id.example.test", login: .pocketID)
    }
}
#endif
