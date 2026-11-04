import Foundation

public enum ManagementAction:String,Codable,Sendable {case cancel,remove}
public enum ManagementOutcome:Codable,Equatable,Sendable {
    case cancellation(acceptedAt:Date,exists:Bool)
    case removalConfirmed,absenceObserved
}
public enum ManagementIntentError:Error,Equatable,Sendable,LocalizedError {
    case invalid,unresolved
    public var errorDescription:String? {
        switch self {
        case .invalid:"This saved action could not be read safely. Its original request is kept."
        case .unresolved:"A saved action is still unresolved. Check its result before starting another action."
        }
    }
}
public struct ManagementIntent:Codable,Sendable,CustomStringConvertible,CustomReflectable {
    public enum Phase:String,Codable,Sendable {case prepared,rejected,accepted,blocked}
    let version:Int
    /// Sent to the server for cancellation; local correlation only for removal.
    public let key:UUID
    public let serverURL:URL
    public let kind:ManagementAction
    public let resourceID:String
    public let revision:String
    public let options:RemovalOptions?
    public let createdAt:Date
    public let phase:Phase
    public let outcome:ManagementOutcome?
    init(key:UUID = UUID(),serverURL:URL,kind:ManagementAction,resourceID:String,revision:String,options:RemovalOptions?,createdAt:Date,phase:Phase = .prepared,outcome:ManagementOutcome? = nil) {
        version=1;self.key=key;self.serverURL=serverURL;self.kind=kind;self.resourceID=resourceID;self.revision=revision;self.options=options;self.createdAt=createdAt;self.phase=phase;self.outcome=outcome
    }
    func validate() throws {
        guard version==1,SubmissionIdentity.validKey(key),SubmissionIdentity.validID(resourceID),FileSelectionIdentity.revisionIsValid(revision),createdAt.timeIntervalSince1970.isFinite,serverURL.absoluteString.utf8.count<=2048,(try? ServerAddress(serverURL.absoluteString))?.url==serverURL,(kind == .remove)==(options != nil) else {throw ManagementIntentError.invalid}
        if phase == .accepted {
            switch (kind,outcome) {
            case (.cancel,.cancellation(let date,_)):guard date.timeIntervalSince1970.isFinite else {throw ManagementIntentError.invalid}
            case (.remove,.removalConfirmed),(.remove,.absenceObserved):break
            default:throw ManagementIntentError.invalid
            }
        } else {guard outcome==nil else {throw ManagementIntentError.invalid}}
    }
    func replacingPhase(_ phase:Phase,outcome:ManagementOutcome? = nil)->Self {
        Self(key:key,serverURL:serverURL,kind:kind,resourceID:resourceID,revision:revision,options:options,createdAt:createdAt,phase:phase,outcome:outcome)
    }
    public var description:String {"ManagementIntent(redacted)"}
    public var customMirror:Mirror {Mirror(self,children:["contents":"redacted"])}
}
public struct ManagementClaim:Sendable,CustomStringConvertible,CustomReflectable {
    public let id:UUID
    public let token:UUID
    public let serverURL:URL
    public let kind:ManagementAction
    public let resourceID:String
    public let name:String
    public let intent:ManagementIntent?
    public var description:String {"ManagementClaim(redacted)"}
    public var customMirror:Mirror {Mirror(self,children:["contents":"redacted"])}
}
public struct PreparedManagement:Sendable,CustomStringConvertible,CustomReflectable {
    public let intent:ManagementIntent
    let rejectionToken:UUID?
    public var wasCreated:Bool {rejectionToken != nil}
    public var description:String {"PreparedManagement(redacted)"}
    public var customMirror:Mirror {Mirror(self,children:["contents":"redacted"])}
}
public struct SavedManagement:Identifiable,Sendable,CustomStringConvertible,CustomReflectable {
    public let id:UUID
    public let serverURL:URL
    public let kind:ManagementAction
    public let resourceID:String
    public let name:String
    public var description:String {"SavedManagement(redacted)"}
    public var customMirror:Mirror {Mirror(self,children:["contents":"redacted"])}
}
public protocol ManagementIntentStoring:Sendable {
    func savedManagement() async throws -> [SavedManagement]
    func claimManagement(server:URL,kind:ManagementAction,resourceID:String,name:String) async throws -> ManagementClaim
    func managementClaim(_ id:UUID,token:UUID) async throws -> ManagementClaim
    func renewManagement(_ id:UUID,token:UUID) async throws
    func releaseManagement(_ id:UUID,token:UUID) async throws
    func prepareManagement(_ id:UUID,token:UUID,resourceID:String,revision:String,options:RemovalOptions?) async throws -> PreparedManagement
    func acceptCancellation(_ id:UUID,token:UUID,key:UUID,receipt:CancellationAcceptanceDTO) async throws
    func completeRemoval(_ id:UUID,token:UUID,key:UUID,confirmed:Bool) async throws
    func rejectManagement(_ id:UUID,token:UUID,key:UUID,freshToken:UUID) async throws
    func blockManagement(_ id:UUID,token:UUID,key:UUID) async throws
}
extension PendingInbox:ManagementIntentStoring {}
