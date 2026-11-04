import DebridKit
import SwiftUI

struct ProviderEditorSheet: View {
    @Bindable var model: ProviderEditorModel
    let preview: Bool
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var task: Task<Void, Never>?
    @State private var confirmingRemoval = false
    @State private var actionID: UUID?
    var body: some View {
        NavigationStack {
            Form {
                if preview { Section { Label("Preview · Sample account", systemImage: "viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) } }
                if let message = model.message {
                    Section { Label(message, systemImage: model.phase == .unknown ? "questionmark.circle" : "info.circle").foregroundStyle(DebridStyle.secondary) }
                }
                switch model.phase {
                case .saved:
                    Section { Label("Account saved", systemImage: "checkmark.circle").foregroundStyle(Color("StageGreen")).accessibilityIdentifier("provider-saved") }
                case .removed:
                    Section { Label("Account removed", systemImage: "checkmark.circle").accessibilityIdentifier("provider-removed"); Text("File and provider-copy deletion were not requested.").font(.footnote) }
                case .unknown:
                    Section {
                        Text("No request will be repeated automatically.").font(.footnote)
                        Button("Check server state", systemImage: "arrow.clockwise") { run { await model.checkResult() } }
                            .buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).disabled(model.isBusy).accessibilityIdentifier("provider-check-result")
                    }
                case .observed:
                    Section { Label(model.observedAccount == nil ? "No account found" : "Account found", systemImage: "externaldrive").accessibilityIdentifier("provider-observed") }
                case .editing, .saving:
                    fields.disabled(model.isBusy)
                    if model.isBusy { Section { ProgressView("Saving account…") } }
                    else {
                        Section {
                            Button(model.isNew ? "Add account" : "Save changes", systemImage: "checkmark") { run { await model.save() } }
                                .buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).disabled(!model.canSave).accessibilityIdentifier("provider-save")
                        }
                        if !model.isNew { removal }
                    }
                }
            }.modifier(DebridListStyle())
                .navigationTitle(model.isNew ? "Add provider" : "Edit provider").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Done") { stop(); dismiss() } } }
                .confirmationDialog("Remove account?", isPresented: Binding(get: { confirmingRemoval && !typeSize.isAccessibilitySize }, set: { if !$0 { confirmingRemoval = false } }), titleVisibility: .visible) {
                    Button(model.includingTransfers ? "Remove" : "Confirm removal", role: .destructive) { run { await model.remove() } }
                    Button("Keep account") {}
                } message: {
                    Text(model.includingTransfers ? "Remove all associated transfers. File and provider-copy deletion stays off." : "Remove this saved account from the server. Accounts with transfers cannot be removed with this option.")
                }
        }.tint(DebridStyle.tint)
        .sheet(isPresented: Binding(get: { confirmingRemoval && typeSize.isAccessibilitySize }, set: { if !$0 { confirmingRemoval = false } })) {
            NavigationStack {
                List {
                    Section {
                        Text(TransferSearchIndex.bounded(model.name, limit: 256)).font(.headline)
                        Text(model.includingTransfers ? "Remove this account and all its transfer records. Stop local work." : "Remove this account. Accounts with transfers are kept.")
                        Text("File and provider-copy deletion stays off.")
                    }
                    Section {
                        Button("Keep account") { confirmingRemoval = false }
                            .buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint)
                        Button("Remove", role: .destructive) { confirmingRemoval = false; run { await model.remove() } }
                            .buttonStyle(DebridRowActionStyle()).foregroundStyle(Color("DangerInk"))
                    }
                }.modifier(DebridListStyle()).navigationTitle("Remove account").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { confirmingRemoval = false } } }
            }.tint(DebridStyle.tint)
        }
        .task(id: scenePhase) {
            guard scenePhase == .active, !Task.isCancelled else { stop(); return }
            model.start(); await model.loadCapabilities()
        }
        .onDisappear { stop() }
    }
    private var fields: some View {
        Group {
            Section("Account") {
                if model.isNew {
                    Picker("Provider", selection: $model.kind) { ForEach(ProviderEditorModel.kinds, id: \.self) { Text(ProviderNames.label($0)).tag($0) } }
                } else { LabeledContent("Provider", value: ProviderNames.label(model.kind)) }
                TextField("Account name", text: $model.name).autocorrectionDisabled().accessibilityIdentifier("provider-name")
                    .onChange(of: model.name) { model.name = TransferSearchIndex.bounded(model.name, limit: 256) }
                if !model.isNew { Toggle("Enabled for new transfers", isOn: $model.enabled).accessibilityIdentifier("provider-enabled") }
                Toggle("Default provider", isOn: $model.makeDefault).disabled(model.original?.isDefault == true).accessibilityIdentifier("provider-default")
                Text(model.isNew ? "New accounts are enabled. The first account becomes the default." : "Disabling an account prevents new transfers from choosing it. Existing work can continue.")
                    .font(.footnote).foregroundStyle(DebridStyle.secondary)
            }
            Section {
                if !model.isNew { Toggle("Replace credentials", isOn: $model.replaceCredentials).accessibilityIdentifier("provider-replace-credentials").onChange(of: model.replaceCredentials) { if !model.replaceCredentials { model.secret = "" } } }
                if model.isNew || model.replaceCredentials {
                    SecureField("API key or token", text: $model.secret).textContentType(nil).textInputAutocapitalization(.never).autocorrectionDisabled()
                        .privacySensitive().accessibilityIdentifier("provider-secret")
                        .onChange(of: model.secret) { if model.secret.utf8.count > 8192 { model.secret = "" } }
                }
            } header: { Text("Credentials") } footer: {
                Text("Existing credentials are never shown. New credentials are verified by your server before saving and are not saved on this iPhone. Leaving this screen clears the entry.")
            }
        }
    }
    private var removal: some View {
        Section {
            Toggle("Also remove associated transfers", isOn: $model.includingTransfers)
                .disabled(!model.supportsForceRemoval).accessibilityIdentifier("provider-remove-transfers")
            Text(model.supportsForceRemoval ? "Off by default. Removing associated transfers stops their local work and removes their records. It requests no file or provider-copy deletion. A failed stop can leave removal partly complete." : "To remove an account with transfers, remove the transfers individually first or update your server.")
                .font(.footnote).foregroundStyle(DebridStyle.secondary)
            Button("Remove account", role: .destructive) { confirmingRemoval = true }
                .buttonStyle(DebridRowActionStyle()).foregroundStyle(Color("DangerInk")).accessibilityIdentifier("provider-remove")
        } header: { Text("Remove account") }
    }
    private func run(_ action: @escaping @MainActor () async -> Void) {
        guard task == nil, !model.isBusy else { return }
        let id = UUID(); actionID = id
        task = Task {
            await action()
            guard actionID == id else { return }
            task = nil; actionID = nil
        }
    }
    private func stop() { task?.cancel(); task = nil; actionID = nil; model.stop() }
}
