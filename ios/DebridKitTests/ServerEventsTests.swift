import Foundation
import Observation
import Synchronization
import Testing
@testable import DebridKit

struct SSEParserTests {
    private func parse(_ bytes: [UInt8], limit: Int = 65536) throws -> [SSEFrame] {
        var parser = SSEParser(maximumFrameBytes:limit), frames: [SSEFrame] = []
        for byte in bytes { if let frame = try parser.consume(byte) { frames.append(frame) } }
        return frames
    }
    @Test func fragmentedUnicodeBOMCommentsAndMixedLineEndings() throws {
        let raw = "\u{feff}: hello\r\nevent: torrent.updated\rdata: café\r\ndata: 🌊\n\r\nevent: heartbeat\ndata: {}\n\n"
        let frames = try parse(Array(raw.utf8))
        #expect(frames == [SSEFrame(event:"torrent.updated",data:"café\n🌊"),SSEFrame(event:"heartbeat",data:"{}")])
    }
    @Test func emptyDataUnknownFieldsAndEOFAreFramedSafely() throws {
        #expect(try parse(Array("event: ignored\n\ndata:\n\nid: ignored\nretry: 1\ndata: ok\n\ndata: incomplete".utf8)) == [SSEFrame(event:"message",data:""),SSEFrame(event:"message",data:"ok")])
        #expect(try parse(Array("data: incomplete\n".utf8)).isEmpty)
    }
    @Test func boundsLongLinesAndAccumulatedFramesButNotSeparateComments() throws {
        #expect(throws:APIError.responseTooLarge) { try parse(Array(("data: "+String(repeating:"x",count:100)).utf8),limit:32) }
        #expect(throws:APIError.responseTooLarge) { try parse(Array(String(repeating:"data: x\n",count:20).utf8),limit:32) }
        #expect(try parse(Array((String(repeating:": heartbeat\n",count:1000)+"data: ok\n\n").utf8),limit:32).count == 1)
        #expect(throws:APIError.invalidResponse) { try parse([100,97,116,97,58,32,0xff,10]) }
    }
    @Test func retryBackoffIsBoundedJitteredAndHonorsServerDelay() {
        #expect(SSEReconnectPolicy.delay(failures:1,jitter:1) == .seconds(1))
        #expect(SSEReconnectPolicy.delay(failures:3,jitter:1) == .seconds(4))
        #expect(SSEReconnectPolicy.delay(failures:Int.max,jitter:1.2) == .seconds(30))
        #expect(SSEReconnectPolicy.delay(failures:1,jitter:0) == .milliseconds(800))
        #expect(SSEReconnectPolicy.delay(failures:1,jitter:.nan,retryAfter:120) == .seconds(120))
    }
}

private actor ManualLiveClock {
    struct Sleep: Sendable { let id: UUID; let duration: Duration }
    private var pending: [UUID:CheckedContinuation<Void, any Error>] = [:]
    private var history: [Sleep] = []
    private var readers: [(Duration,CheckedContinuation<Sleep,Never>)] = []
    func sleep(_ duration: Duration) async throws {
        try Task.checkCancellation()
        let id = UUID()
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                pending[id] = continuation
                let request = Sleep(id:id,duration:duration)
                if let index = readers.firstIndex(where: { $0.0 == duration }) { readers.remove(at:index).1.resume(returning:request) }
                else { history.append(request) }
            }
        } onCancel: { Task { await self.cancel(id) } }
    }
    private func cancel(_ id: UUID) { pending.removeValue(forKey:id)?.resume(throwing:CancellationError()) }
    func next(_ duration: Duration) async -> Sleep {
        if let index = history.firstIndex(where: { $0.duration == duration }) { return history.remove(at:index) }
        return await withCheckedContinuation { readers.append((duration,$0)) }
    }
    func release(_ request: Sleep) { pending.removeValue(forKey:request.id)?.resume() }
}
private final class EventFixture: ServerEventStreaming, Sendable {
    struct State: Sendable { var streams: [AsyncThrowingStream<ServerEvent,any Error>.Continuation] = []; var terminated = 0 }
    let state = Synchronization.Mutex(State())
    let connections = AsyncStream<Int>.makeStream()
    func events() -> AsyncThrowingStream<ServerEvent,any Error> {
        let (stream,continuation) = AsyncThrowingStream<ServerEvent,any Error>.makeStream(bufferingPolicy:.bufferingNewest(16))
        let index = state.withLock { let index = $0.streams.count; $0.streams.append(continuation); return index }
        continuation.onTermination = { [weak self] _ in self?.state.withLock { $0.terminated += 1 } }
        connections.continuation.yield(index)
        return stream
    }
    func send(_ event: ServerEvent, on index: Int = 0) { state.withLock { $0.streams[index] }.yield(event) }
    func fail(_ error: any Error, on index: Int = 0) { state.withLock { $0.streams[index] }.finish(throwing:error) }
    func finish(on index: Int = 0) { state.withLock { $0.streams[index] }.finish() }
}
private actor LiveListingFixture: TransferListing {
    private(set) var calls = 0
    private var held: CheckedContinuation<[TorrentDTO],Never>?
    private var starts = AsyncStream<Int>.makeStream()
    var requests: AsyncStream<Int> { starts.stream }
    var hold = false
    func setHold(_ value: Bool) { hold = value }
    func torrents() async throws -> [TorrentDTO] {
        calls += 1; starts.continuation.yield(calls)
        if hold { return await withCheckedContinuation { held = $0 } }
        return [try TransferFixtures.torrent()]
    }
    func release() { held?.resume(returning:[]); held = nil }
}

@MainActor @Suite(.timeLimit(.minutes(1))) struct LiveReconciliationTests {
    private func fixture() throws -> (TransferListModel,LiveListingFixture,EventFixture,ManualLiveClock) {
        let api = LiveListingFixture(), events = EventFixture(), clock = ManualLiveClock()
        var timing = LiveUpdateTiming(); timing.sleep = { try await clock.sleep($0) }; timing.jitter = { 1 }
        let model = TransferListModel(profile:try ServerProfile(server:ServerAddress("https://fixture.example.test")),api:api,events:events,timing:timing)
        return (model,api,events,clock)
    }
    private func next(_ duration: Duration, clock: ManualLiveClock) async -> ManualLiveClock.Sleep {
        await clock.next(duration)
    }
    @Test func repeatedActivationUsesOneStreamAndResumeReconciles() async throws {
        let (model,api,events,clock) = try fixture()
        defer { model.stopLiveUpdates() }
        var connections = events.connections.stream.makeAsyncIterator()
        model.startLiveUpdates(); model.startLiveUpdates()
        #expect(await connections.next() == 0)
        let initial = await next(.zero,clock:clock)
        var requests = await api.requests.makeAsyncIterator()
        await clock.release(initial); #expect(await requests.next() == 1)
        model.stopLiveUpdates(); #expect(!model.isLiveActive && model.liveStatus == .paused)
        model.startLiveUpdates(); #expect(await connections.next() == 1)
        let resumed = await next(.zero,clock:clock)
        await clock.release(resumed); #expect(await requests.next() == 2)
        #expect(events.state.withLock { $0.streams.count } == 2)
    }
    @Test func burstDoesNotCancelInflightLoadAndReconcilesOnceAfterIt() async throws {
        let (model,api,events,clock) = try fixture()
        defer { model.stopLiveUpdates() }
        await api.setHold(true)
        var connections = events.connections.stream.makeAsyncIterator()
        var requests = await api.requests.makeAsyncIterator()
        model.startLiveUpdates(); _ = await connections.next()
        await clock.release(await next(.zero,clock:clock)); _ = await requests.next()
        for _ in 0..<40 { events.send(.changed("download.updated")) }
        // A connection boundary signals receipt of the queued hints before releasing the held snapshot.
        events.send(.connected)
        await api.setHold(false); await api.release()
        let refresh = await next(.milliseconds(350),clock:clock)
        #expect(await api.calls == 1)
        await clock.release(refresh); #expect(await requests.next() == 2)
        model.stopLiveUpdates()
        let calls = await api.calls
        await model.refreshWhileActive()
        #expect(await api.calls == calls)
    }
    @Test func reconnectBackoffResyncAndLateOldEventsAreIsolated() async throws {
        let (model,api,events,clock) = try fixture()
        defer { model.stopLiveUpdates() }
        var connections = events.connections.stream.makeAsyncIterator()
        model.startLiveUpdates(); _ = await connections.next()
        events.fail(APIError.connection(.networkConnectionLost))
        let delay = await next(.seconds(1),clock:clock)
        #expect(model.liveStatus == .reconnecting)
        await clock.release(delay); #expect(await connections.next() == 1)
        events.send(.connected,on:1)
        let initial = await next(.zero,clock:clock); await clock.release(initial)
        var requests = await api.requests.makeAsyncIterator(); _ = await requests.next()
        model.stopLiveUpdates(); events.send(.changed("torrent.deleted"),on:0)
        #expect(model.liveStatus == .paused && !model.requiresSignIn)
        let count = await api.calls
        await model.refreshWhileActive(); #expect(await api.calls == count)
    }
    @Test func unsupportedStreamKeepsPeriodicFallbackAndUnauthorizedStops() async throws {
        let (model,_,events,clock) = try fixture()
        defer { model.stopLiveUpdates() }
        var connections = events.connections.stream.makeAsyncIterator()
        model.startLiveUpdates(); _ = await connections.next()
        events.fail(APIError.http(status:404))
        let periodic = await next(.seconds(45),clock:clock)
        await clock.release(periodic)
        let initial = await next(.zero,clock:clock); await clock.release(initial)
        model.stopLiveUpdates()
        model.startLiveUpdates(); _ = await connections.next()
        await withCheckedContinuation { continuation in
            withObservationTracking { _ = model.requiresSignIn } onChange: { continuation.resume() }
            events.fail(APIError.unauthorized,on:1)
        }
        #expect(model.requiresSignIn && !model.isLiveActive)
    }
}
