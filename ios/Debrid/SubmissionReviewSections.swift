import DebridKit
import SwiftUI

struct SubmissionReviewSections:View {
    @Bindable var model:TransferAddModel
    let action:(Action)->Void
    @State private var showingOptions = false
    @Environment(\.scenePhase) private var scenePhase
    enum Action { case add, check, retry, reload, finish, view(String) }
    var body:some View {
        if model.isPreview { Section { Label("Preview · Fictional submission",systemImage:"viewfinder").font(.footnote);Text("This preview does not contact your server.").font(.footnote).foregroundStyle(DebridStyle.secondary) } }
        if let settings = model.editableOverrides {
            Section {
                Button("Transfer options", systemImage: "slider.horizontal.3") { showingOptions = true }
                    .buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).accessibilityIdentifier("transfer-options")
                    .sheet(isPresented: $showingOptions) {
                        NavigationStack {
                            TransferPolicyEditor(initial: settings, allowRules: model.filePolicy == .rules, preview: model.isPreview) { model.applyOverrides($0) }
                                .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { showingOptions = false } } }
                        }.tint(DebridStyle.tint)
                    }
                    .onChange(of: model.connectionID) { showingOptions = false }
                    .onChange(of: model.canEdit) { if !model.canEdit { showingOptions = false } }
                    .onChange(of: scenePhase) { if scenePhase != .active { showingOptions = false } }
            }
        }
        Section("Destination") {
            Text(model.serverURL.absoluteString).font(.footnote).textSelection(.enabled)
            if model.canEdit {
                Picker("Provider",selection:$model.accountID) {
                    Text("Choose provider").tag("")
                    ForEach(model.accounts) { account in Text(account.name+" · "+provider(account.kind)).tag(account.id) }
                }.accessibilityIdentifier("add-provider")
                Picker("Category",selection:Binding(get:{Data(model.category.utf8)},set:{if let value=String(data:$0,encoding:.utf8){model.category=value}})) {
                    Text("Uncategorized").tag(Data())
                    ForEach(model.categories,id:\.submissionCategoryBytes) { value in Text(value).tag(Data(value.utf8)) }
                    if !model.category.isEmpty,!model.categories.contains(where:{Data($0.utf8)==Data(model.category.utf8)}) { Text("Saved: "+model.category).tag(Data(model.category.utf8)) }
                }.accessibilityIdentifier("add-category")
                if model.needsTargetConfirmation { Toggle("Use this server instead of the saved destination",isOn:$model.confirmedTargetChange) }
            } else if let options=model.visibleOptions {
                LabeledContent("Provider account",value:model.accounts.first(where:{ $0.id==options.accountID })?.name ?? options.accountID)
                LabeledContent("Category",value:options.category.isEmpty ? "Uncategorized" : options.category)
            }
        }
        if model.canEdit {
            Section {
                Picker("File selection",selection:$model.filePolicy) { Text("Reviewed rules").tag(TransferAddModel.FilePolicy.rules);Text("All files").tag(TransferAddModel.FilePolicy.all);Text("Choose after adding").tag(TransferAddModel.FilePolicy.choose) }.accessibilityIdentifier("review-file-policy")
                Toggle("Extract archives on the server",isOn:$model.unpack).accessibilityIdentifier("review-unpack")
                Picker("Library organization",selection:$model.organization) {
                    Text("Inherit server").tag(TransferAddModel.Organization.inherit)
                    Text("On").tag(TransferAddModel.Organization.on)
                    Text("Off").tag(TransferAddModel.Organization.off)
                }
                if model.hasAutomaticDeletion { Toggle("Keep copies instead of automatic cleanup",isOn:$model.keepCopies).accessibilityIdentifier("review-keep-copies") }
            } header: { Text("Files and processing") } footer: { Text(model.filePolicy == .choose ? "The provider prepares this transfer. Downloads to your NAS wait until you choose files and confirm." : "Files are selected automatically using the reviewed rules when metadata becomes available.") }
        }
        if !model.canEdit,let settings=model.visibleSettings {
            Section("Saved processing options") {
                LabeledContent("File selection",value:settings.waitForSelection == true ? "Choose after adding" : "Automatic")
                LabeledContent("Archive extraction",value:settings.unpack ? "On" : "Off")
                LabeledContent("Library organization",value:settings.organize.map { $0 ? "On" : "Off" } ?? "Inherit server")
            }
        }
        if let settings=model.visibleSettings { rules(settings) }
        if let message=model.message { Section { Label(message,systemImage:"exclamationmark.triangle").font(.subheadline).fixedSize(horizontal:false,vertical:true).accessibilityIdentifier("submission-message") } }
        switch model.phase {
        case .loading: Section { ProgressView("Loading review options…") }
        case .review:
            Section {
                Button("Add transfer",systemImage:"plus") { action(.add) }
                    .buttonStyle(.glassProminent).tint(Color(red:0.62,green:0.17,blue:0)).foregroundStyle(.white).controlSize(.large)
                    .frame(maxWidth:.infinity).disabled(!model.canSubmit).accessibilityIdentifier("add-reviewed-transfer")
            } footer: { Text("Adds this transfer using the reviewed settings. Files download to your server.") }
        case .sending: Section { ProgressView("Sending to server…");Text("Your request is saved. If the connection is interrupted, Debrid can check its result.").font(.footnote) }
        case .checking: Section { ProgressView("Checking the saved request…") }
        case .uncertain:
            Section("Result not confirmed") {
                Button("Check result",systemImage:"arrow.clockwise") { action(.check) }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).accessibilityIdentifier("check-submission-result")
                Button("Retry same submission") { action(.retry) }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).disabled(!model.canRetry).accessibilityIdentifier("retry-saved-submission")
                Text("The original options are kept until the server confirms the result.").font(.footnote).foregroundStyle(DebridStyle.secondary)
            }
        case .accepted(let id,let exists):
            Section {
                Label(exists ? "Added to your server" : "Added earlier, since removed",systemImage:"checkmark.circle.fill").foregroundStyle(DebridStyle.tint).accessibilityElement(children:.combine).accessibilityIdentifier("submission-accepted")
                if exists { Button("View transfer",systemImage:"arrow.up.right") { action(.view(id)) }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).accessibilityIdentifier("view-added-transfer") }
                Button(model.durableAcceptance ? "Done" : "Keep tracking for later") { action(.finish) }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).disabled(model.isBusy)
            }
        case .duplicate(let id):
            Section(model.canViewPriorDuplicate ? "Already on this provider" : "Prior request on another server") {
                Text("An active transfer with this info hash already exists.")
                if model.canViewPriorDuplicate { Button("View existing transfer") { action(.view(id)) }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint) }
                else if let prior=model.attempt?.serverURL { Text(prior.absoluteString).font(.footnote) }
                Button("Review options") { model.editRejectedOptions() }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint)
            }
        case .rejected: Section { Text("The server rejected these options. No transfer was added by this request.");Button("Review options") { model.editRejectedOptions() }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint) }
        case .wrongServer(let url): Section { Label("Reconnect to the original server",systemImage:"server.rack");Text(url.absoluteString).font(.footnote).textSelection(.enabled);Text("This saved request is bound to that server.").font(.footnote) }
        case .requiresUpdate: Section { Label("Server update required",systemImage:"arrow.up.circle");Text("This server does not advertise safe submission recovery. The item remains saved on this iPhone.").font(.footnote);Button("Check server again") { action(.reload) }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint) }
        case .mismatched: Section { Label("Saved request mismatch",systemImage:"exclamationmark.shield");Text("The saved request does not match the server’s record. It has been kept without resubmitting.").font(.footnote) }
        case .unavailable: Section { Button("Try again") { action(.reload) }.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint) }
        }
    }
    private func provider(_ kind:String) -> String { ["torbox":"TorBox","realdebrid":"Real-Debrid","alldebrid":"AllDebrid","premiumize":"Premiumize","debridlink":"Debrid-Link"][kind] ?? "Provider" }
    @ViewBuilder private func rules(_ settings:TorrentSettingsDTO) -> some View {
        Section("Reviewed file rules") {
            if settings.waitForSelection == true { Label("NAS downloads wait for your confirmation",systemImage:"hand.raised").foregroundStyle(Color("StageAmber")) }
            if (settings.minFileSize ?? 0)>0 { LabeledContent("Minimum size",value:ByteCountFormatter.string(fromByteCount:settings.minFileSize ?? 0,countStyle:.file)) }
            if let include=settings.includeRegex,!include.isEmpty { Text("Include: "+String(include.prefix(1024))).font(.footnote.monospaced()) }
            if let exclude=settings.excludeRegex,!exclude.isEmpty { Text("Exclude: "+String(exclude.prefix(1024))).font(.footnote.monospaced()) }
            if let manual=settings.manualFiles,!manual.isEmpty { LabeledContent("Saved file IDs",value:"\(manual.count)") }
            if (settings.minFileSize ?? 0)==0,(settings.includeRegex ?? "").isEmpty,(settings.excludeRegex ?? "").isEmpty,(settings.manualFiles ?? []).isEmpty { Text("All files") }
            LabeledContent("File retries",value:"\(settings.downloadRetries)")
            LabeledContent("Provider retries",value:"\(settings.torrentRetries)")
        }
        if settings.finishedAction=="remove_from_provider" || !(settings.deleteOnError ?? "").isEmpty || !(settings.lifetime ?? "").isEmpty {
            Section("Automatic lifecycle rules") {
                if settings.finishedAction=="remove_from_provider" {
                    Label("Remove provider copy after completion",systemImage:"trash").foregroundStyle(Color("DangerInk"))
                    if let delay=settings.finishedDelay,!delay.isEmpty { Text("Completion delay: "+delay).font(.footnote) }
                }
                if let delay=settings.deleteOnError,!delay.isEmpty { Text("After a failed transfer, delete downloaded files and the provider copy after "+delay+".").foregroundStyle(Color("DangerInk")).fixedSize(horizontal:false,vertical:true) }
                if let lifetime=settings.lifetime,!lifetime.isEmpty { Text("The provider must finish within "+lifetime+".").font(.footnote) }
            }
        }
    }
}

private extension String { var submissionCategoryBytes: Data { Data(utf8) } }
