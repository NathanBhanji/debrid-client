import Foundation

/// Presentation fixtures, deliberately separate from future API DTOs.
public struct TransferPreview: Identifiable, Sendable {
    public let id: String
    public let name: String
    public let category: String
    public let phase: Phase
    public let fraction: Double
    public let detail: String

    public enum Phase: String, Sendable {
        case downloading = "Downloading to NAS"
        case selecting = "Choose files"
        case completed = "On your NAS"
        case failed = "Needs attention"
    }

    public static let examples: [Self] = [
        .init(id: "ocean", name: "The Open Ocean", category: "Documentaries", phase: .downloading,
              fraction: 0.64, detail: "6.4 GB of 10 GB · TorBox"),
        .init(id: "archive", name: "Field Recordings · Vol. 02", category: "Audio", phase: .selecting,
              fraction: 0, detail: "12 files · Real-Debrid"),
        .init(id: "earth", name: "Earth from Above", category: "Documentaries", phase: .completed,
              fraction: 1, detail: "4.2 GB · TorBox")
    ]
}
