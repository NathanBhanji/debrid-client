import Foundation
import Observation

public protocol TransferListing: Sendable { func torrents() async throws -> [TorrentDTO] }
extension DebridAPIClient: TransferListing {}

public enum TransferStage: Sendable, Equatable {
    case queued, adding, processing, selecting, providerDownloading, providerUploading, providerReady
    case nasQueued, nasDownloading, nasFinishing, unpacking, completed, failed, unknown
    public var label: String {
        switch self {
        case .queued: "Queued"
        case .adding: "Adding at provider"
        case .processing: "Processing at provider"
        case .selecting: "Choose files"
        case .providerDownloading: "Downloading at provider"
        case .providerUploading: "Preparing at provider"
        case .providerReady: "Ready at provider"
        case .nasQueued: "Queued for NAS"
        case .nasDownloading: "Downloading to NAS"
        case .nasFinishing: "Finishing on NAS"
        case .unpacking: "Unpacking on NAS"
        case .completed: "Complete"
        case .failed: "Needs attention"
        case .unknown: "Unknown state"
        }
    }
    public var isActive: Bool { self != .completed && self != .failed && self != .selecting && self != .unknown }
}

/// Presentation derives from the two distinct server lifecycles; finished is not local completion.
public struct TransferSummary: Identifiable, Sendable {
    public let transfer: TorrentDTO
    public var id: String { transfer.id }
    public let stage: TransferStage
    public let progress: Double?
    public let progressLabel: String
    public var name: String {
        var bounded = ""
        bounded.unicodeScalars.append(contentsOf: transfer.name.unicodeScalars.prefix(1024))
        let name = bounded.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? "Unnamed transfer" : String(name.prefix(256))
    }
    public init(_ transfer: TorrentDTO) {
        self.transfer = transfer
        switch transfer.status {
        case "queued": stage = .queued
        case "adding": stage = .adding
        case "processing": stage = .processing
        case "waiting_selection": stage = .selecting
        case "downloading": stage = .providerDownloading
        case "uploading": stage = .providerUploading
        case "completed": stage = .completed
        case "error": stage = .failed
        case "finished":
            let states = Set(transfer.downloads.map(\.state))
            if states.contains("error") { stage = .failed }
            else if states.contains("unpacking") { stage = .unpacking }
            else if states.contains("downloading") { stage = .nasDownloading }
            else if states.contains("unrestricting") || states.contains("pending") { stage = .nasQueued }
            else if states.contains("downloaded") || states.contains("done") { stage = .nasFinishing }
            else { stage = transfer.downloads.isEmpty ? .providerReady : .unknown }
        default: stage = .unknown
        }
        let isProvider = stage == .providerDownloading || stage == .providerUploading
        progressLabel = isProvider ? "Provider progress" : "NAS progress"
        let raw = isProvider ? transfer.providerProgress : transfer.localProgress
        if stage == .completed { progress = 1 }
        else if isProvider || stage == .nasDownloading || stage == .nasFinishing {
            progress = raw.isFinite && (transfer.size > 0 || raw > 0) ? min(1,max(0,raw)) : nil
        } else { progress = nil }
    }
}

@Observable @MainActor public final class TransferListModel {
    public enum Phase: Equatable { case idle, loading, ready, failed(String) }
    public private(set) var phase: Phase = .idle
    public private(set) var items: [TransferSummary] = []
    public private(set) var updatedAt: Date?
    public private(set) var requiresSignIn = false
    public let profile: ServerProfile
    private let api: any TransferListing
    private var activeRequest: Task<[TorrentDTO], any Error>?
    private var revision = 0
    public init(profile: ServerProfile, api: any TransferListing) { self.profile = profile; self.api = api }
    public convenience init(server: AuthenticatedServer) throws {
        try self.init(profile: server.profile, api: DebridAPIClient(server: ServerAddress(server.profile.serverURL.absoluteString), credential: server.credential))
    }
    public func reload() async {
        guard !Task.isCancelled else { return }
        revision &+= 1
        let request = revision
        phase = .loading; requiresSignIn = false
        activeRequest?.cancel()
        let api = self.api
        let task = Task { try Task.checkCancellation(); return try await api.torrents() }
        activeRequest = task
        defer { if request == revision { activeRequest = nil } }
        do {
            let transfers = try await withTaskCancellationHandler {
                try await task.value
            } onCancel: { task.cancel() }
            try Task.checkCancellation()
            guard request == revision else { return }
            guard Set(transfers.map(\.id)).count == transfers.count, transfers.allSatisfy({ !$0.id.isEmpty }) else { throw APIError.invalidResponse }
            items = transfers.map(TransferSummary.init)
            updatedAt = Date(); phase = .ready
        } catch is CancellationError {
            if request == revision { phase = items.isEmpty ? .idle : .ready }
        } catch {
            guard request == revision else { return }
            guard !Task.isCancelled else { phase = items.isEmpty ? .idle : .ready; requiresSignIn = false; return }
            requiresSignIn = error as? APIError == .unauthorized
            let message = requiresSignIn ? "Your session has expired. Sign in again." :
                (error as? APIError == .forbidden ? "This server did not permit access to transfers." : ServerConnectionModel.message(for: error))
            phase = .failed(message)
        }
    }
    public func invalidate() { revision &+= 1; activeRequest?.cancel(); activeRequest = nil; requiresSignIn = false; phase = items.isEmpty ? .idle : .ready }
}
