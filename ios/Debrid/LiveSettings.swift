import DebridKit
import SwiftUI

private enum RuntimeSettingsRoute: Hashable { case policy, categories, organization, connection, conflict }

struct LiveSettings: View {
    let server: AuthenticatedServer
    let auth: SessionCoordinator?
    private let fixtureAPI: (any SettingsReviewAPI)?
    @Environment(\.scenePhase) private var scenePhase
    @State private var model: RuntimeSettingsModel?
    @State private var path: [RuntimeSettingsRoute] = []
    @State private var task: Task<Void, Never>?
    @State private var commandID: UUID?
    @State private var reviewing = false
    @State private var visible = false
    @State private var setupFailed = false
    init(server: AuthenticatedServer, auth: SessionCoordinator) { self.server = server; self.auth = auth; fixtureAPI = nil }
    #if DEBUG
    init(server: AuthenticatedServer, api: any SettingsReviewAPI) { self.server = server; auth = nil; fixtureAPI = api }
    #endif
    var body: some View {
        NavigationStack(path: $path) {
            List {
                Section {
                    if auth == nil { Label("Preview · Sample settings", systemImage: "viewfinder").font(.footnote).fixedSize(horizontal: false, vertical: true).foregroundStyle(DebridStyle.secondary) }
                    DebridWordmark().padding(.vertical, 6)
                    Text("Your server, your rules.").font(.title2.weight(.semibold))
                    Text("Defaults apply to new transfers. Existing download paths stay where they are.").font(.subheadline).foregroundStyle(DebridStyle.secondary)
                }.listRowBackground(Color.clear).listRowSeparator(.hidden)
                if let model {
                    state(model)
                    if model.baseline != nil {
                        Section("Server preferences") {
                            NavigationLink(value: RuntimeSettingsRoute.policy) { Label("Download defaults", systemImage: "slider.horizontal.3") }.accessibilityIdentifier("settings-defaults")
                            NavigationLink(value: RuntimeSettingsRoute.categories) { Label("Categories", systemImage: "folder") }.accessibilityIdentifier("settings-categories")
                            NavigationLink(value: RuntimeSettingsRoute.organization) { Label("Library organization", systemImage: "square.stack.3d.up") }.accessibilityIdentifier("settings-organization")
                        }.disabled(model.phase != .editing || model.isBusy)
                        if model.isDirty && model.phase == .editing {
                            Section { Button("Review changes", systemImage: "checklist") { reviewing = true }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).disabled(!model.canReview).accessibilityIdentifier("settings-review") }
                        }
                    }
                } else if setupFailed { ContentUnavailableView("Settings unavailable", systemImage: "exclamationmark.triangle", description: Text("Reconnect and try again.")) } else { ProgressView("Loading settings…") }
                Section("Connection") {
                    Text(server.profile.serverURL.absoluteString).font(.footnote).textSelection(.enabled).accessibilityLabel("Server address").accessibilityValue(server.profile.serverURL.absoluteString)
                    NavigationLink("Server and sign-in", value: RuntimeSettingsRoute.connection)
                }
            }.modifier(DebridListStyle()).navigationTitle("Settings")
                .navigationDestination(for: RuntimeSettingsRoute.self) { route in
                    if route == .connection { ConnectionSettings(server: server, auth: auth, embedded: true) }
                    else if let model {
                        switch route {
                        case .policy: policy(model)
                        case .categories: categories(model)
                        case .organization: organization(model)
                        case .conflict: SettingsConflictView(model: model, preview: auth == nil, reload: { run { await model.checkLatest() } })
                        case .connection: EmptyView()
                        }
                    }
                }
        }.tint(DebridStyle.tint)
        .sheet(isPresented: $reviewing) { if let model { SettingsSaveReview(model: model, preview: auth == nil, save: { guard task == nil, !model.isBusy else { return }; run { await model.save() }; reviewing = false }) } }
        .task(id: scenePhase) {
            guard scenePhase == .active, !Task.isCancelled else { stop(); return }
            visible = true
            do {
                if model == nil {
                    let api: any SettingsReviewAPI
                    if let fixtureAPI { api = fixtureAPI } else { api = try DebridAPIClient(server: ServerAddress(server.profile.serverURL.absoluteString), credential: server.credential) }
                    let connection = server.connectionID, preview = fixtureAPI != nil
                    model = try RuntimeSettingsModel(server: server, api: api, current: { [weak auth] in
                        if preview { return true }
                        guard let auth, case .connected(let current) = auth.phase else { return false }; return current.connectionID == connection
                    })
                }
                model?.start(); await model?.load()
            } catch { setupFailed = true }
        }
        .onChange(of: model?.phase) {
            if model?.phase == .saved || model?.phase == .unknown || model?.phase == .conflict { path = [] }
        }
        .onChange(of: model?.isBusy) {
            if model?.isBusy == false, model?.phase == .editing, model?.message != nil { path = [] }
        }
        .task(id: [model?.requiresSignIn == true, scenePhase == .active, visible]) {
            guard visible, scenePhase == .active, model?.requiresSignIn == true else { return }
            await auth?.sessionRejected(for: server.profile.id, connectionID: server.connectionID)
        }
        .onDisappear { visible = false; stop() }
    }
    private func run(_ action: @escaping @MainActor () async -> Void) {
        guard task == nil, visible, scenePhase == .active else { return }
        let id = UUID(); commandID = id
        task = Task { await action(); if commandID == id { task = nil; commandID = nil } }
    }
    private func stop() { task?.cancel(); task = nil; commandID = nil; model?.stop() }
    @ViewBuilder private func state(_ model: RuntimeSettingsModel) -> some View {
        if model.isBusy { Section { ProgressView("Checking settings…") } }
        if let message = model.message { Section { Label(message, systemImage: model.phase == .unknown ? "questionmark.circle" : "info.circle").foregroundStyle(DebridStyle.secondary) } }
        switch model.phase {
        case .unsupported: Section { Label("Server update needed", systemImage: "arrow.up.circle"); Text("Update this server to edit settings safely from this iPhone.").foregroundStyle(DebridStyle.secondary) }
        case .failed: Section { Button("Try again") { run { await model.load() } }.disabled(model.isBusy).foregroundStyle(DebridStyle.tint) }
        case .saved: Section { Label("Settings saved", systemImage: "checkmark.circle").foregroundStyle(Color("StageGreen")).accessibilityIdentifier("settings-saved"); Button("Continue editing") { model.continueEditing() }.foregroundStyle(DebridStyle.tint) }
        case .unknown: Section { Button("Check server state", systemImage: "arrow.clockwise") { run { await model.checkLatest() } }.disabled(model.isBusy).foregroundStyle(DebridStyle.tint).accessibilityIdentifier("settings-check-result") }
        case .conflict: Section {
            NavigationLink("Review server changes", value: RuntimeSettingsRoute.conflict).accessibilityIdentifier("settings-conflict")
        }
        default: EmptyView()
        }
    }
    private func policy(_ model: RuntimeSettingsModel) -> some View {
        @Bindable var bound = model
        return Form {
            if auth == nil { Section { SettingsSampleNotice() } }
            if let message = model.validationMessage { Section { Label(message, systemImage: "exclamationmark.triangle").foregroundStyle(Color("DangerInk")) } }
            DownloadPolicyFields(draft: $bound.draft.policy, allowFileRules: true, allowHold: true)
            Section("Archives") { Stepper("Nested archive depth: \(model.draft.unpackMaxDepth)", value: $bound.draft.unpackMaxDepth, in: 0...5); Text("Affects future unpack work. Depth zero skips nested archives.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
        }.disabled(model.phase != .editing || model.isBusy).modifier(DebridListStyle()).navigationTitle("Download defaults").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { reviewButton(model) } }
    }
    private func categories(_ model: RuntimeSettingsModel) -> some View {
        SettingsCategories(model: model, preview: auth == nil, review: { reviewing = true })
    }
    private func organization(_ model: RuntimeSettingsModel) -> some View {
        SettingsOrganization(model: model, sample: auth == nil, preview: { name in run { await model.preview(name: name) } }, review: { reviewing = true })
    }
    private func reviewButton(_ model: RuntimeSettingsModel) -> some View { Button("Review") { reviewing = true }.disabled(!model.canReview).accessibilityIdentifier("settings-review-toolbar") }
}

struct DownloadPolicyFields: View {
    @Binding var draft: DownloadPolicyDraft
    let allowFileRules: Bool
    let allowHold: Bool
    var body: some View {
        Group {
            if allowFileRules { Section("File rules") {
                LabeledContent("Minimum bytes") { TextField("No minimum", text: $draft.minFileSize).keyboardType(.numberPad).multilineTextAlignment(.trailing).accessibilityIdentifier("policy-min-size") }
                LabeledContent("Include regex") { TextField("All paths", text: $draft.includeRegex).textInputAutocapitalization(.never).autocorrectionDisabled().accessibilityIdentifier("policy-include") }
                LabeledContent("Exclude regex") { TextField("None", text: $draft.excludeRegex).textInputAutocapitalization(.never).autocorrectionDisabled().accessibilityIdentifier("policy-exclude") }
                Text("Include overrides exclude. Your server validates Go regular expressions before saving.").font(.footnote).foregroundStyle(DebridStyle.secondary)
            } }
            Section("Downloads") {
                if allowHold { Toggle("Review files before downloading", isOn: $draft.reviewFiles).accessibilityIdentifier("policy-hold") }
                Toggle("Extract archives", isOn: $draft.unpack).accessibilityIdentifier("policy-unpack")
                Picker("Library organization", selection: $draft.organization) { Text("Use global setting").tag(DownloadPolicyDraft.Organization.inherit); Text("On").tag(DownloadPolicyDraft.Organization.on); Text("Off").tag(DownloadPolicyDraft.Organization.off) }
                LabeledContent("File retries") { TextField("0", text: $draft.downloadRetries).keyboardType(.numberPad).multilineTextAlignment(.trailing).accessibilityIdentifier("policy-file-retries") }
                LabeledContent("Provider retries") { TextField("0", text: $draft.torrentRetries).keyboardType(.numberPad).multilineTextAlignment(.trailing) }
            }
            Section("After completion") {
                Picker("Provider copy", selection: $draft.finishedAction) { Text("Keep").tag("keep"); Text("Remove after download").tag("remove_from_provider") }
                if draft.finishedAction == "remove_from_provider" { TextField("Delay · e.g. 10m", text: $draft.finishedDelay).textInputAutocapitalization(.never).autocorrectionDisabled(); Label("Automatically removes the provider copy after local completion.", systemImage: "trash").foregroundStyle(Color("DangerInk")).font(.footnote) }
            }
            Section("Lifecycle limits") {
                LabeledContent("Delete after error") { TextField("Off · e.g. 24h", text: $draft.deleteOnError).textInputAutocapitalization(.never).autocorrectionDisabled().accessibilityIdentifier("policy-delete-after-error") }
                if !draft.deleteOnError.isEmpty { Label("After a terminal error, this rule deletes NAS files and the provider copy.", systemImage: "exclamationmark.triangle").font(.footnote).foregroundStyle(Color("DangerInk")) }
                LabeledContent("Provider time limit") { TextField("None · e.g. 72h", text: $draft.lifetime).textInputAutocapitalization(.never).autocorrectionDisabled() }
                Text("Leave durations empty to disable. Use h, m or s; the server validates them. Changes take effect only after review and save.").font(.footnote).foregroundStyle(DebridStyle.secondary)
            }
        }
    }
}

private struct SettingsCategories: View {
    @Bindable var model: RuntimeSettingsModel
    let preview: Bool
    let review: () -> Void
    @State private var name = ""
    private var canAdd: Bool { RuntimeSettingsDraft.validCategory(name) && !model.draft.categories.contains(where: { Data($0.utf8) == Data(name.utf8) }) && model.draft.categories.count < 256 }
    var body: some View {
        Form {
            if preview { Section { SettingsSampleNotice() } }
            if let message = model.validationMessage { Section { Label(message, systemImage: "exclamationmark.triangle").foregroundStyle(Color("DangerInk")) } }
            Section { Text("Categories become folders for new transfers. Removing a category does not move or delete existing files.").foregroundStyle(DebridStyle.secondary) }
            Section("Categories") {
                ForEach(model.draft.categories, id: \.settingsUTF8Identity) { Text($0) }.onDelete { model.draft.categories.remove(atOffsets: $0) }
                if model.draft.categories.isEmpty { Text("No categories").foregroundStyle(DebridStyle.secondary) }
            }
            Section("Add category") {
                TextField("Folder name", text: $name).autocorrectionDisabled().accessibilityIdentifier("category-name")
                Button("Add category", systemImage: "plus") { guard canAdd else { return }; model.draft.categories.append(name); name = "" }.disabled(!canAdd).foregroundStyle(DebridStyle.tint).accessibilityIdentifier("category-add")
                Text("Use a plain folder name, with no path separators or hidden-folder prefix.").font(.footnote).foregroundStyle(DebridStyle.secondary)
            }
        }.disabled(model.phase != .editing || model.isBusy).modifier(DebridListStyle()).navigationTitle("Categories").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Review", action: review).disabled(!model.canReview).accessibilityIdentifier("settings-review-toolbar") }; ToolbarItem(placement: .primaryAction) { EditButton() } }
    }
}
private struct SettingsOrganization: View {
    @Bindable var model: RuntimeSettingsModel
    let sample: Bool
    let preview: (String) -> Void
    let review: () -> Void
    @State private var name = "The.Open.Ocean.2024.1080p.WEB-DL"
    var body: some View {
        Form {
            if sample { Section { SettingsSampleNotice() } }
            if let message = model.validationMessage { Section { Label(message, systemImage: "exclamationmark.triangle").foregroundStyle(Color("DangerInk")) } }
            Section { Toggle("Organize new transfers", isOn: $model.draft.organizationEnabled).accessibilityIdentifier("organization-enabled"); Text("Existing download paths stay fixed. Per-transfer settings can override this default.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
            Section("Folder templates") {
                LabeledContent("Movie template") { TextField("Server default", text: $model.draft.movieTemplate).textInputAutocapitalization(.never).autocorrectionDisabled().accessibilityIdentifier("organization-movie") }
                LabeledContent("TV template") { TextField("Server default", text: $model.draft.tvTemplate).textInputAutocapitalization(.never).autocorrectionDisabled() }
                Text("Tokens include {title}, {year} and {season:02}. Empty templates use the server defaults.").font(.footnote).foregroundStyle(DebridStyle.secondary)
            }
            Section("Preview without saving") {
                Text("Preview shows the template even when organization is off.").font(.footnote).foregroundStyle(DebridStyle.secondary)
                TextField("Release name", text: $name).textInputAutocapitalization(.never).autocorrectionDisabled()
                Button("Preview folder", systemImage: "folder") { preview(name) }.foregroundStyle(DebridStyle.tint).accessibilityIdentifier("organization-preview")
                if let path = model.previewPath { Text(path).font(.callout.monospaced()).textSelection(.enabled).accessibilityIdentifier("organization-path") }
                if let message = model.previewMessage { Text(message).font(.footnote).foregroundStyle(DebridStyle.secondary) }
            }
        }.disabled(model.phase != .editing || model.isBusy).modifier(DebridListStyle()).navigationTitle("Library organization").navigationBarTitleDisplayMode(.inline)
            .onChange(of: model.draft.movieTemplate) { model.invalidatePreview() }.onChange(of: model.draft.tvTemplate) { model.invalidatePreview() }.onChange(of: name) { model.invalidatePreview() }
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Review", action: review).disabled(!model.canReview).accessibilityIdentifier("settings-review-toolbar") } }
    }
}

private extension String { var settingsUTF8Identity: Data { Data(utf8) } }

struct SettingsSampleNotice: View { var body: some View { Label("Preview · Sample settings", systemImage: "viewfinder").font(.footnote).fixedSize(horizontal: false, vertical: true).foregroundStyle(DebridStyle.secondary) } }
