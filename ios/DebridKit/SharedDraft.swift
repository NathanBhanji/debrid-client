import Foundation
import Observation

/// Validated local input only. No credentials, destination defaults or networking.
public enum SharedDraft:Sendable,CustomStringConvertible,CustomReflectable {
    case magnet(MagnetLink),torrent(TorrentFile)
    public var name:String {switch self {case .magnet(let value):value.displayName ?? "Magnet link";case .torrent(let value):value.summary.displayName}}
    public var description:String {"SharedDraft(redacted)"}
    public var customMirror:Mirror {Mirror(self,children:["contents":"redacted"])}
    var identity:Data {switch self {case .magnet(let value):Data(("magnet:"+value.rawURI).utf8);case .torrent(let value):Data(("torrent:"+value.digest).utf8)}}
    var bytes:Int {switch self {case .magnet(let value):value.rawURI.utf8.count;case .torrent(let value):value.data.count}}
    @concurrent public static func text(_ value:String) async throws -> [Self] {try await MagnetPaste.parse(value).map {Self.magnet($0)}}
}
public enum SharedDraftError:Error,Sendable,LocalizedError {
    case unsupported,tooMany,tooLarge,providerUnavailable,timedOut
    public var errorDescription:String? {
        switch self {
        case .unsupported:"Share a magnet link, plain magnet text or a torrent file. Webpage links cannot be added as magnets."
        case .tooMany:"Share up to 16 items at a time."
        case .tooLarge:"The shared items exceed the inbox size limit."
        case .providerUnavailable:"The sharing app could not provide this item. Try sharing it again."
        case .timedOut:"The sharing app took too long to provide this item. Try again."
        }
    }
}
/// Save is explicit. Each durable receipt is accounted for before completion.
@Observable @MainActor public final class SharedDraftModel {
    public enum Phase:Equatable,Sendable {case reviewing,saving,saved,partial,failed}
    public private(set) var phase:Phase = .reviewing
    public private(set) var drafts:[SharedDraft]
    public private(set) var savedCount=0
    public private(set) var duplicateCount=0
    public private(set) var message:String?
    private let store:any PendingInboxAccess
    private var completed=Set<Int>()
    public init(drafts:[SharedDraft],store:any PendingInboxAccess) throws {
        guard !drafts.isEmpty,drafts.count<=16 else {throw SharedDraftError.tooMany}
        var uriBytes=0,torrentBytes=0
        for draft in drafts {switch draft {case .magnet:uriBytes+=draft.bytes;case .torrent:torrentBytes+=draft.bytes}}
        guard uriBytes<=PendingInbox.uriByteLimit,torrentBytes<=PendingInbox.payloadByteLimit else {throw SharedDraftError.tooLarge}
        // Keep first original URI/encoding when the same input is offered more than once.
        var seen=Set<Data>();self.drafts=drafts.filter {seen.insert($0.identity).inserted};self.store=store
    }
    public var canSave:Bool {phase != .saving && phase != .saved}
    public func save() async {
        guard canSave,!Task.isCancelled else {return}
        phase = .saving;message=nil
        do {
            for (index,draft) in drafts.enumerated() where !completed.contains(index) {
                try Task.checkCancellation()
                let receipt:PendingReceipt
                switch draft {
                case .magnet(let value):receipt=try await store.enqueue(value,source:.share,target:nil)
                case .torrent(let value):receipt=try await store.enqueueTorrent(value,source:.share,target:nil)
                }
                // Account for a committed write even when cancellation arrives after its commit.
                completed.insert(index)
                if receipt.wasDuplicate {duplicateCount+=1} else {savedCount+=1}
            }
            phase = .saved
        } catch {
            phase = completed.isEmpty ? .failed : .partial
            message=completed.isEmpty ? "Nothing was saved. Try again when protected storage is available." : "Some items are saved on this iPhone. Try again to save the remaining items."
        }
    }
}
