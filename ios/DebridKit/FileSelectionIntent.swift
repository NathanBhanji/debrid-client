import Foundation

public enum FileSelectionError:Error,Equatable,Sendable,LocalizedError {
    case invalidMetadata,tooLarge,unsupported,unresolved
    public var errorDescription:String? {
        switch self {
        case .invalidMetadata:"The provider file list could not be reviewed safely. Refresh it before choosing files."
        case .tooLarge:"This file review exceeds the supported size. Choose fewer files or use your server."
        case .unsupported:"This server does not support manual file review."
        case .unresolved:"A saved selection is still being checked. Its original files are kept until the result is known."
        }
    }
}

/// Provider IDs are opaque UTF-8, including path-based IDs. Swift String equality
/// normalizes canonically equivalent Unicode; it must not merge these identities.
public enum FileSelectionIdentity {
    public static let fileLimit=20_000
    public static let intentByteLimit=512*1024
    public static let totalIntentByteLimit=1024*1024
    public static func revisionIsValid(_ value:String)->Bool {
        value.utf8.count==64 && value.utf8.allSatisfy { (48...57).contains($0) || (97...102).contains($0) }
    }
    public static func sameIDs(_ a:[String],_ b:[String])->Bool {
        a.count==b.count && zip(a,b).allSatisfy { $0.utf8.elementsEqual($1.utf8) }
    }
    public static func canonicalIDs(_ ids:[String],maximumBytes:Int = intentByteLimit) throws -> [String] {
        guard !ids.isEmpty,ids.count<=fileLimit,(1...2*1024*1024).contains(maximumBytes) else { throw FileSelectionError.invalidMetadata }
        var bytes=0
        let keyed=try ids.map { value -> (Data,String) in
            bytes+=value.utf8.count
            guard !value.isEmpty,value.utf8.count<=8192 else { throw FileSelectionError.invalidMetadata }
            guard bytes<=maximumBytes else { throw FileSelectionError.tooLarge }
            return (Data(value.utf8),value)
        }.sorted { $0.0.lexicographicallyPrecedes($1.0) }
        guard Set(keyed.map(\.0)).count==keyed.count else { throw FileSelectionError.invalidMetadata }
        let result=keyed.map(\.1)
        try validateCanonicalIDs(result,maximumBytes:maximumBytes)
        return result
    }
    static func validateCanonicalIDs(_ ids:[String],maximumBytes:Int = intentByteLimit) throws {
        guard !ids.isEmpty,ids.count<=fileLimit,(1...2*1024*1024).contains(maximumBytes) else { throw FileSelectionError.invalidMetadata }
        var bytes=0
        for (index,id) in ids.enumerated() {
            bytes+=id.utf8.count
            guard !id.isEmpty,id.utf8.count<=8192 else { throw FileSelectionError.invalidMetadata }
            guard bytes<=maximumBytes else { throw FileSelectionError.tooLarge }
            if index>0 { guard ids[index-1].utf8.lexicographicallyPrecedes(id.utf8) else { throw FileSelectionError.invalidMetadata } }
        }
        guard try encodedIDs(ids).count<=maximumBytes else { throw FileSelectionError.tooLarge }
    }
    static func encodedIDs(_ ids:[String]) throws -> Data { try JSONEncoder().encode(ids) }
}

public struct FileSelectionIntent:Codable,Sendable,CustomStringConvertible,CustomReflectable {
    public enum Phase:String,Codable,Sendable { case prepared,rejected,resolved,blocked }
    let version:Int
    public let id:UUID
    public let serverURL:URL
    public let transferID:String
    public let revision:String
    public let fileIDs:[String]
    public let createdAt:Date
    public let phase:Phase
    init(id:UUID = UUID(),serverURL:URL,transferID:String,revision:String,fileIDs:[String],createdAt:Date,phase:Phase = .prepared) {
        version=1;self.id=id;self.serverURL=serverURL;self.transferID=transferID;self.revision=revision;self.fileIDs=fileIDs;self.createdAt=createdAt;self.phase=phase
    }
    func validate() throws {
        guard version==1,SubmissionIdentity.validKey(id),SubmissionIdentity.validID(transferID),FileSelectionIdentity.revisionIsValid(revision),serverURL.absoluteString.utf8.count<=2048,(try? ServerAddress(serverURL.absoluteString))?.url==serverURL,createdAt.timeIntervalSince1970.isFinite else { throw FileSelectionError.invalidMetadata }
        try FileSelectionIdentity.validateCanonicalIDs(fileIDs)
    }
    func replacingPhase(_ phase:Phase)->Self { Self(id:id,serverURL:serverURL,transferID:transferID,revision:revision,fileIDs:fileIDs,createdAt:createdAt,phase:phase) }
    public var description:String { "FileSelectionIntent(redacted)" }
    public var customMirror:Mirror { Mirror(self,children:["contents":"redacted"]) }
}

public struct FileSelectionClaim:Sendable,CustomStringConvertible,CustomReflectable {
    public let id:UUID
    public let token:UUID
    public let serverURL:URL
    public let transferID:String
    public let name:String
    public let intent:FileSelectionIntent?
    public var description:String { "FileSelectionClaim(redacted)" }
    public var customMirror:Mirror { Mirror(self,children:["contents":"redacted"]) }
}
public struct PreparedFileSelection:Sendable,CustomStringConvertible,CustomReflectable {
    public let intent:FileSelectionIntent
    let rejectionToken:UUID?
    public var wasCreated:Bool { rejectionToken != nil }
    public var description:String { "PreparedFileSelection(redacted)" }
    public var customMirror:Mirror { Mirror(self,children:["contents":"redacted"]) }
}
public struct SavedFileSelection:Identifiable,Sendable,CustomStringConvertible,CustomReflectable {
    public let id:UUID
    public let serverURL:URL
    public let transferID:String
    public let name:String
    public let phase:FileSelectionIntent.Phase
    public var description:String { "SavedFileSelection(redacted)" }
    public var customMirror:Mirror { Mirror(self,children:["contents":"redacted"]) }
}
public protocol FileSelectionIntentStoring:Sendable {
    func savedFileSelections() async throws -> [SavedFileSelection]
    func claimFileSelection(server:URL,transferID:String,name:String) async throws -> FileSelectionClaim
    func renewFileSelection(_ id:UUID,token:UUID) async throws
    func releaseFileSelection(_ id:UUID,token:UUID) async throws
    func fileSelectionClaim(_ id:UUID,token:UUID) async throws -> FileSelectionClaim
    func prepareFileSelection(_ id:UUID,token:UUID,revision:String,fileIDs:[String]) async throws -> PreparedFileSelection
    func resolveFileSelection(_ id:UUID,token:UUID,intentID:UUID) async throws
    func rejectFileSelection(_ id:UUID,token:UUID,intentID:UUID,freshToken:UUID) async throws
    func blockFileSelection(_ id:UUID,token:UUID,intentID:UUID) async throws
}
extension PendingInbox:FileSelectionIntentStoring {}
