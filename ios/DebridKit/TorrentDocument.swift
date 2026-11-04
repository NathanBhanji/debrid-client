import Darwin
import Foundation

/// Copies only a bounded regular file while the document provider's access is active.
public enum TorrentDocument {
    @concurrent public static func load(_ url:URL) async throws -> TorrentFile {
        try Task.checkCancellation()
        guard url.isFileURL else { throw TorrentFileError.unavailable }
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        let path = url.standardizedFileURL.path
        let home = NSHomeDirectory()+"/", temporary = FileManager.default.temporaryDirectory.standardizedFileURL.path+"/"
        guard scoped || path.hasPrefix(home) || path.hasPrefix(temporary) else { throw TorrentFileError.unavailable }
        let session = ReadCoordination()
        return try await withTaskCancellationHandler {
        var coordinationError:NSError?, result:Result<TorrentFile,any Error>?
        session.coordinator.coordinate(readingItemAt:url,options:.withoutChanges,error:&coordinationError) { readingURL in
            result = Result { try copy(readingURL) }
        }
        try Task.checkCancellation()
        guard coordinationError == nil, let result else { throw TorrentFileError.unavailable }
        return try result.get()
        } onCancel: { session.coordinator.cancel() }
    }
    // NSFileCoordinator explicitly supports cancelling pending coordination from another thread.
    // The object is immutable here; only coordinate and its documented cancel cross threads.
    private final class ReadCoordination: @unchecked Sendable { let coordinator = NSFileCoordinator() }
    /// Invoke synchronously inside NSItemProvider's temporary-file callback.
    /// The callback owns the URL lifetime; no URL escapes into an async task.
    public static func copyProvidedFile(_ url:URL,requireTorrentExtension:Bool = true,maximumBytes:Int = TorrentFile.byteLimit) throws -> TorrentFile {
        guard url.isFileURL,!requireTorrentExtension || url.pathExtension.lowercased()=="torrent" else {throw TorrentFileError.unavailable}
        guard (0...TorrentFile.byteLimit).contains(maximumBytes) else {throw TorrentFileError.tooLarge}
        return try copy(url,maximumBytes:maximumBytes)
    }
    private static func copy(_ url:URL,maximumBytes:Int = TorrentFile.byteLimit) throws -> TorrentFile {
        let fd = Darwin.open(url.path,O_RDONLY|O_NOFOLLOW|O_CLOEXEC|O_NONBLOCK)
        guard fd >= 0 else { throw TorrentFileError.unavailable }; defer { Darwin.close(fd) }
        var info = stat()
        guard fstat(fd,&info) == 0, (info.st_mode & S_IFMT) == S_IFREG, info.st_size >= 0 else { throw TorrentFileError.unavailable }
        guard info.st_size <= maximumBytes else { throw TorrentFileError.tooLarge }
        var data = Data(), buffer = [UInt8](repeating:0,count:64*1024)
        while true {
            try Task.checkCancellation()
            let count = Darwin.read(fd,&buffer,buffer.count)
            if count < 0 && errno == EINTR { continue }
            guard count >= 0 else { throw TorrentFileError.unavailable }; if count == 0 { break }
            guard data.count+count <= maximumBytes else { throw TorrentFileError.tooLarge }
            data.append(contentsOf:buffer.prefix(count))
        }
        return try TorrentFile(data)
    }
}

public enum MagnetPasteError: Error, Sendable, LocalizedError {
    case empty, tooMany, tooLarge
    public var errorDescription:String? {
        switch self {
        case .empty: "Paste a magnet link to continue."
        case .tooMany: "Paste up to 16 magnet links, with one link on each line."
        case .tooLarge: "The pasted text exceeds the 1 MB limit."
        }
    }
}
public enum MagnetPaste {
    /// Validate the entire batch before any durable write. Never extract or reconstruct a URL.
    @concurrent public static func parse(_ text:String) async throws -> [MagnetLink] {
        try Task.checkCancellation()
        guard text.utf8.count <= PendingInbox.uriByteLimit else { throw MagnetPasteError.tooLarge }
        let lines = text.split(whereSeparator: { $0.isNewline }).map { String($0).trimmingCharacters(in:.whitespacesAndNewlines) }.filter { !$0.isEmpty }
        guard !lines.isEmpty else { throw MagnetPasteError.empty }; guard lines.count <= 16 else { throw MagnetPasteError.tooMany }
        return try lines.map { try Task.checkCancellation(); return try MagnetLink($0) }
    }
}
