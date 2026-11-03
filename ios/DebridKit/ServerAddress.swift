import Foundation

/// A server root, including an optional reverse-proxy prefix. Credentials never belong here.
public struct ServerAddress: Equatable, Sendable {
    public let url: URL

    public enum ValidationError: Error, Equatable {
        case invalidAddress
        case httpsRequired
        case credentialsNotAllowed
        case queryOrFragmentNotAllowed
    }

    public init(_ input: String) throws {
        let raw = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let parts = URLComponents(string: raw), let host = parts.host, !host.isEmpty,
              let url = parts.url else { throw ValidationError.invalidAddress }
        guard parts.scheme?.lowercased() == "https" else { throw ValidationError.httpsRequired }
        guard parts.user == nil, parts.password == nil else { throw ValidationError.credentialsNotAllowed }
        guard parts.query == nil, parts.fragment == nil else { throw ValidationError.queryOrFragmentNotAllowed }
        self.url = url
    }

    /// Appends individual path components rather than allowing a leading slash to discard the prefix.
    public func endpoint(_ components: String...) -> URL {
        components.reduce(url.appendingPathComponent("api").appendingPathComponent("v1")) {
            $0.appendingPathComponent($1)
        }
    }
}
