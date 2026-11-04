import Foundation
import Synchronization
import Testing
@testable import DebridKit

private struct StubReply: Sendable {
    var status = 200
    var headers = ["Content-Type": "application/json"]
    var data = Data(#"{"ok":true,"version":"fixture"}"#.utf8)
    var failure: URLError.Code?
    var holdOpen = false
    var redirectTo: URL?
}
private struct StubState: Sendable {
    var reply: StubReply
    var requests: [URLRequest] = []
    var stopped = false
    var redirectProposed = false
}

/// URLProtocol callbacks are owned by Foundation. All cross-request fixture state is mutex-protected.
private final class APIStub: URLProtocol, @unchecked Sendable {
    static let states = Mutex<[String: StubState]>([:])
    static let observations = Mutex<[URLRequest]>([])
    override class func canInit(with request: URLRequest) -> Bool { request.url?.host?.hasSuffix(".example.test") == true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let host = request.url!.host!
        Self.observations.withLock { $0.append(request) }
        let reply = Self.states.withLock { states in
            guard states[host] != nil else { return StubReply() }
            states[host]!.requests.append(request)
            return states[host]!.reply
        }
        if reply.holdOpen { return }
        if let failure = reply.failure {
            client?.urlProtocol(self, didFailWithError: URLError(failure)); return
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: reply.status, httpVersion: "HTTP/1.1", headerFields: reply.headers)!
        if let target = reply.redirectTo {
            Self.states.withLock { if $0[host] != nil { $0[host]!.redirectProposed = true } }
            var proposed = request
            proposed.url = target
            client?.urlProtocol(self, wasRedirectedTo: proposed, redirectResponse: response)
            return
        }
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: reply.data)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {
        guard let host = request.url?.host else { return }
        Self.states.withLock { if $0[host] != nil { $0[host]!.stopped = true } }
    }
}

struct DebridTransportTests {
    private func setup(_ reply: StubReply = StubReply(), credential: APICredential? = nil, limit: Int = 1024,
                       configuration: URLSessionConfiguration = .ephemeral) throws -> (DebridAPIClient, String) {
        let host = UUID().uuidString.lowercased() + ".example.test"
        APIStub.states.withLock { $0[host] = StubState(reply: reply) }
        configuration.protocolClasses = [APIStub.self]
        let client = try DebridAPIClient(server: ServerAddress("https://" + host + "/debrid"), credential: credential,
                                         configuration: configuration, maximumResponseBytes: limit)
        return (client, host)
    }
    private func remove(_ host: String) { APIStub.states.withLock { _ = $0.removeValue(forKey: host) } }

    @Test func healthPreservesPrefixAndHeaderCredential() async throws {
        let (client, host) = try setup(credential: .bearer("fixture-key"))
        defer { remove(host) }
        #expect(try await client.health().ok)
        let request = APIStub.states.withLock { $0[host]!.requests[0] }
        #expect(request.url?.path == "/debrid/api/v1/health")
        #expect(request.value(forHTTPHeaderField: "Authorization") == "Bearer fixture-key")
        #expect(request.url?.query == nil)
    }

    @Test func sessionAndCookieStorageAreIsolated() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.httpAdditionalHeaders = ["Cookie":"foreign=secret", "Authorization":"Bearer wrong"]
        let (client, host) = try setup(StubReply(headers: ["Content-Type":"application/json", "Set-Cookie":"foreign=secret; Path=/"]), configuration: configuration)
        defer { remove(host) }
        _ = try await client.health(); _ = try await client.health()
        let requests = APIStub.states.withLock { $0[host]!.requests }
        #expect(requests.allSatisfy { $0.value(forHTTPHeaderField: "Cookie") == nil && $0.value(forHTTPHeaderField: "Authorization") == nil })
    }

    @Test func sessionCredentialIsExplicitAndDescriptionsRedact() async throws {
        let credential = try APICredential.session("fixture_session")
        let (client, host) = try setup(credential: credential)
        defer { remove(host) }
        _ = try await client.health()
        #expect(APIStub.states.withLock { $0[host]!.requests[0].value(forHTTPHeaderField: "Cookie") } == "debrid_session=fixture_session")
        #expect(!String(describing: credential).contains("fixture_session"))
        #expect(!String(reflecting: credential).contains("fixture_session"))
        #expect(throws: APIError.invalidCredential) { try APICredential.session("bad; injected=value") }
        #expect(throws: APIError.invalidCredential) { try APICredential.bearer("bad\r\nHeader: x") }
    }

    @Test(arguments: [401,403,429,500,503,302])
    func classifiesStatusesWithoutLeakingBody(_ status: Int) async throws {
        let (client, host) = try setup(StubReply(status: status, headers: ["Content-Type":"text/html", "Retry-After":"12"], data: Data("private-body".utf8)))
        defer { remove(host) }
        do { _ = try await client.health(); Issue.record("Expected HTTP failure") }
        catch let error as APIError {
            let expected: APIError = switch status {
            case 401: .unauthorized
            case 403: .forbidden
            case 429: .rateLimited(retryAfter:12)
            case 302: .redirectRefused
            default: .http(status:status)
            }
            #expect(error == expected)
            #expect(!String(describing:error).contains("private-body"))
        }
    }

    @Test func rejectsHTMLMalformedJSONAndOversizedData() async throws {
        for (reply, expected) in [
            (StubReply(headers:["Content-Type":"text/html"]), APIError.invalidContentType),
            (StubReply(data:Data("not JSON".utf8)), APIError.invalidResponse),
            (StubReply(data:Data(repeating:32,count:2048)), APIError.responseTooLarge)
        ] {
            let (client, host) = try setup(reply)
            defer { remove(host) }
            await #expect(throws: expected) { try await client.health() }
        }
    }

    @Test func propagatesCancellationAndTimeout() async throws {
        for (code, cancelled) in [(URLError.cancelled,true),(.timedOut,false)] {
            let (client, host) = try setup(StubReply(failure:code))
            defer { remove(host) }
            if cancelled { await #expect(throws: CancellationError.self) { try await client.health() } }
            else { await #expect(throws: APIError.connection(.timedOut)) { try await client.health() } }
        }
    }

    @Test(arguments: [URLError.notConnectedToInternet, .cannotFindHost, .cannotConnectToHost, .serverCertificateUntrusted])
    func preservesNetworkAndTLSFailureCodes(_ code: URLError.Code) async throws {
        let (client, host) = try setup(StubReply(failure: code))
        defer { remove(host) }
        await #expect(throws: APIError.connection(code)) { try await client.health() }
    }

    @Test func cancellingCallingTaskStopsAnInflightRequest() async throws {
        let (client, host) = try setup(StubReply(holdOpen: true))
        defer { remove(host) }
        let task = Task { try await client.health() }
        defer { task.cancel() }
        for _ in 0..<100 {
            if APIStub.states.withLock({ !($0[host]?.requests.isEmpty ?? true) }) { break }
            try await Task.sleep(for: .milliseconds(10))
        }
        #expect(APIStub.states.withLock { $0[host]?.requests.count } == 1)
        task.cancel()
        await #expect(throws: CancellationError.self) { try await task.value }
        for _ in 0..<100 {
            if APIStub.states.withLock({ $0[host]?.stopped == true }) { break }
            try await Task.sleep(for: .milliseconds(10))
        }
        #expect(APIStub.states.withLock { $0[host]?.stopped } == true)
    }

    @Test(arguments: [false, true])
    func actualRedirectNeverReachesAnotherOriginOrPrefix(_ sameOrigin: Bool) async throws {
        let (client, host) = try setup(credential: .bearer("fixture-key"))
        defer { remove(host) }
        let targetHost = sameOrigin ? host : "blocked-" + host
        let target = URL(string: "https://" + targetHost + "/outside-prefix")!
        APIStub.states.withLock {
            $0[host]!.reply = StubReply(status:302, headers:["Location":target.absoluteString], redirectTo:target)
        }
        // A refused custom-protocol redirect leaves the original response suspended.
        // Observe real target requests, then cancel rather than racing a synthetic finish callback.
        let task = Task { try await client.health() }
        defer { task.cancel() }
        for _ in 0..<100 {
            if APIStub.states.withLock({ $0[host]?.redirectProposed == true }) { break }
            try await Task.sleep(for: .milliseconds(10))
        }
        try #require(APIStub.states.withLock { $0[host]?.redirectProposed } == true)
        for _ in 0..<100 {
            if APIStub.observations.withLock({ $0.contains(where: { $0.url == target }) }) { break }
            try await Task.sleep(for: .milliseconds(10))
        }
        #expect(APIStub.states.withLock { $0[host]?.requests.count } == 1)
        task.cancel()
        do { _ = try await task.value; Issue.record("Redirect unexpectedly completed") }
        catch is CancellationError { }
        catch let error as APIError { #expect(error == .redirectRefused) }
        #expect(APIStub.observations.withLock { !$0.contains(where: { $0.url == target }) })
    }

    @Test func refusesRedirectDelegateAndUnderstandsRetryDate() async throws {
        let session = URLSession(configuration:.ephemeral)
        defer { session.invalidateAndCancel() }
        let url = URL(string:"https://example.test/api/v1/health")!
        let task = session.dataTask(with:url)
        let target = URLRequest(url:URL(string:"https://foreign.example.test")!)
        let response = HTTPURLResponse(url:url,statusCode:302,httpVersion:nil,headerFields:nil)!
        let result: URLRequest? = await withCheckedContinuation { continuation in
            RefuseAPIRedirects().urlSession(session, task:task, willPerformHTTPRedirection:response, newRequest:target) {
                continuation.resume(returning:$0)
            }
        }
        #expect(result == nil)
        #expect(DebridAPIClient.retryAfter("Thu, 01 Jan 1970 00:00:30 GMT", now:Date(timeIntervalSince1970:0)) == 30)
        #expect(DebridAPIClient.retryAfter("-10") == nil)
    }
    @Test func nativeAuthPostsSecretsOnlyInJSONAndLogoutAcceptsEmptyBody() async throws {
        let (client, host) = try setup(StubReply(data: Data(#"{"authorization_url":"https://id.example.test/authorize"}"#.utf8)))
        defer { remove(host) }
        let started = try await client.startNative(challenge:"fixture_challenge",state:"fixture_state")
        #expect(started.authorizationUrl == "https://id.example.test/authorize")
        APIStub.states.withLock { $0[host]!.reply.data = Data(#"{"session_token":"fixture_session","username":"Fixture"}"#.utf8) }
        let session = try await client.exchangeNative(code:"fixture_code",verifier:"fixture_verifier")
        #expect(!String(reflecting:session).contains("fixture_session"))
        APIStub.states.withLock { $0[host]!.reply = StubReply(headers:[:],data:Data()) }
        try await client.logout()
        let requests = APIStub.states.withLock { $0[host]!.requests }
        #expect(requests.map(\.httpMethod) == ["POST","POST","POST"])
        #expect(requests.allSatisfy { $0.url?.query == nil && $0.value(forHTTPHeaderField:"Cookie") == nil && $0.value(forHTTPHeaderField:"Authorization") == nil })
        #expect(requests[0].url?.path == "/debrid/api/v1/auth/native/start")
        #expect(requests[1].url?.path == "/debrid/api/v1/auth/native/exchange")
        #expect(requests[0].value(forHTTPHeaderField:"Content-Type") == "application/json")
        // URLSession may expose a body as an input stream to URLProtocol.
        var posted = Data()
        if let data = requests[1].httpBody { posted = data }
        else if let stream = requests[1].httpBodyStream {
            stream.open(); defer { stream.close() }
            var buffer = [UInt8](repeating:0,count:1024)
            while stream.hasBytesAvailable { let count = stream.read(&buffer,maxLength:buffer.count); if count <= 0 { break }; posted.append(buffer,count:count) }
        }
        let body = try JSONDecoder().decode([String:String].self,from:posted)
        #expect(body == ["code":"fixture_code","verifier":"fixture_verifier"])
    }

}
