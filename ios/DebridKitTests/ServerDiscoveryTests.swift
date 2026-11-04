import Foundation
import Testing
@testable import DebridKit

private actor DeferredDiscovery: ServerDiscoveryChecking {
    var starters: [String: CheckedContinuation<Void, Never>] = [:]
    var continuations: [String: CheckedContinuation<DiscoveredServer, any Error>] = [:]
    func inspect(_ profile: ServerProfile) async throws -> DiscoveredServer {
        try await withCheckedThrowingContinuation { continuations[profile.serverURL.host!] = $0; starters.removeValue(forKey: profile.serverURL.host!)?.resume() }
    }
    func waitUntilStarted(_ host: String) async {
        if continuations[host] != nil { return }
        await withCheckedContinuation { starters[host] = $0 }
    }
    func finish(_ profile: ServerProfile) {
        continuations.removeValue(forKey: profile.serverURL.host!)?.resume(returning:
            DiscoveredServer(profile: profile, version:"fixture", issuer:nil, login:.upgradeRequired))
    }
}

@MainActor @Suite(.timeLimit(.minutes(1))) struct ServerDiscoveryTests {
    @Test func delayedRestoreCannotReplaceEditedOrClearedInput() {
        let model = ServerConnectionModel(discovery: DeferredDiscovery())
        let revision = model.addressRevision
        model.address = "https://typed.example.test"
        model.address = ""
        model.restoreAddress("https://saved.example.test", expectedRevision: revision)
        #expect(model.address.isEmpty)
        model.restoreAddress("https://saved.example.test", expectedRevision: model.addressRevision)
        #expect(model.address == "https://saved.example.test")
    }
    @Test func cancelledBeforeStartCannotChangeNewerState() async {
        let model = ServerConnectionModel(discovery: DeferredDiscovery())
        model.address = "https://a.example.test"
        let obsolete = Task { await model.check() }
        obsolete.cancel()
        model.address = "https://b.example.test"
        await obsolete.value
        #expect(model.phase == .idle)
        #expect(model.address == "https://b.example.test")
    }
    @Test func invalidAddressNeverStartsNetworkWork() async {
        let model = ServerConnectionModel(discovery: DeferredDiscovery())
        model.address = "http://example.test"
        await model.check()
        guard case .failed = model.phase else { Issue.record("Expected validation failure"); return }
    }
    @Test func oldServerResultCannotReplaceEditedAddress() async throws {
        let discovery = DeferredDiscovery()
        let model = ServerConnectionModel(discovery: discovery)
        let profile = try ServerProfile(server: ServerAddress("https://a.example.test"))
        model.address = profile.serverURL.absoluteString
        let task = Task { await model.check() }
        await discovery.waitUntilStarted("a.example.test")
        model.address = "https://b.example.test"
        await discovery.finish(profile)
        await task.value
        #expect(model.phase == .idle)
    }
    @Test func cancellationAndExplicitInvalidationDiscardLateResponse() async throws {
        let discovery = DeferredDiscovery()
        let model = ServerConnectionModel(discovery: discovery)
        let profile = try ServerProfile(server: ServerAddress("https://a.example.test"))
        model.address = profile.serverURL.absoluteString
        let task = Task { await model.check() }
        await discovery.waitUntilStarted("a.example.test")
        task.cancel(); model.invalidate()
        await discovery.finish(profile)
        await task.value
        #expect(model.phase == .idle)
    }
    @Test func errorsAreUsefulAndDoNotExposeServerBodies() {
        #expect(ServerConnectionModel.message(for: APIError.connection(.cannotFindHost)).contains("Tailscale"))
        #expect(ServerConnectionModel.message(for: APIError.connection(.serverCertificateUntrusted)).contains("certificate"))
        #expect(ServerConnectionModel.message(for: APIError.redirectRefused).contains("final HTTPS"))
        #expect(ServerConnectionModel.message(for: APIError.connection(.notConnectedToInternet)).contains("offline"))
    }
}

private struct DiscoveryFixture: PublicDiscoveryAPI {
    let ok: Bool
    let configured: Bool
    let mode: String
    let native: Bool?
    func health() async throws -> HealthDTO { HealthDTO(ok:ok,version:"fixture") }
    func authStatus() async throws -> AuthStatusDTO { AuthStatusDTO(configured:configured,mode:mode,oidcIssuer:"https://id.example.test",nativeSso:native) }
}

struct PublicDiscoveryTests {
    @Test(arguments: [
        (true,"oidc",true,DiscoveredServer.Login.pocketID),
        (true,"oidc",false,.upgradeRequired),
        (true,"password",false,.passwordOnly),
        (false,"",false,.webSetupRequired),
        (true,"future",false,.unsupported)
    ]) func serverCapabilities(value: (Bool,String,Bool,DiscoveredServer.Login)) async throws {
        let fixture = DiscoveryFixture(ok:true,configured:value.0,mode:value.1,native:value.2)
        let service = ServerDiscoveryService(makeAPI: { _ in fixture })
        let profile = try ServerProfile(server:ServerAddress("https://debrid.example.test/prefix"))
        let result = try await service.inspect(profile)
        #expect(result.profile == profile)
        #expect(result.login == value.3)
    }
    @Test func unhealthyServerCannotBecomeReady() async throws {
        let service = ServerDiscoveryService(makeAPI: { _ in DiscoveryFixture(ok:false,configured:true,mode:"oidc",native:true) })
        let profile = try ServerProfile(server:ServerAddress("https://debrid.example.test"))
        await #expect(throws:APIError.invalidResponse) { try await service.inspect(profile) }
    }
}
