import Foundation
import Testing
@testable import DebridKit

private final class TransferFixtureBundle: NSObject {}
enum TransferFixtures {
    static func torrent(status: String = "finished", downloadState: String? = "downloading", overrides: [String:Any] = [:]) throws -> TorrentDTO {
        let url = try #require(Bundle(for:TransferFixtureBundle.self).url(forResource:"contract",withExtension:"json",subdirectory:"ios-api"))
        let root = try #require(JSONSerialization.jsonObject(with:Data(contentsOf:url)) as? [String:Any])
        var torrent = try #require(root["torrent"] as? [String:Any])
        torrent["status"] = status
        if let downloadState {
            var downloads = try #require(torrent["downloads"] as? [[String:Any]])
            downloads[0]["state"] = downloadState
            torrent["downloads"] = downloads
        } else { torrent["downloads"] = [] }
        for (key,value) in overrides { torrent[key] = value }
        return try DebridAPIClient.decoder().decode(TorrentDTO.self,from:JSONSerialization.data(withJSONObject:torrent))
    }
}

struct TransferPresentationTests {
    @Test func providerFinishedIsNotLocalCompletion() throws {
        #expect(TransferSummary(try TransferFixtures.torrent(downloadState:nil)).stage == .providerReady)
        #expect(TransferSummary(try TransferFixtures.torrent()).stage == .nasDownloading)
        #expect(TransferSummary(try TransferFixtures.torrent(downloadState:"done")).stage == .nasFinishing)
        #expect(TransferSummary(try TransferFixtures.torrent(status:"completed")).stage == .completed)
        #expect(TransferSummary(try TransferFixtures.torrent(status:"downloading")).stage == .providerDownloading)
    }
    @Test(arguments: [("pending",TransferStage.nasQueued),("unrestricting",.nasQueued),("unpacking",.unpacking),("error",.failed),("downloaded",.nasFinishing)])
    func localStages(value: (String,TransferStage)) throws {
        #expect(TransferSummary(try TransferFixtures.torrent(downloadState:value.0)).stage == value.1)
    }
    @Test func unknownStateAndUnknownSizeStayHonest() throws {
        let unknown = TransferSummary(try TransferFixtures.torrent(status:"future_state"))
        #expect(unknown.stage == .unknown && unknown.progress == nil)
        #expect(TransferSummary(try TransferFixtures.torrent(downloadState:"future_local_state")).stage == .unknown)
        let noSize = TransferSummary(try TransferFixtures.torrent(status:"downloading",overrides:["size":0,"provider_progress":0]))
        #expect(noSize.progress == nil)
        let provider = TransferSummary(try TransferFixtures.torrent(status:"downloading",overrides:["provider_progress":2,"local_progress":0.1]))
        #expect(provider.progress == 1 && provider.progressLabel == "Provider progress")
        let local = TransferSummary(try TransferFixtures.torrent(overrides:["provider_progress":1,"local_progress":0.64]))
        #expect(local.progress == 0.64 && local.progressLabel == "NAS progress")
        let negative = TransferSummary(try TransferFixtures.torrent(overrides:["local_progress":-1]))
        #expect(negative.progress == 0)
    }
    @Test func displayNameIsBoundedWithoutChangingRawData() throws {
        let raw = String(repeating:"a",count:1000)
        let summary = TransferSummary(try TransferFixtures.torrent(overrides:["name":raw]))
        #expect(summary.name.count == 256)
        #expect(summary.transfer.name == raw)
        let combining = "a" + String(repeating:"\u{0301}",count:6000)
        let bounded = TransferSummary(try TransferFixtures.torrent(overrides:["name":combining]))
        #expect(bounded.name.utf8.count <= 4096)
        #expect(bounded.transfer.name == combining)
    }
}

private actor DeferredTransfers: TransferListing {
    private var next = 0
    private var pending: [Int:CheckedContinuation<[TorrentDTO],any Error>] = [:]
    private var waiters: [Int:CheckedContinuation<Void,Never>] = [:]
    private(set) var cancelled: Set<Int> = []
    func torrents() async throws -> [TorrentDTO] {
        let request = next; next += 1
        let result = try await withCheckedThrowingContinuation { pending[request] = $0; waiters.removeValue(forKey:request)?.resume() }
        if Task.isCancelled { cancelled.insert(request); throw CancellationError() }
        return result
    }
    func waitFor(_ request: Int) async {
        if pending[request] != nil { return }
        await withCheckedContinuation { waiters[request] = $0 }
    }
    func finish(_ request: Int, result: Result<[TorrentDTO],any Error>) { pending.removeValue(forKey:request)?.resume(with:result) }
    var requests: Int { next }
}

@MainActor @Suite(.timeLimit(.minutes(1))) struct TransferListTests {
    private func fixture() throws -> (TransferListModel,DeferredTransfers) {
        let api = DeferredTransfers()
        return (TransferListModel(profile:try ServerProfile(server:ServerAddress("https://fixture.example.test")),api:api),api)
    }
    @Test func newerRefreshCancelsOldRequestAndWinsOutOfOrderCompletion() async throws {
        let (model,api) = try fixture()
        let first = Task { await model.reload() }
        await api.waitFor(0)
        let second = Task { await model.reload() }
        await api.waitFor(1)
        await api.finish(1,result:.success([try TransferFixtures.torrent(overrides:["id":"new"])]))
        await second.value
        await api.finish(0,result:.success([try TransferFixtures.torrent(overrides:["id":"old"])]))
        await first.value
        #expect(model.items.map(\.id) == ["new"])
        #expect(await api.cancelled == [0])
    }
    @Test func offlineRefreshPreservesSnapshotAndTimestamp() async throws {
        let (model,api) = try fixture()
        let first = Task { await model.reload() }; await api.waitFor(0)
        await api.finish(0,result:.success([try TransferFixtures.torrent()])); await first.value
        let date = model.updatedAt
        let second = Task { await model.reload() }; await api.waitFor(1)
        await api.finish(1,result:.failure(APIError.connection(.notConnectedToInternet))); await second.value
        #expect(model.items.count == 1 && model.updatedAt == date)
        guard case .failed(let message) = model.phase else { Issue.record("Expected offline error"); return }
        #expect(message.contains("offline"))
        #expect(!model.requiresSignIn)
    }
    @Test func malformedSnapshotCannotReplaceValidDataAnd401RequiresSignIn() async throws {
        let (model,api) = try fixture()
        let transfer = try TransferFixtures.torrent()
        let task = Task { await model.reload() }; await api.waitFor(0)
        await api.finish(0,result:.success([transfer,transfer])); await task.value
        #expect(model.items.isEmpty)
        guard case .failed = model.phase else { Issue.record("Duplicate IDs accepted"); return }
        let expired = Task { await model.reload() }; await api.waitFor(1)
        await api.finish(1,result:.failure(APIError.unauthorized)); await expired.value
        #expect(model.requiresSignIn)
    }
    @Test func invalidationAndPrestartCancellationDoNotPublishLateSnapshot() async throws {
        let (model,api) = try fixture()
        let task = Task { await model.reload() }; await api.waitFor(0)
        model.invalidate()
        await api.finish(0,result:.success([try TransferFixtures.torrent()])); await task.value
        #expect(model.items.isEmpty && model.updatedAt == nil && model.phase == .idle)
        #expect(await api.cancelled == [0])
        let cancelled = Task { await model.reload() }; cancelled.cancel(); await cancelled.value
        #expect(await api.requests == 1)
    }
    @Test func cancelledFailureCannotAskToClearSessionAndIdleCanReload() async throws {
        let (model,api) = try fixture()
        let cancelled = Task { await model.reload() }; await api.waitFor(0)
        cancelled.cancel()
        await api.finish(0,result:.failure(APIError.unauthorized)); await cancelled.value
        #expect(!model.requiresSignIn && model.phase == .idle)
        let reappeared = Task { await model.reload() }; await api.waitFor(1)
        await api.finish(1,result:.success([try TransferFixtures.torrent()])); await reappeared.value
        #expect(model.items.count == 1 && model.phase == .ready)
    }

}
