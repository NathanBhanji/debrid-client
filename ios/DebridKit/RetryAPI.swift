import Foundation

public protocol RetryAPI:Sendable {
    var serverURL:URL {get}
    var isPreview:Bool {get}
    func health() async throws -> HealthDTO
    func retryReview(kind:RetryResourceKind,id:String) async throws -> RetryReviewDTO
    func requestRetry(kind:RetryResourceKind,id:String,key:UUID,revision:String) async throws -> RetryAcceptanceDTO
    func retryAcceptance(kind:RetryResourceKind,id:String,key:UUID,revision:String) async throws -> RetryAcceptanceDTO?
}
extension RetryAPI {public var isPreview:Bool {false}}
extension DebridAPIClient:RetryAPI {
    public func retryReview(kind:RetryResourceKind,id:String) async throws -> RetryReviewDTO {
        guard SubmissionIdentity.validID(id) else {throw APIError.invalidConfiguration}
        let result:RetryReviewDTO=try await send([kind.collection,id,"retry-review"],method:"GET",expectedStatus:200)
        guard result.kind==kind.rawValue,result.resourceId==id,(kind != .torrent || result.torrentId==id),SubmissionIdentity.validID(result.torrentId),FileSelectionIdentity.revisionIsValid(result.revision) else {throw APIError.invalidResponse}
        return result
    }
    public func requestRetry(kind:RetryResourceKind,id:String,key:UUID,revision:String) async throws -> RetryAcceptanceDTO {
        try validateRetryInput(id:id,key:key,revision:revision)
        struct Body:Encodable {let key:String;let revision:String}
        let result:RetryAcceptanceDTO=try await send([kind.collection,id,"retry-requests"],method:"POST",body:JSONEncoder().encode(Body(key:key.uuidString.lowercased(),revision:revision)),expectedStatus:200,submissionErrors:true)
        return try validateRetryAcceptance(result,kind:kind,id:id)
    }
    public func retryAcceptance(kind:RetryResourceKind,id:String,key:UUID,revision:String) async throws -> RetryAcceptanceDTO? {
        try validateRetryInput(id:id,key:key,revision:revision)
        do {
            let result:RetryAcceptanceDTO=try await send([kind.collection,id,"retry-requests",key.uuidString.lowercased()],method:"GET",expectedStatus:200,submissionErrors:true,query:[URLQueryItem(name:"revision",value:revision)])
            return try validateRetryAcceptance(result,kind:kind,id:id)
        } catch APIError.http(status:404) {return nil}
    }
    private func validateRetryInput(id:String,key:UUID,revision:String) throws {
        guard SubmissionIdentity.validID(id),SubmissionIdentity.validKey(key),FileSelectionIdentity.revisionIsValid(revision) else {throw APIError.invalidConfiguration}
    }
    private func validateRetryAcceptance(_ value:RetryAcceptanceDTO,kind:RetryResourceKind,id:String) throws -> RetryAcceptanceDTO {
        guard value.kind==kind.rawValue,value.resourceId==id,(kind != .torrent || value.torrentId==id),SubmissionIdentity.validID(value.torrentId),value.acceptedAt.timeIntervalSince1970.isFinite else {throw APIError.invalidResponse}
        return value
    }
}
