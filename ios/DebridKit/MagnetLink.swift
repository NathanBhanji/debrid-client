import Foundation

public enum MagnetError: Error, Equatable, Sendable, LocalizedError {
    case tooLarge, invalidScheme, invalidEncoding, invalidCharacters, tooManyParameters
    case missingV1Hash, unsupportedV2Only, invalidHash, invalidV2Hash, duplicateHash
    public var errorDescription: String? {
        switch self {
        case .tooLarge: "This magnet link exceeds the 64 KB limit."
        case .invalidScheme: "Use a complete magnet link beginning with magnet:?."
        case .invalidEncoding: "The magnet link contains invalid percent encoding or text."
        case .invalidCharacters: "Use an encoded magnet link without control characters, raw spaces, fragments or semicolons."
        case .tooManyParameters: "This magnet link has too many parameters."
        case .missingV1Hash: "The magnet link needs a BitTorrent v1 info hash."
        case .unsupportedV2Only: "This server requires a v1 info hash. A v2-only magnet is not supported."
        case .invalidHash: "The magnet link has an invalid v1 info hash."
        case .invalidV2Hash: "The magnet link has an unsupported or invalid v2 hash."
        case .duplicateHash: "The magnet link contains more than one hash of the same type."
        }
    }
}

/// Validated intake metadata. Submission must use rawURI, never reconstruct a magnet from these fields.
public struct MagnetLink: Equatable, Sendable, CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    public static let byteLimit = 65_536
    public static let parameterLimit = 512
    public let rawURI: String
    public let infoHash: String
    public let displayName: String?
    public let trackerCount: Int
    public let isHybrid: Bool
    public var description: String { "MagnetLink(redacted)" }
    public var debugDescription: String { description }
    public var customMirror: Mirror { Mirror(self,children:["contents":"redacted"]) }

    public init(_ input: String) throws {
        guard input.utf8.count <= Self.byteLimit else { throw MagnetError.tooLarge }
        let raw = input.trimmingCharacters(in:Self.goWhitespace)
        guard raw.prefix(8).lowercased() == "magnet:?" else { throw MagnetError.invalidScheme }
        guard !raw.unicodeScalars.contains(where:{ $0.value <= 0x20 || (0x7f...0x9f).contains($0.value) }), !raw.contains("#"), !raw.contains(";") else { throw MagnetError.invalidCharacters }
        let fields = raw.dropFirst(8).split(separator:"&",maxSplits:Self.parameterLimit,omittingEmptySubsequences:false)
        guard fields.count <= Self.parameterLimit else { throw MagnetError.tooManyParameters }
        var hash: String?, hasV2 = false, name: String?, sawName = false, trackers = 0
        for field in fields where !field.isEmpty {
            let pair = field.split(separator:"=",maxSplits:1,omittingEmptySubsequences:false)
            let key = try Self.decode(pair[0]), value = try Self.decode(pair.count > 1 ? pair[1] : "")
            if key == "xt", value.hasPrefix("urn:btih:") {
                guard hash == nil else { throw MagnetError.duplicateHash }
                hash = try Self.v1(String(value.dropFirst(9)))
            } else if key == "xt", value.hasPrefix("urn:btmh:") {
                guard !hasV2 else { throw MagnetError.duplicateHash }
                let encoded = value.dropFirst(9)
                guard encoded.utf8.count == 68, encoded.hasPrefix("1220"), encoded.utf8.allSatisfy(Self.isHex) else { throw MagnetError.invalidV2Hash }
                hasV2 = true
            } else if key == "dn", !sawName {
                sawName = true
                let safe = value.unicodeScalars.filter { $0.properties.generalCategory != .format }.prefix(256)
                var display = ""; display.unicodeScalars.append(contentsOf:safe)
                let trimmed = display.trimmingCharacters(in:.whitespacesAndNewlines)
                name = trimmed.isEmpty ? nil : trimmed
            } else if key == "tr" { trackers += 1 }
        }
        guard let hash else { throw hasV2 ? MagnetError.unsupportedV2Only : MagnetError.missingV1Hash }
        rawURI = raw; infoHash = hash; displayName = name; trackerCount = trackers; isHybrid = hasV2
    }
    private static let goWhitespace = CharacterSet(charactersIn:"\u{0009}\u{000a}\u{000b}\u{000c}\u{000d} \u{0085}\u{00a0}\u{1680}\u{2000}\u{2001}\u{2002}\u{2003}\u{2004}\u{2005}\u{2006}\u{2007}\u{2008}\u{2009}\u{200a}\u{2028}\u{2029}\u{202f}\u{205f}\u{3000}")
    private static func isHex(_ byte:UInt8) -> Bool { (48...57).contains(byte) || (65...70).contains(byte) || (97...102).contains(byte) }
    private static func hex(_ byte:UInt8) -> UInt8? {
        switch byte {
        case 48...57: byte-48
        case 65...70: byte-55
        case 97...102: byte-87
        default: nil
        }
    }
    private static func decode(_ text:Substring) throws -> String {
        let input = Array(text.utf8)
        var output: [UInt8] = []; output.reserveCapacity(input.count)
        var offset = 0
        while offset < input.count {
            let byte = input[offset]
            if byte == 37 {
                guard offset+2 < input.count, let high = hex(input[offset+1]), let low = hex(input[offset+2]) else { throw MagnetError.invalidEncoding }
                output.append(high*16+low); offset += 3
            } else { output.append(byte == 43 ? 32 : byte); offset += 1 }
        }
        guard let value = String(bytes:output,encoding:.utf8) else { throw MagnetError.invalidEncoding }
        guard !value.unicodeScalars.contains(where:{ $0.value < 0x20 || (0x7f...0x9f).contains($0.value) }) else { throw MagnetError.invalidCharacters }
        return value
    }
    private static func v1(_ encoded:String) throws -> String {
        let bytes = Array(encoded.utf8)
        if bytes.count == 40, bytes.allSatisfy(isHex) { return encoded.lowercased() }
        guard bytes.count == 32 else { throw MagnetError.invalidHash }
        var output: [UInt8] = [], buffer: UInt32 = 0, bits = 0
        for byte in bytes {
            let value: UInt32
            switch byte {
            case 65...90: value = UInt32(byte-65)
            case 50...55: value = UInt32(byte-24)
            default: throw MagnetError.invalidHash
            }
            buffer = (buffer << 5) | value; bits += 5
            if bits >= 8 { bits -= 8; output.append(UInt8((buffer >> bits) & 255)); buffer &= (1 << bits)-1 }
        }
        guard output.count == 20, bits == 0 else { throw MagnetError.invalidHash }
        return output.map { String(format:"%02x",$0) }.joined()
    }
}
