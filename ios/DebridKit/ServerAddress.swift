import Foundation

/// A server root, including an optional reverse-proxy prefix. Credentials never belong here.
public struct ServerAddress: Equatable, Sendable {
    public let url: URL

    public enum ValidationError: Error, Equatable {
        case invalidAddress
        case httpsRequired
        case credentialsNotAllowed
        case queryOrFragmentNotAllowed
        case invalidPathComponent
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

    /// Route names and resource IDs are individual components, never relative path syntax.
    public func endpoint(_ components: String...) throws -> URL {
        var result = url.appendingPathComponent("api").appendingPathComponent("v1")
        for component in components {
            guard !component.isEmpty, component != ".", component != "..",
                  !component.contains(where: { "/\\%?#".contains($0) || $0.isNewline || $0.asciiValue.map({ $0 < 32 }) == true })
            else { throw ValidationError.invalidPathComponent }
            result.appendPathComponent(component)
        }
        return result
    }
}
