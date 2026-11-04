import Darwin
import Foundation

public enum PendingInboxError: Error, Equatable, Sendable, LocalizedError {
    case unavailable, sharedContainerUnavailable, busy, full, corrupt, unsupportedVersion, notFound, lostClaim, expired, submissionPending, serverMismatch
    public var errorDescription: String? {
        switch self {
        case .unavailable: "Pending items are unavailable. Unlock your iPhone and try again."
        case .sharedContainerUnavailable: "Shared storage is unavailable for this build."
        case .busy: "Another screen is updating pending links. Try again shortly."
        case .full: "Pending items are full. Review or discard a saved item before adding another."
        case .corrupt: "Pending items could not be read safely. Saved data has been preserved."
        case .unsupportedVersion: "Pending items use an unsupported version. Update Debrid to continue."
        case .notFound: "This pending link is no longer listed."
        case .lostClaim: "This review is no longer current. Reopen the pending link."
        case .submissionPending: "This item has an unresolved submission. Check its result before changing or discarding it."
        case .serverMismatch: "Review and confirm the destination server before adding this item."
        case .expired: "This saved item needs confirmation before it can be reviewed again."
        }
    }
}
public enum PendingSource: String, Codable, Sendable { case url, paste, share, file }
public enum PendingContent: Sendable {
    case magnet(MagnetLink), torrent(TorrentSummary)
}
public struct PendingItem: Identifiable, Sendable, CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public enum State: Sendable, Equatable { case pending, reviewing, expired }
    public let id: UUID
    public let content: PendingContent
    public var magnet: MagnetLink? { if case .magnet(let link) = content { link } else { nil } }
    public var torrent: TorrentSummary? { if case .torrent(let file) = content { file } else { nil } }
    public var displayName: String { magnet?.displayName ?? torrent?.displayName ?? "Magnet link" }
    public var infoHash: String { magnet?.infoHash ?? torrent?.infoHash ?? "" }
    public let submission:PendingSubmission?
    public let source: PendingSource
    public let targetServerURL: URL?
    public let createdAt: Date
    public let state: State
    public var description: String { "PendingItem(redacted)" }
    public var debugDescription: String { description }
    public var customMirror: Mirror { Mirror(self,children:["contents":"redacted"]) }
}
public struct PendingReceipt: Sendable { public let id: UUID; public let wasDuplicate: Bool }
public struct PendingClaim: Sendable, CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public let item: PendingItem
    public let token: UUID
    public let expiresAt: Date
    public var description: String { "PendingClaim(redacted)" }
    public var debugDescription: String { description }
    public var customMirror: Mirror { Mirror(self,children:["contents":"redacted"]) }
}

/// One protected snapshot. No network, credentials, defaults, automatic submission or eviction.
public struct PendingInbox: Sendable {
    public static let appGroup = "group.dev.bhanji.debrid"
    public static let itemLimit = 32
    public static let uriByteLimit = 1_048_576
    public static let payloadByteLimit = 32*1024*1024
    public static let stateByteLimit = 3_145_728
    public static let reviewLifetime: TimeInterval = 120
    public static let retentionLifetime: TimeInterval = 30*24*60*60
    private let directory: URL
    private let clock: @Sendable () -> Date
    public init(directory: URL, clock: @escaping @Sendable () -> Date = Date.init) throws {
        guard directory.isFileURL, directory.path != "/", directory.host == nil || directory.host == "" || directory.host == "localhost" else { throw PendingInboxError.unavailable }
        self.directory = directory.standardizedFileURL; self.clock = clock
    }
    static func groupRoot() throws -> URL {
        guard let url = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier:appGroup) else { throw PendingInboxError.sharedContainerUnavailable }
        return url
    }
    public static func shared() throws -> PendingInbox { try PendingInbox(directory:groupRoot().appendingPathComponent("PendingInbox",isDirectory:true)) }

    public func snapshot() async throws -> [PendingItem] {
        try await transaction { state,now in (try state.items.map { try $0.publicItem(at:now) },false) }
    }
    public func enqueue(_ magnet:MagnetLink,source:PendingSource,target:ServerAddress? = nil) async throws -> PendingReceipt {
        try await transaction { state,now in
            if let row = state.items.first(where:{ $0.rawURI == magnet.rawURI && $0.serverURL == target?.url }) { return (PendingReceipt(id:row.id,wasDuplicate:true),false) }
            guard state.items.count < Self.itemLimit, state.items.reduce(0,{ $0+($1.rawURI?.utf8.count ?? 0) })+magnet.rawURI.utf8.count <= Self.uriByteLimit else { throw PendingInboxError.full }
            if let target, target.url.absoluteString.utf8.count > 2048 { throw PendingInboxError.unavailable }
            let row = Row(id:UUID(),rawURI:magnet.rawURI,torrent:nil,source:source,serverURL:target?.url,createdAt:now,retainedAt:now,lease:nil,submission:nil,freshRejectionToken:nil)
            state.items.append(row); return (PendingReceipt(id:row.id,wasDuplicate:false),true)
        }
    }
    /// Payload is committed before its manifest reference. A failed manifest write is never acknowledged.
    public func enqueueTorrent(_ file:TorrentFile,source:PendingSource,target:ServerAddress? = nil) async throws -> PendingReceipt {
        try await transaction { state,now in
            if let row = state.items.first(where:{ $0.torrent?.digest == file.digest && $0.serverURL == target?.url }) {
                _ = try verifiedTorrent(row); return (PendingReceipt(id:row.id,wasDuplicate:true),false)
            }
            guard state.items.count < Self.itemLimit, state.items.reduce(0,{ $0+($1.torrent?.byteCount ?? 0) })+file.data.count <= Self.payloadByteLimit else { throw PendingInboxError.full }
            if let target, target.url.absoluteString.utf8.count > 2048 { throw PendingInboxError.unavailable }
            let id = UUID()
            try write(file.data,to:payloadURL(id))
            let row = Row(id:id,rawURI:nil,torrent:Payload(summary:file.summary,digest:file.digest,byteCount:file.data.count),source:source,serverURL:target?.url,createdAt:now,retainedAt:now,lease:nil,submission:nil,freshRejectionToken:nil)
            state.items.append(row); return (PendingReceipt(id:id,wasDuplicate:false),true)
        }
    }
    /// Require the live review lease and verify the exact copied bytes before a future upload.
    public func loadTorrent(_ id:UUID,token:UUID) async throws -> TorrentFile {
        try await transaction { state,now in
            let row = state.items[try Self.offset(id,in:state)]
            guard let lease = row.lease, lease.token == token, lease.live(at:now), !row.expired(at:now) else { throw PendingInboxError.lostClaim }
            return (try verifiedTorrent(row),false)
        }
    }
    private func verifiedTorrent(_ row:Row) throws -> TorrentFile {
        guard let payload = row.torrent else { throw PendingInboxError.notFound }
        let data = try read(payloadURL(row.id),limit:TorrentFile.byteLimit)
        guard data.count == payload.byteCount else { throw PendingInboxError.corrupt }
        let file:TorrentFile
        do { file = try TorrentFile(data) }
        catch is CancellationError { throw CancellationError() }
        catch { throw PendingInboxError.corrupt }
        guard file.digest == payload.digest, file.summary == payload.summary else { throw PendingInboxError.corrupt }
        return file
    }
    private func payloadURL(_ id:UUID) -> URL { directory.appendingPathComponent(id.uuidString+".torrent-payload") }
    public func reviewItem(_ id:UUID,token:UUID) async throws -> PendingItem {
        try await transaction { state,now in
            let offset=try Self.reviewOffset(id,token:token,state:state,now:now)
            return (try state.items[offset].publicItem(at:now),false)
        }
    }
    public func prepareSubmission(_ id:UUID,token:UUID,server:ServerAddress,options:SubmissionOptions,confirmedTargetChange:Bool) async throws -> PreparedSubmission {
        try options.validate()
        return try await transaction { state,now in
            let offset=try Self.reviewOffset(id,token:token,state:state,now:now)
            if let attempt=state.items[offset].submission {
                if attempt.phase != .rejected {
                    guard attempt.phase == .prepared, attempt.serverURL==server.url, attempt.options==options else { throw PendingInboxError.submissionPending }
                    return (PreparedSubmission(attempt:attempt,rejectionToken:nil),false)
                }
            }
            if let target=state.items[offset].serverURL ?? (state.items[offset].submission?.phase == .rejected ? state.items[offset].submission?.serverURL : nil), target != server.url, !confirmedTargetChange { throw PendingInboxError.serverMismatch }
            let attempt=PendingSubmission(serverURL:server.url,options:options,createdAt:now), fresh=UUID()
            try attempt.validate()
            state.items[offset].submission=attempt;state.items[offset].freshRejectionToken=fresh
            return (PreparedSubmission(attempt:attempt,rejectionToken:fresh),true)
        }
    }
    public func recordSubmissionAccepted(_ id:UUID,token:UUID,key:UUID,server:ServerAddress,transferID:String,exists:Bool) async throws {
        guard SubmissionIdentity.validID(transferID) else { throw PendingInboxError.corrupt }
        try await transaction { state,now in
            let offset=try Self.reviewOffset(id,token:token,state:state,now:now)
            guard let attempt=state.items[offset].submission, attempt.key==key, attempt.serverURL==server.url else { throw PendingInboxError.submissionPending }
            if attempt.phase == .accepted {
                guard attempt.acceptedID==transferID else { throw PendingInboxError.corrupt };return ((),false)
            }
            guard attempt.phase == .prepared else { throw PendingInboxError.submissionPending }
            state.items[offset].submission=PendingSubmission(key:key,serverURL:attempt.serverURL,options:attempt.options,createdAt:attempt.createdAt,phase:.accepted,acceptedID:transferID,torrentExists:exists)
            state.items[offset].freshRejectionToken=nil;return ((),true)
        }
    }
    public func recordSubmissionMismatch(_ id:UUID,token:UUID,key:UUID) async throws {
        try await transaction { state,now in
            let offset=try Self.reviewOffset(id,token:token,state:state,now:now)
            guard let attempt=state.items[offset].submission,attempt.key==key,attempt.phase == .prepared || attempt.phase == .blocked else { throw PendingInboxError.submissionPending }
            state.items[offset].submission=PendingSubmission(key:key,serverURL:attempt.serverURL,options:attempt.options,createdAt:attempt.createdAt,phase:.blocked)
            state.items[offset].freshRejectionToken=nil;return ((),true)
        }
    }
    /// A one-use nonce is returned only to the creator's first dispatch, never restored/replayed.
    public func recordSubmissionRejected(_ id:UUID,token:UUID,key:UUID,freshToken:UUID,reason:SubmissionRejection) async throws {
        try await transaction { state,now in
            let offset=try Self.reviewOffset(id,token:token,state:state,now:now)
            guard let attempt=state.items[offset].submission, attempt.key==key, attempt.phase == .prepared, state.items[offset].freshRejectionToken==freshToken else { throw PendingInboxError.submissionPending }
            let rejected=PendingSubmission(key:key,serverURL:attempt.serverURL,options:attempt.options,createdAt:attempt.createdAt,phase:.rejected,rejection:reason)
            try rejected.validate();state.items[offset].submission=rejected;state.items[offset].freshRejectionToken=nil;return ((),true)
        }
    }
    private static func reviewOffset(_ id:UUID,token:UUID,state:Envelope,now:Date) throws -> Int {
        let offset=try offset(id,in:state)
        guard !state.items[offset].expired(at:now), let lease=state.items[offset].lease, lease.token==token, lease.live(at:now) else { throw PendingInboxError.lostClaim }
        return offset
    }
    public func claim(_ id:UUID) async throws -> PendingClaim {
        try await transaction { state,now in
            let offset = try Self.offset(id,in:state)
            guard !state.items[offset].expired(at:now) else { throw PendingInboxError.expired }
            guard state.items[offset].lease?.live(at:now) != true else { throw PendingInboxError.busy }
            let lease = Lease(token:UUID(),issuedAt:now,expiresAt:now.addingTimeInterval(Self.reviewLifetime))
            state.items[offset].lease = lease
            return (PendingClaim(item:try state.items[offset].publicItem(at:now),token:lease.token,expiresAt:lease.expiresAt),true)
        }
    }
    public func renew(_ id:UUID,token:UUID) async throws -> Date {
        try await transaction { state,now in
            let offset = try Self.offset(id,in:state)
            guard !state.items[offset].expired(at:now) else { throw PendingInboxError.expired }
            guard let lease = state.items[offset].lease, lease.token == token, lease.live(at:now) else { throw PendingInboxError.lostClaim }
            let expires = now.addingTimeInterval(Self.reviewLifetime)
            state.items[offset].lease = Lease(token:token,issuedAt:now,expiresAt:expires)
            return (expires,true)
        }
    }
    public func release(_ id:UUID,token:UUID) async throws {
        try await transaction { state,_ in
            let offset = try Self.offset(id,in:state)
            guard state.items[offset].lease?.token == token else { throw PendingInboxError.lostClaim }
            state.items[offset].lease = nil; return ((),true)
        }
    }
    /// Explicit user confirmation extends retention; expiration never silently removes raw content.
    public func keep(_ id:UUID) async throws {
        try await transaction { state,now in
            let offset = try Self.offset(id,in:state)
            guard state.items[offset].lease?.live(at:now) != true else { throw PendingInboxError.busy }
            state.items[offset].retainedAt = now; state.items[offset].lease = nil; return ((),true)
        }
    }
    /// Removing an actively reviewed item requires its exact token.
    public func discard(_ id:UUID,token:UUID? = nil) async throws {
        try await transaction { state,now in
            let offset = try Self.offset(id,in:state), lease = state.items[offset].lease
            guard state.items[offset].submission?.phase != .prepared, state.items[offset].submission?.phase != .blocked else { throw PendingInboxError.submissionPending }
            if let token { guard lease?.token == token else { throw PendingInboxError.lostClaim } }
            else if lease?.live(at:now) == true { throw PendingInboxError.busy }
            state.items.remove(at:offset); return ((),true)
        }
    }
    public func savedFileSelections() async throws -> [SavedFileSelection] {
        try await transaction { state,_ in
            let result=state.fileSelections.compactMap { row -> SavedFileSelection? in
                guard let intent=row.intent,intent.phase == .prepared || intent.phase == .blocked else {return nil}
                return SavedFileSelection(id:row.id,serverURL:row.serverURL,transferID:row.transferID,name:row.name,phase:intent.phase)
            }
            return (result,false)
        }
    }
    public func claimFileSelection(server:URL,transferID:String,name:String) async throws -> FileSelectionClaim {
        guard SubmissionIdentity.validID(transferID),server.absoluteString.utf8.count<=2048,(try? ServerAddress(server.absoluteString))?.url==server else {throw FileSelectionError.invalidMetadata}
        let title=String(name.unicodeScalars.prefix(256).map { $0.properties.generalCategory == .control ? " " : String($0) }.joined())
        return try await transaction { state,now in
            var rows=state.fileSelections
            // Empty or definitively finished abandoned reviews contain no uncertain request.
            rows.removeAll { row in row.lease?.live(at:now) != true && (row.intent == nil || row.intent?.phase == .resolved || row.intent?.phase == .rejected) }
            let offset:Int
            if let existing=rows.firstIndex(where:{$0.serverURL==server && $0.transferID==transferID}) {
                guard rows[existing].lease?.live(at:now) != true else {throw PendingInboxError.busy};offset=existing
            } else {
                guard rows.count<Self.itemLimit else {throw PendingInboxError.full}
                rows.append(SelectionRow(id:UUID(),serverURL:server,transferID:transferID,name:title,lease:nil,intent:nil,freshToken:nil));offset=rows.count-1
            }
            rows[offset].name=title
            rows[offset].lease=Lease(token:UUID(),issuedAt:now,expiresAt:now.addingTimeInterval(Self.reviewLifetime))
            state.fileSelections=rows;return (try rows[offset].claim(),true)
        }
    }
    private static func selectionOffset(_ id:UUID,token:UUID,state:Envelope,now:Date) throws -> Int {
        let rows=state.fileSelections
        guard let index=rows.firstIndex(where:{$0.id==id}),let lease=rows[index].lease,lease.token==token,lease.live(at:now) else {throw PendingInboxError.lostClaim}
        return index
    }
    public func fileSelectionClaim(_ id:UUID,token:UUID) async throws -> FileSelectionClaim {
        try await transaction { state,now in let index=try Self.selectionOffset(id,token:token,state:state,now:now);return (try state.fileSelections[index].claim(),false) }
    }
    public func renewFileSelection(_ id:UUID,token:UUID) async throws {
        try await transaction { state,now in
            let index=try Self.selectionOffset(id,token:token,state:state,now:now)
            state.fileSelections[index].lease=Lease(token:token,issuedAt:now,expiresAt:now.addingTimeInterval(Self.reviewLifetime));return ((),true)
        }
    }
    public func releaseFileSelection(_ id:UUID,token:UUID) async throws {
        try await transaction { state,_ in
            guard let index=state.fileSelections.firstIndex(where:{$0.id==id}),state.fileSelections[index].lease?.token==token else {throw PendingInboxError.lostClaim}
            let phase=state.fileSelections[index].intent?.phase
            if phase == nil || phase == .resolved || phase == .rejected {state.fileSelections.remove(at:index)}
            else {state.fileSelections[index].lease=nil}
            return ((),true)
        }
    }
    @concurrent public func prepareFileSelection(_ id:UUID,token:UUID,revision:String,fileIDs:[String]) async throws -> PreparedFileSelection {
        guard FileSelectionIdentity.revisionIsValid(revision) else {throw FileSelectionError.invalidMetadata}
        let ids=try FileSelectionIdentity.canonicalIDs(fileIDs)
        return try await transaction { state,now in
            let index=try Self.selectionOffset(id,token:token,state:state,now:now),row=state.fileSelections[index]
            if let existing=row.intent,existing.phase != .rejected {
                guard existing.phase == .prepared,existing.revision==revision,FileSelectionIdentity.sameIDs(existing.fileIDs,ids) else {throw FileSelectionError.unresolved}
                return (PreparedFileSelection(intent:existing,rejectionToken:nil),false)
            }
            let intent=FileSelectionIntent(serverURL:row.serverURL,transferID:row.transferID,revision:revision,fileIDs:ids,createdAt:now)
            try intent.validate()
            var bytes=try FileSelectionIdentity.encodedIDs(ids).count
            for (otherIndex,other) in state.fileSelections.enumerated() where otherIndex != index {
                if let previous=other.intent {bytes += try FileSelectionIdentity.encodedIDs(previous.fileIDs).count}
            }
            guard bytes<=FileSelectionIdentity.totalIntentByteLimit else {throw PendingInboxError.full}
            let fresh=UUID();state.fileSelections[index].intent=intent;state.fileSelections[index].freshToken=fresh
            return (PreparedFileSelection(intent:intent,rejectionToken:fresh),true)
        }
    }
    public func resolveFileSelection(_ id:UUID,token:UUID,intentID:UUID) async throws {
        try await transaction { state,now in
            let index=try Self.selectionOffset(id,token:token,state:state,now:now)
            guard let intent=state.fileSelections[index].intent,intent.id==intentID else {throw FileSelectionError.unresolved}
            state.fileSelections[index].intent=intent.replacingPhase(.resolved);state.fileSelections[index].freshToken=nil;return ((),true)
        }
    }
    public func rejectFileSelection(_ id:UUID,token:UUID,intentID:UUID,freshToken:UUID) async throws {
        try await transaction { state,now in
            let index=try Self.selectionOffset(id,token:token,state:state,now:now)
            guard let intent=state.fileSelections[index].intent,intent.id==intentID,intent.phase == .prepared,state.fileSelections[index].freshToken==freshToken else {throw FileSelectionError.unresolved}
            state.fileSelections[index].intent=intent.replacingPhase(.rejected);state.fileSelections[index].freshToken=nil;return ((),true)
        }
    }
    public func blockFileSelection(_ id:UUID,token:UUID,intentID:UUID) async throws {
        try await transaction { state,now in
            let index=try Self.selectionOffset(id,token:token,state:state,now:now)
            guard let intent=state.fileSelections[index].intent,intent.id==intentID,intent.phase == .prepared else {throw FileSelectionError.unresolved}
            state.fileSelections[index].intent=intent.replacingPhase(.blocked);state.fileSelections[index].freshToken=nil;return ((),true)
        }
    }
    private struct Envelope: Codable, Sendable {
        var version=4;var items:[Row]=[];var fileSelections:[SelectionRow]=[]
        enum CodingKeys:String,CodingKey {case version,items,fileSelections}
        init() {}
        init(from decoder:any Decoder) throws {
            let values=try decoder.container(keyedBy:CodingKeys.self)
            version=try values.decode(Int.self,forKey:.version);items=try values.decode([Row].self,forKey:.items)
            if version>=4 {fileSelections=try values.decode([SelectionRow].self,forKey:.fileSelections)}
            else {fileSelections=try values.decodeIfPresent([SelectionRow].self,forKey:.fileSelections) ?? []}
        }
    }
    private struct SelectionRow:Codable,Sendable { let id:UUID;let serverURL:URL;let transferID:String;var name:String;var lease:Lease?;var intent:FileSelectionIntent?;var freshToken:UUID?
        func claim() throws -> FileSelectionClaim { guard let lease else {throw PendingInboxError.lostClaim};return FileSelectionClaim(id:id,token:lease.token,serverURL:serverURL,transferID:transferID,name:name,intent:intent) }
    }
    private struct Header: Decodable { let version:Int }
    private struct Lease: Codable, Sendable {
        let token:UUID; let issuedAt:Date; let expiresAt:Date
        func live(at now:Date) -> Bool { now >= issuedAt.addingTimeInterval(-5) && now < expiresAt }
    }
    private struct Payload: Codable, Sendable { let summary:TorrentSummary; let digest:String; let byteCount:Int }
    private struct Row: Codable, Sendable {
        let id:UUID; let rawURI:String?; let torrent:Payload?; let source:PendingSource; let serverURL:URL?; let createdAt:Date
        var retainedAt:Date; var lease:Lease?
        var submission:PendingSubmission?; var freshRejectionToken:UUID?
        func expired(at now:Date) -> Bool { now.timeIntervalSince(retainedAt) >= PendingInbox.retentionLifetime }
        func content() throws -> PendingContent {
            if let rawURI, torrent == nil { return .magnet(try MagnetLink(rawURI)) }
            if let torrent, rawURI == nil { return .torrent(torrent.summary) }
            throw PendingInboxError.corrupt
        }
        func publicItem(at now:Date) throws -> PendingItem {
            PendingItem(id:id,content:try content(),submission:submission,source:source,targetServerURL:serverURL,createdAt:createdAt,state:expired(at:now) ? .expired : lease?.live(at:now) == true ? .reviewing : .pending)
        }
    }
    private static func offset(_ id:UUID,in state:Envelope) throws -> Int {
        guard let offset = state.items.firstIndex(where:{ $0.id == id }) else { throw PendingInboxError.notFound }; return offset
    }
    @concurrent private func transaction<Value:Sendable>(_ operation:@Sendable (inout Envelope,Date) throws -> (Value,Bool)) async throws -> Value {
        try Task.checkCancellation()
        do {
            try prepare()
            let lockURL = directory.appendingPathComponent(".inbox.lock")
            let deadline = ContinuousClock.now.advanced(by:.seconds(2))
            var descriptor: Int32 = -1
            while descriptor < 0 {
                try Task.checkCancellation()
                descriptor = Darwin.open(lockURL.path,O_RDWR|O_CREAT|O_NOFOLLOW|O_CLOEXEC|O_EXLOCK|O_NONBLOCK,mode_t(0o600))
                if descriptor >= 0 { break }
                guard errno == EWOULDBLOCK || errno == EAGAIN else { throw PendingInboxError.unavailable }
                guard ContinuousClock.now < deadline else { throw PendingInboxError.busy }
                try await Task.sleep(for:.milliseconds(25))
            }
            defer { Darwin.close(descriptor) }
            var lockInfo = stat()
            guard fstat(descriptor,&lockInfo) == 0, (lockInfo.st_mode & S_IFMT) == S_IFREG else { throw PendingInboxError.unavailable }
            try Self.protect(lockURL)
            try Task.checkCancellation()
            // No suspension point while holding the cross-process lock.
            var state = try load()
            let migrating=state.version<4
            if migrating { state.fileSelections=[] }
            try cleanup(state)
            let now = clock()
            guard now.timeIntervalSince1970.isFinite else { throw PendingInboxError.unavailable }
            let (value,changed) = try operation(&state,now)
            if changed || migrating { state.version = 4; try save(state) }
            // After a committed removal, orphan cleanup is best effort: never turn success into an ambiguous failure.
            try? cleanup(state)
            return value
        } catch is CancellationError { throw CancellationError() }
        catch let error as PendingInboxError { throw error }
        catch let error as FileSelectionError { throw error }
        catch { throw PendingInboxError.unavailable }
    }
    private func prepare() throws {
        let manager = FileManager.default
        if manager.fileExists(atPath:directory.path), try directory.resourceValues(forKeys:[.isSymbolicLinkKey]).isSymbolicLink == true { throw PendingInboxError.unavailable }
        try manager.createDirectory(at:directory,withIntermediateDirectories:true,attributes:[.posixPermissions:0o700])
        try Self.protect(directory)
        #if os(iOS)
        var url = directory, values = URLResourceValues(); values.isExcludedFromBackup = true; try url.setResourceValues(values)
        #endif
    }
    private static func protect(_ url:URL) throws {
        #if os(iOS)
        try FileManager.default.setAttributes([.protectionKey:FileProtectionType.complete],ofItemAtPath:url.path)
        #endif
    }
    private enum ReadFailure: Error { case missing }
    private func read(_ url:URL,limit:Int) throws -> Data {
        let fd = Darwin.open(url.path,O_RDONLY|O_NOFOLLOW|O_CLOEXEC|O_NONBLOCK)
        if fd < 0 { if errno == ENOENT { throw ReadFailure.missing }; throw PendingInboxError.unavailable }
        defer { Darwin.close(fd) }
        var info = stat()
        guard fstat(fd,&info) == 0, (info.st_mode & S_IFMT) == S_IFREG, info.st_size >= 0, info.st_size <= limit else { throw PendingInboxError.corrupt }
        var data = Data(count:Int(info.st_size)), position = 0
        while position < data.count {
            try Task.checkCancellation()
            let count = data.withUnsafeMutableBytes { buffer in
                guard let base = buffer.baseAddress else { return -1 }; return Darwin.read(fd,base.advanced(by:position),buffer.count-position)
            }
            if count < 0 && errno == EINTR { continue }; guard count > 0 else { throw PendingInboxError.corrupt }; position += count
        }
        return data
    }
    private func load() throws -> Envelope {
        let url = directory.appendingPathComponent("inbox.json")
        let data: Data
        do { data = try read(url,limit:Self.stateByteLimit) }
        catch ReadFailure.missing { return Envelope() }
        let decoder = JSONDecoder(); decoder.dateDecodingStrategy = .millisecondsSince1970
        guard let header = try? decoder.decode(Header.self,from:data) else { throw PendingInboxError.corrupt }
        guard (1...4).contains(header.version) else { throw PendingInboxError.unsupportedVersion }
        guard let state = try? decoder.decode(Envelope.self,from:data), state.items.count <= Self.itemLimit, Set(state.items.map(\.id)).count == state.items.count,
              state.items.reduce(0,{ $0+($1.rawURI?.utf8.count ?? 0) }) <= Self.uriByteLimit,
              state.items.allSatisfy({ ($0.torrent?.byteCount ?? 0) >= 0 && ($0.torrent?.byteCount ?? 0) <= TorrentFile.byteLimit }),
              state.items.reduce(0,{ $0+($1.torrent?.byteCount ?? 0) }) <= Self.payloadByteLimit else { throw PendingInboxError.corrupt }
        if header.version<4, !state.fileSelections.isEmpty {throw PendingInboxError.corrupt}
        let selections=state.fileSelections
        guard selections.count<=Self.itemLimit,Set(selections.map(\.id)).count==selections.count else {throw PendingInboxError.corrupt}
        var identities=Set<String>(),selectionBytes=0
        for row in selections {
            guard SubmissionIdentity.validID(row.transferID),row.name.utf8.count<=1024,row.serverURL.absoluteString.utf8.count<=2048,(try? ServerAddress(row.serverURL.absoluteString))?.url==row.serverURL,identities.insert(row.serverURL.absoluteString+"\n"+row.transferID).inserted else {throw PendingInboxError.corrupt}
            if let lease=row.lease {guard lease.issuedAt.timeIntervalSince1970.isFinite,lease.expiresAt.timeIntervalSince1970.isFinite,abs(lease.expiresAt.timeIntervalSince(lease.issuedAt)-Self.reviewLifetime)<1 else {throw PendingInboxError.corrupt}}
            if let intent=row.intent {
                guard (try? intent.validate()) != nil,intent.serverURL==row.serverURL,intent.transferID==row.transferID,intent.phase == .prepared || row.freshToken == nil else {throw PendingInboxError.corrupt}
                selectionBytes += (try? FileSelectionIdentity.encodedIDs(intent.fileIDs).count) ?? Self.stateByteLimit
            } else if row.freshToken != nil {throw PendingInboxError.corrupt}
        }
        guard selectionBytes<=FileSelectionIdentity.totalIntentByteLimit else {throw PendingInboxError.corrupt}
        for row in state.items {
            guard (try? row.content()) != nil, row.createdAt.timeIntervalSince1970.isFinite, row.retainedAt.timeIntervalSince1970.isFinite else { throw PendingInboxError.corrupt }
            if let attempt=row.submission {
                guard header.version>=3, (header.version>=4 || attempt.version==1), (try? attempt.validate()) != nil, attempt.phase == .prepared || row.freshRejectionToken == nil else { throw PendingInboxError.corrupt }
            } else if row.freshRejectionToken != nil { throw PendingInboxError.corrupt }
            if let raw = row.rawURI { guard (try? MagnetLink(raw))?.rawURI == raw else { throw PendingInboxError.corrupt } }
            if let payload = row.torrent {
                guard header.version >= 2, payload.byteCount >= 0, payload.byteCount <= TorrentFile.byteLimit,
                      payload.digest.utf8.count == 64, payload.digest.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }),
                      payload.summary.infoHash.utf8.count == 40, payload.summary.infoHash.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }),
                      payload.summary.totalBytes >= 0, (1...20_000).contains(payload.summary.fileCount), payload.summary.displayName.utf8.count <= 1024 else { throw PendingInboxError.corrupt }
                let fd = Darwin.open(payloadURL(row.id).path,O_RDONLY|O_NOFOLLOW|O_CLOEXEC|O_NONBLOCK)
                guard fd >= 0 else { throw PendingInboxError.corrupt }
                var info = stat(); let valid = fstat(fd,&info) == 0 && (info.st_mode & S_IFMT) == S_IFREG && info.st_size == payload.byteCount
                Darwin.close(fd); guard valid else { throw PendingInboxError.corrupt }
            }
            if let server = row.serverURL { guard server.absoluteString.utf8.count <= 2048, (try? ServerAddress(server.absoluteString)) != nil else { throw PendingInboxError.corrupt } }
            if let lease = row.lease { guard lease.issuedAt.timeIntervalSince1970.isFinite, lease.expiresAt.timeIntervalSince1970.isFinite, abs(lease.expiresAt.timeIntervalSince(lease.issuedAt)-Self.reviewLifetime)<1 else { throw PendingInboxError.corrupt } }
        }
        return state
    }
    private func save(_ state:Envelope) throws {
        let encoder = JSONEncoder(); encoder.dateEncodingStrategy = .millisecondsSince1970
        let data = try encoder.encode(state)
        guard data.count <= Self.stateByteLimit else { throw PendingInboxError.full }
        try write(data,to:directory.appendingPathComponent("inbox.json"))
    }
    private func write(_ data:Data,to destination:URL) throws {
        let temp = directory.appendingPathComponent(".pending-write-"+UUID().uuidString+".tmp")
        let fd = Darwin.open(temp.path,O_WRONLY|O_CREAT|O_EXCL|O_NOFOLLOW|O_CLOEXEC,mode_t(0o600))
        guard fd >= 0 else { throw PendingInboxError.unavailable }
        defer { Darwin.close(fd); _ = Darwin.unlink(temp.path) }
        try Self.protect(temp)
        var position = 0
        while position < data.count {
            try Task.checkCancellation()
            let count = data.withUnsafeBytes { buffer in
                guard let base = buffer.baseAddress else { return -1 }; return Darwin.write(fd,base.advanced(by:position),buffer.count-position)
            }
            if count < 0 && errno == EINTR { continue }
            guard count > 0 else { throw PendingInboxError.unavailable }; position += count
        }
        guard Darwin.fsync(fd) == 0 else { throw PendingInboxError.unavailable }
        try Task.checkCancellation()
        guard Darwin.rename(temp.path,destination.path) == 0 else { throw PendingInboxError.unavailable }
        // A process crash sees either the prior or complete new snapshot. No post-commit cancellation.
    }
    private func cleanup(_ state:Envelope) throws {
        let referenced = Set(state.items.filter { $0.torrent != nil }.map { $0.id.uuidString+".torrent-payload" })
        for file in try FileManager.default.contentsOfDirectory(at:directory,includingPropertiesForKeys:nil,options:[]) {
            let name = file.lastPathComponent
            let temporary = name.hasPrefix(".pending-write-") && name.hasSuffix(".tmp") && UUID(uuidString:String(name.dropFirst(15).dropLast(4))) != nil
            let payload = name.hasSuffix(".torrent-payload") && UUID(uuidString:String(name.dropLast(16))) != nil && !referenced.contains(name)
            guard temporary || payload else { continue }
            guard Darwin.unlink(file.path) == 0 || errno == ENOENT else { throw PendingInboxError.unavailable }
        }
    }
}
