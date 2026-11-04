import CryptoKit
import Foundation
import Security

public struct NativeStartDTO: Decodable, Sendable { public let authorizationUrl: String }
public struct NativeSessionDTO: Decodable, Sendable, CustomStringConvertible, CustomDebugStringConvertible {
    public let sessionToken: String
    public let username: String
    public var description: String { "<redacted native session>" }
    public var debugDescription: String { description }
}
public enum NativeSignInError: Error, Equatable, Sendable {
    case secureRandomUnavailable, invalidAuthorizationURL, invalidCallback, stateMismatch, refused, browserUnavailable
}

public protocol NativeSignInAPI: Sendable {
    func startNative(challenge: String, state: String) async throws -> NativeStartDTO
    func exchangeNative(code: String, verifier: String) async throws -> NativeSessionDTO
    func identity() async throws -> IdentityDTO
    func logout() async throws
}
extension DebridAPIClient: NativeSignInAPI {}

/// Attempt secrets are transient; descriptions never expose them. A killed app restarts sign-in.
public struct NativeSignInAttempt: Sendable, CustomStringConvertible, CustomDebugStringConvertible {
    public static let callbackScheme = "dev.bhanji.debrid"
    private let verifier: String
    public let state: String
    public var challenge: String { Self.base64url(Data(SHA256.hash(data: Data(verifier.utf8)))) }
    public var description: String { "<redacted sign-in attempt>" }
    public var debugDescription: String { description }
    public init() throws { verifier = try Self.random(); state = try Self.random() }
    init(verifier: String, state: String) { self.verifier = verifier; self.state = state }
    private static func random() throws -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else { throw NativeSignInError.secureRandomUnavailable }
        return base64url(Data(bytes))
    }
    static func base64url(_ data: Data) -> String { data.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "") }
    public func authorizationURL(_ response: NativeStartDTO) throws -> URL {
        guard response.authorizationUrl.utf8.count <= 8192,
              let parts = URLComponents(string: response.authorizationUrl), parts.scheme?.lowercased() == "https",
              let host = parts.host, !host.isEmpty, parts.user == nil, parts.password == nil, parts.fragment == nil,
              let url = parts.url else { throw NativeSignInError.invalidAuthorizationURL }
        return url
    }
    func code(from callback: URL) throws -> String {
        guard callback.absoluteString.utf8.count <= 8192,
              let parts = URLComponents(url: callback, resolvingAgainstBaseURL: false),
              parts.scheme?.lowercased() == Self.callbackScheme, parts.host == "auth", parts.percentEncodedPath == "/callback",
              parts.user == nil, parts.password == nil, parts.port == nil, parts.fragment == nil,
              let items = parts.queryItems, items.count == 2,
              Set(items.map(\.name)).count == 2,
              let returnedState = items.first(where: { $0.name == "state" })?.value else { throw NativeSignInError.invalidCallback }
        guard returnedState == state else { throw NativeSignInError.stateMismatch }
        if items.contains(where: { $0.name == "error" && $0.value == "sign_in_failed" }) { throw NativeSignInError.refused }
        guard let code = items.first(where: { $0.name == "code" })?.value,
              code.utf8.count == 43, let bytes = Self.decodeBase64url(code), bytes.count == 32,
              Self.base64url(bytes) == code else { throw NativeSignInError.invalidCallback }
        return code
    }
    private static func decodeBase64url(_ value: String) -> Data? {
        Data(base64Encoded: value.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/") + "=")
    }
    public func complete(callback: URL, using api: any NativeSignInAPI) async throws -> NativeSessionDTO {
        try Task.checkCancellation()
        return try await api.exchangeNative(code: code(from: callback), verifier: verifier)
    }
}

@MainActor public protocol NativeSignInBrowser: AnyObject {
    func authenticate(url: URL) async throws -> URL
    func cancel()
}
