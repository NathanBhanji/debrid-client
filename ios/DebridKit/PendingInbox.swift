import Darwin
import Foundation

public enum PendingInboxError: Error, Equatable, Sendable, LocalizedError {
    case unavailable, sharedContainerUnavailable, busy, full, corrupt, unsupportedVersion, notFound, lostClaim, expired
    public var errorDescription: String? {
        switch self {
        case .unavailable: "Pending links are unavailable. Unlock your iPhone and try again."
        case .sharedContainerUnavailable: "Shared storage is unavailable for this build."
        case .busy: "Another screen is updating pending links. Try again shortly."
        case .full: "Pending links are full. Review or discard a saved link before adding another."
        case .corrupt: "Pending links could not be read safely. Saved data has been preserved."
        case .unsupportedVersion: "Pending links use an unsupported version. Update Debrid to continue."
        case .notFound: "This pending link is no longer listed."
        case .lostClaim: "This review is no longer current. Reopen the pending link."
        case .expired: "This saved link needs confirmation before it can be reviewed again."
        }
    }
}
public enum PendingSource: String, Codable, Sendable { case url, paste, share }
public struct PendingMagnet: Identifiable, Sendable, CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public enum State: Sendable, Equatable { case pending, reviewing, expired }
    public let id: UUID
    public let magnet: MagnetLink
    public let source: PendingSource
    public let targetServerURL: URL?
    public let createdAt: Date
    public let state: State
    public var description: String { "PendingMagnet(redacted)" }
    public var debugDescription: String { description }
    public var customMirror: Mirror { Mirror(self,children:["contents":"redacted"]) }
}
public struct PendingReceipt: Sendable { public let id: UUID; public let wasDuplicate: Bool }
public struct PendingClaim: Sendable, CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public let item: PendingMagnet
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

    public func snapshot() async throws -> [PendingMagnet] {
        try await transaction { state,now in (try state.items.map { try $0.publicItem(at:now) },false) }
    }
    public func enqueue(_ magnet:MagnetLink,source:PendingSource,target:ServerAddress? = nil) async throws -> PendingReceipt {
        try await transaction { state,now in
            if let row = state.items.first(where:{ $0.rawURI == magnet.rawURI && $0.serverURL == target?.url }) { return (PendingReceipt(id:row.id,wasDuplicate:true),false) }
            guard state.items.count < Self.itemLimit, state.items.reduce(0,{ $0+$1.rawURI.utf8.count })+magnet.rawURI.utf8.count <= Self.uriByteLimit else { throw PendingInboxError.full }
            if let target, target.url.absoluteString.utf8.count > 2048 { throw PendingInboxError.unavailable }
            let row = Row(id:UUID(),rawURI:magnet.rawURI,source:source,serverURL:target?.url,createdAt:now,retainedAt:now,lease:nil)
            state.items.append(row); return (PendingReceipt(id:row.id,wasDuplicate:false),true)
        }
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
            if let token { guard lease?.token == token else { throw PendingInboxError.lostClaim } }
            else if lease?.live(at:now) == true { throw PendingInboxError.busy }
            state.items.remove(at:offset); return ((),true)
        }
    }
    private struct Envelope: Codable, Sendable { var version = 1; var items: [Row] = [] }
    private struct Header: Decodable { let version:Int }
    private struct Lease: Codable, Sendable {
        let token:UUID; let issuedAt:Date; let expiresAt:Date
        func live(at now:Date) -> Bool { now >= issuedAt.addingTimeInterval(-5) && now < expiresAt }
    }
    private struct Row: Codable, Sendable {
        let id:UUID; let rawURI:String; let source:PendingSource; let serverURL:URL?; let createdAt:Date
        var retainedAt:Date; var lease:Lease?
        func expired(at now:Date) -> Bool { now.timeIntervalSince(retainedAt) >= PendingInbox.retentionLifetime }
        func publicItem(at now:Date) throws -> PendingMagnet {
            PendingMagnet(id:id,magnet:try MagnetLink(rawURI),source:source,targetServerURL:serverURL,createdAt:createdAt,state:expired(at:now) ? .expired : lease?.live(at:now) == true ? .reviewing : .pending)
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
            try cleanupTemps()
            let now = clock()
            guard now.timeIntervalSince1970.isFinite else { throw PendingInboxError.unavailable }
            let (value,changed) = try operation(&state,now)
            if changed { try save(state) }
            return value
        } catch is CancellationError { throw CancellationError() }
        catch let error as PendingInboxError { throw error }
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
    private func load() throws -> Envelope {
        let url = directory.appendingPathComponent("inbox.json")
        let fd = Darwin.open(url.path,O_RDONLY|O_NOFOLLOW|O_CLOEXEC|O_NONBLOCK)
        if fd < 0 { if errno == ENOENT { return Envelope() }; throw PendingInboxError.unavailable }
        defer { Darwin.close(fd) }
        var info = stat()
        guard fstat(fd,&info) == 0, (info.st_mode & S_IFMT) == S_IFREG, info.st_size >= 0, info.st_size <= Self.stateByteLimit else { throw PendingInboxError.corrupt }
        var data = Data(count:Int(info.st_size)), position = 0
        while position < data.count {
            try Task.checkCancellation()
            let count = data.withUnsafeMutableBytes { buffer in
                guard let base = buffer.baseAddress else { return -1 }; return Darwin.read(fd,base.advanced(by:position),buffer.count-position)
            }
            if count < 0 && errno == EINTR { continue }
            guard count > 0 else { throw PendingInboxError.corrupt }; position += count
        }
        let decoder = JSONDecoder(); decoder.dateDecodingStrategy = .millisecondsSince1970
        guard let header = try? decoder.decode(Header.self,from:data) else { throw PendingInboxError.corrupt }
        guard header.version == 1 else { throw PendingInboxError.unsupportedVersion }
        guard let state = try? decoder.decode(Envelope.self,from:data), state.items.count <= Self.itemLimit, Set(state.items.map(\.id)).count == state.items.count,
              state.items.reduce(0,{ $0+$1.rawURI.utf8.count }) <= Self.uriByteLimit else { throw PendingInboxError.corrupt }
        for row in state.items {
            guard let link = try? MagnetLink(row.rawURI), link.rawURI == row.rawURI, row.createdAt.timeIntervalSince1970.isFinite, row.retainedAt.timeIntervalSince1970.isFinite else { throw PendingInboxError.corrupt }
            if let server = row.serverURL { guard server.absoluteString.utf8.count <= 2048, (try? ServerAddress(server.absoluteString)) != nil else { throw PendingInboxError.corrupt } }
            if let lease = row.lease { guard lease.issuedAt.timeIntervalSince1970.isFinite, lease.expiresAt.timeIntervalSince1970.isFinite, abs(lease.expiresAt.timeIntervalSince(lease.issuedAt)-Self.reviewLifetime)<1 else { throw PendingInboxError.corrupt } }
        }
        return state
    }
    private func save(_ state:Envelope) throws {
        let encoder = JSONEncoder(); encoder.dateEncodingStrategy = .millisecondsSince1970
        let data = try encoder.encode(state)
        guard data.count <= Self.stateByteLimit else { throw PendingInboxError.full }
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
        guard Darwin.rename(temp.path,directory.appendingPathComponent("inbox.json").path) == 0 else { throw PendingInboxError.unavailable }
        // A process crash sees either the prior or complete new snapshot. No post-commit cancellation.
    }
    private func cleanupTemps() throws {
        for file in try FileManager.default.contentsOfDirectory(at:directory,includingPropertiesForKeys:nil,options:[]) {
            let name = file.lastPathComponent
            guard name.hasPrefix(".pending-write-"), name.hasSuffix(".tmp"), UUID(uuidString:String(name.dropFirst(15).dropLast(4))) != nil else { continue }
            guard Darwin.unlink(file.path) == 0 || errno == ENOENT else { throw PendingInboxError.unavailable }
        }
    }
}
