import Foundation

public struct SettingsReviewDTO: Decodable, Sendable {
    public let settings: SettingsDTO
    public let revision: String
    public init(settings: SettingsDTO, revision: String) { self.settings = settings; self.revision = revision }
}
public protocol SettingsReviewAPI: Sendable {
    var serverURL: URL { get }
    func health() async throws -> HealthDTO
    func settingsReview() async throws -> SettingsReviewDTO
    func updateSettings(_ settings: SettingsDTO, revision: String) async throws -> SettingsReviewDTO
}
extension DebridAPIClient: SettingsReviewAPI {
    public func settingsReview() async throws -> SettingsReviewDTO {
        let view: SettingsReviewDTO = try await send(["settings", "review"], method: "GET", expectedStatus: 200)
        guard FileSelectionIdentity.revisionIsValid(view.revision) else { throw APIError.invalidResponse }
        return view
    }
    public func updateSettings(_ settings: SettingsDTO, revision: String) async throws -> SettingsReviewDTO {
        guard FileSelectionIdentity.revisionIsValid(revision) else { throw APIError.invalidConfiguration }
        let encoder = JSONEncoder(); encoder.keyEncodingStrategy = .convertToSnakeCase
        let view: SettingsReviewDTO = try await send(["settings", "review"], method: "PUT", body: encoder.encode(settings),
                                                      headers: ["If-Match": "\"" + revision + "\""], expectedStatus: 200)
        guard FileSelectionIdentity.revisionIsValid(view.revision), view.revision != revision else { throw APIError.invalidResponse }
        return view
    }
}
