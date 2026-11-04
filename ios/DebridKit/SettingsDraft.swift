import Foundation

public enum SettingsDraftError: Error { case invalidNumber, invalidCategory, tooLarge }
public struct DownloadPolicyDraft: Equatable, Sendable {
    public enum Organization: String, CaseIterable, Sendable { case inherit, on, off }
    public var minFileSize: String
    public var includeRegex: String
    public var excludeRegex: String
    public var downloadRetries: String
    public var torrentRetries: String
    public var finishedAction: String
    public var finishedDelay: String
    public var deleteOnError: String
    public var lifetime: String
    public var unpack: Bool
    public var reviewFiles: Bool
    public var organization: Organization
    private var manualFiles: [String]?
    public init(_ value: TorrentSettingsDTO) {
        minFileSize = value.minFileSize.map(String.init) ?? ""
        includeRegex = value.includeRegex ?? ""; excludeRegex = value.excludeRegex ?? ""
        downloadRetries = String(value.downloadRetries); torrentRetries = String(value.torrentRetries)
        finishedAction = value.finishedAction.flatMap { $0.isEmpty ? nil : $0 } ?? "keep"
        finishedDelay = value.finishedDelay ?? ""; deleteOnError = value.deleteOnError ?? ""; lifetime = value.lifetime ?? ""
        unpack = value.unpack; reviewFiles = value.waitForSelection == true
        organization = value.organize.map { $0 ? .on : .off } ?? .inherit; manualFiles = value.manualFiles
    }
    public static func == (lhs: Self, rhs: Self) -> Bool {
        let a = [lhs.minFileSize, lhs.includeRegex, lhs.excludeRegex, lhs.downloadRetries, lhs.torrentRetries, lhs.finishedAction, lhs.finishedDelay, lhs.deleteOnError, lhs.lifetime].map { Data($0.utf8) }
        let b = [rhs.minFileSize, rhs.includeRegex, rhs.excludeRegex, rhs.downloadRetries, rhs.torrentRetries, rhs.finishedAction, rhs.finishedDelay, rhs.deleteOnError, rhs.lifetime].map { Data($0.utf8) }
        return a == b && lhs.unpack == rhs.unpack && lhs.reviewFiles == rhs.reviewFiles && lhs.organization == rhs.organization && lhs.manualFiles?.map({ Data($0.utf8) }) == rhs.manualFiles?.map({ Data($0.utf8) })
    }
    public var preservedFileIDs: [String] { manualFiles ?? [] }
    public var hasDeletion: Bool { finishedAction == "remove_from_provider" || !deleteOnError.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    public func output() throws -> TorrentSettingsDTO {
        guard let downloads = Int(downloadRetries), downloads >= 0, let torrents = Int(torrentRetries), torrents >= 0 else { throw SettingsDraftError.invalidNumber }
        let size: Int64?
        if minFileSize.isEmpty { size = nil } else {
            guard let value = Int64(minFileSize), value >= 0 else { throw SettingsDraftError.invalidNumber }; size = value
        }
        guard includeRegex.utf8.count <= 4096, excludeRegex.utf8.count <= 4096,
              [finishedDelay, deleteOnError, lifetime].allSatisfy({ $0.utf8.count <= 64 && !$0.contains(where: { $0.isNewline }) }),
              ["keep", "remove_from_provider"].contains(finishedAction) else { throw SettingsDraftError.tooLarge }
        func optional(_ value: String) -> String? { value.isEmpty ? nil : value }
        return TorrentSettingsDTO(minFileSize: size, includeRegex: optional(includeRegex), excludeRegex: optional(excludeRegex), manualFiles: manualFiles,
                                  finishedAction: finishedAction, finishedDelay: optional(finishedDelay), downloadRetries: downloads, torrentRetries: torrents,
                                  deleteOnError: optional(deleteOnError), lifetime: optional(lifetime), unpack: unpack,
                                  organize: organization == .inherit ? nil : organization == .on, waitForSelection: reviewFiles)
    }
}
public enum RuntimeSettingsSection: String, CaseIterable, Sendable { case defaults, categories, organization, archives
    public var label: String { switch self { case .defaults: "Download defaults"; case .categories: "Categories"; case .organization: "Library organization"; case .archives: "Archive depth" } }
}
public struct RuntimeSettingsDraft: Equatable, Sendable {
    public var policy: DownloadPolicyDraft
    public var categories: [String]
    public var organizationEnabled: Bool
    public var movieTemplate: String
    public var tvTemplate: String
    public var unpackMaxDepth: Int
    public init(_ value: SettingsDTO) {
        policy = DownloadPolicyDraft(value.torrentDefaults); categories = value.categories ?? []
        organizationEnabled = value.organize.enabled; movieTemplate = value.organize.movieTemplate ?? ""; tvTemplate = value.organize.tvTemplate ?? ""
        unpackMaxDepth = value.unpackMaxDepth
    }
    static var placeholder: Self { Self(SettingsDTO(torrentDefaults: TorrentSettingsDTO(downloadRetries: 0, torrentRetries: 0, unpack: false), unpackMaxDepth: 0, organize: OrganizeSettingsDTO(enabled: false))) }
    public func output() throws -> SettingsDTO {
        guard (0...5).contains(unpackMaxDepth), categories.count <= 256, movieTemplate.utf8.count <= 2048, tvTemplate.utf8.count <= 2048 else { throw SettingsDraftError.tooLarge }
        guard categories.allSatisfy(Self.validCategory) else { throw SettingsDraftError.invalidCategory }
        return SettingsDTO(torrentDefaults: try policy.output(), categories: Self.byteDistinct(categories), unpackMaxDepth: unpackMaxDepth,
                           organize: OrganizeSettingsDTO(enabled: organizationEnabled, movieTemplate: movieTemplate, tvTemplate: tvTemplate))
    }
    public static func == (lhs: Self, rhs: Self) -> Bool { RuntimeSettingsSection.allCases.allSatisfy { lhs.matches(rhs, section: $0) } }
    static func byteDistinct(_ values: [String]) -> [String] {
        var seen = Set<Data>()
        return values.filter { seen.insert(Data($0.utf8)).inserted }.sorted { Data($0.utf8).lexicographicallyPrecedes(Data($1.utf8)) }
    }
    public static func validCategory(_ value: String) -> Bool {
        !value.isEmpty && value.utf8.count <= 240 && value == value.trimmingCharacters(in: .whitespacesAndNewlines) && !value.hasPrefix(".") && !value.hasSuffix(".") &&
        !value.unicodeScalars.contains { CharacterSet.controlCharacters.contains($0) || "/\\:*?\"<>|".unicodeScalars.contains($0) }
    }
    public func matches(_ other: Self, section: RuntimeSettingsSection) -> Bool {
        switch section {
        case .defaults: policy == other.policy
        case .categories: Set(categories.map { Data($0.utf8) }) == Set(other.categories.map { Data($0.utf8) })
        case .organization: organizationEnabled == other.organizationEnabled && Data(movieTemplate.utf8) == Data(other.movieTemplate.utf8) && Data(tvTemplate.utf8) == Data(other.tvTemplate.utf8)
        case .archives: unpackMaxDepth == other.unpackMaxDepth
        }
    }
    mutating func copy(_ section: RuntimeSettingsSection, from other: Self) {
        switch section {
        case .defaults: policy = other.policy
        case .categories: categories = other.categories
        case .organization: organizationEnabled = other.organizationEnabled; movieTemplate = other.movieTemplate; tvTemplate = other.tvTemplate
        case .archives: unpackMaxDepth = other.unpackMaxDepth
        }
    }
}
