import CryptoKit
import Foundation

public enum TorrentFileError: Error, Equatable, Sendable, LocalizedError {
    case tooLarge, invalid, unsupportedV2, unavailable
    public var errorDescription: String? {
        switch self {
        case .tooLarge: "This torrent metadata file exceeds the 8 MB limit."
        case .invalid: "This file contains invalid or unsupported torrent metadata."
        case .unsupportedV2: "This server requires v1 torrent metadata. A v2-only torrent is not supported."
        case .unavailable: "The selected file could not be read. Try importing it again from Files."
        }
    }
}
public struct TorrentSummary: Codable, Equatable, Sendable {
    public let infoHash: String
    public let displayName: String
    public let totalBytes: Int64
    public let fileCount: Int
    public let isHybrid: Bool
}
/// Original bytes are explicit private input, never reconstructed from the summary or logged.
public struct TorrentFile: Equatable, Sendable, CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public static let byteLimit = 8*1024*1024
    public let data: Data
    public let summary: TorrentSummary
    public let digest: String
    public var description: String { "TorrentFile(redacted)" }
    public var debugDescription: String { description }
    public var customMirror: Mirror { Mirror(self,children:["contents":"redacted"]) }
    @concurrent public static func parse(_ data:Data) async throws -> TorrentFile { try Task.checkCancellation(); return try TorrentFile(data) }
    public init(_ data:Data) throws {
        guard data.count <= Self.byteLimit else { throw TorrentFileError.tooLarge }
        var decoder = Bencode(data)
        guard case .dictionary(let root) = try decoder.value(depth:0), decoder.position == decoder.bytes.count,
              case .dictionary(let info)? = root["info"], let range = decoder.infoRange else { throw TorrentFileError.invalid }
        let version = try info.integer("meta version") ?? 0
        guard version == 0 || version == 1 || version == 2 else { throw TorrentFileError.invalid }
        if version == 2 && info["pieces"] == nil { throw TorrentFileError.unsupportedV2 }
        guard let pieceLength = try info.integer("piece length"), pieceLength > 0,
              case .bytes(let pieces)? = info["pieces"], pieces.count % 20 == 0 else { throw TorrentFileError.invalid }
        let utf8Name = try decoder.text(info["name.utf-8"])
        let name = try (utf8Name?.isEmpty == false ? utf8Name : decoder.text(info["name"])) ?? ""
        guard !name.isEmpty else { throw TorrentFileError.invalid }
        var size: Int64 = 0, count = 0
        if case .list(let files)? = info["files"] {
            guard info["length"] == nil, !files.isEmpty, files.count <= 20_000 else { throw TorrentFileError.invalid }
            for file in files {
                try Task.checkCancellation()
                guard case .dictionary(let fields) = file, let length = try fields.integer("length"), length >= 0 else { throw TorrentFileError.invalid }
                let utf8Path = fields["path.utf-8"]
                let path: BValue?
                if let utf8Path {
                    guard case .list(let values) = utf8Path else { throw TorrentFileError.invalid }
                    path = values.isEmpty ? fields["path"] : utf8Path
                } else { path = fields["path"] }
                guard case .list(let components)? = path, !components.isEmpty, components.count <= 128 else { throw TorrentFileError.invalid }
                for component in components {
                    guard let part = try decoder.text(component), !part.isEmpty, part != ".", part != "..", !part.contains(where:{ "/\\\0".contains($0) }) else { throw TorrentFileError.invalid }
                }
                let addition = size.addingReportingOverflow(length); guard !addition.overflow else { throw TorrentFileError.invalid }; size = addition.partialValue; count += 1
            }
        } else {
            guard info["files"] == nil, let length = try info.integer("length"), length >= 0 else { throw TorrentFileError.invalid }
            size = length; count = 1
        }
        let expected = size/pieceLength + (size%pieceLength == 0 ? 0 : 1)
        guard expected == Int64(pieces.count/20) else { throw TorrentFileError.invalid }
        if version == 2 {
            guard case .dictionary(let tree)? = info["file tree"] else { throw TorrentFileError.invalid }
            var total: Int64 = 0, leaves = 0
            try Self.treeSize(tree,total:&total,leaves:&leaves)
            guard leaves > 0 else { throw TorrentFileError.invalid }; size = total
        }
        let hash = Insecure.SHA1.hash(data:Data(decoder.bytes[range])).map { String(format:"%02x",$0) }.joined()
        let safeName = name.unicodeScalars.filter { $0.properties.generalCategory != .format && $0.properties.generalCategory != .control }.prefix(256)
        var display = ""; display.unicodeScalars.append(contentsOf:safeName)
        self.data = data
        summary = TorrentSummary(infoHash:hash,displayName:display.isEmpty ? "Torrent file" : display,totalBytes:size,fileCount:count,isHybrid:version == 2)
        digest = SHA256.hash(data:data).map { String(format:"%02x",$0) }.joined()
    }
    private static func treeSize(_ tree:[Data:BValue],total:inout Int64,leaves:inout Int) throws {
        for (key,value) in tree {
            try Task.checkCancellation()
            guard case .dictionary(let child) = value else { throw TorrentFileError.invalid }
            if key.isEmpty {
                guard let length = try child.integer("length"), length >= 0 else { throw TorrentFileError.invalid }
                let sum = total.addingReportingOverflow(length); guard !sum.overflow else { throw TorrentFileError.invalid }
                total = sum.partialValue; leaves += 1
            } else {
                guard let name = String(data:key,encoding:.utf8), !name.contains(where:{ "/\\\0".contains($0) }), name != ".", name != ".." else { throw TorrentFileError.invalid }
                try treeSize(child,total:&total,leaves:&leaves)
            }
        }
    }
}
private indirect enum BValue {
    case integer(Int64), bytes(Range<Int>), list([BValue]), dictionary([Data:BValue])
}
private extension Dictionary where Key == Data, Value == BValue {
    subscript(_ key:String) -> BValue? { self[Data(key.utf8)] }
    func integer(_ key:String) throws -> Int64? {
        guard let value = self[key] else { return nil }
        guard case .integer(let number) = value else { throw TorrentFileError.invalid }; return number
    }
}
private struct Bencode {
    let bytes: [UInt8]
    var position = 0
    var nodes = 0
    var infoRange: Range<Int>?
    init(_ data:Data) { bytes = Array(data) }
    mutating func value(depth:Int) throws -> BValue {
        try Task.checkCancellation(); nodes += 1
        guard depth <= 32, nodes <= 100_000, position < bytes.count else { throw TorrentFileError.invalid }
        switch bytes[position] {
        case 105:
            position += 1; let start = position
            while position < bytes.count && bytes[position] != 101 { position += 1; guard position-start <= 20 else { throw TorrentFileError.invalid } }
            guard position < bytes.count else { throw TorrentFileError.invalid }
            let token = String(decoding:bytes[start..<position],as:UTF8.self); position += 1
            let digits = token.hasPrefix("-") ? token.dropFirst() : token[...]
            guard !digits.isEmpty, digits.utf8.allSatisfy({ (48...57).contains($0) }), !(digits.count > 1 && digits.first == "0"), token != "-0", let number = Int64(token) else { throw TorrentFileError.invalid }
            return .integer(number)
        case 108:
            position += 1; var values: [BValue] = []
            while position < bytes.count && bytes[position] != 101 { values.append(try value(depth:depth+1)) }
            guard position < bytes.count else { throw TorrentFileError.invalid }; position += 1; return .list(values)
        case 100:
            position += 1; var values: [Data:BValue] = [:], previous: [UInt8]?
            while position < bytes.count && bytes[position] != 101 {
                let range = try string(), keyBytes = Array(bytes[range])
                let key = Data(keyBytes)
                guard keyBytes.count <= 256, values[key] == nil,
                      previous.map({ $0.lexicographicallyPrecedes(keyBytes) }) ?? true else { throw TorrentFileError.invalid }
                previous = keyBytes; let start = position
                values[key] = try value(depth:depth+1)
                if depth == 0 && key == Data("info".utf8) { infoRange = start..<position }
            }
            guard position < bytes.count else { throw TorrentFileError.invalid }; position += 1; return .dictionary(values)
        case 48...57: return .bytes(try string())
        default: throw TorrentFileError.invalid
        }
    }
    mutating func string() throws -> Range<Int> {
        let start = position
        while position < bytes.count && bytes[position] != 58 { guard (48...57).contains(bytes[position]), position-start < 10 else { throw TorrentFileError.invalid }; position += 1 }
        guard position < bytes.count, position > start, !(position-start > 1 && bytes[start] == 48), let length = Int(String(decoding:bytes[start..<position],as:UTF8.self)) else { throw TorrentFileError.invalid }
        position += 1; guard length <= bytes.count-position else { throw TorrentFileError.invalid }
        let range = position..<(position+length); position += length; return range
    }
    func text(_ value:BValue?) throws -> String? {
        guard let value else { return nil }
        guard case .bytes(let range) = value, range.count <= 4096, let text = String(bytes:bytes[range],encoding:.utf8) else { throw TorrentFileError.invalid }; return text
    }
}
