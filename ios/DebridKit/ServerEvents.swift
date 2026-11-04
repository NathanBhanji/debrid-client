import Foundation

public struct SSEFrame: Equatable, Sendable {
    public let event: String
    public let data: String
}

/// Incremental UTF-8 SSE framing. No Foundation line sequence may accumulate an unbounded line.
public struct SSEParser: Sendable {
    private var line: [UInt8] = []
    private var event = "message"
    private var data: [String] = []
    private var frameBytes = 0
    private var hasData = false
    private var afterCR = false
    private var firstLine = true
    private let limit: Int
    public init(maximumFrameBytes: Int = 64 * 1024) { limit = max(1, min(maximumFrameBytes, 256 * 1024)) }
    public mutating func consume(_ byte: UInt8) throws -> SSEFrame? {
        if afterCR { afterCR = false; if byte == 10 { return nil } }
        if byte == 13 || byte == 10 {
            afterCR = byte == 13
            return try finishLine()
        }
        guard line.count < limit, frameBytes < limit else { throw APIError.responseTooLarge }
        line.append(byte); frameBytes += 1
        return nil
    }
    private mutating func finishLine() throws -> SSEFrame? {
        defer { line.removeAll(keepingCapacity: true) }
        if firstLine {
            firstLine = false
            if line.starts(with: [0xef,0xbb,0xbf]) { line.removeFirst(3) }
        }
        guard let text = String(bytes: line, encoding: .utf8) else { throw APIError.invalidResponse }
        if text.isEmpty {
            let frame = hasData ? SSEFrame(event: event, data: data.joined(separator: "\n")) : nil
            event = "message"; data.removeAll(keepingCapacity: true); hasData = false; frameBytes = 0
            return frame
        }
        if text.hasPrefix(":") { frameBytes -= line.count; return nil }
        let parts = text.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
        let field = String(parts[0])
        var value = parts.count == 2 ? String(parts[1]) : ""
        if value.hasPrefix(" ") { value.removeFirst() }
        switch field {
        case "event": event = value.isEmpty ? "message" : value
        case "data": data.append(value); hasData = true
        default: break // No replay cursor: this server's bus drops slow-consumer events.
        }
        return nil
    }
    // An unterminated final event is deliberately discarded at EOF per SSE framing rules.
}

public enum ServerEvent: Equatable, Sendable { case connected, changed(String) }
public protocol ServerEventStreaming: Sendable {
    func events() -> AsyncThrowingStream<ServerEvent, any Error>
}
