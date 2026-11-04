import Foundation

public struct TransferSearch: Codable, Equatable, Sendable {
    public enum Scope: String, Codable, CaseIterable, Sendable {
        case all, active, attention, completed
        public var label: String {
            switch self { case .all: "All transfers"; case .active: "Active"; case .attention: "Needs attention"; case .completed: "Completed" }
        }
    }
    public enum Category: Codable, Hashable, Sendable { case all, uncategorized, named(String) }
    public var query = ""
    public var scope: Scope = .all
    public var category: Category = .all
    public var accountID: String?
    public init() {}
    public var hasCriteria: Bool { !query.trimmingCharacters(in:.whitespacesAndNewlines).isEmpty || scope != .all || category != .all || accountID != nil }
    public var activeFilterCount: Int { (scope == .all ? 0 : 1) + (category == .all ? 0 : 1) + (accountID == nil ? 0 : 1) }
    func validate() throws {
        guard query.unicodeScalars.count <= 256, accountID.map({ $0.utf8.count <= 256 }) ?? true else { throw SearchPreferenceError.invalid }
        if case .named(let name) = category, name.utf8.count > 2048 { throw SearchPreferenceError.invalid }
    }
}
public enum ProviderNames {
    public static func label(_ kind: String) -> String {
        switch kind {
        case "torbox": "TorBox"
        case "realdebrid": "Real-Debrid"
        case "alldebrid": "AllDebrid"
        case "premiumize": "Premiumize"
        case "debridlink": "Debrid-Link"
        default: TransferSearchIndex.bounded(kind,limit:64)
        }
    }
}
public protocol AccountListing: Sendable { func accounts() async throws -> [AccountDTO] }
extension DebridAPIClient: AccountListing {}

public struct AccountFilterOption: Identifiable, Equatable, Sendable {
    public let id: String
    public let label: String
}

public struct TransferSearchIndex: Sendable {
    private let entries: [(TransferSummary,String)]
    private let locale: Locale
    public init(items: [TransferSummary], accounts: [AccountDTO], locale: Locale = .current) {
        self.locale = locale
        var labels: [String:String] = [:]
        for account in accounts { labels[account.id] = Self.bounded(account.name,limit:256)+" "+Self.bounded(account.kind,limit:64)+" "+ProviderNames.label(account.kind) }
        entries = items.map { item in
            let category = item.transfer.category.flatMap { $0.isEmpty ? nil : $0 } ?? "Uncategorized"
            let text = Self.bounded(item.transfer.name,limit:2048)+" "+Self.bounded(item.transfer.hash,limit:128)+" "+Self.bounded(category,limit:256)+" "+(labels[item.transfer.accountId] ?? "")
            return (item, Self.fold(text,locale:locale))
        }
    }
    public func results(for search: TransferSearch) -> [TransferSummary] {
        let query = Self.fold(Self.bounded(search.query,limit:256).trimmingCharacters(in:.whitespacesAndNewlines),locale:locale)
        let words = query.split(whereSeparator: \.isWhitespace)
        return entries.compactMap { item,text in
            guard words.allSatisfy({ text.contains($0) }), search.accountID == nil || item.transfer.accountId == search.accountID else { return nil }
            switch search.scope {
            case .all: break
            case .active: guard item.stage.isActive else { return nil }
            case .attention: guard item.stage.needsAttention else { return nil }
            case .completed: guard item.stage == .completed else { return nil }
            }
            switch search.category {
            case .all: break
            case .uncategorized: guard item.transfer.category?.isEmpty != false else { return nil }
            case .named(let name): guard item.transfer.category == name else { return nil }
            }
            return item
        }
    }
    public static func bounded(_ value: String, limit: Int) -> String {
        var text = ""; text.unicodeScalars.append(contentsOf:value.unicodeScalars.prefix(limit)); return text
    }
    private static func fold(_ value: String,locale:Locale) -> String { value.folding(options:[.caseInsensitive,.diacriticInsensitive,.widthInsensitive],locale:locale).replacingOccurrences(of:"-",with:" ") }
}

public enum SearchPreferenceError: Error, Equatable, Sendable { case invalid, corrupt }
public protocol TransferPreferenceStoring: Sendable {
    func load(for profile: ServerProfile) async throws -> TransferSearch?
    func save(_ search: TransferSearch, for profile: ServerProfile) async throws
}

/// Queries may contain private filenames. Keep them in a protected app file, never defaults/shared credentials.
public actor ProtectedTransferPreferences: TransferPreferenceStoring {
    private struct Envelope: Codable { var version = 1; let serverURL: URL; let search: TransferSearch }
    private let directory: URL
    public init(directory: URL) { self.directory = directory }
    private static let shared: ProtectedTransferPreferences? = {
        guard let base = FileManager.default.urls(for:.applicationSupportDirectory,in:.userDomainMask).first else { return nil }
        return ProtectedTransferPreferences(directory:base.appendingPathComponent("TransferSearch",isDirectory:true))
    }()
    public static func applicationStore() throws -> ProtectedTransferPreferences {
        guard let shared else { throw SearchPreferenceError.invalid }; return shared
    }
    public func load(for profile: ServerProfile) throws -> TransferSearch? {
        try Task.checkCancellation()
        let url = directory.appendingPathComponent(profile.id+".json")
        guard FileManager.default.fileExists(atPath:url.path) else { return nil }
        let size = try url.resourceValues(forKeys:[.fileSizeKey]).fileSize ?? 0
        guard size <= 8192 else { throw SearchPreferenceError.corrupt }
        let data = try Data(contentsOf:url)
        guard data.count <= 8192, let envelope = try? JSONDecoder().decode(Envelope.self,from:data), envelope.version == 1, envelope.serverURL == profile.serverURL else { throw SearchPreferenceError.corrupt }
        try envelope.search.validate()
        return envelope.search
    }
    public func save(_ search: TransferSearch, for profile: ServerProfile) throws {
        try Task.checkCancellation(); try search.validate()
        let data = try JSONEncoder().encode(Envelope(serverURL:profile.serverURL,search:search))
        guard data.count <= 8192 else { throw SearchPreferenceError.invalid }
        let manager = FileManager.default
        try manager.createDirectory(at:directory,withIntermediateDirectories:true,attributes:[.protectionKey:FileProtectionType.complete])
        try manager.setAttributes([.protectionKey:FileProtectionType.complete],ofItemAtPath:directory.path)
        let target = directory.appendingPathComponent(profile.id+".json")
        let files = try manager.contentsOfDirectory(at:directory,includingPropertiesForKeys:[.contentModificationDateKey],options:.skipsHiddenFiles)
            .filter { $0.lastPathComponent.range(of:"^[a-f0-9]{64}\\.json$",options:.regularExpression) != nil && $0 != target }
            .sorted { ((try? $0.resourceValues(forKeys:[.contentModificationDateKey]).contentModificationDate) ?? .distantPast) < ((try? $1.resourceValues(forKeys:[.contentModificationDateKey]).contentModificationDate) ?? .distantPast) }
        try data.write(to:target,options:[.atomic,.completeFileProtection])
        for file in files.prefix(max(0,files.count - 15)) { try manager.removeItem(at:file) }
    }
}
