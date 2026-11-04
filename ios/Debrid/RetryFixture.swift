#if DEBUG
import DebridKit
import SwiftUI

private actor RetryFixtureAPI:RetryAPI {
    nonisolated let serverURL:URL
    nonisolated let isPreview=true
    private var dropFirst:Bool
    private var receipts:[UUID:RetryAcceptanceDTO]=[:]
    init(serverURL:URL,uncertain:Bool) {self.serverURL=serverURL;dropFirst=uncertain}
    func health() async throws -> HealthDTO {HealthDTO(ok:true,version:"fixture",capabilities:["native_retry_v1"])}
    private func decode<T:Decodable>(_ type:T.Type,_ object:[String:Any]) throws -> T {
        let decoder=JSONDecoder();decoder.keyDecodingStrategy = .convertFromSnakeCase;decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(type,from:JSONSerialization.data(withJSONObject:object))
    }
    func retryReview(kind:RetryResourceKind,id:String) async throws -> RetryReviewDTO {
        try decode(RetryReviewDTO.self,["kind":kind.rawValue,"resource_id":id,"torrent_id":"sample-retry","revision":String(repeating:"a",count:64),"allowed":true,"wait_for_selection":kind == .torrent])
    }
    func requestRetry(kind:RetryResourceKind,id:String,key:UUID,revision:String) async throws -> RetryAcceptanceDTO {
        if let receipt=receipts[key] {return receipt}
        let receipt=try decode(RetryAcceptanceDTO.self,["kind":kind.rawValue,"resource_id":id,"torrent_id":"sample-retry","accepted_at":"2026-01-02T03:04:05Z","torrent_exists":true])
        receipts[key]=receipt
        if dropFirst {dropFirst=false;throw APIError.connection(.timedOut)}
        return receipt
    }
    func retryAcceptance(kind:RetryResourceKind,id:String,key:UUID,revision:String) async throws -> RetryAcceptanceDTO? {receipts[key]}
}
/// The root selects this only with a validated UUID namespace in Debug.
struct RetryFixtureRoot:View {
    let kind:RetryResourceKind
    let uncertain:Bool
    @State private var model:RetryModel?
    @State private var failed=false
    var body:some View {
        Group {
            if let model {RetrySheet(model:model,connectionID:model.connectionID)}
            else if failed {ContentUnavailableView("Fixture unavailable",systemImage:"viewfinder")}
            else {ProgressView("Preparing fictional retry…")}
        }.task {
            guard model==nil else {return}
            do {
                let profile=try ServerProfile(server:ServerAddress("https://debrid.example.test"))
                let server=AuthenticatedServer(profile:profile,username:"Sample User",credential:try .session("fixture_unused"))
                guard let store=try openPendingInboxForNativeFixture() as? any RetryIntentStoring else {failed=true;return}
                model=try RetryModel(server:server,kind:kind,resourceID:kind == .torrent ? "sample-retry" : "sample-file",torrentID:"sample-retry",name:kind == .torrent ? "The Open Ocean · Season 1" : "01 — The Open Ocean.mkv",api:RetryFixtureAPI(serverURL:profile.serverURL,uncertain:uncertain),store:store)
            } catch {failed=true}
        }
    }
}
#endif
