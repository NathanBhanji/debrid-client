import DebridKit
import SwiftUI

struct RetrySheet:View {
    let model:RetryModel
    let connectionID:UUID?
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    @State private var command:Task<Void,Never>?
    private var title:String {model.kind == .torrent ? "Retry transfer" : "Retry file"}
    private struct Context:Equatable {let phase:ScenePhase;let connection:UUID?}
    var body:some View {
        NavigationStack {
            List {
                if model.isPreview {Section {Label("Preview · Fictional retry",systemImage:"viewfinder").font(.footnote);Text("This preview does not contact your server.").font(.footnote).foregroundStyle(DebridStyle.secondary)}}
                Section {
                    Label(model.name,systemImage:model.kind == .torrent ? "arrow.trianglehead.2.clockwise" : "doc").font(.headline).fixedSize(horizontal:false,vertical:true)
                    Text(model.serverURL.absoluteString).font(.footnote).foregroundStyle(DebridStyle.secondary).textSelection(.enabled)
                } header: {Text(model.kind == .torrent ? "Transfer" : "NAS download")}
                if model.phase == .review {Section {effects} header: {Text("When you retry")}}
                if model.phase != .uncertain,let message=model.message {Section {Label(message,systemImage:"exclamationmark.triangle").font(.subheadline).fixedSize(horizontal:false,vertical:true).accessibilityIdentifier("retry-message")}}
                status
                if model.phase != .review {Section {DisclosureGroup("Original retry") {effects}}}
            }.modifier(DebridListStyle()).navigationTitle(title).navigationBarTitleDisplayMode(.inline)
                .toolbar {ToolbarItem(placement:.confirmationAction) {Button("Done") {dismiss()}}}
        }.tint(DebridStyle.tint)
            .task(id:Context(phase:scenePhase,connection:connectionID)) {
                guard connectionID==model.connectionID else {command?.cancel();model.stop();dismiss();return}
                if scenePhase == .active {await model.open()} else {command?.cancel();model.stop()}
            }
            .onChange(of:connectionID) {if connectionID != model.connectionID {command?.cancel();model.stop();dismiss()}}
            .onDisappear {command?.cancel();model.stop()}
    }
    @ViewBuilder private var effects:some View {
        if model.kind == .torrent {
            Text("Starts a new attempt and resets download progress. Existing NAS files stay in place; later downloads may resume or replace them.")
            if model.waitForSelection {Label("Choose files again before NAS downloads start.",systemImage:"checklist").foregroundStyle(Color("StageAmber"))}
        } else {Text("Retries this failed NAS download. Already completed files are kept.")}
    }
    @ViewBuilder private var status:some View {
        switch model.phase {
        case .loading,.checking:Section {ProgressView(model.phase == .checking ? "Checking saved retry…" : "Loading retry review…")}
        case .review:
            Section {
                Button(model.kind == .torrent ? "Retry transfer" : "Retry this file") {run {await model.confirm()}}
                    .buttonStyle(.glassProminent).tint(Color(red:0.62,green:0.17,blue:0)).foregroundStyle(.white).controlSize(.large).frame(maxWidth:.infinity)
                    .disabled(!model.canConfirm).accessibilityIdentifier("confirm-retry")
            } footer: {Text("Confirm to send this retry to your server.")}
        case .sending:Section {ProgressView("Sending saved retry…");Text("Closing this sheet does not undo a retry already accepted by your server.").font(.footnote).foregroundStyle(DebridStyle.secondary)}
        case .uncertain:
            Section("Result not confirmed") {
                refresh
                Button("Retry same request") {run {await model.retry()}}.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint).disabled(!model.canRetry).accessibilityIdentifier("retry-same-request")
                Text(model.message ?? "The original retry is kept until its result is confirmed.").font(.footnote).foregroundStyle(DebridStyle.secondary)
            }
        case .accepted(let exists):
            Section {
                Label("Retry request accepted",systemImage:"checkmark.circle.fill").foregroundStyle(DebridStyle.tint).accessibilityIdentifier("retry-accepted")
                Text(exists ? "Refresh the transfer for current progress. Acceptance does not mean the download has completed." : "The server accepted this retry earlier. Its transfer has since been removed.").font(.subheadline).fixedSize(horizontal:false,vertical:true)
                if exists,model.kind == .torrent,model.waitForSelection {Label("Choose files again before NAS downloads start.",systemImage:"checklist").foregroundStyle(Color("StageAmber"))}
                if model.kind == .download,exists {Text("This receipt confirms the earlier request, even if the file row has since changed or been removed.").font(.footnote).foregroundStyle(DebridStyle.secondary)}
            }
        case .rejected:Section {Label("Retry was not accepted",systemImage:"exclamationmark.circle");refresh}
        case .requiresUpdate:Section {Label("Server update required",systemImage:"arrow.up.circle");refresh}
        case .blocked:Section {Label("Saved retry mismatch",systemImage:"exclamationmark.shield");refresh}
        case .unavailable:Section {Label("Retry unavailable",systemImage:"arrow.trianglehead.2.clockwise");refresh}
        }
    }
    private var refresh:some View {Button(refreshTitle) {run {await model.refresh()}}.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint).disabled(model.isBusy).accessibilityIdentifier("check-retry-result")}
    private var refreshTitle:String {
        switch model.phase {case .rejected:"Review again";case .requiresUpdate:"Check server";case .unavailable:"Reload review";default:"Check result"}
    }
    private func run(_ operation:@escaping @MainActor () async -> Void) {guard command==nil,scenePhase == .active,connectionID==model.connectionID else {return};command=Task {await operation();command=nil}}
}
