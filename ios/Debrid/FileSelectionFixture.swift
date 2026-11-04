#if DEBUG
import DebridKit
import SwiftUI

/// Reachable only from the validated UUID intake namespace in Debug.
private actor FileSelectionFixtureAPI:FileSelectionAPI {
    nonisolated let serverURL:URL
    nonisolated let isPreview=true
    private var accepted:[String] = []
    private var dropFirst:Bool
    init(serverURL:URL,uncertain:Bool) {self.serverURL=serverURL;dropFirst=uncertain}
    func health() async throws -> HealthDTO {HealthDTO(ok:true,version:"fixture",capabilities:["manual_file_selection_v1"])}
    func fileSelection(id:String) async throws -> FileSelectionDTO {
        let data:[String:Any] = ["torrent_id":id,"state":accepted.isEmpty ? "ready" : "accepted","revision":String(repeating:"a",count:64),"selected_ids":accepted,"provider_selects":true,"message":"","files":[
            ["id":"episode-1","path":"Season 1/01 — The Open Ocean.mkv","size":4_831_838_208,"selected":false],
            ["id":"episode-2","path":"Season 1/02 — Coastal Waters.mkv","size":4_294_967_296,"selected":false],
            ["id":"subtitle-1","path":"Season 1/Subtitles/01 — English.srt","size":84_992,"selected":false],
            ["id":"making-of","path":"Extras/Making of The Open Ocean.mkv","size":938_475_520,"selected":false],
            ["id":"readme","path":"Read me.txt","size":2048,"selected":false]
        ]]
        let decoder=JSONDecoder();decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try decoder.decode(FileSelectionDTO.self,from:JSONSerialization.data(withJSONObject:data))
    }
    func confirmFileSelection(id:String,revision:String,fileIDs:[String]) async throws -> FileSelectionDTO {
        accepted=fileIDs
        if dropFirst {dropFirst=false;throw APIError.connection(.timedOut)}
        return try await fileSelection(id:id)
    }
}
struct SelectionFixtureRoot:View {
    let uncertain:Bool
    @State private var model:FileSelectionModel?
    @State private var failed=false
    var body:some View {
        Group {
            if let model {FileSelectionSheet(model:model)}
            else if failed {ContentUnavailableView("Fixture unavailable",systemImage:"viewfinder")}
            else {ProgressView("Preparing fictional files…")}
        }.task {
            guard model==nil else {return}
            do {
                let profile=try ServerProfile(server:ServerAddress("https://debrid.example.test"))
                let server=AuthenticatedServer(profile:profile,username:"Sample User",credential:try .session("fixture_unused"))
                // The same Debug namespace factory as magnet/torrent UI tests.
                guard let store=try openPendingInboxForSelectionFixture() else {failed=true;return}
                model=try FileSelectionModel(server:server,transferID:"sample-selection",name:"The Open Ocean · Season 1",api:FileSelectionFixtureAPI(serverURL:profile.serverURL,uncertain:uncertain),store:store)
            } catch {failed=true}
        }
    }
}
#endif
