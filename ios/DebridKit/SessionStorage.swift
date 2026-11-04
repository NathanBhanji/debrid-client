import CryptoKit
import Foundation
import Security

public enum SessionStorageError: Error, Equatable, Sendable {
    case keychain(OSStatus), locked, corruptData, invalidProfile, invalidKeychainScope, unknownProfile, tooManyProfiles, metadataTooLarge
}

public protocol SecretStorage: Sendable {
    func read(account: String) throws -> Data?
    func write(_ data: Data, account: String) throws
    func delete(account: String) throws
}

/// Every operation is explicitly scoped to an entitled group. Nothing searches all groups implicitly.
public struct KeychainSecretStorage: SecretStorage {
    private let service: String
    private let accessGroup: String
    public init(service: String = "dev.bhanji.debrid.sessions.v1", accessGroup: String) throws {
        guard !service.isEmpty, !accessGroup.isEmpty, !accessGroup.contains("$(") else { throw SessionStorageError.invalidKeychainScope }
        self.service = service
        self.accessGroup = accessGroup
    }
    private func query(_ account: String) -> [String: Any] {
        var query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                  kSecAttrService as String: service, kSecAttrAccount as String: account,
                                  kSecAttrSynchronizable as String: false]
        query[kSecAttrAccessGroup as String] = accessGroup
        return query
    }
    private func check(_ status: OSStatus) throws {
        if status == errSecInteractionNotAllowed { throw SessionStorageError.locked }
        guard status == errSecSuccess else { throw SessionStorageError.keychain(status) }
    }
    public func read(account: String) throws -> Data? {
        var query = query(account)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        try check(status)
        guard let data = result as? Data else { throw SessionStorageError.corruptData }
        return data
    }
    public func write(_ data: Data, account: String) throws {
        let query = query(account)
        let attributes: [String: Any] = [kSecValueData as String: data,
                                        kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly]
        let updated = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        if updated != errSecItemNotFound { try check(updated); return }
        let inserted = SecItemAdd(query.merging(attributes) { _, new in new } as CFDictionary, nil)
        if inserted == errSecDuplicateItem { try check(SecItemUpdate(query as CFDictionary, attributes as CFDictionary)) }
        else { try check(inserted) }
    }
    public func delete(account: String) throws {
        let status = SecItemDelete(query(account) as CFDictionary)
        if status != errSecItemNotFound { try check(status) }
    }
}

public protocol ProfileMetadataStorage: Sendable {
    func read() throws -> Data?
    func write(_ data: Data) throws
}

public struct DefaultsProfileMetadata: ProfileMetadataStorage {
    private let suiteName: String?
    private let key: String
    public init(suiteName: String? = nil, key: String = "debrid.server-profiles.v1") {
        self.suiteName = suiteName
        self.key = key
    }
    // UserDefaults never crosses a concurrency boundary; each synchronous call owns its reference.
    private func defaults() throws -> UserDefaults {
        guard let suiteName else { return .standard }
        guard let defaults = UserDefaults(suiteName: suiteName) else { throw SessionStorageError.corruptData }
        return defaults
    }
    public func read() throws -> Data? {
        guard let value = try defaults().object(forKey: key) else { return nil }
        guard let data = value as? Data else { throw SessionStorageError.corruptData }
        return data
    }
    public func write(_ data: Data) throws { try defaults().set(data, forKey: key) }
}

public struct ServerProfile: Codable, Equatable, Identifiable, Sendable {
    public let serverURL: URL
    public let name: String
    public var id: String { SHA256.hash(data: Data(serverURL.absoluteString.utf8)).map { String(format: "%02x", $0) }.joined() }

    public init(server: ServerAddress, name: String = "Debrid") throws {
        guard var parts = URLComponents(url: server.url, resolvingAgainstBaseURL: false),
              server.url.absoluteString.utf8.count <= 2048 else { throw SessionStorageError.invalidProfile }
        parts.scheme = "https"
        parts.host = parts.host?.lowercased()
        if parts.port == 443 { parts.port = nil }
        let path = parts.percentEncodedPath
        guard !path.contains("//"), !path.split(separator: "/").contains(where: {
            guard let decoded = String($0).removingPercentEncoding else { return true }
            return decoded == "." || decoded == ".." || decoded.contains(where: { "/\\%".contains($0) })
        }) else { throw SessionStorageError.invalidProfile }
        if parts.percentEncodedPath.hasSuffix("/") { parts.percentEncodedPath.removeLast() }
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, trimmed.count <= 128, trimmed.utf8.count <= 512, let url = parts.url else { throw SessionStorageError.invalidProfile }
        serverURL = url
        self.name = trimmed
    }
    enum CodingKeys: String, CodingKey { case serverURL, name }
    public init(from decoder: any Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        let address = try ServerAddress(values.decode(String.self, forKey: .serverURL))
        try self.init(server: address, name: values.decode(String.self, forKey: .name))
    }
}

public struct SavedSession: Sendable, CustomStringConvertible, CustomDebugStringConvertible {
    public let username: String
    public let expiresAt: Date?
    public let credential: APICredential
    public var description: String { "<redacted saved session>" }
    public var debugDescription: String { description }
}

/// Synchronous storage operations are serialized without suspension points between state changes.
public actor SessionStore {
    private struct State: Codable {
        var version = 1
        var profiles: [ServerProfile] = []
        var selectedID: String?
    }
    private struct Envelope: Codable {
        var version = 1
        let serverURL: URL
        let username: String
        let token: String
        let expiresAt: Date?
    }
    private let secrets: any SecretStorage
    private let metadata: any ProfileMetadataStorage
    private let now: @Sendable () -> Date
    public init(secrets: any SecretStorage, metadata: any ProfileMetadataStorage = DefaultsProfileMetadata(),
                now: @escaping @Sendable () -> Date = Date.init) {
        self.secrets = secrets
        self.metadata = metadata
        self.now = now
    }
    private func state() throws -> State {
        guard let data = try metadata.read() else { return State() }
        guard data.count <= 65536, let state = try? JSONDecoder().decode(State.self, from: data), state.version == 1,
              state.profiles.count <= 16, Set(state.profiles.map(\.id)).count == state.profiles.count,
              state.selectedID == nil || state.profiles.contains(where: { $0.id == state.selectedID })
        else { throw SessionStorageError.corruptData }
        return state
    }
    private func persist(_ state: State) throws {
        let data = try JSONEncoder().encode(state)
        guard data.count <= 65536 else { throw SessionStorageError.metadataTooLarge }
        try metadata.write(data)
    }
    private func profile(_ id: String) throws -> ServerProfile {
        guard let profile = try state().profiles.first(where: { $0.id == id }) else { throw SessionStorageError.unknownProfile }
        return profile
    }
    public func profiles() throws -> [ServerProfile] { try state().profiles }
    public func selectedProfile() throws -> ServerProfile? {
        let state = try state()
        return state.profiles.first(where: { $0.id == state.selectedID })
    }
    public func saveProfile(_ profile: ServerProfile) throws {
        var state = try state()
        state.profiles.removeAll { $0.id == profile.id }
        guard state.profiles.count < 16 else { throw SessionStorageError.tooManyProfiles }
        state.profiles.append(profile)
        try persist(state)
    }
    public func select(_ id: String) throws {
        _ = try profile(id)
        var state = try state()
        state.selectedID = id
        try persist(state)
    }
    public func saveSession(token: String, username: String, expiresAt: Date? = nil, for id: String) throws {
        _ = try APICredential.session(token)
        guard username.utf8.count <= 256 else { throw SessionStorageError.corruptData }
        let profile = try profile(id)
        let envelope = Envelope(serverURL: profile.serverURL, username: username, token: token, expiresAt: expiresAt)
        try secrets.write(JSONEncoder().encode(envelope), account: id)
    }
    public func session(for id: String) throws -> SavedSession? {
        let profile = try profile(id)
        guard let data = try secrets.read(account: id) else { return nil }
        guard data.count <= 16384, let envelope = try? JSONDecoder().decode(Envelope.self, from: data),
              envelope.version == 1, envelope.serverURL == profile.serverURL else { throw SessionStorageError.corruptData }
        if let expiry = envelope.expiresAt, expiry <= now() {
            try secrets.delete(account: id)
            return nil
        }
        return SavedSession(username: envelope.username, expiresAt: envelope.expiresAt, credential: try .session(envelope.token))
    }
    /// Local sign-out only. Server revocation is performed separately by the live session coordinator.
    public func signOut(_ id: String) throws { _ = try profile(id); try secrets.delete(account: id) }
    public func removeProfile(_ id: String) throws {
        _ = try profile(id)
        try secrets.delete(account: id)
        var state = try state()
        state.profiles.removeAll { $0.id == id }
        if state.selectedID == id { state.selectedID = nil }
        try persist(state)
    }
}
