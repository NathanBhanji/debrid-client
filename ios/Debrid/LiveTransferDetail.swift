import DebridKit
import SwiftUI

/// Uses the navigation stack's one live snapshot/stream, including while the list is off screen.
struct LiveTransferDetail: View {
    let transferID: String
    let model: TransferListModel
    let preview: Bool
    @Environment(\.dismiss) private var dismiss
    var body: some View {
        List {
            if preview {
                Section { Label("Preview · Sample transfer",systemImage:"viewfinder").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                    .listRowBackground(Color.clear).listRowSeparator(.hidden)
            }
            if case .failed(let message) = model.phase {
                Section {
                    Label(message,systemImage:"exclamationmark.triangle").foregroundStyle(DebridStyle.secondary)
                    if model.updatedAt != nil { Text("Showing the last loaded information.").font(.footnote) }
                }
            }
            if let item = model.items.first(where:{ $0.id == transferID }) {
                let detail = TransferDetailSummary(item.transfer)
                Section {
                    VStack(alignment:.leading,spacing:12) {
                        Text(item.name).font(.title2.weight(.semibold)).foregroundStyle(DebridStyle.ink)
                        Text(item.stage.label).font(.headline).foregroundStyle(stageColor(item.stage))
                        Text(model.liveStatus.label).font(.footnote).foregroundStyle(DebridStyle.secondary)
                        if let updated = model.updatedAt { Text("Updated \(updated,style:.relative) ago").font(.footnote).foregroundStyle(DebridStyle.secondary) }
                    }.padding(.vertical,8).accessibilityElement(children:.combine)
                }.listRowBackground(Color.clear).listRowSeparator(.hidden)
                if let issue = detail.issue {
                    Section { Label(issue,systemImage:"exclamationmark.circle").foregroundStyle(DebridStyle.secondary).fixedSize(horizontal:false,vertical:true) }
                }
                Section("At provider") {
                    progress(detail.providerLabel,fraction:detail.providerFraction,color:Color("StageBlue"))
                    if item.transfer.size > 0 { LabeledContent("Torrent size",value:item.transfer.size.formatted(.byteCount(style:.file))) }
                }
                Section {
                    progress(detail.nasLabel,fraction:detail.nasFraction,color:stageColor(item.stage))
                    if let done = detail.downloadedBytes, let total = detail.downloadBytes {
                        LabeledContent("Download bytes",value:done.formatted(.byteCount(style:.file))+" of "+total.formatted(.byteCount(style:.file)))
                    }
                    if !item.transfer.files.isEmpty { LabeledContent("Selected files",value:"\(detail.selectedFiles) of \(item.transfer.files.count)") }
                } header: { Text("On your NAS") } footer: { Text("Downloading, unpacking and finalizing are separate stages. Completion is confirmed by the server.") }
                Section {
                    LabeledContent("Category",value:TransferSearchIndex.bounded(item.transfer.category.flatMap { $0.isEmpty ? nil : $0 } ?? "Uncategorized",limit:256))
                    if let folder = detail.serverFolder { VStack(alignment:.leading,spacing:6) { Text("Folder reported by server").font(.subheadline); Text(folder).font(.body.monospaced()).textSelection(.enabled) } }
                    else { Text("The server has not assigned a destination folder yet.").foregroundStyle(DebridStyle.secondary) }
                    if let organized = item.transfer.organized { LabeledContent("Organized",value:organized ? "Yes" : "No") }
                } header: { Text("Storage") } footer: { Text("Folder paths belong to the server’s download location.") }
                Section("Transfer") {
                    LabeledContent("Provider account",value:model.accountOptions.first(where:{ $0.id == item.transfer.accountId })?.label ?? "Unavailable account")
                    LabeledContent("Added") { Text(item.transfer.addedAt,format:.dateTime.day().month().year().hour().minute()) }
                    if let date = item.transfer.completedAt { LabeledContent("Completed") { Text(date,format:.dateTime.day().month().year().hour().minute()) } }
                    LabeledContent("Retry count",value:"\(item.transfer.retryCount)")
                    VStack(alignment:.leading,spacing:6) { Text("Info hash").font(.subheadline); Text(TransferSearchIndex.bounded(item.transfer.hash,limit:128)).font(.footnote.monospaced()).textSelection(.enabled) }
                }
            } else if model.updatedAt == nil {
                Section { ContentUnavailableView("Transfer unavailable",systemImage:"arrow.trianglehead.2.clockwise",description:Text("Pull to refresh when your server is reachable.")) }.listRowBackground(Color.clear)
            } else {
                Section {
                    ContentUnavailableView { Label("Transfer no longer listed",systemImage:"tray") } description: { Text("It may have been removed from this server.") } actions: { Button("Back to transfers") { dismiss() } }
                }.listRowBackground(Color.clear)
            }
        }.modifier(DebridListStyle()).navigationTitle("Transfer").navigationBarTitleDisplayMode(.inline)
            .refreshable { await model.refreshWhileActive() }
    }
    private func stageColor(_ stage: TransferStage) -> Color {
        switch stage {
        case .completed: Color("StageGreen")
        case .failed: .red
        case .selecting: Color("StageAmber")
        default: Color("StageBlue")
        }
    }
    private func progress(_ label:String,fraction:Double?,color:Color) -> some View {
        VStack(alignment:.leading,spacing:10) {
            Text(label).font(.headline)
            if let fraction {
                ProgressView(value:fraction).tint(color).accessibilityLabel(label)
                Text(fraction.formatted(.percent.precision(.fractionLength(0)))).font(.subheadline.monospacedDigit()).foregroundStyle(DebridStyle.secondary)
            } else { Text("Progress unavailable").font(.subheadline).foregroundStyle(DebridStyle.secondary) }
        }.padding(.vertical,6).accessibilityElement(children:.combine)
    }
}
