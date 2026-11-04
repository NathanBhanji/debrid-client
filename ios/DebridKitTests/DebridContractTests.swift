import Foundation
import Testing
@testable import DebridKit

private final class ContractBundle: NSObject {}
private struct WireContract: Decodable {
    let health: HealthDTO
    let authStatus: AuthStatusDTO
    let identity: IdentityDTO
    let torrent: TorrentDTO
    let account: AccountDTO
    let settings: SettingsDTO
    let systemStatus: SystemStatusDTO
    let retryReview:RetryReviewDTO
    let retryAcceptance:RetryAcceptanceDTO
}

struct DebridContractTests {
    private func fixture() throws -> Data {
        let url = try #require(Bundle(for:ContractBundle.self).url(forResource:"contract",withExtension:"json",subdirectory:"ios-api"))
        return try Data(contentsOf:url)
    }
    @Test func decodesGoProducedWireShapes() throws {
        let contract = try DebridAPIClient.decoder().decode(WireContract.self, from:fixture())
        #expect(contract.health.ok)
        #expect(contract.retryReview.resourceId==contract.torrent.id && contract.retryReview.kind=="torrent")
        #expect(contract.retryReview.allowed && contract.retryReview.waitForSelection && contract.retryReview.revision.utf8.count==64)
        #expect(contract.retryAcceptance.kind=="download" && contract.retryAcceptance.resourceId==contract.torrent.downloads[0].id)
        #expect(contract.retryAcceptance.torrentId==contract.torrent.id && contract.retryAcceptance.torrentExists)
        #expect(!contract.authStatus.configured)
        #expect(contract.identity.apiKey)
        #expect(contract.torrent.accountId == contract.account.id)
        #expect(contract.torrent.size == 10_000_000_000)
        #expect(contract.torrent.downloads[0].bytesDone == 6_400_000_000)
        #expect(contract.torrent.files[0].selected)
        #expect(contract.torrent.completedAt == nil)
        #expect(contract.account.user == nil)
        #expect(contract.settings.torrentDefaults.organize == nil)
        #expect(contract.settings.categories == ["Documentaries", "Audio"])
        #expect(contract.systemStatus.diskTotalBytes == 1_000_000_000_000)
        #expect(contract.torrent.addedAt.timeIntervalSince1970 > 1_700_000_000)
        #expect(contract.torrent.downloads[0].startedAt != nil)
    }

    @Test func toleratesFutureStatusAndUnknownFieldsButNotBrokenRequiredArrays() throws {
        let data = try fixture()
        var root = try #require(JSONSerialization.jsonObject(with:data) as? [String:Any])
        var torrent = try #require(root["torrent"] as? [String:Any])
        torrent["status"] = "future_provider_stage"
        torrent["future_field"] = true
        root["torrent"] = torrent
        let future = try DebridAPIClient.decoder().decode(WireContract.self,from:JSONSerialization.data(withJSONObject:root))
        #expect(future.torrent.status == "future_provider_stage")
        torrent["files"] = NSNull()
        root["torrent"] = torrent
        #expect(throws:DecodingError.self) {
            try DebridAPIClient.decoder().decode(WireContract.self,from:JSONSerialization.data(withJSONObject:root))
        }
    }

    @Test func acceptsBothRFC3339TimestampPrecisionsAndOptionalNulls() throws {
        struct Dates: Decodable { let at: Date; let optional: Date? }
        let decoder = DebridAPIClient.decoder()
        for raw in ["2026-01-02T03:04:05Z", "2026-01-02T03:04:05.123456789Z"] {
            let data = Data("{\"at\":\"\(raw)\",\"optional\":null}".utf8)
            let dates = try decoder.decode(Dates.self,from:data)
            #expect(dates.optional == nil)
        }
        #expect(throws:(any Error).self) { try decoder.decode(Dates.self,from:Data(#"{"at":"invalid"}"#.utf8)) }
    }
}
