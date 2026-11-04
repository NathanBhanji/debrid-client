#if DEBUG
import DebridKit
import SwiftUI

private actor ManagementFixtureAPI:ManagementAPI {
    nonisolated let serverURL:URL
    nonisolated let isPreview=true
    private var dropFirst:Bool
    private var removed=false
    private var receipts:[UUID:CancellationAcceptanceDTO]=[:]
    init(serverURL:URL,uncertain:Bool){self.serverURL=serverURL;dropFirst=uncertain}
    func health() async throws -> HealthDTO {HealthDTO(ok:true,version:"fixture",capabilities:["native_cancellation_v1","native_removal_precondition_v1"])}
    private func decode<T:Decodable>(_ type:T.Type,_ object:[String:Any])throws->T {
        let decoder=JSONDecoder();decoder.keyDecodingStrategy = .convertFromSnakeCase;decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(type,from:JSONSerialization.data(withJSONObject:object))
    }
    func cancellationReview(id:String) async throws -> CancellationReviewDTO {try decode(CancellationReviewDTO.self,["torrent_id":id,"revision":String(repeating:"a",count:64),"allowed":true,"provider_may_continue":true])}
    func requestCancellation(id:String,key:UUID,revision:String) async throws -> CancellationAcceptanceDTO {
        if let receipt=receipts[key]{return receipt}
        let receipt=try decode(CancellationAcceptanceDTO.self,["torrent_id":id,"accepted_at":"2026-01-02T03:04:05Z","torrent_exists":true]);receipts[key]=receipt
        if dropFirst{dropFirst=false;throw APIError.connection(.timedOut)}
        return receipt
    }
    func cancellationAcceptance(id:String,key:UUID,revision:String) async throws -> CancellationAcceptanceDTO? {receipts[key]}
    func removalReview(id:String) async throws -> RemovalReviewDTO {
        if removed{return try decode(RemovalReviewDTO.self,["torrent_id":id,"exists":false,"provider_copy_known":false])}
        return try decode(RemovalReviewDTO.self,["torrent_id":id,"exists":true,"revision":String(repeating:"a",count:64),"provider_copy_known":true])
    }
    func removeTransfer(id:String,revision:String,options:RemovalOptions) async throws {
        removed=true
        if dropFirst{dropFirst=false;throw APIError.connection(.timedOut)}
    }
}
struct ManagementFixtureRoot:View {
    let kind:ManagementAction
    let uncertain:Bool
    @State private var model:ManagementModel?
    @State private var failed=false
    var body:some View {
        Group {
            if let model {ManagementSheet(model:model,connectionID:model.connectionID)}
            else if failed {ContentUnavailableView("Fixture unavailable",systemImage:"viewfinder")}
            else {ProgressView("Preparing fictional transfer…")}
        }.task {
            guard model==nil else{return}
            do {
                let profile=try ServerProfile(server:ServerAddress("https://debrid.example.test"))
                let server=AuthenticatedServer(profile:profile,username:"Sample User",credential:try .session("fixture_unused"))
                guard let store=try openPendingInboxForNativeFixture() as? any ManagementIntentStoring else{failed=true;return}
                model=try ManagementModel(server:server,kind:kind,resourceID:"sample-management",name:"The Open Ocean · Season 1",api:ManagementFixtureAPI(serverURL:profile.serverURL,uncertain:uncertain),store:store)
            }catch{failed=true}
        }
    }
}
#endif
