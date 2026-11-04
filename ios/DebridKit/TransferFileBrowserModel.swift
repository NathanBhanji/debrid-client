import Foundation
import Observation

@Observable @MainActor public final class TransferFileBrowserModel {
    public private(set) var index: TransferFileIndex?
    private var loadedAt: Date?
    private var revision = 0
    private var loadedTransferID: String?
    private let build: @Sendable (TorrentDTO) async throws -> TransferFileIndex
    public init(build: @escaping @Sendable (TorrentDTO) async throws -> TransferFileIndex = { try await TransferFileIndex.build($0) }) { self.build = build }
    public func update(transfer: TorrentDTO?,updatedAt:Date?) async {
        guard !Task.isCancelled else { return }
        guard let transfer else { revision &+= 1; index = nil; loadedAt = nil; loadedTransferID = nil; return }
        guard index == nil || loadedAt != updatedAt || loadedTransferID != transfer.id else { return }
        revision &+= 1
        let expected = revision
        do {
            let built = try await build(transfer)
            guard !Task.isCancelled, expected == revision else { return }
            index = built; loadedAt = updatedAt; loadedTransferID = transfer.id
        } catch { /* A cancelled navigation task retains the prior snapshot. */ }
    }
}
