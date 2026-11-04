import Foundation
import Testing
@testable import DebridKit

private struct UnsafeDiagnosticError: Error, CustomStringConvertible { var description: String { "SECRET https://user:pass@private.test/?token=secret" } }
private actor HeldConnectionCheck: ServerDiscoveryChecking {
    var calls = 0
    var result: CheckedContinuation<DiscoveredServer, any Error>?
    var started: CheckedContinuation<Void, Never>?
    func inspect(_ profile: ServerProfile) async throws -> DiscoveredServer {
        calls += 1
        return try await withCheckedThrowingContinuation { result = $0; started?.resume(); started = nil }
    }
    func wait() async { if result == nil { await withCheckedContinuation { started = $0 } } }
    func finish(_ profile: ServerProfile) { result?.resume(returning: DiscoveredServer(profile: profile, version: "0.2.8", issuer: "https://private.test/secret", login: .pocketID)); result = nil }
}
private struct DiagnosticFailure: ServerDiscoveryChecking {
    let error: APIError
    func inspect(_ profile: ServerProfile) async throws -> DiscoveredServer { throw error }
}
private actor LimitedTransfers: TransferListing {
    var calls = 0
    let initial: TorrentDTO?
    init(initial: TorrentDTO? = nil) { self.initial = initial }
    func torrents() async throws -> [TorrentDTO] {
        calls += 1
        if calls == 1, let initial { return [initial] }
        throw APIError.rateLimited(retryAfter: 90)
    }
}

private actor ShortLimitedTransfers: TransferListing {
    var calls = 0
    func torrents() async throws -> [TorrentDTO] {
        calls += 1
        if calls == 1 { throw APIError.rateLimited(retryAfter: 0) }
        return []
    }
}
private actor LateLimitedTransfers: TransferListing {
    var result: CheckedContinuation<[TorrentDTO], any Error>?
    var started: CheckedContinuation<Void, Never>?
    var calls = 0
    func torrents() async throws -> [TorrentDTO] {
        calls += 1
        return try await withCheckedThrowingContinuation { result = $0; started?.resume(); started = nil }
    }
    func wait() async { if result == nil { await withCheckedContinuation { started = $0 } } }
    func fail() { result?.resume(throwing: APIError.rateLimited(retryAfter: 90)); result = nil }
}

@MainActor @Suite(.timeLimit(.minutes(1))) struct ConnectionRecoveryTests {
    @Test func networkAndServerFailuresHaveDistinctSafeGuidance() {
        let cases: [(APIError, ConnectionIssue)] = [(.connection(.notConnectedToInternet), .offline), (.connection(.cannotFindHost), .dns), (.connection(.timedOut), .unreachable), (.connection(.serverCertificateHasUnknownRoot), .tls), (.connection(.appTransportSecurityRequiresSecureConnection), .tls), (.connection(.networkConnectionLost), .interrupted), (.unauthorized, .sessionExpired), (.forbidden, .denied), (.rateLimited(retryAfter: 10), .rateLimited), (.http(status: 503), .serverError), (.redirectRefused, .redirect), (.invalidContentType, .incompatible)]
        for (error, issue) in cases { #expect(ConnectionIssue(error) == issue); #expect(!issue.guidance.isEmpty) }
        #expect(ConnectionIssue(APIError.connection(.cannotFindHost)).guidance.contains("cannot determine"))
        #expect(ConnectionIssue(UnsafeDiagnosticError()) == .incompatible)
        #expect(!ConnectionIssue(UnsafeDiagnosticError()).message.contains("SECRET"))
    }
    @Test func reportNeverIncludesRawErrorURLsOrArbitraryServerVersion() {
        let date = Date(timeIntervalSince1970: 0)
        for version in ["SECRET", "https://private.test/?token=abc", "1.2.3-secret", "0.2.8\ncredential", "0.2.8\n", String(repeating: "1", count: 10_000)] {
            let report = ConnectionReport(checkedAt: date, issue: nil, serverVersion: version, pocketIDAvailable: true)
            #expect(report.serverVersion == nil); #expect(report.text.utf8.count < 800); #expect(!report.text.contains(version))
        }
        let report = ConnectionReport(checkedAt: date, issue: .tls, serverVersion: "0.2.8", pocketIDAvailable: true)
        #expect(!report.publicServerReached && !report.pocketIDAvailable)
        #expect(report.text.contains("Session acceptance: not tested"))
        #expect(report.text.contains("Tailscale state: not detected"))
    }
    @Test func checkIsExplicitSingleFlightAndLateResultCannotSurviveInvalidation() async throws {
        let profile = try ServerProfile(server: ServerAddress("https://secret.example.test/private")), api = HeldConnectionCheck()
        let model = ConnectionDiagnosticsModel(profile: profile, discovery: api)
        #expect(await api.calls == 0 && model.report == nil)
        let task = Task { await model.check() }; await api.wait()
        await model.check(); #expect(await api.calls == 1)
        model.invalidate(); task.cancel(); await api.finish(profile); await task.value
        #expect(model.report == nil && !model.isBusy)
        let retry = Task { await model.check() }; await api.wait(); await api.finish(profile); await retry.value
        #expect(model.report?.serverVersion == "0.2.8")
        #expect(model.report?.text.contains("secret.example.test") == false)
    }
    @Test func changedConnectionAndFailureCannotPublishOldSuccessfulResult() async throws {
        let profile = try ServerProfile(server: ServerAddress("https://fixture.test")), api = HeldConnectionCheck()
        var current = true
        let model = ConnectionDiagnosticsModel(profile: profile, discovery: api, current: { current })
        let task = Task { await model.check() }; await api.wait(); current = false; await api.finish(profile); await task.value
        #expect(model.report == nil)
        let failed = ConnectionDiagnosticsModel(profile: profile, discovery: DiagnosticFailure(error: .connection(.dnsLookupFailed)))
        await failed.check(); #expect(failed.report?.issue == .dns && failed.report?.publicServerReached == false)
    }
    @Test func refreshResumesAfterMonotonicCooldown() async throws {
        let api = ShortLimitedTransfers(), profile = try ServerProfile(server: ServerAddress("https://fixture.test"))
        let model = TransferListModel(profile: profile, api: api)
        await model.reload(); #expect(model.isRefreshThrottled)
        await model.reload(); #expect(await api.calls == 1)
        try await Task.sleep(for: .milliseconds(1100))
        #expect(!model.isRefreshThrottled)
        await model.reload(); #expect(await api.calls == 2)
        #expect(model.phase == .ready && model.failureMessage == nil && model.retryAvailableAt == nil)
    }
    @Test func observed429AfterCancellationStillBlocksSameClientRefresh() async throws {
        let api = LateLimitedTransfers(), profile = try ServerProfile(server: ServerAddress("https://fixture.test"))
        let model = TransferListModel(profile: profile, api: api)
        let task = Task { await model.reload() }; await api.wait()
        model.invalidate(); task.cancel(); await api.fail(); await task.value
        #expect(model.retryAvailableAt != nil && model.items.isEmpty && !model.requiresSignIn)
        #expect(model.failureMessage == ConnectionIssue.rateLimited.message)
        await model.reload(); #expect(await api.calls == 1)
    }
    @Test(arguments: [false, true]) func rateLimitStopsManualAndForegroundTransferRefreshWithoutErasingState(preloaded: Bool) async throws {
        let api = LimitedTransfers(initial: preloaded ? try TransferFixtures.torrent() : nil), profile = try ServerProfile(server: ServerAddress("https://fixture.test"))
        let model = TransferListModel(profile: profile, api: api)
        if preloaded { await model.reload() }
        let date = model.updatedAt, count = preloaded ? 2 : 1
        await model.reload(); #expect(await api.calls == count); #expect(model.retryAvailableAt != nil)
        for _ in 0..<5 { await model.reload() }
        model.startLiveUpdates(); try await Task.sleep(for: .milliseconds(40)); model.stopLiveUpdates(); model.startLiveUpdates()
        try await Task.sleep(for: .milliseconds(40)); await model.refreshWhileActive(); model.stopLiveUpdates()
        #expect(await api.calls == count); #expect(model.retryAvailableAt != nil)
        #expect(model.updatedAt == date && model.items.count == (preloaded ? 1 : 0))
        #expect(model.failureMessage == ConnectionIssue.rateLimited.message)
        if case .failed = model.phase {} else { Issue.record("Foreground restart hid the cooldown failure") }
    }
}
