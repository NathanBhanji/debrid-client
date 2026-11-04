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
    var holdAfterData = false
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
        if !reply.holdAfterData { client?.urlProtocolDidFinishLoading(self) }
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

    @Test func retryRequestAndReceiptPreserveBindingCredentialAndQuery() async throws {
        let body=Data(#"{"kind":"torrent","resource_id":"retry-target","torrent_id":"retry-target","accepted_at":"2026-01-02T03:04:05Z","torrent_exists":true}"#.utf8)
        let (client,host)=try setup(StubReply(data:body),credential:.session("fixture_cookie"));defer {remove(host)}
        let key=UUID(),revision=String(repeating:"a",count:64)
        _=try await client.requestRetry(kind:.torrent,id:"retry-target",key:key,revision:revision)
        _=try await client.retryAcceptance(kind:.torrent,id:"retry-target",key:key,revision:revision)
        let requests=APIStub.states.withLock {$0[host]?.requests ?? []}
        #expect(requests.count==2 && requests[0].httpMethod=="POST" && requests[1].httpMethod=="GET")
        #expect(requests[0].url?.path=="/debrid/api/v1/torrents/retry-target/retry-requests")
        #expect(requests.allSatisfy {$0.value(forHTTPHeaderField:"Cookie")=="debrid_session=fixture_cookie"})
        let submittedBody=try requestBody(requests[0])
        let submitted=try #require(JSONSerialization.jsonObject(with:submittedBody) as? [String:String])
        #expect(submitted==["key":key.uuidString.lowercased(),"revision":revision])
        #expect(URLComponents(url:try #require(requests[1].url),resolvingAgainstBaseURL:false)?.queryItems==[URLQueryItem(name:"revision",value:revision)])
    }
    @Test func missingRetryReceiptIsUnknownAndWrongResourceIsRejected() async throws {
        let (missing,missingHost)=try setup(StubReply(status:404));defer {remove(missingHost)}
        #expect(try await missing.retryAcceptance(kind:.download,id:"failed-file",key:UUID(),revision:String(repeating:"a",count:64))==nil)
        let body=Data(#"{"kind":"download","resource_id":"another-file","torrent_id":"parent","accepted_at":"2026-01-02T03:04:05Z","torrent_exists":true}"#.utf8)
        let (wrong,wrongHost)=try setup(StubReply(data:body));defer {remove(wrongHost)}
        await #expect(throws:APIError.invalidResponse) {_=try await wrong.requestRetry(kind:.download,id:"failed-file",key:UUID(),revision:String(repeating:"a",count:64))}
    }

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

    @Test func eventBytesUseBoundHeaderCredentialAndIgnoreHeartbeat() async throws {
        let bytes = Data("event: heartbeat\ndata: {}\n\nevent: torrent.updated\ndata: {}\n\nevent: future.changed\ndata: {}\n\n".utf8)
        let (client,host) = try setup(StubReply(headers:["Content-Type":"text/event-stream"],data:bytes),credential:.session("fixture_session"))
        defer { remove(host) }
        var received: [ServerEvent] = []
        for try await event in client.events() { received.append(event) }
        #expect(received == [.connected,.changed("torrent.updated"),.changed("future.changed")])
        let request = try #require(APIStub.states.withLock { $0[host]?.requests.first })
        #expect(request.url?.path == "/debrid/api/v1/events" && request.url?.query == nil)
        #expect(request.value(forHTTPHeaderField:"Cookie") == "debrid_session=fixture_session")
        #expect(request.value(forHTTPHeaderField:"Accept") == "text/event-stream")
    }
    @Test func cancellingEventConsumerStopsHeldNetworkStream() async throws {
        let bytes = Data("event: torrent.updated\ndata: {}\n\n".utf8)
        let (client,host) = try setup(StubReply(headers:["Content-Type":"text/event-stream"],data:bytes,holdAfterData:true))
        defer { remove(host) }
        let (notifications,continuation) = AsyncStream<ServerEvent>.makeStream()
        let task = Task { for try await event in client.events() { continuation.yield(event) } }
        defer { task.cancel(); continuation.finish() }
        var iterator = notifications.makeAsyncIterator()
        #expect(await iterator.next() == .connected)
        #expect(await iterator.next() == .changed("torrent.updated"))
        task.cancel(); _ = try? await task.value
        for _ in 0..<100 {
            if APIStub.states.withLock({ $0[host]?.stopped == true }) { break }
            try await Task.sleep(for:.milliseconds(10))
        }
        #expect(APIStub.states.withLock { $0[host]?.stopped } == true)
    }
    @Test func eventStatusMIMEAndFrameErrorsRemainTyped() async throws {
        let (client,host) = try setup(StubReply(status:401))
        defer { remove(host) }
        await #expect(throws:APIError.unauthorized) { for try await _ in client.events() {} }
        APIStub.states.withLock { $0[host]!.reply = StubReply() }
        await #expect(throws:APIError.invalidContentType) { for try await _ in client.events() {} }
        APIStub.states.withLock { $0[host]!.reply = StubReply(headers:["Content-Type":"text/event-stream"],data:Data(("data: "+String(repeating:"x",count:70000)).utf8)) }
        await #expect(throws:APIError.responseTooLarge) { for try await _ in client.events() {} }
    }
    @Test func actualEventRedirectNeverSendsCredentialToTarget() async throws {
        let (client,host) = try setup(credential:.session("fixture_session"))
        defer { remove(host) }
        let target = URL(string:"https://blocked-"+host+"/outside/events")!
        APIStub.states.withLock { $0[host]!.reply = StubReply(status:302,headers:["Location":target.absoluteString],redirectTo:target) }
        let task = Task { for try await _ in client.events() {} }
        defer { task.cancel() }
        for _ in 0..<100 {
            if APIStub.states.withLock({ $0[host]?.redirectProposed == true }) { break }
            try await Task.sleep(for:.milliseconds(10))
        }
        try #require(APIStub.states.withLock { $0[host]?.redirectProposed } == true)
        // Leave time for an incorrectly accepted redirect to issue its real target request.
        for _ in 0..<100 {
            if APIStub.observations.withLock({ $0.contains(where: { $0.url == target }) }) { break }
            try await Task.sleep(for:.milliseconds(10))
        }
        task.cancel(); _ = try? await task.value
        #expect(APIStub.observations.withLock { !$0.contains(where: { $0.url == target }) })
    }

}


extension DebridTransportTests {
    private func submissionFixture<T:Decodable>(_ key:String,as type:T.Type) throws -> T {
        let url=try #require(Bundle(for:APIStub.self).url(forResource:"contract",withExtension:"json",subdirectory:"ios-api"))
        let root=try #require(JSONSerialization.jsonObject(with:Data(contentsOf:url)) as? [String:Any])
        return try DebridAPIClient.decoder().decode(type,from:JSONSerialization.data(withJSONObject:try #require(root[key])))
    }
    private func requestBody(_ request:URLRequest) throws -> Data {
        if let data=request.httpBody { return data }
        let stream=try #require(request.httpBodyStream);stream.open();defer { stream.close() }
        var data=Data(),buffer=[UInt8](repeating:0,count:4096)
        while stream.hasBytesAvailable {
            let count=stream.read(&buffer,maxLength:buffer.count);guard count>=0 else { throw APIError.invalidResponse };if count==0 { break }
            guard data.count+count<=TorrentFile.byteLimit+64*1024 else { throw APIError.responseTooLarge };data.append(contentsOf:buffer.prefix(count))
        }
        return data
    }
    @Test func nativeAddUsesExactRawMagnetFullSettingsCookieAndSubmissionHeader() async throws {
        let settings:SettingsDTO=try submissionFixture("settings",as:SettingsDTO.self)
        let torrent:TorrentDTO=try submissionFixture("torrent",as:TorrentDTO.self)
        // Reuse the actual wire response without needing an Encodable transfer DTO.
        let url=try #require(Bundle(for:APIStub.self).url(forResource:"contract",withExtension:"json",subdirectory:"ios-api")),root=try #require(JSONSerialization.jsonObject(with:Data(contentsOf:url)) as? [String:Any])
        let response=try JSONSerialization.data(withJSONObject:try #require(root["torrent"]))
        let (client,host)=try setup(StubReply(status:201,data:response),credential:.session("fixture_session"),limit:16384);defer { remove(host) }
        let raw="magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&tr=https%3a%2f%2ffixture.test%2fa%3ftoken%3dprivate&dn=Original%2bText",key=UUID()
        let options=try SubmissionOptions(accountID:"fixture-account",category:"Documentaries",settings:settings.torrentDefaults)
        #expect(try await client.submit(.magnet(MagnetLink(raw)),options:options,key:key).id==torrent.id)
        let request=try #require(APIStub.states.withLock { $0[host]?.requests.first })
        let body=try #require(JSONSerialization.jsonObject(with:requestBody(request)) as? [String:Any])
        #expect(request.url?.path=="/debrid/api/v1/torrents" && request.httpMethod=="POST")
        #expect(request.value(forHTTPHeaderField:"Cookie")=="debrid_session=fixture_session" && request.value(forHTTPHeaderField:"Origin")==nil && request.value(forHTTPHeaderField:"Idempotency-Key")==key.uuidString)
        #expect(body["magnet"] as? String==raw && body["account"] as? String=="fixture-account")
        let encoded=try #require(body["settings"] as? [String:Any]);#expect(encoded["download_retries"] as? Int==settings.torrentDefaults.downloadRetries && encoded["unpack"] as? Bool==settings.torrentDefaults.unpack)
    }
    @Test func nativeMultipartKeepsExactTorrentBytesAndJSONSettingsField() async throws {
        let settings:SettingsDTO=try submissionFixture("settings",as:SettingsDTO.self)
        let url=try #require(Bundle(for:APIStub.self).url(forResource:"contract",withExtension:"json",subdirectory:"ios-api")),root=try #require(JSONSerialization.jsonObject(with:Data(contentsOf:url)) as? [String:Any])
        let response=try JSONSerialization.data(withJSONObject:try #require(root["torrent"]))
        let (client,host)=try setup(StubReply(status:201,data:response),limit:16384);defer { remove(host) }
        let file=try fixtureTorrent(),options=try SubmissionOptions(accountID:"fixture-account",category:"",settings:settings.torrentDefaults)
        _ = try await client.submit(.torrent(file),options:options,key:UUID())
        let request=try #require(APIStub.states.withLock { $0[host]?.requests.first }),body=try requestBody(request)
        #expect(request.url?.path=="/debrid/api/v1/torrents/file" && request.value(forHTTPHeaderField:"Content-Type")?.hasPrefix("multipart/form-data; boundary=Debrid-")==true)
        #expect(body.range(of:file.data) != nil)
        #expect(body.range(of:Data("name=\"settings\"".utf8)) != nil && body.range(of:Data("download_retries".utf8)) != nil)
    }
    @Test func submissionConflictHeadersAreTypedWithoutReadingPrivateErrorBodiesAndReceipt404IsOptional() async throws {
        let settings:SettingsDTO=try submissionFixture("settings",as:SettingsDTO.self),options=try SubmissionOptions(accountID:"fixture-account",category:"",settings:settings.torrentDefaults)
        let reply=StubReply(status:409,headers:["Content-Type":"application/problem+json","X-Debrid-Error-Code":"active_torrent","X-Debrid-Existing-Torrent-ID":"existing-transfer"],data:Data("private-error-body".utf8))
        let (client,host)=try setup(reply);defer { remove(host) }
        let raw="magnet:?xt=urn:btih:"+String(repeating:"0",count:40)
        await #expect(throws:SubmissionFailure.activeTorrent("existing-transfer")) { try await client.submit(.magnet(MagnetLink(raw)),options:options,key:UUID()) }
        APIStub.states.withLock { $0[host]?.reply=StubReply(status:404,data:Data("private-error-body".utf8)) }
        #expect(try await client.submissionReceipt(key:UUID())==nil)
        let requests=APIStub.states.withLock { $0[host]?.requests ?? [] }
        #expect(requests.count==2 && requests.last?.httpMethod=="GET" && requests.last?.url?.path.contains("torrent-submissions")==true)
    }
}
