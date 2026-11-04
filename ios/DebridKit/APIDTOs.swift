import Foundation

public struct HealthDTO: Decodable, Sendable { public let ok: Bool; public let version: String; public let capabilities: [String]?
 public init(ok:Bool,version:String,capabilities:[String]? = nil) { self.ok=ok;self.version=version;self.capabilities=capabilities }
}
public struct AuthStatusDTO: Decodable, Sendable {
    public let configured: Bool
    public let mode: String
    public let oidcIssuer: String?
    public let nativeSso: Bool?
}
public struct IdentityDTO: Decodable, Sendable {
    public let username: String?
    public let mode: String
    public let apiKey: Bool
}
public struct AccountDTO: Decodable, Sendable, Identifiable {
    public let id: String
    public let kind: String
    public let name: String
    public let enabled: Bool
    public let isDefault: Bool
    public let createdAt: Date
    public let updatedAt: Date
    public let user: ProviderUserDTO?
}
public struct ProviderUserDTO: Decodable, Sendable {
    public let username: String?
    public let email: String?
    public let premium: Bool
    public let plan: String?
    public let expiresAt: Date?
}
public struct TorrentSettingsDTO: Codable, Equatable, Sendable {
    public let minFileSize: Int64?
    public let includeRegex: String?
    public let excludeRegex: String?
    public let manualFiles: [String]?
    public let finishedAction: String?
    public let finishedDelay: String?
    public let downloadRetries: Int
    public let torrentRetries: Int
    public let deleteOnError: String?
    public let lifetime: String?
    public let unpack: Bool
    public let organize: Bool?
    public let waitForSelection: Bool?
    public init(minFileSize:Int64? = nil,includeRegex:String? = nil,excludeRegex:String? = nil,manualFiles:[String]? = nil,finishedAction:String? = nil,finishedDelay:String? = nil,downloadRetries:Int,torrentRetries:Int,deleteOnError:String? = nil,lifetime:String? = nil,unpack:Bool,organize:Bool? = nil,waitForSelection:Bool? = nil) {
        self.minFileSize=minFileSize;self.includeRegex=includeRegex;self.excludeRegex=excludeRegex;self.manualFiles=manualFiles;self.finishedAction=finishedAction;self.finishedDelay=finishedDelay;self.downloadRetries=downloadRetries;self.torrentRetries=torrentRetries;self.deleteOnError=deleteOnError;self.lifetime=lifetime;self.unpack=unpack;self.organize=organize;self.waitForSelection=waitForSelection
    }
}
public struct OrganizeSettingsDTO: Codable, Sendable {
    public let enabled: Bool
    public let movieTemplate: String?
    public let tvTemplate: String?
}
public struct SettingsDTO: Codable, Sendable {
    public let torrentDefaults: TorrentSettingsDTO
    public let categories: [String]?
    public let unpackMaxDepth: Int
    public let organize: OrganizeSettingsDTO
}
public struct FileDTO: Decodable, Sendable, Identifiable {
    public let id: String
    public let path: String
    public let size: Int64
    public let selected: Bool
}
public struct DownloadDTO: Decodable, Sendable, Identifiable {
    public let id: String
    public let torrentId: String
    public let fileId: String?
    public let path: String
    public let filename: String
    public let size: Int64
    public let bytesDone: Int64
    public let progress: Double
    /// Raw strings preserve unknown server versions for domain mapping in later milestones.
    public let state: String
    public let error: String?
    public let retryCount: Int
    public let queuedAt: Date
    public let startedAt: Date?
    public let finishedAt: Date?
    public let completedAt: Date?
}
public struct TorrentDTO: Decodable, Sendable, Identifiable {
    public let fileSelectionState:String?
    public let id: String
    public let accountId: String
    public let hash: String
    public let name: String
    public let category: String?
    public let dirName: String?
    public let organized: Bool?
    public let status: String
    public let statusReason: String?
    public let error: String?
    public let providerProgress: Double
    public let localProgress: Double
    public let size: Int64
    public let speed: Int64
    public let seeders: Int
    public let providerId: String?
    public let providerStatus: String?
    public let files: [FileDTO]
    public let settings: TorrentSettingsDTO
    public let retryCount: Int
    public let addedAt: Date
    public let completedAt: Date?
    public let downloads: [DownloadDTO]
}
public struct SystemStatusDTO: Decodable, Sendable {
    public let version: String
    public let downloadDir: String
    public let accounts: Int
    public let torrents: [String: Int]
    public let downloads: [String: Int]
    public let diskFreeBytes: Int64
    public let diskTotalBytes: Int64
}

public enum RetryResourceKind:String,Codable,Sendable {case torrent,download
    var collection:String {self == .torrent ? "torrents" : "downloads"}
}
public struct RetryReviewDTO:Decodable,Sendable {
    public let kind:String
    public let resourceId:String
    public let torrentId:String
    public let revision:String
    public let allowed:Bool
    public let waitForSelection:Bool
}
public struct RetryAcceptanceDTO:Decodable,Sendable {
    public let kind:String
    public let resourceId:String
    public let torrentId:String
    public let acceptedAt:Date
    /// Parent existence only. A previously retried file row may since be gone.
    public let torrentExists:Bool
}

public struct CancellationReviewDTO:Decodable,Sendable {
    public let torrentId:String
    public let revision:String
    public let allowed:Bool
    public let providerMayContinue:Bool
}
public struct CancellationAcceptanceDTO:Decodable,Sendable {
    public let torrentId:String
    public let acceptedAt:Date
    /// Existence only: the transfer may have been explicitly retried since acceptance.
    public let torrentExists:Bool
}

public struct RemovalReviewDTO:Decodable,Sendable {
    public let torrentId:String
    public let exists:Bool
    public let revision:String?
    public let providerCopyKnown:Bool
}
public struct RemovalOptions:Codable,Equatable,Sendable {
    public let deleteFiles:Bool
    public let deleteFromProvider:Bool
    public init(deleteFiles:Bool = false,deleteFromProvider:Bool = false) {
        self.deleteFiles=deleteFiles;self.deleteFromProvider=deleteFromProvider
    }
}
