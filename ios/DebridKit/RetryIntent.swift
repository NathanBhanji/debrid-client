import Foundation

public enum RetryIntentError:Error,Equatable,Sendable,LocalizedError {
    case invalid,unresolved,parentChanged
    public var errorDescription:String? {
        switch self {
        case .invalid:"This saved retry could not be read safely. Its original request is kept."
        case .unresolved:"A saved retry is still unresolved. Check its result before creating another request."
        case .parentChanged:"This file no longer belongs to the transfer that was reviewed. Refresh before trying again."
        }
    }
}
public struct RetryIntent:Codable,Sendable,CustomStringConvertible,CustomReflectable {
    public enum Phase:String,Codable,Sendable {case prepared,rejected,accepted,blocked}
    let version:Int
    public let key:UUID
    public let serverURL:URL
    public let kind:RetryResourceKind
    public let resourceID:String
    public let torrentID:String
    public let revision:String
    public let waitForSelection:Bool
    public let createdAt:Date
    public let phase:Phase
    public let acceptedAt:Date?
    public let torrentExists:Bool?
    init(key:UUID = UUID(),serverURL:URL,kind:RetryResourceKind,resourceID:String,torrentID:String,revision:String,waitForSelection:Bool,createdAt:Date,phase:Phase = .prepared,acceptedAt:Date? = nil,torrentExists:Bool? = nil) {
        version=1;self.key=key;self.serverURL=serverURL;self.kind=kind;self.resourceID=resourceID;self.torrentID=torrentID;self.revision=revision;self.waitForSelection=waitForSelection;self.createdAt=createdAt;self.phase=phase;self.acceptedAt=acceptedAt;self.torrentExists=torrentExists
    }
    func validate() throws {
        guard version==1,SubmissionIdentity.validKey(key),SubmissionIdentity.validID(resourceID),SubmissionIdentity.validID(torrentID),kind != .torrent || resourceID==torrentID,FileSelectionIdentity.revisionIsValid(revision),createdAt.timeIntervalSince1970.isFinite,serverURL.absoluteString.utf8.count<=2048,(try? ServerAddress(serverURL.absoluteString))?.url==serverURL else {throw RetryIntentError.invalid}
        if phase == .accepted {guard let acceptedAt,acceptedAt.timeIntervalSince1970.isFinite,torrentExists != nil else {throw RetryIntentError.invalid}}
        else {guard acceptedAt==nil,torrentExists==nil else {throw RetryIntentError.invalid}}
    }
    func replacingPhase(_ phase:Phase,receipt:RetryAcceptanceDTO? = nil)->Self {
        Self(key:key,serverURL:serverURL,kind:kind,resourceID:resourceID,torrentID:torrentID,revision:revision,waitForSelection:waitForSelection,createdAt:createdAt,phase:phase,acceptedAt:receipt?.acceptedAt,torrentExists:receipt?.torrentExists)
    }
    public var description:String {"RetryIntent(redacted)"}
    public var customMirror:Mirror {Mirror(self,children:["contents":"redacted"])}
}
public struct RetryClaim:Sendable,CustomStringConvertible,CustomReflectable {
    public let id:UUID
    public let token:UUID
    public let serverURL:URL
    public let kind:RetryResourceKind
    public let resourceID:String
    public let torrentID:String
    public let name:String
    public let intent:RetryIntent?
    public var description:String {"RetryClaim(redacted)"}
    public var customMirror:Mirror {Mirror(self,children:["contents":"redacted"])}
}
public struct PreparedRetry:Sendable,CustomStringConvertible,CustomReflectable {
    public let intent:RetryIntent
    let rejectionToken:UUID?
    public var wasCreated:Bool {rejectionToken != nil}
    public var description:String {"PreparedRetry(redacted)"}
    public var customMirror:Mirror {Mirror(self,children:["contents":"redacted"])}
}
public struct SavedRetry:Identifiable,Sendable,CustomStringConvertible,CustomReflectable {
    public let id:UUID
    public let serverURL:URL
    public let kind:RetryResourceKind
    public let resourceID:String
    public let torrentID:String
    public let name:String
    public var description:String {"SavedRetry(redacted)"}
    public var customMirror:Mirror {Mirror(self,children:["contents":"redacted"])}
}
public protocol RetryIntentStoring:Sendable {
    func savedRetries() async throws -> [SavedRetry]
    func claimRetry(server:URL,kind:RetryResourceKind,resourceID:String,torrentID:String,name:String) async throws -> RetryClaim
    func retryClaim(_ id:UUID,token:UUID) async throws -> RetryClaim
    func renewRetry(_ id:UUID,token:UUID) async throws
    func releaseRetry(_ id:UUID,token:UUID) async throws
    func prepareRetry(_ id:UUID,token:UUID,review:RetryReviewDTO) async throws -> PreparedRetry
    func acceptRetry(_ id:UUID,token:UUID,key:UUID,receipt:RetryAcceptanceDTO) async throws
    func rejectRetry(_ id:UUID,token:UUID,key:UUID,freshToken:UUID) async throws
    func blockRetry(_ id:UUID,token:UUID,key:UUID) async throws
}
extension PendingInbox:RetryIntentStoring {}
