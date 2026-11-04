import DebridKit
import SwiftUI

struct ManagementSheet:View {
    let model:ManagementModel
    let connectionID:UUID?
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    @State private var command:Task<Void,Never>?
    @State private var showCleanupConfirmation=false
    @State private var retrying=false
    private var title:String {model.kind == .cancel ? "Cancel transfer":"Remove transfer"}
    private struct Context:Equatable {let phase:ScenePhase;let connection:UUID?}
    var body:some View {
        NavigationStack {
            List {
                if model.phase != .review {status}
                if model.phase != .uncertain,let message=model.message {Section {Label(message,systemImage:"exclamationmark.triangle").font(.subheadline).fixedSize(horizontal:false,vertical:true).accessibilityIdentifier("management-message")}}
                if model.isPreview {Section {Label("Preview · Fictional transfer",systemImage:"viewfinder").font(.footnote);Text("This preview does not contact your server.").font(.footnote).foregroundStyle(DebridStyle.secondary)}}
                Section {
                    Label(model.name,systemImage:model.kind == .cancel ? "stop.circle":"trash").font(.headline).fixedSize(horizontal:false,vertical:true)
                    Text(model.serverURL.absoluteString).font(.footnote).foregroundStyle(DebridStyle.secondary).textSelection(.enabled)
                } header: {Text("Transfer")}
                if model.phase == .review {
                    Section {effects} header: {Text(model.kind == .cancel ? "What will stop":"What will be removed")}
                    if model.kind == .remove {cleanupChoices}
                }
                if model.phase == .review {status}
                if model.phase != .review {Section {DisclosureGroup("Original action") {effects;if model.kind == .remove {originalChoices}}}}
            }.modifier(DebridListStyle()).navigationTitle(title).navigationBarTitleDisplayMode(.inline)
                .toolbar {ToolbarItem(placement:.confirmationAction) {Button("Done") {dismiss()}}}
                .confirmationDialog("Apply cleanup?",isPresented:$showCleanupConfirmation,titleVisibility:.visible) {
                    Button("Remove transfer",role:.destructive) {perform()}.accessibilityIdentifier("confirm-management-cleanup")
                    Button("Keep reviewing") {}.accessibilityIdentifier("keep-management-review")
                } message: {Text(cleanupSummary)}
        }.tint(DebridStyle.tint)
            .task(id:Context(phase:scenePhase,connection:connectionID)) {
                guard connectionID==model.connectionID else {command?.cancel();model.stop();dismiss();return}
                if scenePhase == .active {await model.open()} else {command?.cancel();model.stop()}
            }
            .onChange(of:connectionID) {if connectionID != model.connectionID {command?.cancel();model.stop();dismiss()}}
            .onDisappear {command?.cancel();model.stop()}
    }
    @ViewBuilder private var effects:some View {
        if model.kind == .cancel {
            Label("Stop local transfer work",systemImage:"stop.circle")
            Label("Keep NAS files and this transfer",systemImage:"folder")
            Text("Activity at your provider may continue. You can explicitly retry the transfer later.").font(.subheadline).foregroundStyle(DebridStyle.secondary)
        } else {
            Text("Stop local work and remove this transfer from your server’s list. File and provider cleanup are optional.")
            Text("Shared or untracked files and unknown provider copies may remain after requested cleanup.").font(.footnote).foregroundStyle(DebridStyle.secondary)
        }
    }
    private var cleanupSummary:String {
        switch (model.options.deleteFiles,model.options.deleteFromProvider) {
        case (true,true):"Delete NAS files and the known provider copy."
        case (true,false):"Delete NAS files. Keep provider copy."
        case (false,true):"Keep NAS files. Remove known provider copy."
        case (false,false):"Keep NAS files and provider copy."
        }
    }
    private var cleanupChoices:some View {
        Section {
            Toggle("Request NAS file deletion",isOn:Binding(get:{model.draftOptions.deleteFiles},set:{model.setDeleteFiles($0)})).disabled(!model.canEditOptions).accessibilityIdentifier("management-delete-files")
            Toggle("Remove known provider copy",isOn:Binding(get:{model.draftOptions.deleteFromProvider},set:{model.setDeleteProvider($0)})).disabled(!model.canEditOptions).accessibilityIdentifier("management-delete-provider")
            if model.review?.providerCopyKnown == false {Text("The server has no confirmed provider copy to remove. A provider request with an unknown result may still exist.").font(.footnote).foregroundStyle(DebridStyle.secondary)}
        } header: {Text("Optional cleanup")} footer: {Text("Both choices start off. Review carefully before requesting deletion.")}
    }
    private var originalChoices:some View {
        Group {
            LabeledContent("NAS files",value:model.options.deleteFiles ? "Deletion requested":"Keep")
            LabeledContent("Provider copy",value:model.options.deleteFromProvider ? "Removal requested":"Keep")
        }.font(.subheadline)
    }
    @ViewBuilder private var status:some View {
        switch model.phase {
        case .loading,.checking:Section {ProgressView(model.phase == .checking ? "Checking saved action…":"Loading action review…")}
        case .review:
            Section {
                Button(title,role:model.kind == .remove ? .destructive:nil) {request(retry:false)}
                    .buttonStyle(.glassProminent).tint(model.kind == .remove ? Color(red:0.62,green:0.10,blue:0.06):Color(red:0.62,green:0.17,blue:0)).foregroundStyle(.white).controlSize(.large).frame(maxWidth:.infinity)
                    .disabled(!model.canConfirm).accessibilityIdentifier("confirm-management")
            } footer: {Text("Confirm to send this action to the server shown above.")}
        case .sending:Section {ProgressView(model.kind == .cancel ? "Stopping local work…":"Sending saved removal…");Text("Closing this sheet does not undo server work. An unconfirmed request stays in Pending Items.").font(.footnote).foregroundStyle(DebridStyle.secondary)}
        case .uncertain:
            Section("Result not confirmed") {
                refresh
                Button(model.kind == .cancel ? "Retry saved cancellation":"Retry saved removal",role:model.kind == .remove ? .destructive:nil) {request(retry:true)}.buttonStyle(.borderless).foregroundStyle(model.kind == .remove ? Color("DangerInk"):DebridStyle.tint).disabled(!model.canRetry).accessibilityIdentifier("retry-management")
                Text(model.message ?? "The original action is kept until its result is confirmed.").font(.footnote).foregroundStyle(DebridStyle.secondary)
            }
        case .accepted(let outcome):Section {
            switch outcome {
            case .cancellation(_,let exists):
                Label("Cancellation accepted",systemImage:"checkmark.circle.fill").foregroundStyle(DebridStyle.tint).accessibilityIdentifier("management-accepted")
                Text(exists ? "Your server accepted the cancellation. Refresh the transfer for its current state; a later explicit retry may have restarted it.":"Your server accepted the cancellation earlier. The transfer has since been removed.")
                Text("Provider activity may continue. Saved files are retained as they stood after local work stopped.").font(.footnote).foregroundStyle(DebridStyle.secondary)
            case .removalConfirmed:
                Label("Transfer removed",systemImage:"checkmark.circle.fill").foregroundStyle(DebridStyle.tint).accessibilityIdentifier("management-accepted")
                Text("The server finished removing this record. Cleanup followed your choices where safe; shared files and unknown provider copies may remain.")
                originalChoices
            case .absenceObserved:
                Label("No longer listed",systemImage:"tray").accessibilityIdentifier("management-absent")
                Text(model.intent == nil ? "This transfer is no longer listed. No removal was sent by this review.":"The transfer is no longer listed. The earlier removal and its file or provider cleanup cannot be confirmed.").accessibilityIdentifier("management-cleanup-unconfirmed")
            }
        }
        case .rejected:Section {Label("Action was not accepted",systemImage:"exclamationmark.circle");refresh}
        case .requiresUpdate:Section {Label("Server update required",systemImage:"arrow.up.circle");refresh}
        case .blocked:Section {Label("Saved action needs checking",systemImage:"exclamationmark.shield");refresh}
        case .unavailable:Section {Label("Action unavailable",systemImage:"exclamationmark.triangle");refresh}
        }
    }
    private var refresh:some View {Button(refreshTitle) {run {await model.refresh()}}.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint).disabled(model.isBusy).accessibilityIdentifier("check-management-result")}
    private var refreshTitle:String {switch model.phase {case .rejected:"Review again";case .requiresUpdate:"Check server";case .unavailable:"Reload review";default:"Check result"}}
    private func request(retry:Bool) {
        retrying=retry
        if model.kind == .remove && (model.options.deleteFiles || model.options.deleteFromProvider) {showCleanupConfirmation=true}
        else {perform()}
    }
    private func perform() {if retrying {run {await model.retry()}}else{run {await model.confirm()}}}
    private func run(_ operation:@escaping @MainActor () async -> Void) {guard command==nil,scenePhase == .active,connectionID==model.connectionID else {return};command=Task {await operation();command=nil}}
}
