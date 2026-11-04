import Foundation
import Observation

public struct DiscoveredServer: Equatable, Sendable {
    public enum Login: Equatable, Sendable { case pocketID, upgradeRequired, webSetupRequired, passwordOnly, unsupported }
    public let profile: ServerProfile
    public let version: String
    public let issuer: String?
    public let login: Login
    public init(profile: ServerProfile, version: String, issuer: String?, login: Login) {
        self.profile = profile; self.version = version; self.issuer = issuer; self.login = login
    }
}

public protocol ServerDiscoveryChecking: Sendable {
    func inspect(_ profile: ServerProfile) async throws -> DiscoveredServer
}

protocol PublicDiscoveryAPI: Sendable {
    func health() async throws -> HealthDTO
    func authStatus() async throws -> AuthStatusDTO
}
extension DebridAPIClient: PublicDiscoveryAPI {}

/// Only public health/auth routes, using an anonymous immutable client. Never probes providers or sends credentials.
public struct ServerDiscoveryService: ServerDiscoveryChecking {
    private let makeAPI: @Sendable (ServerAddress) throws -> any PublicDiscoveryAPI
    public init() { makeAPI = { try DebridAPIClient(server: $0) } }
    init(makeAPI: @escaping @Sendable (ServerAddress) throws -> any PublicDiscoveryAPI) { self.makeAPI = makeAPI }
    public func inspect(_ profile: ServerProfile) async throws -> DiscoveredServer {
        let client = try makeAPI(ServerAddress(profile.serverURL.absoluteString))
        let health = try await client.health()
        guard health.ok else { throw APIError.invalidResponse }
        let auth = try await client.authStatus()
        try Task.checkCancellation()
        let login: DiscoveredServer.Login
        if !auth.configured { login = .webSetupRequired }
        else if auth.mode == "oidc" { login = auth.nativeSso == true ? .pocketID : .upgradeRequired }
        else if auth.mode == "password" { login = .passwordOnly }
        else { login = .unsupported }
        return DiscoveredServer(profile: profile, version: health.version, issuer: auth.oidcIssuer, login: login)
    }
}

@Observable @MainActor public final class ServerConnectionModel {
    public enum Phase: Equatable { case idle, checking, discovered(DiscoveredServer), failed(String) }
    public var address = "" { didSet { if address != oldValue { addressRevision &+= 1; invalidate() } } }
    public private(set) var addressRevision = 0
    public private(set) var phase: Phase = .idle
    private let discovery: any ServerDiscoveryChecking
    private var revision = 0
    public init(discovery: any ServerDiscoveryChecking = ServerDiscoveryService()) { self.discovery = discovery }
    public func invalidate() { revision &+= 1; phase = .idle }
    public func restoreAddress(_ value: String, expectedRevision: Int) {
        guard !Task.isCancelled, address.isEmpty, addressRevision == expectedRevision else { return }
        address = value
    }
    public func check() async {
        guard !Task.isCancelled else { return }
        revision &+= 1
        let requestRevision = revision
        let profile: ServerProfile
        do { profile = try ServerProfile(server: ServerAddress(address)) }
        catch { phase = .failed("Enter a full HTTPS server address without credentials, a query or a fragment."); return }
        phase = .checking
        do {
            let server = try await discovery.inspect(profile)
            try Task.checkCancellation()
            guard requestRevision == revision else { return }
            phase = .discovered(server)
        } catch is CancellationError {
            if requestRevision == revision { phase = .idle }
        } catch {
            guard requestRevision == revision else { return }
            phase = .failed(Self.message(for: error))
        }
    }
    public static func message(for error: any Error) -> String {
        ConnectionIssue(error).message
    }
}
