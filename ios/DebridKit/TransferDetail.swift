import Foundation

/// A transfer has independent provider availability and NAS download/finalization stages.
public struct TransferDetailSummary: Sendable {
    public let item: TransferSummary
    public let providerFraction: Double?
    public let providerLabel: String
    public let nasFraction: Double?
    public let nasLabel: String
    public let downloadedBytes: Int64?
    public let downloadBytes: Int64?
    public let selectedFiles: Int
    public let issue: String?
    public let serverFolder: String?

    public init(_ transfer: TorrentDTO) {
        item = TransferSummary(transfer)
        let available = transfer.status == "finished" || transfer.status == "completed"
        if available { providerFraction = 1; providerLabel = "Available at provider" }
        else if transfer.status == "downloading" || transfer.status == "uploading" {
            providerFraction = (transfer.size > 0 || transfer.providerProgress > 0) && transfer.providerProgress.isFinite ? min(1,max(0,transfer.providerProgress)) : nil
            providerLabel = transfer.status == "downloading" ? "Downloading at provider" : "Uploading at provider"
        } else { providerFraction = nil; providerLabel = "Availability unconfirmed" }
        var total: Int64 = 0, done: Int64 = 0
        var valid = !transfer.downloads.isEmpty
        for download in transfer.downloads {
            guard download.size >= 0 else { valid = false; continue }
            let bytes = download.state == "done" ? download.size : min(download.size,max(0,download.bytesDone))
            let nextTotal = total.addingReportingOverflow(download.size)
            let nextDone = done.addingReportingOverflow(bytes)
            if nextTotal.overflow || nextDone.overflow { valid = false }
            else { total = nextTotal.partialValue; done = nextDone.partialValue }
        }
        downloadedBytes = valid ? done : nil
        downloadBytes = valid && total > 0 ? total : nil
        nasFraction = transfer.status == "completed" ? 1 : (valid && total > 0 ? Double(done)/Double(total) : nil)
        switch item.stage {
        case .completed: nasLabel = "Complete on NAS"
        case .unpacking: nasLabel = "Unpacking on NAS"
        case .nasFinishing: nasLabel = "Finalizing on NAS"
        case .nasDownloading: nasLabel = "Downloading to NAS"
        case .nasQueued: nasLabel = "Queued for NAS"
        case .selecting: nasLabel = transfer.fileSelectionState=="pending" ? "Waiting for your file selection" : "Waiting for provider selection"
        case .failed: nasLabel = "Needs attention"
        case .unknown: nasLabel = "Unknown server state"
        default: nasLabel = "Waiting for download"
        }
        selectedFiles = transfer.files.filter(\.selected).count
        // Provider messages can contain signed URLs or credentials. Show actionable domain states,
        // never arbitrary provider error/status bodies in the native detail or accessibility tree.
        if transfer.statusReason == "account disabled" { issue = "This provider account is disabled. Enable it to continue." }
        else if transfer.fileSelectionState=="pending" {issue = "Choose files when the provider metadata is ready. NAS downloads stay on hold until you confirm."}
        else if item.stage == .selecting { issue = "This automatic transfer is waiting for provider file selection. Check the server for details." }
        else if item.stage == .failed { issue = "The server reported a transfer error. Check the server for details before retrying." }
        else if item.stage == .unknown { issue = "This server returned a state this app does not recognize. Refresh to check for an update." }
        else if transfer.statusReason == "waiting for download links" { issue = "Waiting for the provider to make download links available." }
        else { issue = nil }
        if let folder = transfer.dirName, !folder.isEmpty { serverFolder = TransferSearchIndex.bounded(folder,limit:1024) }
        else { serverFolder = nil }
    }
}
