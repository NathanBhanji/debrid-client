import Foundation
import Testing
@testable import DebridKit

@MainActor @Suite(.timeLimit(.minutes(1))) struct SharedDraftTests {
    private func link(_ number:Int) throws -> MagnetLink {try MagnetLink("magnet:?xt=urn:btih:"+String(format:"%040x",number)+"&dn=Fixture%2bOriginal")}
    @Test func saveIsExplicitAndPreservesOriginalEncodingWithoutDestination() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer {try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory),magnet=try link(1)
        let model=try SharedDraftModel(drafts:[.magnet(magnet),.magnet(magnet)],store:store)
        #expect(try await store.snapshot().isEmpty && model.drafts.count==1)
        await model.save();await model.save()
        let saved=try #require(try await store.snapshot().first)
        #expect(saved.magnet?.rawURI==magnet.rawURI && saved.source == .share && saved.targetServerURL==nil)
        #expect(saved.submission==nil && model.savedCount==1 && model.duplicateCount==0 && model.phase == .saved)
    }
    @Test func sameHashWithDifferentTrackersRemainsDistinct() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer {try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory),first=try link(1)
        let second=try MagnetLink(first.rawURI+"&tr=https%3a%2f%2ftracker.example.test%2fprivate")
        let model=try SharedDraftModel(drafts:[.magnet(first),.magnet(second)],store:store)
        #expect(model.drafts.count==2)
        await model.save();#expect(model.savedCount==2)
        #expect(try await store.snapshot().count==2)
    }
    @Test func partialSaveRetriesOnlyUncommittedItems() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer {try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory)
        var old:[UUID]=[]
        for number in 1...31 {old.append(try await store.enqueue(link(number),source:.share,target:nil).id)}
        let model=try SharedDraftModel(drafts:[.magnet(link(40)),.magnet(link(41)),.magnet(link(42))],store:store)
        await model.save();#expect(model.phase == .partial && model.savedCount==1)
        try await store.discard(old[0],token:nil);try await store.discard(old[1],token:nil)
        await model.save();#expect(model.phase == .saved && model.savedCount==3 && model.duplicateCount==0)
        #expect(try await store.snapshot().count==32)
    }
    @Test func cancelledSaveAndInvalidBatchHaveNoWrites() async throws {
        let directory=FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer {try? FileManager.default.removeItem(at:directory)}
        let store=try PendingInbox(directory:directory),model=try SharedDraftModel(drafts:[.magnet(link(1))],store:store)
        let task=Task {await model.save()};task.cancel();await task.value
        #expect(try await store.snapshot().isEmpty)
        await #expect(throws:MagnetError.invalidScheme) {_=try await SharedDraft.text("magnet:?xt=urn:btih:"+String(repeating:"0",count:40)+"\nhttps://example.test")}
        #expect(try await store.snapshot().isEmpty)
    }
}
