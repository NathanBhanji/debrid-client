import DebridKit
import SwiftUI

struct LiveProviders: View {
    let server: AuthenticatedServer
    let auth: SessionCoordinator?
    @Environment(\.scenePhase) private var scenePhase
    @State private var model: ProviderAccountsModel?
    @State private var checkTask: Task<Void, Never>?
    @State private var query = ""
    @State private var setupFailed = false
    @State private var editor: ProviderEditorModel?
    @State private var editorError: String?
    @State private var isVisible = false
    private var editingAPI: (any ProviderManagingAPI)?
    init(server: AuthenticatedServer, model: ProviderAccountsModel, editingAPI: any ProviderManagingAPI) {
        self.server = server; auth = nil; _model = State(initialValue: model); self.editingAPI = editingAPI
    }

    init(server: AuthenticatedServer, auth: SessionCoordinator) { self.server = server; self.auth = auth }
    #if DEBUG
    init(server: AuthenticatedServer, model: ProviderAccountsModel) {
        self.server = server; auth = nil; _model = State(initialValue: model)
    }
    #endif
    private var filtered: [AccountDTO] {
        (model?.accounts ?? []).filter { row in
            query.isEmpty || (row.name + " " + ProviderNames.label(row.kind)).localizedStandardContains(query)
        }.sorted { lhs, rhs in
            if lhs.isDefault != rhs.isDefault { return lhs.isDefault }
            return lhs.name.localizedStandardCompare(rhs.name) == .orderedAscending
        }
    }
    var body: some View {
        NavigationStack {
            List {
                if let model {
                    if query.isEmpty { Section {
                        if auth == nil { Label("Preview · Sample accounts", systemImage: "viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                        DebridWordmark().padding(.vertical, 6)
                        Text("Your provider accounts")
                            .font(.title2.weight(.semibold))
                        Text("Browse saved accounts. Check a connection when you need it.")
                            .font(.subheadline).foregroundStyle(DebridStyle.secondary)
                    }.listRowBackground(Color.clear).listRowSeparator(.hidden) }
                    if let message = model.message {
                        Section {
                            Label(message, systemImage: "wifi.exclamationmark")
                            if model.hasLoaded { Text("Showing the last loaded accounts.").font(.footnote) }
                            Button("Try again") { checkTask = Task { await model.refresh() } }.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint).disabled(model.isLoading || model.isChecking)
                        }.foregroundStyle(DebridStyle.secondary)
                    }
                    if model.isLoading && !model.hasLoaded {
                        Section { ProgressView("Loading accounts…") }
                    } else if model.hasLoaded && model.accounts.isEmpty {
                        ContentUnavailableView("No provider accounts", systemImage: "externaldrive", description: Text("Accounts you connect to this server appear here."))
                            .listRowBackground(Color.clear)
                    } else if !model.accounts.isEmpty && filtered.isEmpty {
                        ContentUnavailableView.search(text: query).listRowBackground(Color.clear).accessibilityIdentifier("provider-no-results")
                    } else if !model.accounts.isEmpty {
                        Section("Accounts") {
                            ForEach(filtered) { account in
                                NavigationLink {
                                    ProviderAccountDetail(id: account.id, model: model, preview: auth == nil, edit: { openEditor(account) }, check: { check(account.id) })
                                } label: { ProviderAccountRow(account: account, check: model.checks[account.id], failed: model.checkErrors[account.id] != nil) }
                                .listRowBackground(DebridStyle.surface)
                                .accessibilityIdentifier("provider-" + account.id)
                            }
                        }
                        Section {
                            Label("Checks contact the provider through your server. They never start a download.", systemImage: "checkmark.shield")
                                .font(.footnote).foregroundStyle(DebridStyle.secondary)
                        }.listRowBackground(Color.clear)
                    }
                } else if setupFailed {
                    ContentUnavailableView("Providers unavailable", systemImage: "exclamationmark.triangle", description: Text("Reconnect to your server and try again."))
                } else { ProgressView("Loading accounts…") }
            }.modifier(DebridListStyle()).navigationTitle("Providers")
                .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Search accounts")
                .autocorrectionDisabled().textInputAutocapitalization(.never)
                .onChange(of: query) { query = TransferSearchIndex.bounded(query, limit: 256) }
                .refreshable { await model?.refresh() }
                .toolbar { ToolbarItem(placement: .primaryAction) { Button("Add provider", systemImage: "plus") { openEditor(nil) }.accessibilityIdentifier("provider-add") } }
        }.tint(DebridStyle.tint)
        .sheet(isPresented: Binding(get: { editor != nil }, set: { if !$0 { editor = nil } }), onDismiss: { Task { guard isVisible, scenePhase == .active else { return }; model?.start(); await model?.refresh() } }) {
            if let editor { ProviderEditorSheet(model: editor, preview: auth == nil).onChange(of: editor.requiresSignIn) { if editor.requiresSignIn { Task { await auth?.sessionRejected(for: server.profile.id, connectionID: server.connectionID) } } } }
        }
        .alert("Account unavailable", isPresented: Binding(get: { editorError != nil }, set: { if !$0 { editorError = nil } })) { Button("OK", role: .cancel) {} } message: { Text(editorError ?? "") }
        .task(id: scenePhase) {
            guard scenePhase == .active, !Task.isCancelled else { stop(); return }
            isVisible = true
            do {
                if model == nil {
                    let api = try DebridAPIClient(server: ServerAddress(server.profile.serverURL.absoluteString), credential: server.credential)
                    let connectionID = server.connectionID
                    model = try ProviderAccountsModel(server: server, api: api, isCurrent: { [weak auth] in
                        guard let auth, case .connected(let current) = auth.phase else { return false }
                        return current.connectionID == connectionID
                    })
                }
                model?.start()
                await model?.refresh()
            } catch { setupFailed = true }
        }
        .onChange(of: model?.requiresSignIn) {
            guard model?.requiresSignIn == true, model?.isActive == true, scenePhase == .active else { return }
            Task {
                guard model?.isActive == true, scenePhase == .active else { return }
                await auth?.sessionRejected(for: server.profile.id, connectionID: server.connectionID)
            }
        }
        .onDisappear { isVisible = false; stop() }
    }
    private func openEditor(_ account: AccountDTO?) {
        do {
            let api: any ProviderManagingAPI
            if let editingAPI { api = editingAPI } else { api = try DebridAPIClient(server: ServerAddress(server.profile.serverURL.absoluteString), credential: server.credential) }
            let connectionID = server.connectionID
            let isFixture = editingAPI != nil
            editor = try ProviderEditorModel(server: server, original: account, api: api, isCurrent: { [weak auth] in
                if let auth { if case .connected(let current) = auth.phase { return current.connectionID == connectionID }; return false }
                return isFixture
            })
        } catch { editorError = "The account editor could not open. Reconnect and try again." }
    }
    private func check(_ id: String) {
        guard let model, !model.isChecking else { return }
        checkTask = Task { await model.check(id: id) }
    }
    private func stop() { checkTask?.cancel(); checkTask = nil; model?.stop() }
}

private struct ProviderAccountRow: View {
    @Environment(\.dynamicTypeSize) private var typeSize
    let account: AccountDTO
    let check: ProviderCheck?
    let failed: Bool
    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            if !typeSize.isAccessibilitySize { Image(systemName: "externaldrive")
                .font(.title3).foregroundStyle(DebridStyle.tint)
                .frame(width: 36, height: 36).background(DebridStyle.edge.opacity(0.35), in: .rect(cornerRadius: 10))
                .accessibilityHidden(true) }
            VStack(alignment: .leading, spacing: 5) {
                Text(TransferSearchIndex.bounded(account.name, limit: 256)).font(.headline)
                Text(ProviderNames.label(account.kind)).font(.subheadline).foregroundStyle(DebridStyle.secondary)
                Group {
                    if typeSize.isAccessibilitySize { VStack(alignment: .leading, spacing: 4) { badges } }
                    else { HStack(spacing: 12) { badges } }
                }.font(.caption)
                if failed { Label("Last check failed", systemImage: "exclamationmark.triangle").foregroundStyle(Color("DangerInk")).font(.caption) }
                else if check != nil { Label("Credentials checked", systemImage: "checkmark.circle").foregroundStyle(Color("StageGreen")).font(.caption) }
                else { Text("Not checked this session").foregroundStyle(DebridStyle.secondary).font(.caption) }
            }.fixedSize(horizontal: false, vertical: true)
        }.padding(.vertical, 8).accessibilityElement(children: .combine)
    }
    @ViewBuilder private var badges: some View {
        Text(account.enabled ? "Enabled" : "Disabled").foregroundStyle(DebridStyle.secondary)
        if account.isDefault { Label("Default", systemImage: "star.fill").foregroundStyle(DebridStyle.tint) }
    }
}

private struct ProviderAccountDetail: View {
    let id: String
    let model: ProviderAccountsModel
    let preview: Bool
    let edit: () -> Void
    let check: () -> Void
    private var account: AccountDTO? { model.accounts.first { $0.id == id } }
    var body: some View {
        List {
            if let account {
                if preview { Section { Label("Preview · Sample account", systemImage: "viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) } }
                Section {
                    if model.checkErrors[id] == nil { ProviderAccountRow(account: account, check: model.checks[id], failed: false) }
                    else { Text(ProviderNames.label(account.kind)).foregroundStyle(DebridStyle.secondary) }
                }
                Section("Connection") {
                    if let message = model.checkErrors[id] {
                        Label(message, systemImage: "exclamationmark.triangle").font(.callout).foregroundStyle(Color("DangerInk"))
                            .accessibilityIdentifier("provider-check-error")
                    }
                    if model.checkingID == id { ProgressView("Checking connection…").accessibilityIdentifier("provider-check-progress") }
                    else {
                        Button("Check connection", systemImage: "arrow.trianglehead.2.clockwise") { check() }
                            .buttonStyle(.borderless).foregroundStyle(DebridStyle.tint)
                            .disabled(model.isLoading || model.isChecking)
                            .accessibilityIdentifier("provider-check")
                    }
                    if let result = model.checks[id] {
                        LabeledContent("Last successful check") { Text(result.checkedAt, format: .dateTime.month(.abbreviated).day().hour().minute()) }
                        Text("Credentials were accepted at this time. Downloads may still be limited by the provider.").font(.footnote).foregroundStyle(DebridStyle.secondary)
                    } else { Text("This account has not been checked during this session.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                }
                if let result = model.checks[id] {
                    Section("Provider account · Last check") {
                        if let username = result.user.username, !username.isEmpty { LabeledContent("Username", value: TransferSearchIndex.bounded(username, limit: 256)) }
                        if let email = result.user.email, !email.isEmpty { LabeledContent("Email", value: TransferSearchIndex.bounded(email, limit: 256)) }
                        LabeledContent("Membership", value: result.user.premium ? "Premium" : "Free")
                            .accessibilityElement(children: .ignore).accessibilityLabel("Membership")
                            .accessibilityValue(result.user.premium ? "Premium" : "Free").accessibilityIdentifier("provider-membership")
                        if let plan = result.user.plan, !plan.isEmpty { LabeledContent("Plan", value: TransferSearchIndex.bounded(plan, limit: 256)) }
                        if let expires = result.user.expiresAt {
                            LabeledContent("Expiry") { Text(expires, format: .dateTime.year().month(.abbreviated).day()) }
                        } else { LabeledContent("Expiry", value: "Not reported") }
                        LabeledContent("Quota", value: "Not reported")
                    }
                }
                Section("On this server") {
                    LabeledContent("Account", value: account.enabled ? "Enabled" : "Disabled")
                    LabeledContent("Default provider", value: account.isDefault ? "Yes" : "No")
                    if !account.enabled { Text("Disabled accounts are not used for new transfers. Checking credentials does not enable this account.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                }
                if model.isStale {
                    Section { Label("Account details may have changed. Pull down to refresh.", systemImage: "clock").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                }
            } else { ContentUnavailableView("Account no longer available", systemImage: "externaldrive.badge.xmark", description: Text("Go back and refresh the account list.")) }
        }.modifier(DebridListStyle())
        .navigationTitle(account.map { TransferSearchIndex.bounded($0.name, limit: 256) } ?? "Provider")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .primaryAction) { Button("Edit") { edit() }.foregroundStyle(DebridStyle.tint).accessibilityIdentifier("provider-edit") } }
        .refreshable { await model.refresh() }
    }
}
