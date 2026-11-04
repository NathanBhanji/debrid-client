import Foundation

public struct FileSelectionDTO:Decodable,Sendable {
    public let torrentId:String
    public let state:String
    public let revision:String?
    public let files:[FileDTO]
    public let selectedIds:[String]
    public let providerSelects:Bool
    public let message:String
}
public protocol FileSelectionAPI:Sendable {
    var serverURL:URL {get}
    var isPreview:Bool {get}
    func health() async throws -> HealthDTO
    func fileSelection(id:String) async throws -> FileSelectionDTO
    func confirmFileSelection(id:String,revision:String,fileIDs:[String]) async throws -> FileSelectionDTO
}
extension FileSelectionAPI {public var isPreview:Bool {false}}
extension DebridAPIClient:FileSelectionAPI {
    public func fileSelection(id:String) async throws -> FileSelectionDTO {
        guard SubmissionIdentity.validID(id) else {throw FileSelectionError.invalidMetadata}
        return try await send(["torrents",id,"file-selection"],method:"GET",expectedStatus:200)
    }
    @concurrent public func confirmFileSelection(id:String,revision:String,fileIDs:[String]) async throws -> FileSelectionDTO {
        guard SubmissionIdentity.validID(id),FileSelectionIdentity.revisionIsValid(revision) else {throw FileSelectionError.invalidMetadata}
        let ids=try FileSelectionIdentity.canonicalIDs(fileIDs)
        struct Body:Encodable {
            let revision:String;let fileIDs:[String]
            enum CodingKeys:String,CodingKey {case revision;case fileIDs="file_ids"}
        }
        return try await send(["torrents",id,"file-selection"],method:"PUT",body:JSONEncoder().encode(Body(revision:revision,fileIDs:ids)),expectedStatus:200)
    }
}

/// A fixed review snapshot, never a reconstruction of provider identifiers.
public struct FileSelectionReview:Sendable {
    public struct File:Identifiable,Sendable {
        public let id:Data
        public let providerID:String
        public let name:String
        public let path:String
        public let size:Int64
        public let folder:Data
        let search:String
    }
    public struct Folder:Identifiable,Sendable {
        public let id:Data
        public let name:String
        public let parent:Data
    }
    public let response:FileSelectionDTO
    public let files:[File]
    public let folders:[Data:Folder]
    public let filesByID:[Data:File]
    private let children:[Data:[Data]]
    private let fileChildren:[Data:[Data]]
    @concurrent public static func build(_ response:FileSelectionDTO,transferID:String) async throws -> Self {
        try Task.checkCancellation()
        guard response.torrentId==transferID,response.files.count<=FileSelectionIdentity.fileLimit,response.message.utf8.count<=8192,["automatic","pending","ready","accepted","unavailable"].contains(response.state) else {throw FileSelectionError.invalidMetadata}
        if response.state=="ready" || response.state=="accepted" {guard let revision=response.revision,FileSelectionIdentity.revisionIsValid(revision) else {throw FileSelectionError.invalidMetadata}}
        if response.state=="ready" {guard !response.files.isEmpty,response.selectedIds.isEmpty else {throw FileSelectionError.invalidMetadata}}
        var seen=Set<Data>(),bytes=0,files:[File]=[],folders:[Data:Folder]=[:],children:[Data:[Data]]=[:],fileChildren:[Data:[Data]]=[:]
        let root=Data()
        for raw in response.files {
            try Task.checkCancellation()
            let id=Data(raw.id.utf8)
            bytes+=id.count+raw.path.utf8.count
            guard !id.isEmpty,id.count<=8192,seen.insert(id).inserted,raw.path.utf8.count<=8192,raw.size>=0,bytes<=2*1024*1024 else {throw FileSelectionError.invalidMetadata}
            let pieces=raw.path.split(separator:"/",maxSplits:32,omittingEmptySubsequences:false)
            let safeFolders=pieces.count<=32 && !pieces.contains(where:{$0.isEmpty || $0=="." || $0==".."}) && !raw.path.contains("\\")
            var parent=root,prefix=""
            if safeFolders {
                for piece in pieces.dropLast() {
                    prefix=prefix.isEmpty ? String(piece) : prefix+"/"+piece
                    let folderID=Data(prefix.utf8)
                    if folders[folderID]==nil {
                        guard folders.count<4096 else {throw FileSelectionError.tooLarge}
                        folders[folderID]=Folder(id:folderID,name:display(String(piece)).isEmpty ? "Unnamed folder" : display(String(piece)),parent:parent);children[parent,default:[]].append(folderID)
                    }
                    parent=folderID
                }
            }
            let name=display(String(pieces.last ?? ""))
            let file=File(id:id,providerID:raw.id,name:name.isEmpty ? "Unnamed file" : name,path:display(raw.path),size:raw.size,folder:parent,search:raw.path.folding(options:[.caseInsensitive,.diacriticInsensitive],locale:Locale(identifier:"en_US_POSIX")))
            files.append(file);fileChildren[parent,default:[]].append(id)
        }
        if response.state=="accepted" { _=try FileSelectionIdentity.canonicalIDs(response.selectedIds,maximumBytes:2*1024*1024) }
        files.sort {$0.path.localizedStandardCompare($1.path) == .orderedAscending}
        fileChildren=[:]
        for file in files {fileChildren[file.folder,default:[]].append(file.id)}
        for key in Array(children.keys) {children[key]?.sort {(folders[$0]?.name ?? "").localizedStandardCompare(folders[$1]?.name ?? "") == .orderedAscending}}
        try Task.checkCancellation()
        let byID=Dictionary(uniqueKeysWithValues:files.map {($0.id,$0)})
        return Self(response:response,files:files,folders:folders,filesByID:byID,children:children,fileChildren:fileChildren)
    }
    public func subfolders(in folder:Data)->[Folder] {
        (children[folder] ?? []).compactMap {folders[$0]}
    }
    public func descendants(in folder:Data)->[File] {
        if folder.isEmpty {return files}
        var prefix=folder;prefix.append(47)
        return files.filter {$0.folder==folder || $0.folder.starts(with:prefix)}
    }
    public func contents(in folder:Data)->[File] {(fileChildren[folder] ?? []).compactMap {filesByID[$0]}}
    @concurrent public func search(_ query:String,in folder:Data) async throws -> [File] {
        let needle=String(query.unicodeScalars.prefix(256)).trimmingCharacters(in:.whitespacesAndNewlines).folding(options:[.caseInsensitive,.diacriticInsensitive],locale:Locale(identifier:"en_US_POSIX"))
        guard !needle.isEmpty else {return contents(in:folder)}
        var result:[File]=[]
        for file in files {try Task.checkCancellation();if file.search.contains(needle) {result.append(file)}}
        return result
    }
    static func display(_ value:String)->String {
        String(value.unicodeScalars.prefix(1024).map {($0.properties.generalCategory == .control || $0.properties.generalCategory == .format) ? " " : String($0)}.joined()).trimmingCharacters(in:.whitespacesAndNewlines)
    }
}
