import Foundation

public struct APICredential: Sendable, CustomStringConvertible, CustomDebugStringConvertible {
    private let name: String
    private let value: String
    public var description: String { "<redacted credential>" }
    public var debugDescription: String { description }

    private init(name: String, prefix: String, token: String, punctuation: String) throws {
        guard !token.isEmpty, token.utf8.count <= 8192,
              token.utf8.allSatisfy({ (48...57).contains($0) || (65...90).contains($0) || (97...122).contains($0) || punctuation.utf8.contains($0) })
        else { throw APIError.invalidCredential }
        self.name = name
        self.value = prefix + token
    }
    public static func bearer(_ token: String) throws -> Self { try .init(name: "Authorization", prefix: "Bearer ", token: token, punctuation: "-._~+/=") }
    public static func session(_ token: String) throws -> Self { try .init(name: "Cookie", prefix: "debrid_session=", token: token, punctuation: "-_") }
    fileprivate func apply(to request: inout URLRequest) { request.setValue(value, forHTTPHeaderField: name) }
}

public enum APIError: Error, Equatable, Sendable {
    case invalidCredential, invalidConfiguration, invalidResponse, invalidContentType, responseTooLarge
    case unauthorized, forbidden, redirectRefused
    case rateLimited(retryAfter: TimeInterval?)
    case http(status: Int)
    case connection(URLError.Code)
}

/// No redirect can carry a credential to another origin or proxy path.
final class RefuseAPIRedirects: NSObject, URLSessionTaskDelegate, Sendable {
    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                    completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

public final class DebridAPIClient: Sendable {
    public var isPreview:Bool {false}
    public var serverURL:URL { server.url }
    private let server: ServerAddress
    private let credential: APICredential?
    private let session: URLSession
    private let eventSession: URLSession
    private let maximumResponseBytes: Int

    public init(server: ServerAddress, credential: APICredential? = nil,
                configuration: URLSessionConfiguration = .ephemeral, maximumResponseBytes: Int = 8 * 1024 * 1024) throws {
        guard configuration.identifier == nil, (1...16 * 1024 * 1024).contains(maximumResponseBytes) else { throw APIError.invalidConfiguration }
        guard let isolated = configuration.copy() as? URLSessionConfiguration else { throw APIError.invalidConfiguration }
        isolated.httpShouldSetCookies = false
        isolated.httpCookieStorage = nil
        isolated.urlCredentialStorage = nil
        isolated.urlCache = nil
        isolated.httpAdditionalHeaders = nil
        isolated.requestCachePolicy = .reloadIgnoringLocalCacheData
        isolated.timeoutIntervalForRequest = 20
        isolated.timeoutIntervalForResource = 60
        isolated.waitsForConnectivity = false
        self.server = server
        self.credential = credential
        self.maximumResponseBytes = maximumResponseBytes
        self.session = URLSession(configuration: isolated, delegate: RefuseAPIRedirects(), delegateQueue: nil)
        isolated.timeoutIntervalForRequest = 45 // Server heartbeat is every15seconds.
        isolated.timeoutIntervalForResource = 30 * 60 // Owned foreground stream; bounded reconnect even if never interrupted.
        self.eventSession = URLSession(configuration: isolated, delegate: RefuseAPIRedirects(), delegateQueue: nil)
    }
    deinit { session.invalidateAndCancel(); eventSession.invalidateAndCancel() }

    public func health() async throws -> HealthDTO { try await get(["health"]) }
    public func authStatus() async throws -> AuthStatusDTO { try await get(["auth", "status"]) }
    public func identity() async throws -> IdentityDTO { try await get(["auth", "me"]) }
    public func torrents() async throws -> [TorrentDTO] { try await get(["torrents"]) }
    public func torrent(id: String) async throws -> TorrentDTO { try await get(["torrents", id]) }
    public func accounts() async throws -> [AccountDTO] { try await get(["accounts"]) }
    public func settings() async throws -> SettingsDTO { try await get(["settings"]) }
    public func systemStatus() async throws -> SystemStatusDTO { try await get(["system", "status"]) }

    public func startNative(challenge: String, state: String) async throws -> NativeStartDTO {
        struct Body: Encodable { let challenge: String; let state: String }
        return try await send(["auth", "native", "start"], method: "POST", body: JSONEncoder().encode(Body(challenge: challenge, state: state)))
    }
    public func exchangeNative(code: String, verifier: String) async throws -> NativeSessionDTO {
        struct Body: Encodable { let code: String; let verifier: String }
        return try await send(["auth", "native", "exchange"], method: "POST", body: JSONEncoder().encode(Body(code: code, verifier: verifier)))
    }
    public func logout() async throws {
        let _: EmptyAPIResponse = try await send(["auth", "logout"], method: "POST", allowsEmpty: true)
    }
    private struct EmptyAPIResponse: Decodable, Sendable {}
    private func get<Response: Decodable & Sendable>(_ path: [String]) async throws -> Response {
        try await send(path, method: "GET")
    }

    /// Bounded byte collection and JSON decoding run away from a caller's UI actor.
    @concurrent func send<Response: Decodable & Sendable>(_ path: [String], method: String, body: Data? = nil, allowsEmpty: Bool = false, contentType:String = "application/json", headers:[String:String] = [:], expectedStatus:Int? = nil, submissionErrors:Bool = false,query:[URLQueryItem] = []) async throws -> Response {
        try Task.checkCancellation()
        var url = try server.endpoint(components: path)
        if !query.isEmpty {url=url.appending(queryItems:query)}
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.httpBody = body
        if body != nil { request.setValue(contentType, forHTTPHeaderField: "Content-Type") }
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        for (name,value) in headers { request.setValue(value,forHTTPHeaderField:name) }
        credential?.apply(to: &request)
        do {
            let (bytes, response) = try await session.bytes(for: request)
            defer { bytes.task.cancel() }
            guard let response = response as? HTTPURLResponse else { throw APIError.invalidResponse }
            if submissionErrors {
                switch (response.statusCode,response.value(forHTTPHeaderField:"X-Debrid-Error-Code")) {
                case (409,"active_torrent"):
                    guard let id = response.value(forHTTPHeaderField:"X-Debrid-Existing-Torrent-ID"), SubmissionIdentity.validID(id) else { throw APIError.invalidResponse }
                    throw SubmissionFailure.activeTorrent(id)
                case (409,"submission_removed"): throw SubmissionFailure.removed
                case (422,"idempotency_mismatch"): throw SubmissionFailure.keyMismatch
                default: break
                }
            }
            switch response.statusCode {
            case 200...299: break
            case 300...399: throw APIError.redirectRefused
            case 401: throw APIError.unauthorized
            case 403: throw APIError.forbidden
            case 429: throw APIError.rateLimited(retryAfter: Self.retryAfter(response.value(forHTTPHeaderField: "Retry-After")))
            default: throw APIError.http(status: response.statusCode)
            }
            if let expectedStatus, response.statusCode != expectedStatus { throw APIError.invalidResponse }
            let isJSON = response.mimeType.map { $0.lowercased() == "application/json" || $0.lowercased().hasSuffix("+json") } == true
            guard isJSON || allowsEmpty else { throw APIError.invalidContentType }
            guard response.expectedContentLength <= Int64(maximumResponseBytes) else { throw APIError.responseTooLarge }
            var data = Data()
            for try await byte in bytes {
                guard data.count < maximumResponseBytes else { throw APIError.responseTooLarge }
                data.append(byte)
            }
            try Task.checkCancellation()
            if allowsEmpty && data.isEmpty { return try Self.decoder().decode(Response.self, from: Data("{}".utf8)) }
            guard isJSON else { throw APIError.invalidContentType }
            do { return try Self.decoder().decode(Response.self, from: data) }
            catch { throw APIError.invalidResponse }
        } catch let error as URLError {
            if error.code == .cancelled { throw CancellationError() }
            throw APIError.connection(error.code)
        }
    }

    public func events() -> AsyncThrowingStream<ServerEvent, any Error> {
        AsyncThrowingStream(bufferingPolicy: .bufferingNewest(16)) { continuation in
            let task = Task { await consumeEvents(into: continuation) }
            continuation.onTermination = { _ in task.cancel() }
        }
    }
    @concurrent private func consumeEvents(into continuation: AsyncThrowingStream<ServerEvent, any Error>.Continuation) async {
        do {
            try Task.checkCancellation()
            var request = URLRequest(url: try server.endpoint("events"))
            request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
            credential?.apply(to: &request)
            let (bytes, response) = try await eventSession.bytes(for: request)
            defer { bytes.task.cancel() }
            guard let response = response as? HTTPURLResponse else { throw APIError.invalidResponse }
            switch response.statusCode {
            case 200: break
            case 300...399: throw APIError.redirectRefused
            case 401: throw APIError.unauthorized
            case 403: throw APIError.forbidden
            case 429: throw APIError.rateLimited(retryAfter: Self.retryAfter(response.value(forHTTPHeaderField: "Retry-After")))
            default: throw APIError.http(status: response.statusCode)
            }
            guard response.mimeType?.lowercased() == "text/event-stream" else { throw APIError.invalidContentType }
            try Task.checkCancellation()
            continuation.yield(.connected)
            var parser = SSEParser()
            for try await byte in bytes {
                try Task.checkCancellation()
                if let frame = try parser.consume(byte), frame.event != "heartbeat" {
                    // Changes are invalidations, not authoritative state. Do not retain/log payloads.
                    continuation.yield(.changed(frame.event))
                }
            }
            try Task.checkCancellation()
            continuation.finish()
        } catch is CancellationError { continuation.finish(throwing: CancellationError()) }
        catch let error as URLError {
            continuation.finish(throwing: error.code == .cancelled ? CancellationError() : APIError.connection(error.code))
        } catch { continuation.finish(throwing: error) }
    }

    static func retryAfter(_ raw: String?, now: Date = Date()) -> TimeInterval? {
        guard let raw else { return nil }
        if let seconds = TimeInterval(raw), seconds.isFinite, seconds >= 0 { return min(seconds, 86400) }
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(secondsFromGMT: 0)
        formatter.dateFormat = "EEE, dd MMM yyyy HH:mm:ss zzz"
        guard let date = formatter.date(from: raw) else { return nil }
        return min(86400, max(0, date.timeIntervalSince(now)))
    }

    static func decoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .custom { input in
            let raw = try input.singleValueContainer().decode(String.self)
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = formatter.date(from: raw) { return date }
            formatter.formatOptions = [.withInternetDateTime]
            guard let date = formatter.date(from: raw) else { throw APIError.invalidResponse }
            return date
        }
        return decoder
    }
}

extension DebridAPIClient: ServerEventStreaming {}
