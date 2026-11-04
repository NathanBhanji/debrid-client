import Foundation

public enum SubmissionFailure: Error, Equatable, Sendable {
    case activeTorrent(String), keyMismatch, removed
}
public enum SubmissionPayload: Sendable { case magnet(MagnetLink), torrent(TorrentFile) }
public protocol TransferSubmissionAPI: Sendable {
    var serverURL:URL { get }
    var isPreview:Bool { get }
    func health() async throws -> HealthDTO
    func accounts() async throws -> [AccountDTO]
    func settings() async throws -> SettingsDTO
    func submit(_ payload:SubmissionPayload,options:SubmissionOptions,key:UUID) async throws -> TorrentDTO
    func submissionReceipt(key:UUID) async throws -> SubmissionReceiptDTO?
}
extension TransferSubmissionAPI { public var isPreview:Bool { false } }
extension DebridAPIClient: TransferSubmissionAPI {
    @concurrent public func submit(_ payload:SubmissionPayload,options:SubmissionOptions,key:UUID) async throws -> TorrentDTO {
        try options.validate(); guard SubmissionIdentity.validKey(key) else { throw SubmissionIntentError.invalid }
        let headers = ["Idempotency-Key":key.uuidString]
        switch payload {
        case .magnet(let magnet):
            struct Body:Encodable { let magnet:String; let account:String; let category:String; let settings:TorrentSettingsDTO }
            let body = try SubmissionIdentity.encoder().encode(Body(magnet:magnet.rawURI,account:options.accountID,category:options.category,settings:options.settings))
            return try await send(["torrents"],method:"POST",body:body,headers:headers,expectedStatus:201,submissionErrors:true)
        case .torrent(let file):
            let boundary = "Debrid-"+UUID().uuidString
            var body = Data()
            func field(_ name:String,_ value:String) { body.append(Data("--\(boundary)\r\nContent-Disposition: form-data; name=\"\(name)\"\r\n\r\n\(value)\r\n".utf8)) }
            field("account",options.accountID); field("category",options.category)
            field("settings",String(decoding:try SubmissionIdentity.encoder().encode(options.settings),as:UTF8.self))
            body.append(Data("--\(boundary)\r\nContent-Disposition: form-data; name=\"file\"; filename=\"transfer.torrent\"\r\nContent-Type: application/x-bittorrent\r\n\r\n".utf8))
            body.append(file.data); body.append(Data("\r\n--\(boundary)--\r\n".utf8))
            return try await send(["torrents","file"],method:"POST",body:body,contentType:"multipart/form-data; boundary=\(boundary)",headers:headers,expectedStatus:201,submissionErrors:true)
        }
    }
    public func submissionReceipt(key:UUID) async throws -> SubmissionReceiptDTO? {
        guard SubmissionIdentity.validKey(key) else { throw SubmissionIntentError.invalid }
        do { return try await send(["torrent-submissions",key.uuidString],method:"GET",expectedStatus:200) }
        catch APIError.http(status:404) { return nil }
    }
}
