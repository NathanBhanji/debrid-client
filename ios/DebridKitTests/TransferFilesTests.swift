import Foundation
import Testing
@testable import DebridKit

enum FileFixtures {
    static func file(_ id:String,_ path:String,selected:Bool = true,size:Int64 = 100) -> [String:Any] { ["id":id,"path":path,"size":size,"selected":selected] }
    static func download(_ id:String,path:String,fileID:String? = nil,state:String = "downloading",torrent:String = "t",size:Int64 = 100,done:Int64 = 64) -> [String:Any] {
        var row: [String:Any] = ["id":id,"torrent_id":torrent,"path":path,"filename":path,"size":size,"bytes_done":done,"progress":0.64,"state":state,"retry_count":0,"queued_at":"2026-01-02T03:04:05Z"]
        if let fileID { row["file_id"] = fileID }; return row
    }
    static func torrent(_ files:[[String:Any]],_ downloads:[[String:Any]] = [],id:String = "t") throws -> TorrentDTO {
        try TransferFixtures.torrent(overrides:["id":id,"files":files,"downloads":downloads])
    }
}
struct TransferFilesTests {
    @Test func nestedFoldersDuplicateNamesAndSearchKeepFullPaths() throws {
        let index = try TransferFileIndex(FileFixtures.torrent([FileFixtures.file("a","Season 2/Episode.mkv"),FileFixtures.file("b","Season 10/Episode.mkv"),FileFixtures.file("c","Notes/Café.txt",selected:false)]))
        #expect(index.subfolders(of:"").map(\.name) == ["Notes","Season 2","Season 10"])
        #expect(index.contents(of:"Season 2").first?.file.id == "a")
        #expect(index.contents(of:"",query:"EPISODE").count == 2)
        #expect(index.contents(of:"Season 2",query:"Episode").count == 1)
        #expect(index.contents(of:"",query:"cafe").first?.status == "Not selected")
    }
    @Test func exactUniqueIDsWinButUnknownIDsNeverFallBackToPath() throws {
        let index = try TransferFileIndex(FileFixtures.torrent([FileFixtures.file("a","Video/A.mkv"),FileFixtures.file("b","Video/B.mkv")],[FileFixtures.download("d1",path:"renamed.mkv",fileID:"a"),FileFixtures.download("d2",path:"Video/B.mkv",fileID:"unknown"),FileFixtures.download("d3",path:"Video/B.mkv",fileID:"b",torrent:"other")]))
        #expect(index.files[0].downloads.map(\.id) == ["d1"])
        #expect(index.files[1].downloads.isEmpty)
        #expect(index.unmatchedDownloads.map(\.id) == ["d2","d3"])
    }
    @Test func pathFallbackRequiresExactUniqueMetadataAndNoID() throws {
        let index = try TransferFileIndex(FileFixtures.torrent([FileFixtures.file("a","A/movie.mkv"),FileFixtures.file("b","B/movie.mkv"),FileFixtures.file("dup1","same"),FileFixtures.file("dup2","same")],[FileFixtures.download("exact",path:"A/movie.mkv"),FileFixtures.download("basename",path:"movie.mkv"),FileFixtures.download("ambiguous",path:"same")]))
        #expect(index.files[0].downloads.map(\.id) == ["exact"])
        #expect(index.unmatchedDownloads.count == 2)
    }
    @Test func duplicateIDsAndRepeatedDownloadRecordsAreExplicit() throws {
        let index = try TransferFileIndex(FileFixtures.torrent([FileFixtures.file("dup","a"),FileFixtures.file("dup","b"),FileFixtures.file("c","c")],[FileFixtures.download("ambiguous",path:"a",fileID:"dup"),FileFixtures.download("first",path:"c",fileID:"c"),FileFixtures.download("second",path:"c",fileID:"c",state:"error")]))
        #expect(index.files[0].downloads.isEmpty && index.unmatchedDownloads.count == 1)
        #expect(index.files[2].downloads.count == 2 && index.files[2].status == "2 download records")
        let duplicate = try TransferFileIndex(FileFixtures.torrent([FileFixtures.file("dup","same"),FileFixtures.file("dup","same")]))
        #expect(Set(duplicate.files.map(\.id)).count == 2)
        #expect(duplicate.files.allSatisfy { $0.ambiguousIdentity })
    }
    @Test func identitySurvivesReorderingSizeSelectionAndPathChanges() throws {
        let initial = try TransferFileIndex(FileFixtures.torrent([FileFixtures.file("a","old",selected:false,size:0),FileFixtures.file("b","b")]))
        let updated = try TransferFileIndex(FileFixtures.torrent([FileFixtures.file("b","b"),FileFixtures.file("a","renamed",selected:true,size:100)]))
        #expect(initial.files[0].id == updated.files[1].id)
        #expect(updated.files[1].file.selected && updated.files[1].file.size == 100)
    }
    @Test func hostileDeepAndOversizedPathsRemainVirtualFlatEntries() throws {
        let longPath = String(repeating:String(repeating:"x",count:100)+"/",count:25)+"needle.mkv"
        let searchable = try TransferFileIndex(FileFixtures.torrent([FileFixtures.file("long",longPath)]))
        #expect(searchable.contents(of:"",query:"needle").count == 1)
        for path in ["../private/file","/absolute/file","a\\b",String(repeating:"x/",count:40)+"file",String(repeating:"/",count:20_000)] {
            let index = try TransferFileIndex(FileFixtures.torrent([FileFixtures.file("a",path)]))
            #expect(index.folders.isEmpty && index.files[0].folder.isEmpty && index.files[0].pathNotice)
            #expect(index.files[0].file.path == path && index.files[0].name.unicodeScalars.count <= 256)
        }
    }
    @Test func largeListsAndFolderBudgetRemainBoundedAndHonest() throws {
        let index = try TransferFileIndex(FileFixtures.torrent((0..<2_000).map { FileFixtures.file("f\($0)","Season/Clip \($0).mkv") }))
        #expect(index.files.count == 2_000 && index.contents(of:"Season").count == 2_000 && !index.limited)
        let capped = try TransferFileIndex(FileFixtures.torrent((0...TransferFileIndex.fileLimit).map { FileFixtures.file("f\($0)","file\($0)") }))
        #expect(capped.limited && capped.totalFiles == TransferFileIndex.fileLimit+1 && capped.files.count == TransferFileIndex.fileLimit)
        let folders = try TransferFileIndex(FileFixtures.torrent((0...TransferFileIndex.folderLimit).map { FileFixtures.file("f\($0)","Folder\($0)/file") }))
        #expect(folders.folders.count == TransferFileIndex.folderLimit)
        #expect(folders.files.last?.pathNotice == true && folders.files.last?.folder == "")
    }
    @Test func folderBudgetLeavesNoEmptyPhantomBranch() throws {
        var files = (0..<(TransferFileIndex.folderLimit-1)).map { FileFixtures.file("f\($0)","Folder\($0)/file") }
        files.append(FileFixtures.file("deep","would-be-empty/nested/file"))
        let index = try TransferFileIndex(FileFixtures.torrent(files))
        #expect(index.folders["would-be-empty"] == nil)
        #expect(index.files.last?.folder == "" && index.files.last?.pathNotice == true)
    }
    @Test func independentFileStatesClampBytesAndHideRawErrors() throws {
        var failed = FileFixtures.download("b",path:"b",state:"error",done:200)
        failed["error"] = "https://user:password@example.test/file?token=private"
        let transfer = try FileFixtures.torrent([], [FileFixtures.download("a",path:"a",state:"done",done:0),failed,FileFixtures.download("c",path:"c",state:"future",size:0),FileFixtures.download("d",path:"d",done:-10)])
        let summaries = transfer.downloads.map(FileDownloadSummary.init)
        #expect(summaries[0].fraction == 1 && summaries[0].bytesDone == 100)
        #expect(summaries[1].label == "Download failed" && summaries[1].fraction == 1)
        #expect(summaries[2].label == "Unknown download state" && summaries[2].fraction == nil)
        #expect(summaries[3].bytesDone == 0)
    }
}
private actor DeferredFileIndex {
    private var pending: CheckedContinuation<TransferFileIndex,any Error>?
    private var waiter: CheckedContinuation<Void,Never>?
    func build(_ transfer:TorrentDTO) async throws -> TransferFileIndex { try await withCheckedThrowingContinuation { pending = $0; waiter?.resume(); waiter = nil } }
    func waitForBuild() async { if pending != nil { return }; await withCheckedContinuation { waiter = $0 } }
    func finish(_ index:TransferFileIndex) { pending?.resume(returning:index); pending = nil }
}
@MainActor @Suite(.timeLimit(.minutes(1))) struct FileBrowserModelTests {
    @Test func preCancelledUpdateCannotInvalidateCurrentBuild() async throws {
        let gate = DeferredFileIndex(), transfer = try FileFixtures.torrent([FileFixtures.file("a","a")])
        let model = TransferFileBrowserModel { try await gate.build($0) }
        let first = Task { await model.update(transfer:transfer,updatedAt:Date(timeIntervalSince1970:1)) }
        await gate.waitForBuild()
        let obsolete = Task { await model.update(transfer:transfer,updatedAt:Date(timeIntervalSince1970:2)) }; obsolete.cancel(); await obsolete.value
        await gate.finish(try TransferFileIndex(transfer)); await first.value
        #expect(model.index?.files.first?.file.id == "a")
    }
    @Test func removalRejectsDelayedBuildAndCacheIsTransferBound() async throws {
        let gate = DeferredFileIndex(), transfer = try FileFixtures.torrent([FileFixtures.file("a","a")])
        let model = TransferFileBrowserModel { try await gate.build($0) }
        let task = Task { await model.update(transfer:transfer,updatedAt:Date()) }; await gate.waitForBuild()
        await model.update(transfer:nil,updatedAt:Date()); await gate.finish(try TransferFileIndex(transfer)); await task.value
        #expect(model.index == nil)
        let live = TransferFileBrowserModel(), date = Date()
        await live.update(transfer:transfer,updatedAt:date)
        await live.update(transfer:try FileFixtures.torrent([FileFixtures.file("b","b")],id:"other"),updatedAt:date)
        #expect(live.index?.files.first?.file.id == "b")
    }
}
