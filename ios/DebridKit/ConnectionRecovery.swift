import Foundation
import Observation

/// Only allowlisted domain information reaches UI/support reports. Never stringify an Error.
public enum ConnectionIssue: String, Sendable, Equatable, CaseIterable {
    case offline, dns, unreachable, tls, interrupted, sessionExpired, denied, rateLimited, serverError, redirect, incompatible
    public init(_ error: any Error) {
        switch error {
        case APIError.unauthorized: self = .sessionExpired
        case APIError.forbidden: self = .denied
        case APIError.rateLimited: self = .rateLimited
        case APIError.http(let status) where (500...599).contains(status): self = .serverError
        case APIError.redirectRefused: self = .redirect
        case APIError.connection(let code):
            switch code {
            case .notConnectedToInternet, .dataNotAllowed, .internationalRoamingOff: self = .offline
            case .cannotFindHost, .dnsLookupFailed: self = .dns
            case .cannotConnectToHost, .timedOut: self = .unreachable
            case .secureConnectionFailed, .serverCertificateUntrusted, .serverCertificateHasBadDate,
                 .serverCertificateNotYetValid, .serverCertificateHasUnknownRoot, .clientCertificateRejected,
                 .clientCertificateRequired, .appTransportSecurityRequiresSecureConnection: self = .tls
            case .networkConnectionLost: self = .interrupted
            default: self = .incompatible
            }
        default: self = .incompatible
        }
    }
    public var title: String {
        switch self {
        case .offline: "You’re offline"
        case .dns: "Server name unavailable"
        case .unreachable: "Server not responding"
        case .tls: "Secure connection unavailable"
        case .interrupted: "Connection interrupted"
        case .sessionExpired: "Sign in again"
        case .denied: "Access not permitted"
        case .rateLimited: "Server asked us to wait"
        case .serverError: "Server needs attention"
        case .redirect: "Server address redirects"
        case .incompatible: "Response unavailable"
        }
    }
    public var guidance: String {
        switch self {
        case .offline: "Check this iPhone’s network connection. If your server uses Tailscale, check that connection too."
        case .dns: "Check the server address. A tailnet-only name needs access to the right Tailscale network. This app cannot determine whether Tailscale is connected."
        case .unreachable: "Check that the server is running and reachable from this iPhone. If it uses Tailscale, check your tailnet access."
        case .tls: "Check the server’s HTTPS certificate and this iPhone’s date. Debrid cannot bypass certificate verification."
        case .interrupted: "The network changed or the connection ended. Check again when the connection is stable."
        case .sessionExpired: "Your session is no longer accepted. Sign in with Pocket ID again; saved links stay on this iPhone."
        case .denied: "This server refused access. Check your account’s permissions; signing in repeatedly may not help."
        case .rateLimited: "Wait before trying again. Respect the server’s requested delay before refreshing."
        case .serverError: "The server returned an error. Try again later or check the server’s logs."
        case .redirect: "Enter the final HTTPS address of your Debrid server. Credentials are not forwarded through redirects."
        case .incompatible: "The server did not return a compatible Debrid response. Check the address and server version."
        }
    }
    public var message: String { title + ". " + guidance }
}

/// A deliberately small, allowlisted report. No URL, username, issuer, path, error body or credential is accepted.
public struct ConnectionReport: Sendable, Equatable {
    public let checkedAt: Date
    public let issue: ConnectionIssue?
    public let publicServerReached: Bool
    public let pocketIDAvailable: Bool
    public let serverVersion: String?
    public init(checkedAt: Date, issue: ConnectionIssue?, serverVersion: String? = nil, pocketIDAvailable: Bool = false) {
        self.checkedAt = checkedAt; self.issue = issue
        publicServerReached = issue == nil; self.pocketIDAvailable = issue == nil && pocketIDAvailable
        // Server-controlled strings could contain secrets. Accept only a semantic version, otherwise omit entirely.
        if issue == nil, let value = serverVersion, value.utf8.count <= 48,
           value.range(of: #"\A[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\z"#, options: .regularExpression) != nil {
            self.serverVersion = value
        } else { self.serverVersion = nil }
    }
    public var text: String {
        ["Debrid connection report · format 1", "Checked: " + checkedAt.ISO8601Format(),
         "Result: " + (issue?.rawValue ?? "public_server_reachable"),
         "Public server reached: " + (publicServerReached ? "yes" : "unconfirmed"),
         "Native Pocket ID available: " + (pocketIDAvailable ? "yes" : "unconfirmed"),
         "Server version: " + (serverVersion ?? "omitted"),
         "Session acceptance: not tested", "Tailscale state: not detected",
         "No server address, account, transfer, file path, credentials or raw logs included."].joined(separator: "\n")
    }
}

@Observable @MainActor public final class ConnectionDiagnosticsModel {
    public private(set) var report: ConnectionReport?
    public private(set) var isBusy = false
    private let profile: ServerProfile
    private let discovery: any ServerDiscoveryChecking
    private let current: @MainActor () -> Bool
    private var generation = 0
    public init(profile: ServerProfile, discovery: any ServerDiscoveryChecking = ServerDiscoveryService(), current: @escaping @MainActor () -> Bool = { true }) {
        self.profile = profile; self.discovery = discovery; self.current = current
    }
    public func invalidate() { generation &+= 1; report = nil }
    public func check() async {
        guard !Task.isCancelled, !isBusy, current() else { return }
        isBusy = true; generation &+= 1; let request = generation; report = nil
        defer { isBusy = false }
        do {
            let result = try await discovery.inspect(profile)
            try Task.checkCancellation()
            guard generation == request, current() else { return }
            report = ConnectionReport(checkedAt: Date(), issue: nil, serverVersion: result.version, pocketIDAvailable: result.login == .pocketID)
        } catch {
            guard !(error is CancellationError), !Task.isCancelled, generation == request, current() else { return }
            report = ConnectionReport(checkedAt: Date(), issue: ConnectionIssue(error))
        }
    }
}
