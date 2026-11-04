#if DEBUG
import DebridKit
import Foundation

/// Selected only with a validated UUID fixture namespace. It cannot contact a server.
@MainActor final class SubmissionFixtureAPI:TransferSubmissionAPI {
    nonisolated let serverURL:URL
    nonisolated let isPreview=true
    private var dropFirst:Bool
    private var responses:[UUID:Data]=[:]
    init(serverURL:URL,dropFirst:Bool) { self.serverURL=serverURL;self.dropFirst=dropFirst }
    @MainActor func health() async throws -> HealthDTO { HealthDTO(ok:true,version:"fixture",capabilities:["torrent_submissions_v1","multipart_torrent_settings_v1"]) }
    @MainActor func accounts() async throws -> [AccountDTO] { try await LiveTransfersPreviewAPI(state:.filled).accounts() }
    private func decode<T:Decodable>(_ type:T.Type,_ value:Any) throws -> T {
        let decoder=JSONDecoder();decoder.keyDecodingStrategy = .convertFromSnakeCase;decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(type,from:JSONSerialization.data(withJSONObject:value))
    }
    @MainActor func settings() async throws -> SettingsDTO {
        try decode(SettingsDTO.self,["torrent_defaults":["min_file_size":1_048_576,"include_regex":"mkv$","finished_action":"keep","download_retries":3,"torrent_retries":1,"unpack":true,"delete_on_error":"24h0m0s","lifetime":"72h0m0s"],"categories":["Documentaries","Audio"],"unpack_max_depth":1,"organize":["enabled":false]])
    }
    @MainActor func submit(_ payload:SubmissionPayload,options:SubmissionOptions,key:UUID) async throws -> TorrentDTO {
        if let response=responses[key] {
            let decoder=JSONDecoder();decoder.keyDecodingStrategy = .convertFromSnakeCase;decoder.dateDecodingStrategy = .iso8601
            return try decoder.decode(TorrentDTO.self,from:response)
        }
        let hash:String,name:String
        switch payload { case .magnet(let link):hash=link.infoHash;name=link.displayName ?? "Sample transfer";case .torrent(let file):hash=file.summary.infoHash;name=file.summary.displayName }
        let settings=try JSONSerialization.jsonObject(with:SubmissionIdentity.encoder().encode(options.settings))
        let value:[String:Any]=["id":"sample-provider","account_id":options.accountID,"hash":hash,"name":name,"category":options.category,"status":"queued","provider_progress":0,"local_progress":0,"size":0,"speed":0,"seeders":0,"files":[],"settings":settings,"retry_count":0,"added_at":"2026-01-02T03:04:05Z","downloads":[]]
        let data=try JSONSerialization.data(withJSONObject:value);responses[key]=data
        if dropFirst { dropFirst=false;throw APIError.connection(.timedOut) }
        return try decode(TorrentDTO.self,value)
    }
    @MainActor func submissionReceipt(key:UUID) async throws -> SubmissionReceiptDTO? {
        guard responses[key] != nil else { return nil }
        return try decode(SubmissionReceiptDTO.self,["torrent_id":"sample-provider","accepted_at":"2026-01-02T03:04:05Z","torrent_exists":true])
    }
}
#endif
