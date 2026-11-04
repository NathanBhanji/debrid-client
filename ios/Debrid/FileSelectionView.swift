import DebridKit
import SwiftUI

/// This sheet owns navigation and the lease; pushing folders never closes the review.
struct FileSelectionSheet:View {
    let model:FileSelectionModel
    var connectionID:UUID? = nil
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    var body:some View {
        NavigationStack {
            SelectionFolderView(model:model,folder:Data())
                .toolbar {ToolbarItem(placement:.confirmationAction) {Button("Done") {dismiss()}}}
        }.tint(DebridStyle.tint)
            .task(id:scenePhase) {guard model.isPreview || connectionID==model.connectionID else {model.stop();dismiss();return};if scenePhase == .active {await model.open()} else {model.stop()}}
            .onChange(of:connectionID) {if !model.isPreview,connectionID != model.connectionID {model.stop();dismiss()}}
            .onDisappear {model.stop()}
    }
}
private struct SelectionFolderView:View {
    @Bindable var model:FileSelectionModel
    let folder:Data
    @Environment(\.scenePhase) private var scenePhase
    @State private var query=""
    @State private var matches:[FileSelectionReview.File]=[]
    @State private var proposal:FileSelectionProposal?
    @State private var command:Task<Void,Never>?
    private var searching:Bool {!query.trimmingCharacters(in:.whitespacesAndNewlines).isEmpty}
    private var title:String {folder.isEmpty ? "Choose files" : model.review?.folders[folder]?.name ?? "Folder"}
    private struct SearchKey:Equatable {let revision:String?;let query:String}
    var body:some View {
        List {
            if model.isPreview {Section {Label("Preview · Fictional files",systemImage:"viewfinder");Text("This preview does not contact your server.").font(.footnote)}}
            if folder.isEmpty {Section {Text(model.name).font(.headline);Text("Downloads to your NAS wait for a confirmed selection.").font(.subheadline).foregroundStyle(DebridStyle.secondary)}}
            if let message=model.message {Section {Label(message,systemImage:"exclamationmark.triangle").font(.subheadline).fixedSize(horizontal:false,vertical:true)}}
            if let review=model.review,model.phase == .editing {
                if !searching {
                    let folders=review.subfolders(in:folder)
                    if !folders.isEmpty {Section("Folders") {ForEach(folders) {item in
                        NavigationLink {SelectionFolderView(model:model,folder:item.id)} label: {Label(item.name,systemImage:"folder").foregroundStyle(DebridStyle.ink)}
                    }}}
                }
                let files=searching ? matches : review.contents(in:folder)
                if !files.isEmpty {Section(searching ? "Matches in all folders" : "Files") {ForEach(files) {file in
                    Button {model.toggle(file.id)} label: {
                        HStack(spacing:12) {
                            Image(systemName:model.selected.contains(file.id) ? "checkmark.circle.fill" : "circle").font(.title3).foregroundStyle(model.selected.contains(file.id) ? DebridStyle.tint : DebridStyle.secondary).accessibilityHidden(true)
                            VStack(alignment:.leading,spacing:4) {
                                Text(file.name).foregroundStyle(DebridStyle.ink)
                                Text(file.size==0 ? "Size unknown" : file.size.formatted(.byteCount(style:.file))).font(.footnote).foregroundStyle(DebridStyle.secondary)
                                if searching {Text(file.path).font(.caption).foregroundStyle(DebridStyle.secondary)}
                            }
                        }.frame(minHeight:44).contentShape(Rectangle())
                    }.buttonStyle(.plain).disabled(!model.canEdit)
                        .accessibilityAddTraits(model.selected.contains(file.id) ? [.isSelected] : [])
                        .accessibilityLabel(searching ? file.path : file.name)
                        .accessibilityValue((model.selected.contains(file.id) ? "Selected. " : "Not selected. ") + (file.size == 0 ? "Size unknown" : file.size.formatted(.byteCount(style: .file))))
                        .accessibilityHint("Changes whether this file is included in your selection. Downloads wait until final confirmation.")
                }}}
                else if searching {Section {ContentUnavailableView.search(text:query)}.listRowBackground(Color.clear)}
                Section {
                    Button(searching ? "Select all matching files" : "Select this folder and subfolders") {model.select(searching ? matches : review.descendants(in:folder))}.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).disabled(!model.canEdit)
                    Button("Clear all selected files") {model.clear()}.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).disabled(!model.canEdit || model.selected.isEmpty)
                } footer: {Text("Selections in other folders are kept. Nothing is selected automatically.")}
            } else {status}
        }.modifier(DebridListStyle()).navigationTitle(title).navigationBarTitleDisplayMode(.inline)
            .searchable(text:$query,prompt:"Search all files").autocorrectionDisabled().textInputAutocapitalization(.never)
            .task(id:SearchKey(revision:model.review?.response.revision,query:query)) {
                guard let review=model.review else {matches=[];return}
                do {let result=try await review.search(query,in:folder);if !Task.isCancelled {matches=result}} catch {}
            }
            .toolbar {ToolbarItemGroup(placement:.bottomBar) {
                Text("\(model.selected.count) selected").font(.subheadline.monospacedDigit()).accessibilityIdentifier("selection-count")
                Spacer()
                Button("Review") {run {proposal=await model.proposal()}}.disabled(!model.canReview).accessibilityIdentifier("review-file-selection")
            }}
            .sheet(isPresented:Binding(get:{proposal != nil},set:{if !$0 {proposal=nil}})) {
                if let value=proposal {confirmation(value)}
            }
            .onChange(of:scenePhase) {if scenePhase != .active {command?.cancel();proposal=nil}}
            .onDisappear {command?.cancel()}
    }
    @ViewBuilder private var status:some View {
        Section {
            switch model.phase {
            case .loading,.checking,.sending:ProgressView(model.phase == .sending ? "Confirming selection…" : "Checking file review…")
            case .waiting:Label("Waiting for provider metadata",systemImage:"clock");Text("Your provider may finish preparing all files before a review is available. NAS downloads remain on hold.").font(.footnote);refresh
            case .uncertain:Label("Result not confirmed",systemImage:"questionmark.circle");refresh;Button("Retry same selection") {run {await model.retry()}}.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).disabled(!model.canRetry)
            case .rejected:Label("Selection was not accepted",systemImage:"exclamationmark.circle");refresh
            case .accepted(let source):
                Label(source == .other ? "Another selection was confirmed" : "Selection confirmed",systemImage:"checkmark.circle.fill").foregroundStyle(DebridStyle.tint)
                Text(source == .other ? "The server reports a different choice. Your saved request will not replace it." : "The server can now download the confirmed files to your NAS.").font(.subheadline)
            case .requiresUpdate:Label("Server update required",systemImage:"arrow.up.circle");refresh
            case .blocked:Label("Saved choice needs attention",systemImage:"exclamationmark.shield");refresh
            case .unavailable:Label("File review unavailable",systemImage:"wifi.exclamationmark");refresh
            case .editing:EmptyView()
            }
        }
    }
    private var refresh:some View {Button("Check again") {run {await model.refresh()}}.buttonStyle(DebridRowActionStyle()).foregroundStyle(DebridStyle.tint).disabled(model.isBusy)}
    private func run(_ action:@escaping @MainActor () async -> Void) {guard command==nil else {return};command=Task {await action();command=nil}}
    private func confirmation(_ value:FileSelectionProposal)->some View {
        NavigationStack {
            Form {
                Section {Text(value.count == 1 ? "1 file" : "\(value.count) files").font(.title2.weight(.semibold));if let bytes=value.bytes {Text(bytes.formatted(.byteCount(style:.file)))};if value.hasUnknownSizes {Text("Some file sizes are unknown.").font(.footnote)}}
                Section {Text("Confirm these files for download to your NAS. Your provider may prepare or retain additional files remotely.")}
                Section {Button("Confirm files and start downloads") {proposal=nil;run {await model.confirm(value)}}.buttonStyle(.glassProminent).tint(Color(red:0.62,green:0.17,blue:0)).foregroundStyle(.white).controlSize(.large).frame(maxWidth:.infinity).disabled(model.isBusy).accessibilityIdentifier("confirm-file-selection")}
            }.modifier(DebridListStyle()).navigationTitle("Review selection").navigationBarTitleDisplayMode(.inline)
                .toolbar {ToolbarItem(placement:.cancellationAction) {Button("Back") {proposal=nil}}}
        }.tint(DebridStyle.tint)
    }
}
