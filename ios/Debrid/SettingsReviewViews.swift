import DebridKit
import SwiftUI

struct SettingsSectionSummary: View {
    let draft: RuntimeSettingsDraft
    let section: RuntimeSettingsSection
    var body: some View {
        Group {
            switch section {
            case .categories:
                Text(draft.categories.isEmpty ? "No categories" : draft.categories.joined(separator: ", ")).textSelection(.enabled)
            case .archives: LabeledContent("Nested archive depth", value: String(draft.unpackMaxDepth))
            case .organization:
                LabeledContent("Organize new transfers", value: draft.organizationEnabled ? "On" : "Off")
                Text("Movie: " + (draft.movieTemplate.isEmpty ? "Server default" : draft.movieTemplate)).font(.footnote).textSelection(.enabled)
                Text("TV: " + (draft.tvTemplate.isEmpty ? "Server default" : draft.tvTemplate)).font(.footnote).textSelection(.enabled)
            case .defaults:
                LabeledContent("File retries", value: draft.policy.downloadRetries)
                LabeledContent("Provider retries", value: draft.policy.torrentRetries)
                LabeledContent("Extract archives", value: draft.policy.unpack ? "On" : "Off")
                LabeledContent("Review files first", value: draft.policy.reviewFiles ? "On" : "Off")
                LabeledContent("Minimum bytes", value: draft.policy.minFileSize.isEmpty ? "No minimum" : draft.policy.minFileSize)
                Text("Include: " + (draft.policy.includeRegex.isEmpty ? "All paths" : draft.policy.includeRegex)).font(.footnote).textSelection(.enabled)
                Text("Exclude: " + (draft.policy.excludeRegex.isEmpty ? "None" : draft.policy.excludeRegex)).font(.footnote).textSelection(.enabled)
                if !draft.policy.preservedFileIDs.isEmpty {
                    DisclosureGroup("Preserved file IDs (\(draft.policy.preservedFileIDs.count))") {
                        ForEach(Array(draft.policy.preservedFileIDs.enumerated()), id: \.offset) { _, id in Text(id).font(.footnote.monospaced()).textSelection(.enabled) }
                    }
                }
                LabeledContent("Organization override", value: draft.policy.organization.rawValue.capitalized)
                LabeledContent("Provider time limit", value: draft.policy.lifetime.isEmpty ? "None" : draft.policy.lifetime)
                if draft.policy.finishedAction == "remove_from_provider" {
                    Label("Remove provider copy after completion", systemImage: "trash").foregroundStyle(Color("DangerInk"))
                    Text("Delay: " + (draft.policy.finishedDelay.isEmpty ? "No delay" : draft.policy.finishedDelay)).font(.footnote)
                } else { LabeledContent("Provider copy", value: "Keep") }
                if !draft.policy.deleteOnError.isEmpty {
                    Label("Delete NAS files and provider copy after terminal error: " + draft.policy.deleteOnError, systemImage: "exclamationmark.triangle").foregroundStyle(Color("DangerInk"))
                } else { LabeledContent("Delete after error", value: "Off") }
            }
        }.fixedSize(horizontal: false, vertical: true)
    }
}

struct SettingsSaveReview: View {
    let model: RuntimeSettingsModel
    let preview: Bool
    let save: () -> Void
    @Environment(\.dismiss) private var dismiss
    private var changed: [RuntimeSettingsSection] {
        guard let base = model.baseline else { return [] }
        let old = RuntimeSettingsDraft(base.settings)
        return RuntimeSettingsSection.allCases.filter { !model.draft.matches(old, section: $0) }
    }
    var body: some View {
        NavigationStack {
            List {
                if preview { Section { Label("Preview · Sample settings", systemImage: "viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) } }
                Section { Text("Save these server defaults?").font(.headline); Text("Changes apply to new transfers and future lifecycle work. Existing paths are not moved.").font(.subheadline).foregroundStyle(DebridStyle.secondary) }
                ForEach(changed, id: \.self) { section in Section(section.label) { SettingsSectionSummary(draft: model.draft, section: section) } }
                Section {
                    Button("Keep editing") { dismiss() }.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint)
                    Button("Save settings", systemImage: "checkmark") { guard model.canReview else { return }; save() }
                        .buttonStyle(.borderless).foregroundStyle(model.draft.policy.hasDeletion ? Color("DangerInk") : DebridStyle.tint)
                        .disabled(!model.canReview).accessibilityIdentifier("settings-save")
                } footer: { Text("Settings are replaced together using the version you reviewed. Newer server changes will be refused for review.") }
            }.modifier(DebridListStyle()).navigationTitle("Review settings").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } } }
        }.tint(DebridStyle.tint)
    }
}
struct SettingsConflictView: View {
    @Environment(\.dismiss) private var dismiss
    @Bindable var model: RuntimeSettingsModel
    let preview: Bool
    let reload: () -> Void
    var body: some View {
        Form {
            if preview { Section { SettingsSampleNotice() } }
            Section { Label("Settings changed", systemImage: "arrow.triangle.branch").font(.headline); Text("Your draft is still here. Changes in separate sections can be combined. Nothing is saved automatically.").foregroundStyle(DebridStyle.secondary) }
            if let latest = model.latest {
                if model.conflicts.isEmpty { Section { Text("There are no competing section changes. Review the combined draft before saving.") } }
                ForEach(model.conflicts, id: \.self) { section in
                    Section(section.label + " · Your draft") { SettingsSectionSummary(draft: model.draft, section: section) }
                    Section(section.label + " · On server") { SettingsSectionSummary(draft: RuntimeSettingsDraft(latest.settings), section: section) }
                    Section {
                        Picker("Use for " + section.label, selection: Binding(get: { model.choices[section]?.rawValue ?? "" }, set: { model.choices[section] = RuntimeSettingsModel.MergeChoice(rawValue: $0) })) {
                            Text("Choose a version").tag(""); Text("My changes").tag("mine"); Text("Server changes").tag("server")
                        }
                    }
                }
                Section { Button("Review combined draft", systemImage: "checklist") { model.mergeForReview(); if model.phase == .editing { dismiss() } }
                    .buttonStyle(.borderless).foregroundStyle(model.conflicts.contains { model.choices[$0] == nil } ? DebridStyle.secondary : DebridStyle.tint)
                    .disabled(model.conflicts.contains { model.choices[$0] == nil }).accessibilityIdentifier("settings-merge") }
            } else { Section { Button("Load latest settings", action: reload).foregroundStyle(DebridStyle.tint).disabled(model.isBusy) } }
        }.modifier(DebridListStyle()).navigationTitle("Review server changes").navigationBarTitleDisplayMode(.inline)
    }
}

struct TransferPolicyEditor: View {
    let initial: TorrentSettingsDTO
    let allowRules: Bool
    let preview: Bool
    let apply: (TorrentSettingsDTO) -> Void
    @State private var draft: DownloadPolicyDraft
    @Environment(\.dismiss) private var dismiss
    init(initial: TorrentSettingsDTO, allowRules: Bool, preview: Bool, apply: @escaping (TorrentSettingsDTO) -> Void) {
        self.initial = initial; self.allowRules = allowRules; self.preview = preview; self.apply = apply; _draft = State(initialValue: DownloadPolicyDraft(initial))
    }
    var body: some View {
        Form {
            if preview { Section { Label("Preview · Sample transfer", systemImage: "viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) } }
            Section { Text("Only this transfer").font(.headline); Text("Overrides are reviewed with this transfer before it is submitted. Server defaults and existing downloads stay unchanged.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
            if !allowRules { Section { Text("Your file-selection choice controls which files download. These options do not change that choice.").font(.footnote).foregroundStyle(DebridStyle.secondary) } }
            DownloadPolicyFields(draft: $draft, allowFileRules: allowRules, allowHold: false)
        }.modifier(DebridListStyle()).navigationTitle("Transfer options").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Use options") { if let value = try? draft.output() { apply(value); dismiss() } }.disabled((try? draft.output()) == nil).accessibilityIdentifier("transfer-options-apply") } }
    }
}
