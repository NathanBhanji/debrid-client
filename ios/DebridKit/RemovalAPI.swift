import Foundation

public protocol RemovalAPI:Sendable {
    var serverURL:URL {get}
    var isPreview:Bool {get}
    func health() async throws -> HealthDTO
    func removalReview(id:String) async throws -> RemovalReviewDTO
    func removeTransfer(id:String,revision:String,options:RemovalOptions) async throws
}
extension RemovalAPI {public var isPreview:Bool {false}}
extension DebridAPIClient:RemovalAPI {
    public func removalReview(id:String) async throws -> RemovalReviewDTO {
        guard SubmissionIdentity.validID(id) else {throw APIError.invalidConfiguration}
        let result:RemovalReviewDTO=try await send(["torrents",id,"removal-review"],method:"GET",expectedStatus:200)
        guard result.torrentId==id else {throw APIError.invalidResponse}
        if result.exists {
            guard let revision=result.revision,FileSelectionIdentity.revisionIsValid(revision) else {throw APIError.invalidResponse}
        } else {
            guard result.revision==nil,!result.providerCopyKnown else {throw APIError.invalidResponse}
        }
        return result
    }
    public func removeTransfer(id:String,revision:String,options:RemovalOptions) async throws {
        guard SubmissionIdentity.validID(id),FileSelectionIdentity.revisionIsValid(revision) else {throw APIError.invalidConfiguration}
        struct Removed:Decodable,Sendable {}
        let _:Removed=try await send(["torrents",id,"removal"],method:"DELETE",allowsEmpty:true,headers:["If-Match":"\""+revision+"\""],expectedStatus:204,query:[URLQueryItem(name:"files",value:options.deleteFiles ? "true":"false"),URLQueryItem(name:"provider",value:options.deleteFromProvider ? "true":"false")])
    }
}
