import CryptoKit
import Foundation

public struct TransferFileEntry: Identifiable, Sendable {
    public let id: String
    public let file: FileDTO
    public let downloads: [DownloadDTO]
    public let folder: String
    public let name: String
    public let pathNotice: Bool
    public let ambiguousIdentity: Bool
    fileprivate let searchText: String
    public var status: String {
        if downloads.count > 1 { return "\(downloads.count) download records" }
        if let download = downloads.first { return FileDownloadSummary(download).label }
        return file.selected ? "Not queued on NAS" : "Not selected"
    }
}
public struct TransferFileFolder: Identifiable, Sendable {
    public let id: String
    public let name: String
    public let parent: String
}
public struct FileDownloadSummary: Sendable {
    public let label: String
    public let fraction: Double?
    public let bytesDone: Int64?
    public init(_ download: DownloadDTO) {
        switch download.state {
        case "pending": label = "Queued for NAS"
        case "unrestricting": label = "Preparing download"
        case "downloading": label = "Downloading to NAS"
        case "downloaded": label = "Finalizing on NAS"
        case "unpacking": label = "Unpacking on NAS"
        case "done": label = "Complete on NAS"
        case "error": label = "Download failed"
        default: label = "Unknown download state"
        }
        bytesDone = download.size >= 0 ? (download.state == "done" ? download.size : min(download.size,max(0,download.bytesDone))) : nil
        fraction = download.state == "done" ? 1 : (download.size > 0 ? bytesDone.map { Double($0)/Double(download.size) } : nil)
    }
}

/// Virtual folders only. Provider paths never become local filesystem URLs.
public struct TransferFileIndex: Sendable {
    public let files: [TransferFileEntry]
    public let unmatchedDownloads: [DownloadDTO]
    public let folders: [String:TransferFileFolder]
    public let totalFiles: Int
    public var limited: Bool { files.count < totalFiles }
    private let children: [String:[String]]
    private let fileChildren: [String:[Int]]
    public static let fileLimit = 20_000
    public static let folderLimit = 4_096
    @concurrent public static func build(_ transfer: TorrentDTO) async throws -> TransferFileIndex {
        try Task.checkCancellation()
        return try TransferFileIndex(transfer)
    }
    public init(_ transfer: TorrentDTO) throws {
        totalFiles = transfer.files.count
        let input = Array(transfer.files.prefix(Self.fileLimit))
        // Ambiguity checks include omitted metadata, so a limit never turns duplicates into a unique match.
        let ids = Dictionary(grouping:transfer.files.indices,by:{ transfer.files[$0].id })
        let paths = Dictionary(grouping:transfer.files.indices,by:{ transfer.files[$0].path })
        let fallbackCounts = Dictionary(grouping:transfer.files,by:{ Self.fallbackKey($0) }).mapValues(\.count)
        var matches: [Int:[DownloadDTO]] = [:], unmatched: [DownloadDTO] = []
        for download in transfer.downloads {
            try Task.checkCancellation()
            guard download.torrentId == transfer.id else { unmatched.append(download); continue }
            let candidates: [Int]?
            if let id = download.fileId, !id.isEmpty { candidates = ids[id] }
            else { candidates = paths[download.path] }
            if let candidates, candidates.count == 1, let index = candidates.first, index < input.count { matches[index,default:[]].append(download) }
            else { unmatched.append(download) }
        }
        var entries: [TransferFileEntry] = [], folders: [String:TransferFileFolder] = [:]
        var children: [String:[String]] = [:], fileChildren: [String:[Int]] = [:], occurrences: [String:Int] = [:]
        for (offset,file) in input.enumerated() {
            try Task.checkCancellation()
            let pieces = file.path.utf8.count <= 8192 ? file.path.split(separator:"/",maxSplits:32,omittingEmptySubsequences:false).map(String.init) : []
            let unusual = pieces.isEmpty || pieces.count > 32 || pieces.contains(where:{ $0.isEmpty || $0 == "." || $0 == ".." }) || file.path.contains("\\")
            var folder = "", notice = unusual
            if !unusual {
                for piece in pieces.dropLast() {
                    let next = folder.isEmpty ? piece : folder+"/"+piece
                    if folders[next] == nil {
                        if folders.count >= Self.folderLimit { notice = true; folder = ""; break }
                        folders[next] = TransferFileFolder(id:next,name:Self.display(piece),parent:folder)
                        children[folder,default:[]].append(next)
                    }
                    folder = next
                }
            }
            let uniqueID = !file.id.isEmpty && ids[file.id]?.count == 1
            let identity = uniqueID ? "id:"+file.id : "path:"+Self.fallbackKey(file)
            let digest = SHA256.hash(data:Data(identity.utf8)).map { String(format:"%02x",$0) }.joined()
            let occurrence = occurrences[digest,default:0]; occurrences[digest] = occurrence+1
            let name = notice ? Self.display(file.path) : Self.display(pieces.last ?? file.path)
            entries.append(TransferFileEntry(id:digest+":\(occurrence)",file:file,downloads:matches[offset] ?? [],folder:folder,name:name,pathNotice:notice,ambiguousIdentity:!uniqueID && (fallbackCounts[Self.fallbackKey(file)] ?? 0) > 1,searchText:Self.fold(TransferSearchIndex.bounded(file.path,limit:2048)+" "+name)))
            fileChildren[folder,default:[]].append(offset)
        }
        var usedFolders: Set<String> = []
        for entry in entries {
            var current = entry.folder
            while !current.isEmpty, usedFolders.insert(current).inserted { current = folders[current]?.parent ?? "" }
        }
        for key in Array(folders.keys) where !usedFolders.contains(key) { folders.removeValue(forKey:key) }
        for key in Array(children.keys) { children[key] = children[key]?.filter { usedFolders.contains($0) } }
        for key in children.keys { children[key]?.sort { (folders[$0]?.name ?? "").localizedStandardCompare(folders[$1]?.name ?? "") == .orderedAscending } }
        for key in fileChildren.keys { fileChildren[key]?.sort { entries[$0].name.localizedStandardCompare(entries[$1].name) == .orderedAscending } }
        self.files = entries; self.folders = folders; self.children = children; self.fileChildren = fileChildren; unmatchedDownloads = unmatched
    }
    public func subfolders(of folder: String) -> [TransferFileFolder] { (children[folder] ?? []).compactMap { folders[$0] } }
    public func contents(of folder: String,query: String = "") -> [TransferFileEntry] {
        let words = Self.fold(TransferSearchIndex.bounded(query,limit:256)).split(whereSeparator: \.isWhitespace)
        if words.isEmpty { return (fileChildren[folder] ?? []).map { files[$0] } }
        return files.filter { file in
            (folder.isEmpty || file.folder == folder || file.folder.hasPrefix(folder+"/")) && words.allSatisfy { file.searchText.contains($0) }
        }
    }
    private static func fallbackKey(_ file:FileDTO) -> String { [file.id,file.path].map { "\($0.utf8.count):"+$0 }.joined() }
    public static func display(_ value: String) -> String { value.isEmpty ? "Unnamed file" : TransferSearchIndex.bounded(value,limit:256) }
    private static func fold(_ value:String) -> String { value.folding(options:[.caseInsensitive,.diacriticInsensitive,.widthInsensitive],locale:.current) }
}
