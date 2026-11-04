import DebridKit
import SwiftUI


struct TransferFilesRoot: View {
    let model: TransferListModel
    let transferID: String
    let preview: Bool
    @State private var browser = TransferFileBrowserModel()
    private var previewFolder: String {
        #if DEBUG
        if preview, ProcessInfo.processInfo.arguments.contains("-FilesFolderPreview") { return "Video" }
        #endif
        return ""
    }
    var body: some View { FileFolderView(model:model,transferID:transferID,preview:preview,browser:browser,folder:previewFolder) }
}
private struct FileFolderView: View {
    let model: TransferListModel
    let transferID: String
    let preview: Bool
    let browser: TransferFileBrowserModel
    let folder: String
    @State private var query = ""
    private var hasQuery: Bool { !query.trimmingCharacters(in:.whitespacesAndNewlines).isEmpty }
    var body: some View {
        List {
            FileBrowserNotice(model:model,transferID:transferID,preview:preview)
            if let index = browser.index, model.items.contains(where:{ $0.id == transferID }) {
                if index.limited { Section { Text("Showing the first \(index.files.count) of \(index.totalFiles) files. Use your server to view the complete list.").foregroundStyle(DebridStyle.secondary) } }
                if !folder.isEmpty, index.folders[folder] == nil {
                    Section { ContentUnavailableView("Folder no longer listed",systemImage:"folder.badge.questionmark",description:Text("Go back to the file list for current information.")) }.listRowBackground(Color.clear)
                } else {
                    let children = index.contents(of:folder,query:query)
                    let folders = hasQuery ? [] : index.subfolders(of:folder)
                    if !folders.isEmpty { Section("Folders") {
                        ForEach(folders) { item in
                            NavigationLink { FileFolderView(model:model,transferID:transferID,preview:preview,browser:browser,folder:item.id) } label: { Label(item.name,systemImage:"folder").foregroundStyle(DebridStyle.ink) }
                        }
                    } }
                    if !children.isEmpty { Section(hasQuery ? "Matching files" : "Files") {
                        ForEach(children) { file in
                            if !file.ambiguousIdentity { NavigationLink { TransferFileDetail(model:model,transferID:transferID,preview:preview,browser:browser,fileID:file.id) } label: {
                                VStack(alignment:.leading,spacing:5) {
                                    Text(file.name).font(.headline).foregroundStyle(DebridStyle.ink)
                                    Text(file.status).font(.subheadline).foregroundStyle(file.downloads.count == 1 ? file.downloads[0].progressInk : DebridStyle.secondary)
                                    if let download = file.downloads.first, file.downloads.count == 1, let fraction = FileDownloadSummary(download).fraction {
                                        ProgressView(value:fraction).tint(download.progressInk).accessibilityLabel(file.status)
                                    }
                                    HStack {
                                        Text(file.file.selected ? "Selected" : "Not selected")
                                        if file.file.size >= 0 { Text(file.file.size.formatted(.byteCount(style:.file))) }
                                    }.font(.footnote).foregroundStyle(DebridStyle.secondary)
                                    if hasQuery { Text(TransferSearchIndex.bounded(file.file.path,limit:512)).font(.caption).foregroundStyle(DebridStyle.secondary) }
                                }.padding(.vertical,6).accessibilityElement(children:.combine)
                            }.accessibilityIdentifier("file-row-"+file.file.id)
                            } else { VStack(alignment:.leading,spacing:6) { Text(file.name).font(.headline); Text("Duplicate file metadata · cannot identify this file uniquely").font(.footnote).foregroundStyle(DebridStyle.secondary) } }
                        }
                    } }
                    if children.isEmpty && folders.isEmpty {
                        Section { ContentUnavailableView(hasQuery ? "No matching files" : "File metadata unavailable",systemImage:hasQuery ? "magnifyingglass" : "doc",description:Text(hasQuery ? "Try another file path or name." : "The provider has not supplied files for this location.")) }.listRowBackground(Color.clear)
                    }
                }
                if folder.isEmpty && !hasQuery && !index.unmatchedDownloads.isEmpty {
                    Section {
                        ForEach(Array(index.unmatchedDownloads.enumerated()),id:\.offset) { _,download in
                            VStack(alignment:.leading,spacing:6) {
                                Text(TransferFileIndex.display(download.path)).font(.headline)
                                DownloadRecordContent(download:download)
                            }.padding(.vertical,6)
                        }
                    } header: { Text("Other download records") } footer: { Text("These records could not be matched uniquely to the current provider file metadata.") }
                }
            } else if model.items.contains(where:{ $0.id == transferID }) { Section { ProgressView("Loading files…") } }
        }.modifier(DebridListStyle())
            .navigationTitle(folder.isEmpty ? "Files" : browser.index?.folders[folder]?.name ?? "Folder").navigationBarTitleDisplayMode(.inline)
            .searchable(text:Binding(get:{ query },set:{ query = TransferSearchIndex.bounded($0,limit:256) }),prompt:"Search file paths")
            .refreshable { await model.refreshWhileActive() }
            .task(id:model.updatedAt) { await browser.update(transfer:model.items.first(where:{ $0.id == transferID })?.transfer,updatedAt:model.updatedAt) }
    }
}
private struct TransferFileDetail: View {
    let model: TransferListModel
    let transferID: String
    let preview: Bool
    let browser: TransferFileBrowserModel
    let fileID: String
    @Environment(\.magnetIntake) private var intake
    @State private var retryModel:RetryModel?
    @State private var retryMessage:String?
    var body: some View {
        List {
            FileBrowserNotice(model:model,transferID:transferID,preview:preview)
            if model.items.contains(where:{ $0.id == transferID }), let file = browser.index?.files.first(where:{ $0.id == fileID }) {
                Section("Provider file") {
                    Text(file.name).font(.headline)
                    LabeledContent("Selection",value:file.file.selected ? "Selected" : "Not selected")
                    if file.file.size >= 0 { LabeledContent("Size",value:file.file.size.formatted(.byteCount(style:.file))) }
                    VStack(alignment:.leading,spacing:6) { Text("Path reported by provider").font(.subheadline); Text(TransferSearchIndex.bounded(file.file.path,limit:4096)).font(.footnote.monospaced()).textSelection(.enabled) }
                    if file.pathNotice { Text("This path is displayed without folder navigation.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                }
                if file.downloads.isEmpty { Section { Text(file.status).foregroundStyle(DebridStyle.secondary) } }
                ForEach(Array(file.downloads.enumerated()),id:\.offset) { offset,download in
                    Section(file.downloads.count > 1 ? "NAS download \(offset+1)" : "NAS download") {
                        DownloadRecordContent(download:download)
                        if !preview,download.state=="error" {
                            Button("Retry this file",systemImage:"arrow.clockwise") {
                                do {retryModel=try intake?.retryModel(kind:.download,resourceID:download.id,torrentID:transferID,name:file.name)} catch {retryMessage="Retry review could not be opened. Check protected storage and try again."}
                            }.buttonStyle(.borderless).foregroundStyle(DebridStyle.tint).accessibilityIdentifier("retry-file-"+download.id)
                        }
                    }
                }
                Section { Text("These files belong to your provider and server. They have not been downloaded to this iPhone.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
            } else if model.items.contains(where:{ $0.id == transferID }) {
                Section { ContentUnavailableView("File no longer listed",systemImage:"doc.badge.ellipsis",description:Text("Return to Files to view the current metadata.")) }.listRowBackground(Color.clear)
            }
        }.modifier(DebridListStyle()).navigationTitle("File").navigationBarTitleDisplayMode(.inline)
            .sheet(isPresented:Binding(get:{retryModel != nil},set:{if !$0 {retryModel=nil}}),onDismiss:{Task {await intake?.refresh();await model.refreshWhileActive()}}) {
                if let retryModel {RetrySheet(model:retryModel,connectionID:intake?.connection?.connectionID).onChange(of:retryModel.requiresSignIn) {if retryModel.requiresSignIn {Task {await intake?.rejectConnection(retryModel.connectionID)}}}}
            }
            .alert("Retry unavailable",isPresented:Binding(get:{retryMessage != nil},set:{if !$0 {retryMessage=nil}})) {Button("OK",role:.cancel) {retryMessage=nil}} message: {Text(retryMessage ?? "")}
            .refreshable { await model.refreshWhileActive() }
            .task(id:model.updatedAt) { await browser.update(transfer:model.items.first(where:{ $0.id == transferID })?.transfer,updatedAt:model.updatedAt) }
    }
}
private struct DownloadRecordContent: View {
    let download: DownloadDTO
    var body: some View {
        let summary = FileDownloadSummary(download)
        Text(summary.label).font(.subheadline).foregroundStyle(download.state == "error" ? Color("DangerInk") : DebridStyle.secondary)
        if let fraction = summary.fraction { ProgressView(value:fraction).tint(download.progressInk).accessibilityLabel(summary.label); Text(fraction.formatted(.percent.precision(.fractionLength(0)))).font(.subheadline.monospacedDigit()) }
        else { Text("Progress unavailable").font(.footnote).foregroundStyle(DebridStyle.secondary) }
        if let done = summary.bytesDone, download.size > 0 { Text(done.formatted(.byteCount(style:.file))+" of "+download.size.formatted(.byteCount(style:.file))).font(.footnote).foregroundStyle(DebridStyle.secondary) }
        if download.state == "error" { Text("Your server couldn’t download this file.").font(.footnote).foregroundStyle(DebridStyle.secondary) }
        if download.retryCount > 0 { LabeledContent("Retries",value:"\(download.retryCount)") }
    }
}
private struct FileBrowserNotice: View {
    let model: TransferListModel
    let transferID: String
    let preview: Bool
    var body: some View {
        if preview { Section { Label("Preview · Sample files",systemImage:"viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) }.listRowBackground(Color.clear).listRowSeparator(.hidden) }
        if case .failed(let message) = model.phase { Section { Label(message,systemImage:"exclamationmark.triangle"); Text("Showing the last loaded file information.").font(.footnote) }.foregroundStyle(DebridStyle.secondary) }
        if !model.items.contains(where:{ $0.id == transferID }) {
            Section {
                if model.updatedAt != nil { ContentUnavailableView("Transfer no longer listed",systemImage:"tray",description:Text("Return to Transfers to view the current queue.")) }
                else if case .failed = model.phase { ContentUnavailableView("Transfer unavailable",systemImage:"wifi.exclamationmark",description:Text("Pull to refresh when your server is reachable.")) }
                else { ProgressView("Waiting for transfer…") }
            }.listRowBackground(Color.clear)
        }
    }
}

private extension DownloadDTO {
    var progressInk: Color {
        switch state {
        case "done": Color("StageGreen")
        case "error": .red
        default: Color("StageBlue")
        }
    }
}
