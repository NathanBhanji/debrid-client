import Foundation
import Observation

/// Write-only credentials. Never retained in a saved intent or reflected in diagnostics.
public struct ProviderAccountWrite: Encodable, Sendable, CustomStringConvertible, CustomReflectable {
    public let kind: String?
    public let name: String?
    public let enabled: Bool?
    public let setDefault: Bool
    private let credentials: Credentials?
    private struct Credentials: Encodable, Sendable { let apiKey: String }
    enum CodingKeys: String, CodingKey { case kind, name, enabled, setDefault = "set_default", credentials }
    public init(kind: String? = nil, name: String? = nil, enabled: Bool? = nil, setDefault: Bool = false, apiKey: String? = nil) {
        self.kind = kind; self.name = name; self.enabled = enabled; self.setDefault = setDefault
        credentials = apiKey.map { Credentials(apiKey: $0) }
    }
    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(kind, forKey: .kind); try c.encodeIfPresent(name, forKey: .name)
        try c.encodeIfPresent(enabled, forKey: .enabled); try c.encode(setDefault, forKey: .setDefault)
        if let credentials {
            var nested = c.nestedContainer(keyedBy: CredentialKeys.self, forKey: .credentials)
            try nested.encode(credentials.apiKey, forKey: .apiKey)
        }
    }
    private enum CredentialKeys: String, CodingKey { case apiKey = "api_key" }
    public var description: String { "<provider account write: redacted>" }
    public var customMirror: Mirror { Mirror(self, children: [:]) }
}
public protocol ProviderManagingAPI: ProviderAccountAPI {
    func health() async throws -> HealthDTO
    func addAccount(_ input: ProviderAccountWrite) async throws -> AccountDTO
    func updateAccount(id: String, input: ProviderAccountWrite) async throws -> AccountDTO
    func removeAccount(id: String, includingTransfers: Bool) async throws
}
extension DebridAPIClient: ProviderManagingAPI {
    public func addAccount(_ input: ProviderAccountWrite) async throws -> AccountDTO {
        try await send(["accounts"], method: "POST", body: JSONEncoder().encode(input), expectedStatus: 201)
    }
    public func updateAccount(id: String, input: ProviderAccountWrite) async throws -> AccountDTO {
        let result: AccountDTO = try await send(["accounts", id], method: "PATCH", body: JSONEncoder().encode(input), expectedStatus: 200)
        guard result.id == id else { throw APIError.invalidResponse }; return result
    }
    public func removeAccount(id: String, includingTransfers: Bool) async throws {
        struct Empty: Decodable, Sendable {}
        let _: Empty = try await send(includingTransfers ? ["accounts", id, "removal"] : ["accounts", id], method: "DELETE", allowsEmpty: true, expectedStatus: 204,
                                     query: [URLQueryItem(name: "force", value: includingTransfers ? "true" : "false")])
    }
}

@Observable @MainActor public final class ProviderEditorModel: CustomReflectable {
    public enum Phase { case editing, saving, saved, unknown, observed, removed }
    public static let kinds = ["torbox", "realdebrid", "alldebrid", "premiumize", "debridlink"]
    public var kind = "torbox"
    public var name = ""
    public var enabled = true
    public var makeDefault = false
    public var replaceCredentials = false
    public var secret = ""
    public var includingTransfers = false
    public private(set) var phase: Phase = .editing
    public private(set) var message: String?
    public private(set) var requiresSignIn = false
    public private(set) var supportsForceRemoval = false
    public private(set) var observedAccount: AccountDTO?
    public let original: AccountDTO?
    public var isNew: Bool { original == nil }
    public var isBusy: Bool { inFlight }
    public nonisolated var customMirror: Mirror { Mirror(self, children: [:]) }
    public var canSave: Bool {
        guard phase == .editing, !inFlight, !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              name.utf8.count <= 256, !name.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }),
              Self.kinds.contains(kind) else { return false }
        let needsSecret = isNew || replaceCredentials
        let trimmedSecret = secret.trimmingCharacters(in: .whitespacesAndNewlines)
        if needsSecret && (trimmedSecret.isEmpty || secret.utf8.count > 8192 || trimmedSecret.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) })) { return false }
        return isNew || replaceCredentials || name != original?.name || enabled != original?.enabled || (makeDefault && original?.isDefault == false)
    }
    private let api: any ProviderManagingAPI
    private let isCurrent: @MainActor () -> Bool
    private var active = true
    private var generation = 0
    private var inFlight = false
    private var sentName: String?
    private var sentKind: String?
    private var removalSent = false
    public init(server: AuthenticatedServer, original: AccountDTO? = nil, api: any ProviderManagingAPI,
                isCurrent: @escaping @MainActor () -> Bool = { true }) throws {
        guard server.profile.serverURL == api.serverURL else { throw APIError.invalidConfiguration }
        self.original = original; self.api = api; self.isCurrent = isCurrent
        if let original { name = original.name; kind = original.kind; enabled = original.enabled; makeDefault = original.isDefault }
    }
    public func start() { if !Task.isCancelled && isCurrent() { active = true } }
    public func stop() {
        active = false; generation &+= 1; secret = ""
        if phase == .saving { phase = .unknown; message = "The server may have applied this request. Check its current account state before making another change." }
    }
    private func valid(_ operation: Int) -> Bool { active && isCurrent() && !Task.isCancelled && generation == operation }
    public func loadCapabilities() async {
        let operation = generation
        guard active, !Task.isCancelled, isCurrent() else { return }
        if let health = try? await api.health(), valid(operation) { supportsForceRemoval = health.ok && health.capabilities?.contains("native_account_removal_drain_v1") == true }
    }
    public func save() async {
        guard active, isCurrent(), !Task.isCancelled, canSave else { return }
        let operation = generation
        let needsSecret = isNew || replaceCredentials
        let expectedEnabled = isNew ? true : enabled
        let expectsDefault = makeDefault
        let input = ProviderAccountWrite(kind: isNew ? kind : nil,
                                         name: name.trimmingCharacters(in: .whitespacesAndNewlines),
                                         enabled: isNew ? nil : enabled,
                                         setDefault: makeDefault && original?.isDefault != true,
                                         apiKey: needsSecret ? secret.trimmingCharacters(in: .whitespacesAndNewlines) : nil)
        secret = ""; sentName = name.trimmingCharacters(in: .whitespacesAndNewlines); sentKind = kind
        inFlight = true; phase = .saving; message = nil; removalSent = false
        var submitted = false
        defer { inFlight = false }
        do {
            if let original {
                let current = try await api.account(id: original.id)
                guard valid(operation) else { return }
                guard current.updatedAt == original.updatedAt else { phase = .editing; message = "This account changed. Close this sheet and reload it before saving."; return }
            }
            guard valid(operation) else { return }
            submitted = true
            let result: AccountDTO
            if let original { result = try await api.updateAccount(id: original.id, input: input) }
            else { result = try await api.addAccount(input) }
            guard valid(operation) else { return }
            guard !result.id.isEmpty, result.name == sentName, result.kind == sentKind, result.enabled == expectedEnabled, !expectsDefault || result.isDefault else { throw APIError.invalidResponse }
            observedAccount = result; phase = .saved; replaceCredentials = false
        } catch {
            guard valid(operation) else { return }
            failure(error, submitted: submitted)
        }
    }
    public func remove() async {
        guard active, isCurrent(), !Task.isCancelled, !inFlight, phase == .editing, let original,
              !includingTransfers || supportsForceRemoval else { return }
        let operation = generation
        inFlight = true; phase = .saving; message = nil; secret = ""; removalSent = true
        var submitted = false
        defer { inFlight = false }
        do {
            let current = try await api.account(id: original.id)
            guard valid(operation) else { return }
            guard current.updatedAt == original.updatedAt else { phase = .editing; message = "This account changed. Close this sheet and reload it before removing."; return }
            submitted = true
            try await api.removeAccount(id: original.id, includingTransfers: includingTransfers)
            guard valid(operation) else { return }; phase = .removed
        } catch {
            guard valid(operation) else { return }; failure(error, submitted: submitted)
        }
    }
    /// Observation only. It does not repeat POST/PATCH/DELETE or claim credential replacement succeeded.
    public func checkResult() async {
        guard active, isCurrent(), !Task.isCancelled, !inFlight, phase == .unknown else { return }
        let operation = generation
        inFlight = true; defer { inFlight = false }
        do {
            let rows = try await api.accounts()
            guard valid(operation) else { return }
            if let original { observedAccount = rows.first { $0.id == original.id } }
            else { observedAccount = rows.first { $0.name == sentName && $0.kind == sentKind } }
            phase = .observed
            if observedAccount != nil { message = "An account is present on the server. Reload it and check its connection; this does not confirm that new credentials were saved." }
            else { message = removalSent ? "The account is absent. Associated cleanup could not be confirmed." : "No matching account was found. No request was repeated." }
        } catch {
            guard valid(operation) else { return }; message = "The result is still unknown. Reconnect and check again."; requiresSignIn = error as? APIError == .unauthorized
        }
    }
    private func failure(_ error: any Error, submitted: Bool) {
        requiresSignIn = error as? APIError == .unauthorized
        let definite: Bool
        switch error {
        case APIError.unauthorized, APIError.forbidden, APIError.http(status: 409), APIError.http(status: 422), APIError.http(status: 404): definite = true
        default: definite = false
        }
        if submitted && !definite { phase = .unknown; message = "The result is unknown. Check the server before making another change."; return }
        phase = .editing
        switch error {
        case APIError.http(status: 409): message = removalSent ? "This account still has transfers. Remove them first, or explicitly include all associated transfers." : "An account with this name already exists. Choose another name."
        case APIError.http(status: 422): message = "The server could not validate this account. Check its name and credentials."
        case APIError.unauthorized: message = "Your session has expired. Sign in with Pocket ID again."
        case APIError.forbidden: message = "This session does not have permission to change accounts."
        default: message = "The account could not be changed. Reload it and try again."
        }
    }
}
