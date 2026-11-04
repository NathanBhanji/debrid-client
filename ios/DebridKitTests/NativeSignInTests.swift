import CryptoKit
import Foundation
import Synchronization
import Testing
@testable import DebridKit

struct NativeSignInTests {
    private let state = NativeSignInAttempt.base64url(Data(repeating: 2, count: 32))
    private let code = NativeSignInAttempt.base64url(Data(repeating: 3, count: 32))
    @Test func pkceGoldenVectorAndSecureRandom() throws {
        let attempt = NativeSignInAttempt(verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", state: state)
        #expect(attempt.challenge == "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM")
        let a = try NativeSignInAttempt(), b = try NativeSignInAttempt()
        #expect(a.state.utf8.count == 43)
        #expect(a.challenge.utf8.count == 43)
        #expect(a.state != b.state && a.challenge != b.challenge)
        #expect(!String(reflecting: a).contains(a.state))
    }
    @Test func exactCallbackAndDenial() throws {
        let attempt = NativeSignInAttempt(verifier: state, state: state)
        #expect(try attempt.code(from: URL(string: "dev.bhanji.debrid://auth/callback?state=\(state)&code=\(code)")!) == code)
        #expect(throws: NativeSignInError.refused) { try attempt.code(from: URL(string:"dev.bhanji.debrid://auth/callback?state=\(state)&error=sign_in_failed")!) }
        #expect(throws: NativeSignInError.stateMismatch) { try attempt.code(from: URL(string:"dev.bhanji.debrid://auth/callback?state=wrong&code=\(code)")!) }
    }
    @Test(arguments: ["other://auth/callback", "dev.bhanji.debrid://other/callback", "dev.bhanji.debrid://auth/other", "dev.bhanji.debrid://user@auth/callback", "dev.bhanji.debrid://auth:443/callback", "dev.bhanji.debrid://auth/%63allback"])
    func rejectsWrongCallbackTarget(target: String) {
        let attempt = NativeSignInAttempt(verifier: state, state: state)
        #expect(throws: NativeSignInError.invalidCallback) { try attempt.code(from: URL(string:target+"?state=\(state)&code=\(code)")!) }
    }
    @Test func rejectsDuplicateExtraFragmentAndNoncanonicalCode() {
        let attempt = NativeSignInAttempt(verifier:state,state:state)
        for query in ["state=\(state)&state=\(state)", "state=\(state)&code=\(code)&extra=x", "state=\(state)&code=bad", "state=\(state)&code=\(code)#fragment", "state=\(state)&code=\(code.dropLast())B"] {
            #expect(throws: NativeSignInError.invalidCallback) { try attempt.code(from: URL(string:"dev.bhanji.debrid://auth/callback?"+query)!) }
        }
    }
    @Test(arguments: ["http://id.example.test/authorize", "https://user:secret@id.example.test/authorize", "https://id.example.test/authorize#fragment", "relative"])
    func rejectsUnsafeAuthorizationURL(raw: String) throws {
        let attempt = NativeSignInAttempt(verifier:state,state:state)
        #expect(throws:NativeSignInError.invalidAuthorizationURL) { try attempt.authorizationURL(NativeStartDTO(authorizationUrl:raw)) }
    }
}

private final class AuthMemory: SecretStorage, ProfileMetadataStorage, Sendable {
    struct State: Sendable { var metadata:Data?; var secrets:[String:Data] = [:]; var failWrites = false; var failDeletes = false; var failMetadataWrites = false; var failMetadataAfterSecret = false }
    let state = Mutex(State())
    func read() throws -> Data? { state.withLock { $0.metadata } }
    func write(_ data: Data) throws { try state.withLock { if $0.failMetadataWrites { throw SessionStorageError.corruptData }; $0.metadata = data } }
    func read(account: String) throws -> Data? { state.withLock { $0.secrets[account] } }
    func write(_ data: Data, account: String) throws {
        try state.withLock { if $0.failWrites { throw SessionStorageError.locked }; $0.secrets[account] = data; if $0.failMetadataAfterSecret { $0.failMetadataWrites = true } }
    }
    func delete(account: String) throws { try state.withLock { if $0.failDeletes { throw SessionStorageError.locked }; _ = $0.secrets.removeValue(forKey:account) } }
}
private final class GatedAuthMetadata: ProfileMetadataStorage, Sendable {
    let backing: AuthMemory
    let release = DispatchSemaphore(value: 0)
    let didStart: AsyncStream<Void>.Continuation
    private let firstRead = Mutex(true)
    init(backing: AuthMemory, didStart: AsyncStream<Void>.Continuation) { self.backing = backing; self.didStart = didStart }
    func read() throws -> Data? {
        let hold = firstRead.withLock { let first = $0; $0 = false; return first }
        if hold {
            didStart.yield(())
            guard release.wait(timeout: .now() + 3) == .success else { throw SessionStorageError.corruptData }
        }
        return try backing.read()
    }
    func write(_ data: Data) throws { try backing.write(data) }
}

private actor AuthFixture: NativeSignInAPI {
    var challenge = ""
    var starts = 0
    var exchanges = 0
    var logouts = 0
    var identities = 0
    var identityError: APIError?
    var logoutError: APIError?
    var holdExchange = false
    var exchangePending: CheckedContinuation<NativeSessionDTO, Never>?
    var waitingForExchange: CheckedContinuation<Void, Never>?
    func startNative(challenge: String, state: String) async throws -> NativeStartDTO {
        starts += 1; self.challenge = challenge
        return NativeStartDTO(authorizationUrl:"https://id.example.test/authorize?fixture_state="+state)
    }
    func exchangeNative(code: String, verifier: String) async throws -> NativeSessionDTO {
        exchanges += 1
        #expect(NativeSignInAttempt.base64url(Data(SHA256.hash(data:Data(verifier.utf8)))) == challenge)
        if holdExchange {
            return await withCheckedContinuation { exchangePending = $0; waitingForExchange?.resume(); waitingForExchange = nil }
        }
        return NativeSessionDTO(sessionToken:"fixture_session",username:"Fixture User")
    }
    func waitForExchange() async {
        if exchangePending != nil { return }
        await withCheckedContinuation { waitingForExchange = $0 }
    }
    func releaseExchange() { exchangePending?.resume(returning:NativeSessionDTO(sessionToken:"fixture_session",username:"Fixture User")); exchangePending = nil }
    func identity() async throws -> IdentityDTO {
        identities += 1
        if let identityError { throw identityError }
        return IdentityDTO(username:"Fixture User",mode:"oidc",apiKey:false)
    }
    func logout() async throws { logouts += 1; if let logoutError { throw logoutError } }
    func setIdentityError(_ error: APIError?) { identityError = error }
    func setLogoutError(_ error: APIError?) { logoutError = error }
    func setHoldExchange() { holdExchange = true }
}
@MainActor private final class BrowserFixture: NativeSignInBrowser {
    var hold = false
    var started = false
    var pending: CheckedContinuation<URL, any Error>?
    var waiting: CheckedContinuation<Void, Never>?
    func authenticate(url: URL) async throws -> URL {
        started = true; waiting?.resume(); waiting = nil
        if hold { return try await withCheckedThrowingContinuation { pending = $0 } }
        let state = URLComponents(url:url,resolvingAgainstBaseURL:false)!.queryItems!.first(where:{$0.name == "fixture_state"})!.value!
        let code = NativeSignInAttempt.base64url(Data(repeating:3,count:32))
        return URL(string:"dev.bhanji.debrid://auth/callback?state=\(state)&code=\(code)")!
    }
    func waitUntilStarted() async { if started { return }; await withCheckedContinuation { waiting = $0 } }
    func cancel() { pending?.resume(throwing:CancellationError()); pending = nil }
}

@MainActor @Suite(.timeLimit(.minutes(1))) struct SessionCoordinatorTests {
    private func profile(_ host: String = "a.example.test") throws -> ServerProfile { try ServerProfile(server:ServerAddress("https://"+host)) }
    private func fixture() -> (SessionCoordinator,SessionStore,AuthMemory,AuthFixture,BrowserFixture) {
        let memory = AuthMemory(), api = AuthFixture(), browser = BrowserFixture()
        let store = SessionStore(secrets:memory,metadata:memory)
        return (SessionCoordinator(store:store,browser:browser,makeAPI:{_,_ in api}),store,memory,api,browser)
    }
    @Test func signInStoresOnlyServerBoundSecretAndRestoresIdentity() async throws {
        let (coordinator,store,memory,api,_) = fixture()
        let target = try profile()
        await coordinator.signIn(to:target)
        guard case .connected(let connected) = coordinator.phase else { Issue.record("Not connected"); return }
        #expect(connected.profile == target)
        #expect(try await store.selectedProfile() == target)
        #expect(try await store.session(for:target.id)?.username == "Fixture User")
        let metadata = try #require(try memory.read())
        #expect(!String(decoding:metadata,as:UTF8.self).contains("fixture_session"))
        #expect(await api.exchanges == 1)
        await coordinator.restore()
        guard case .connected = coordinator.phase else { Issue.record("Not restored"); return }
    }
    @Test func browserCancellationAndPrecancelledTaskNeverExchange() async throws {
        let (coordinator,store,_,api,browser) = fixture()
        let target = try profile()
        browser.hold = true
        let task = Task { await coordinator.signIn(to:target) }
        await browser.waitUntilStarted()
        task.cancel(); coordinator.cancel()
        await task.value
        #expect(await api.exchanges == 0)
        #expect(try await store.profiles().isEmpty)
        let cancelled = Task { await coordinator.signIn(to:target) }
        cancelled.cancel(); await cancelled.value
        #expect(await api.starts == 1)
    }
    @Test func lateExchangeAfterCancellationDoesNotPersistOrAllowOverlappingAttempt() async throws {
        let (coordinator,store,_,api,_) = fixture()
        let target = try profile()
        await api.setHoldExchange()
        let task = Task { await coordinator.signIn(to:target) }
        await api.waitForExchange()
        task.cancel(); coordinator.cancel()
        await coordinator.signIn(to:target)
        #expect(await api.starts == 1)
        await api.releaseExchange(); await task.value
        #expect(try await store.profiles().isEmpty)
        #expect(!coordinator.isBusy)
    }
    @Test func restoreOfflinePreservesSecretAndUnauthorizedRemovesOnlyThatServer() async throws {
        let (coordinator,store,_,api,_) = fixture()
        let a = try profile(), b = try profile("b.example.test")
        for p in [a,b] { try await store.saveProfile(p); try await store.saveSession(token:"fixture_"+p.name,username:"Fixture",for:p.id) }
        try await store.select(a.id)
        await api.setIdentityError(.connection(.notConnectedToInternet)); await coordinator.restore()
        #expect(try await store.session(for:a.id) != nil)
        #expect(coordinator.message?.contains("offline") == true)
        await api.setIdentityError(.unauthorized); await coordinator.restore()
        #expect(try await store.session(for:a.id) == nil)
        #expect(try await store.session(for:b.id) != nil)
    }
    @Test func offlineSignOutIsLocalScopedAndHonestAboutRemoteRevocation() async throws {
        let (coordinator,store,_,api,_) = fixture()
        let a = try profile(), b = try profile("b.example.test")
        await coordinator.signIn(to:a)
        try await store.saveProfile(b); try await store.saveSession(token:"fixture_b",username:"B",for:b.id)
        await api.setLogoutError(.connection(.notConnectedToInternet))
        await coordinator.signOut()
        #expect(try await store.session(for:a.id) == nil)
        #expect(try await store.session(for:b.id) != nil)
        #expect(coordinator.message?.contains("could not confirm") == true)
    }
    @Test func failedSecretWriteRevokesReceivedSessionWithoutPlaintextFallback() async throws {
        let (coordinator,store,memory,api,_) = fixture()
        memory.state.withLock { $0.failWrites = true }
        let target = try profile()
        await coordinator.signIn(to:target)
        #expect(try await store.session(for:target.id) == nil)
        #expect(await api.logouts == 1)
        #expect(coordinator.message?.contains("Unlock") == true)
    }
    @Test func delayedStartupMetadataCannotConnectSavedServerAfterInputEdit() async throws {
        let memory = AuthMemory(), api = AuthFixture(), browser = BrowserFixture()
        let seeded = SessionStore(secrets:memory,metadata:memory)
        let a = try profile()
        try await seeded.saveProfile(a); try await seeded.saveSession(token:"fixture_a",username:"A",for:a.id); try await seeded.select(a.id)
        let (stream, started) = AsyncStream<Void>.makeStream()
        let metadata = GatedAuthMetadata(backing:memory,didStart:started)
        defer { metadata.release.signal(); started.finish() }
        let store = SessionStore(secrets:memory,metadata:metadata)
        let coordinator = SessionCoordinator(store:store,browser:browser,makeAPI:{_,_ in api})
        let connection = ServerConnectionModel()
        let task = Task { await coordinator.restore(connection:connection) }
        var iterator = stream.makeAsyncIterator()
        _ = await iterator.next()
        connection.address = "https://b.example.test"
        metadata.release.signal(); await task.value
        #expect(connection.address == "https://b.example.test")
        #expect(await api.identities == 0)
        guard case .signedOut = coordinator.phase else { Issue.record("Stale startup connected"); return }
    }
    @Test func failedRevokedSessionRemovalCanRetryLocallyWithoutTouchingOtherServer() async throws {
        let (coordinator,store,memory,api,_) = fixture()
        let a = try profile(), b = try profile("b.example.test")
        for p in [a,b] { try await store.saveProfile(p); try await store.saveSession(token:"fixture_session",username:"Fixture",for:p.id) }
        try await store.select(a.id)
        memory.state.withLock { $0.failDeletes = true }
        await api.setIdentityError(.unauthorized); await coordinator.restore()
        #expect(coordinator.pendingLocalRemoval == a)
        #expect(try await store.session(for:a.id) != nil)
        await coordinator.removeSavedSession()
        #expect(coordinator.pendingLocalRemoval == a)
        memory.state.withLock { $0.failDeletes = false }
        await coordinator.removeSavedSession()
        #expect(coordinator.pendingLocalRemoval == nil)
        #expect(try await store.session(for:a.id) == nil)
        #expect(try await store.session(for:b.id) != nil)
        #expect(await api.identities == 1)
    }
    @Test func interruptedSignInCleanupCanRetryWithoutNetwork() async throws {
        let (coordinator,store,memory,api,_) = fixture()
        let a = try profile(), b = try profile("b.example.test")
        try await store.saveProfile(b); try await store.saveSession(token:"fixture_b",username:"B",for:b.id)
        memory.state.withLock { $0.failMetadataAfterSecret = true; $0.failDeletes = true }
        await coordinator.signIn(to:a)
        #expect(coordinator.pendingLocalRemoval == a)
        #expect(await api.logouts == 1)
        memory.state.withLock { $0.failDeletes = false }
        await coordinator.removeSavedSession()
        #expect(coordinator.pendingLocalRemoval == nil)
        #expect(try await store.session(for:a.id) == nil)
        #expect(try await store.session(for:b.id) != nil)
        #expect(await api.logouts == 1)
    }

}
