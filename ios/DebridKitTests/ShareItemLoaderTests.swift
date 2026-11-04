import Foundation
import Testing
import UniformTypeIdentifiers
@testable import DebridKit

@MainActor @Suite(.timeLimit(.minutes(1))) struct ShareItemLoaderTests {
    private let raw="magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"&dn=Original%2bEncoding"
    @Test func textProviderPreservesURIAndRejectsWebPage() async throws {
        let provider=NSItemProvider(item:raw as NSString,typeIdentifier:UTType.plainText.identifier)
        let drafts=try await ShareItemLoader.load([provider])
        guard case .magnet(let magnet)=try #require(drafts.first) else {Issue.record("not a magnet");return}
        #expect(magnet.rawURI==raw)
        let webpage=NSItemProvider(item:URL(string:"https://example.test")! as NSURL,typeIdentifier:UTType.url.identifier)
        await #expect(throws:SharedDraftError.unsupported) {_=try await ShareItemLoader.load([webpage])}
    }
    @Test func attachmentFreeTextPreservesEncodingAndRejectsAnEmptyShare() async throws {
        let drafts=try await ShareItemLoader.load([],fallbackText:[raw])
        guard case .magnet(let link)=try #require(drafts.first) else {Issue.record("lost fallback text");return}
        #expect(link.rawURI==raw)
        await #expect(throws:SharedDraftError.unsupported) {_=try await ShareItemLoader.load([])}
    }
    @Test func URLRepresentationWinsOverPlainCaption() async throws {
        let provider=NSItemProvider()
        let original=raw
        provider.registerItem(forTypeIdentifier:UTType.url.identifier,loadHandler:{completion,_,_ in completion?(URL(string:original)! as NSURL,nil)})
        provider.registerItem(forTypeIdentifier:UTType.plainText.identifier,loadHandler:{completion,_,_ in completion?("A caption, not a magnet" as NSString,nil)})
        let drafts=try await ShareItemLoader.load([provider])
        guard case .magnet(let link)=try #require(drafts.first) else {Issue.record("lost magnet");return}
        #expect(link.rawURI==original)
    }
    @Test func temporaryTorrentIsCopiedBeforeProviderCompletionReturns() async throws {
        let url=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString+".torrent")
        let data=Data("d4:infod6:lengthi3e4:name5:a.mkv12:piece lengthi16384e6:pieces20:00000000000000000000ee".utf8)
        try data.write(to:url);defer {try? FileManager.default.removeItem(at:url)}
        let provider=NSItemProvider()
        provider.registerFileRepresentation(forTypeIdentifier:"org.bittorrent.torrent",fileOptions:[],visibility:.all) {completion in
            completion(url,false,nil)
            return nil
        }
        let drafts=try await ShareItemLoader.load([provider])
        try FileManager.default.removeItem(at:url)
        guard case .torrent(let file)=try #require(drafts.first) else {Issue.record("not a torrent");return}
        #expect(file.data==data)
    }
    @Test func cancellationResumesHeldProviderAndLateGateResultsAreIgnored() async throws {
        let provider=NSItemProvider()
        provider.registerItem(forTypeIdentifier:UTType.plainText.identifier,loadHandler:{_,_,_ in })
        let task=Task {try await ShareItemLoader.load([provider])}
        try await Task.sleep(for:.milliseconds(30));task.cancel()
        await #expect(throws:CancellationError.self) {_=try await task.value}
        let gate=ShareLoadGate<String>()
        gate.finish(.failure(CancellationError()))
        do {
            let _:String=try await withCheckedThrowingContinuation {continuation in
                gate.install(continuation)
                gate.finish(.success("late callback"))
                gate.finish(.success("duplicate callback"))
            }
            Issue.record("cancelled result was replaced")
        } catch {#expect(error is CancellationError)}
        let progress=Progress(totalUnitCount:1);gate.setProgress(progress)
        #expect(progress.isCancelled && gate.isFinished)
    }
    @Test func unsupportedAndCancelledProviderCannotProduceDrafts() async throws {
        await #expect(throws:SharedDraftError.unsupported) {_=try await ShareItemLoader.load([NSItemProvider()])}
        let provider=NSItemProvider(item:raw as NSString,typeIdentifier:UTType.plainText.identifier)
        let task=Task {try await ShareItemLoader.load([provider])};task.cancel()
        await #expect(throws:CancellationError.self) {_=try await task.value}
    }
}
