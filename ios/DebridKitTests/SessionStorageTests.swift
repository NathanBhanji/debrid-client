import Foundation
import Security
import Synchronization
import Testing
@testable import DebridKit

private final class MemorySessionStorage: SecretStorage, ProfileMetadataStorage, Sendable {
    struct State: Sendable {
        var metadata: Data?
        var secrets: [String: Data] = [:]
        var metadataWriteFailure: SessionStorageError?
        var writeFailure: SessionStorageError?
        var deleteFailure: SessionStorageError?
    }
    let state = Mutex(State())
    func read() throws -> Data? { state.withLock { $0.metadata } }
    func write(_ data: Data) throws {
        try state.withLock {
            if let failure = $0.metadataWriteFailure { throw failure }
            $0.metadata = data
        }
    }
    func read(account: String) throws -> Data? { state.withLock { $0.secrets[account] } }
    func write(_ data: Data, account: String) throws {
        try state.withLock {
            if let failure = $0.writeFailure { throw failure }
            $0.secrets[account] = data
        }
    }
    func delete(account: String) throws {
        try state.withLock {
            if let failure = $0.deleteFailure { throw failure }
            $0.secrets.removeValue(forKey: account)
        }
    }
}

struct SessionStorageTests {
    private func profile(_ path: String = "", host: String = "example.test", name: String = "Home") throws -> ServerProfile {
        try ServerProfile(server: ServerAddress("https://" + host + path), name: name)
    }

    @Test func canonicalBindingDistinguishesOriginsAndPrefixes() throws {
        let a = try profile("/debrid/", host: "EXAMPLE.test:443")
        let b = try profile("/debrid", host: "example.test")
        #expect(a.id == b.id)
        #expect(a.serverURL == b.serverURL)
        #expect(a.id != (try profile("/other")).id)
        #expect(a.id != (try profile("/debrid",host:"example.test:8443")).id)
        #expect(a.id != (try profile("/debrid",host:"another.test")).id)
    }

    @Test(arguments: ["/debrid//", "/a/../b", "/%2e%2e/b", "/a%2fb", "/%252e%252e/b"])
    func rejectsAmbiguousProxyPrefixes(_ path: String) {
        #expect(throws: SessionStorageError.invalidProfile) { try profile(path) }
    }

    @Test func profileCanonicalizationPreservesEffectiveAPIPath() throws {
        let address = try ServerAddress("https://EXAMPLE.test:443/debrid/")
        let profile = try ServerProfile(server:address)
        #expect(try ServerAddress(profile.serverURL.absoluteString).endpoint("health").path == address.endpoint("health").path)
        let hugeGrapheme = "a" + String(repeating:"\u{0301}", count:1000)
        #expect(throws: SessionStorageError.invalidProfile) { try ServerProfile(server:address,name:hugeGrapheme) }
    }

    @Test func wrongTypeDefaultsFailClosed() throws {
        let suite = "dev.bhanji.debrid.tests." + UUID().uuidString
        let defaults = try #require(UserDefaults(suiteName:suite))
        defer { defaults.removePersistentDomain(forName:suite) }
        defaults.set("wrong type",forKey:"metadata")
        let storage = DefaultsProfileMetadata(suiteName:suite,key:"metadata")
        #expect(throws: SessionStorageError.corruptData) { try storage.read() }
        #expect(throws: SessionStorageError.invalidKeychainScope) { try KeychainSecretStorage(accessGroup:"") }
    }

    @Test func restoresProfilesWithoutPuttingTokensInMetadata() async throws {
        let memory = MemorySessionStorage()
        let store = SessionStore(secrets: memory, metadata: memory)
        let a = try profile("/a"), b = try profile("/b")
        try await store.saveProfile(a); try await store.saveProfile(b); try await store.select(a.id)
        try await store.saveSession(token: "fixture_session_token", username: "Fixture user", for:a.id)
        let restored = SessionStore(secrets:memory, metadata:memory)
        #expect(try await restored.selectedProfile()?.id == a.id)
        #expect(try await restored.session(for:b.id) == nil)
        let session = try #require(try await restored.session(for:a.id))
        #expect(session.username == "Fixture user")
        #expect(!String(reflecting:session).contains("fixture_session_token"))
        let metadata = try #require(memory.state.withLock { $0.metadata })
        #expect(!String(decoding:metadata,as:UTF8.self).contains("fixture_session_token"))
        try await restored.select(b.id)
        #expect(try await restored.selectedProfile()?.id == b.id)
        #expect(try await restored.session(for:b.id) == nil)
    }

    @Test func lockedWritesNeverFallBackToMetadata() async throws {
        let memory = MemorySessionStorage()
        let store = SessionStore(secrets:memory,metadata:memory)
        let a = try profile()
        try await store.saveProfile(a)
        memory.state.withLock { $0.writeFailure = .locked }
        await #expect(throws: SessionStorageError.locked) {
            try await store.saveSession(token:"fixture_secret",username:"Fixture",for:a.id)
        }
        #expect(try await store.session(for:a.id) == nil)
        #expect(!String(decoding:try #require(try memory.read()),as:UTF8.self).contains("fixture_secret"))
    }

    @Test func expiryLogoutAndRemovalStayScopedToOneServer() async throws {
        let memory = MemorySessionStorage()
        let store = SessionStore(secrets:memory,metadata:memory,now:{ Date(timeIntervalSince1970:10000) })
        let a = try profile("/a"), b = try profile("/b")
        try await store.saveProfile(a); try await store.saveProfile(b); try await store.select(a.id)
        try await store.saveSession(token:"fixture_a",username:"A",expiresAt:Date(timeIntervalSince1970:9999),for:a.id)
        try await store.saveSession(token:"fixture_b",username:"B",expiresAt:Date(timeIntervalSince1970:10001),for:b.id)
        #expect(try await store.session(for:a.id) == nil)
        #expect(try await store.session(for:b.id)?.username == "B")
        try await store.signOut(b.id)
        #expect(try await store.session(for:b.id) == nil)
        #expect(try await store.profiles().count == 2)
        try await store.removeProfile(a.id)
        #expect(try await store.selectedProfile() == nil)
        #expect(try await store.profiles().map(\.id) == [b.id])
    }

    @Test func refusesMisbindingCorruptMetadataAndFailedRemoval() async throws {
        let memory = MemorySessionStorage()
        let store = SessionStore(secrets:memory,metadata:memory)
        let a = try profile("/a"), b = try profile("/b")
        try await store.saveProfile(a); try await store.saveProfile(b)
        try await store.saveSession(token:"fixture_a",username:"A",for:a.id)
        memory.state.withLock { $0.secrets[b.id] = $0.secrets[a.id] }
        await #expect(throws: SessionStorageError.corruptData) { try await store.session(for:b.id) }
        memory.state.withLock { $0.deleteFailure = .locked }
        await #expect(throws: SessionStorageError.locked) { try await store.removeProfile(a.id) }
        #expect(try await store.profiles().count == 2)
        memory.state.withLock { $0.metadata = Data("broken".utf8) }
        await #expect(throws: SessionStorageError.corruptData) { try await store.profiles() }
    }

    @Test func metadataFailureAfterRemovalLeavesOnlyThatProfileSignedOut() async throws {
        let memory = MemorySessionStorage()
        let store = SessionStore(secrets:memory,metadata:memory)
        let a = try profile("/a"), b = try profile("/b")
        try await store.saveProfile(a); try await store.saveProfile(b)
        try await store.saveSession(token:"fixture_a",username:"A",for:a.id)
        try await store.saveSession(token:"fixture_b",username:"B",for:b.id)
        memory.state.withLock { $0.metadataWriteFailure = .corruptData }
        await #expect(throws: SessionStorageError.corruptData) { try await store.removeProfile(a.id) }
        #expect(try await store.profiles().count == 2)
        #expect(try await store.session(for:a.id) == nil)
        #expect(try await store.session(for:b.id)?.username == "B")
    }

    @Test func realKeychainRoundTripProtectionAndNamespaceIsolation() throws {
        let service = "dev.bhanji.debrid.tests." + UUID().uuidString
        // Discover this test host's default group with a unique, owned probe. Production requires an explicit entitlement group.
        let probe: [String:Any] = [kSecClass as String:kSecClassGenericPassword,kSecAttrService as String:service,
                                  kSecAttrAccount as String:"probe",kSecValueData as String:Data("fixture".utf8),kSecReturnAttributes as String:true]
        var probeResult: CFTypeRef?
        try #require(SecItemAdd(probe as CFDictionary,&probeResult) == errSecSuccess)
        let probeAttributes = try #require(probeResult as? [String:Any])
        let group = try #require(probeAttributes[kSecAttrAccessGroup as String] as? String)
        let probeDelete: [String:Any] = [kSecClass as String:kSecClassGenericPassword,kSecAttrService as String:service,
                                        kSecAttrAccount as String:"probe",kSecAttrAccessGroup as String:group]
        defer { _ = SecItemDelete(probeDelete as CFDictionary) }
        let storage = try KeychainSecretStorage(service:service,accessGroup:group)
        let other = try KeychainSecretStorage(service:service + ".other",accessGroup:group)
        defer { try? storage.delete(account:"fixture"); try? other.delete(account:"fixture") }
        #expect(try storage.read(account:"fixture") == nil)
        try storage.write(Data("fixture-one".utf8),account:"fixture")
        try storage.write(Data("fixture-two".utf8),account:"fixture")
        #expect(try storage.read(account:"fixture") == Data("fixture-two".utf8))
        #expect(try other.read(account:"fixture") == nil)
        let query: [String:Any] = [kSecClass as String:kSecClassGenericPassword,kSecAttrService as String:service,
                                   kSecAttrAccount as String:"fixture",kSecAttrAccessGroup as String:group,kSecReturnAttributes as String:true]
        var result: CFTypeRef?
        #expect(SecItemCopyMatching(query as CFDictionary,&result) == errSecSuccess)
        let attributes = try #require(result as? [String:Any])
        #expect(attributes[kSecAttrAccessible as String] as? String == kSecAttrAccessibleWhenUnlockedThisDeviceOnly as String)
        #expect((attributes[kSecAttrSynchronizable as String] as? NSNumber)?.boolValue != true)
        try storage.delete(account:"fixture"); try storage.delete(account:"fixture")
        #expect(try storage.read(account:"fixture") == nil)
    }
}
