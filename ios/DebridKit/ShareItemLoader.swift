import Foundation
import Synchronization
import UniformTypeIdentifiers

final class ShareLoadGate<Value:Sendable>:Sendable {
    private struct State {var continuation:CheckedContinuation<Value,any Error>?;var result:Result<Value,any Error>?;var progress:Progress?}
    private let state=Mutex(State())
    var isFinished:Bool {state.withLock {$0.result != nil}}
    func install(_ continuation:CheckedContinuation<Value,any Error>) {
        let result=state.withLock {value -> Result<Value,any Error>? in
            if let result=value.result {return result};value.continuation=continuation;return nil
        }
        if let result {continuation.resume(with:result)}
    }
    func setProgress(_ progress:Progress) {
        let done=state.withLock {value in value.progress=progress;return value.result != nil}
        if done {progress.cancel()}
    }
    func finish(_ result:Result<Value,any Error>) {
        let pending=state.withLock {value -> (CheckedContinuation<Value,any Error>?,Progress?)? in
            guard value.result==nil else {return nil}
            value.result=result;let pending=(value.continuation,value.progress);value.continuation=nil;value.progress=nil;return pending
        }
        guard let pending else {return};pending.1?.cancel();pending.0?.resume(with:result)
    }
}

@MainActor public enum ShareItemLoader {
    public static func load(_ providers:[NSItemProvider],fallbackText:[String] = []) async throws -> [SharedDraft] {
        guard providers.count<=16,fallbackText.count<=16 else {throw SharedDraftError.tooMany}
        if providers.isEmpty {
            guard !fallbackText.isEmpty else {throw SharedDraftError.unsupported}
            guard fallbackText.reduce(0,{$0+$1.utf8.count})<=PendingInbox.uriByteLimit else {throw SharedDraftError.tooLarge}
            var result:[SharedDraft]=[]
            for text in fallbackText {result += try await SharedDraft.text(text);guard result.count<=16 else {throw SharedDraftError.tooMany}}
            return result
        }
        var drafts:[SharedDraft]=[],uriBytes=0,torrentBytes=0
        let deadline=ContinuousClock.now.advanced(by:.seconds(30))
        func remaining() throws -> Duration {
            let duration=ContinuousClock.now.duration(to:deadline)
            guard duration > .zero else {throw SharedDraftError.timedOut}
            return min(.seconds(20),duration)
        }
        for provider in providers {
            try Task.checkCancellation()
            let incoming:[SharedDraft]
            if provider.hasItemConformingToTypeIdentifier("org.bittorrent.torrent") || provider.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) {
                let file=try await file(provider,timeout:remaining(),maximumBytes:PendingInbox.payloadByteLimit-torrentBytes)
                incoming=[.torrent(file)]
            } else if provider.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
                let value=try await text(provider,type:UTType.url.identifier,timeout:remaining())
                if let magnet=try? MagnetLink(value) {incoming=[.magnet(magnet)]}
                else if provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
                    incoming=try await SharedDraft.text(try await text(provider,type:UTType.plainText.identifier,timeout:remaining()))
                } else {throw SharedDraftError.unsupported}
            } else if provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
                incoming=try await SharedDraft.text(try await text(provider,type:UTType.plainText.identifier,timeout:remaining()))
            } else {throw SharedDraftError.unsupported}
            for draft in incoming {switch draft {case .magnet:uriBytes+=draft.bytes;case .torrent:torrentBytes+=draft.bytes}}
            guard uriBytes<=PendingInbox.uriByteLimit,torrentBytes<=PendingInbox.payloadByteLimit else {throw SharedDraftError.tooLarge}
            guard drafts.count+incoming.count<=16 else {throw SharedDraftError.tooMany}
            drafts += incoming
        }
        return drafts
    }
    private static func text(_ provider:NSItemProvider,type:String,timeout:Duration) async throws -> String {
        let gate=ShareLoadGate<String>()
        let timer=Task {try? await Task.sleep(for:timeout);if !Task.isCancelled {gate.finish(.failure(SharedDraftError.timedOut))}}
        defer {timer.cancel()}
        return try await withTaskCancellationHandler {
            try Task.checkCancellation()
            return try await withCheckedThrowingContinuation {continuation in
                gate.install(continuation)
                provider.loadItem(forTypeIdentifier:type,options:nil) {item,error in
                    guard !gate.isFinished else {return}
                    guard error==nil else {gate.finish(.failure(SharedDraftError.providerUnavailable));return}
                    let text:String?
                    if let url=item as? URL {text=url.isFileURL ? nil : url.absoluteString}
                    else if let value=item as? String {text=value}
                    else if let data=item as? Data,data.count<=PendingInbox.uriByteLimit {text=String(data:data,encoding:.utf8)}
                    else {text=nil}
                    guard let text,text.utf8.count<=PendingInbox.uriByteLimit else {gate.finish(.failure(SharedDraftError.unsupported));return}
                    gate.finish(.success(text))
                }
            }
        } onCancel: {gate.finish(.failure(CancellationError()))}
    }
    private static func file(_ provider:NSItemProvider,timeout:Duration,maximumBytes:Int) async throws -> TorrentFile {
        let gate=ShareLoadGate<TorrentFile>()
        let timer=Task {try? await Task.sleep(for:timeout);if !Task.isCancelled {gate.finish(.failure(SharedDraftError.timedOut))}}
        defer {timer.cancel()}
        return try await withTaskCancellationHandler {
            try Task.checkCancellation()
            return try await withCheckedThrowingContinuation {continuation in
                gate.install(continuation)
                if provider.hasItemConformingToTypeIdentifier("org.bittorrent.torrent") {
                    let progress=provider.loadFileRepresentation(forTypeIdentifier:"org.bittorrent.torrent") {url,error in
                        guard !gate.isFinished else {return}
                        guard error==nil,let url else {gate.finish(.failure(SharedDraftError.providerUnavailable));return}
                        gate.finish(Result {try TorrentDocument.copyProvidedFile(url,requireTorrentExtension:false,maximumBytes:min(TorrentFile.byteLimit,maximumBytes))})
                    }
                    gate.setProgress(progress)
                } else {
                    provider.loadItem(forTypeIdentifier:UTType.fileURL.identifier,options:nil) {item,error in
                        guard !gate.isFinished else {return}
                        guard error==nil,let url=item as? URL else {gate.finish(.failure(SharedDraftError.providerUnavailable));return}
                        gate.finish(Result {try TorrentDocument.copyProvidedFile(url,maximumBytes:min(TorrentFile.byteLimit,maximumBytes))})
                    }
                }
            }
        } onCancel: {gate.finish(.failure(CancellationError()))}
    }
}
