import Foundation

public enum SubmissionIntentError: Error, Sendable, LocalizedError {
    case invalid, tooLarge
    public var errorDescription:String? {
        switch self {
        case .invalid: "These saved submission options could not be validated."
        case .tooLarge: "These server rules exceed the supported size. Choose simpler rules before adding."
        }
    }
}
public enum SubmissionIdentity {
    public static func validID(_ id:String) -> Bool {
        !id.isEmpty && id.utf8.count <= 256 && id.utf8.allSatisfy { (48...57).contains($0) || (65...90).contains($0) || (97...122).contains($0) || $0 == 45 || $0 == 95 }
    }
    static func validKey(_ id:UUID) -> Bool { let value = id.uuid; return value.6 >> 4 == 4 && value.8 >> 6 == 2 }
    public static func encoder() -> JSONEncoder { let encoder = JSONEncoder(); encoder.keyEncodingStrategy = .convertToSnakeCase; return encoder }
}
public struct SubmissionOptions: Codable, Equatable, Sendable, CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public let accountID: String
    public let category: String
    public let settings: TorrentSettingsDTO
    public init(accountID:String,category:String,settings:TorrentSettingsDTO) throws {
        self.accountID = accountID; self.category = category; self.settings = settings; try validate()
    }
    func validate() throws {
        guard SubmissionIdentity.validID(accountID), category.utf8.count <= 1024,
              !category.contains(where:{ "/\\\0".contains($0) }), category != ".", category != "..",
              !category.unicodeScalars.contains(where:{ $0.properties.generalCategory == .control }),
              settings.downloadRetries >= 0, settings.torrentRetries >= 0, (settings.minFileSize ?? 0) >= 0,
              settings.finishedAction == nil || ["", "keep", "remove_from_provider"].contains(settings.finishedAction ?? "") else { throw SubmissionIntentError.invalid }
        guard try SubmissionIdentity.encoder().encode(settings).count <= 16*1024 else { throw SubmissionIntentError.tooLarge }
    }
    public var description:String { "SubmissionOptions(redacted)" }
    public var debugDescription:String { description }
    public var customMirror:Mirror { Mirror(self,children:["contents":"redacted"]) }
}
public enum SubmissionRejection: Codable, Equatable, Sendable {
    case activeTorrent(String), options
}
public struct PendingSubmission: Codable, Equatable, Sendable, CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public enum Phase:String, Codable, Sendable { case prepared, rejected, accepted, blocked }
    let version:Int
    public let key:UUID
    public let serverURL:URL
    public let options:SubmissionOptions
    public let createdAt:Date
    public let phase:Phase
    public let acceptedID:String?
    public let torrentExists:Bool?
    public let rejection:SubmissionRejection?
    init(key:UUID = UUID(),serverURL:URL,options:SubmissionOptions,createdAt:Date,phase:Phase = .prepared,acceptedID:String? = nil,torrentExists:Bool? = nil,rejection:SubmissionRejection? = nil) {
        version = 2; self.key = key; self.serverURL = serverURL; self.options = options; self.createdAt = createdAt; self.phase = phase; self.acceptedID = acceptedID; self.torrentExists = torrentExists; self.rejection = rejection
    }
    func validate() throws {
        guard (version == 1 || version == 2), (version != 1 || options.settings.waitForSelection != true), SubmissionIdentity.validKey(key), serverURL.absoluteString.utf8.count <= 2048,
              (try? ServerAddress(serverURL.absoluteString))?.url == serverURL, createdAt.timeIntervalSince1970.isFinite else { throw SubmissionIntentError.invalid }
        try options.validate()
        switch phase {
        case .prepared,.blocked: guard acceptedID == nil, torrentExists == nil, rejection == nil else { throw SubmissionIntentError.invalid }
        case .rejected:
            guard acceptedID == nil, torrentExists == nil, let rejection else { throw SubmissionIntentError.invalid }
            if case .activeTorrent(let id) = rejection { guard SubmissionIdentity.validID(id) else { throw SubmissionIntentError.invalid } }
        case .accepted: guard let acceptedID, SubmissionIdentity.validID(acceptedID), torrentExists != nil, rejection == nil else { throw SubmissionIntentError.invalid }
        }
    }
    public var description:String { "PendingSubmission(redacted)" }
    public var debugDescription:String { description }
    public var customMirror:Mirror { Mirror(self,children:["contents":"redacted"]) }
}
public struct PreparedSubmission: Sendable, CustomStringConvertible, CustomReflectable {
    public let attempt:PendingSubmission
    let rejectionToken:UUID?
    public var wasCreated:Bool { rejectionToken != nil }
    public var description:String { "PreparedSubmission(redacted)" }
    public var customMirror:Mirror { Mirror(self,children:["contents":"redacted"]) }
}
public struct SubmissionReceiptDTO: Decodable, Sendable {
    public let torrentId:String
    public let acceptedAt:Date
    public let torrentExists:Bool
}
