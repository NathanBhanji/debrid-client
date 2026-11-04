import Foundation

public protocol CancellationAPI:Sendable {
    var serverURL:URL {get}
    var isPreview:Bool {get}
    func health() async throws -> HealthDTO
    func cancellationReview(id:String) async throws -> CancellationReviewDTO
    func requestCancellation(id:String,key:UUID,revision:String) async throws -> CancellationAcceptanceDTO
    func cancellationAcceptance(id:String,key:UUID,revision:String) async throws -> CancellationAcceptanceDTO?
}
extension CancellationAPI {public var isPreview:Bool {false}}
extension DebridAPIClient:CancellationAPI {
    public func cancellationReview(id:String) async throws -> CancellationReviewDTO {
        guard SubmissionIdentity.validID(id) else {throw APIError.invalidConfiguration}
        let result:CancellationReviewDTO=try await send(["torrents",id,"cancellation-review"],method:"GET",expectedStatus:200)
        guard result.torrentId==id,result.providerMayContinue,FileSelectionIdentity.revisionIsValid(result.revision) else {throw APIError.invalidResponse}
        return result
    }
    public func requestCancellation(id:String,key:UUID,revision:String) async throws -> CancellationAcceptanceDTO {
        try validateCancellationInput(id:id,key:key,revision:revision)
        struct Body:Encodable {let key:String;let revision:String}
        let result:CancellationAcceptanceDTO=try await send(["torrents",id,"cancellation-requests"],method:"POST",body:JSONEncoder().encode(Body(key:key.uuidString.lowercased(),revision:revision)),expectedStatus:200,submissionErrors:true)
        return try validateCancellationAcceptance(result,id:id)
    }
    public func cancellationAcceptance(id:String,key:UUID,revision:String) async throws -> CancellationAcceptanceDTO? {
        try validateCancellationInput(id:id,key:key,revision:revision)
        do {
            let result:CancellationAcceptanceDTO=try await send(["torrents",id,"cancellation-requests",key.uuidString.lowercased()],method:"GET",expectedStatus:200,submissionErrors:true,query:[URLQueryItem(name:"revision",value:revision)])
            return try validateCancellationAcceptance(result,id:id)
        } catch APIError.http(status:404) {return nil}
    }
    private func validateCancellationInput(id:String,key:UUID,revision:String) throws {
        guard SubmissionIdentity.validID(id),SubmissionIdentity.validKey(key),FileSelectionIdentity.revisionIsValid(revision) else {throw APIError.invalidConfiguration}
    }
    private func validateCancellationAcceptance(_ value:CancellationAcceptanceDTO,id:String) throws -> CancellationAcceptanceDTO {
        guard value.torrentId==id,value.acceptedAt.timeIntervalSince1970.isFinite else {throw APIError.invalidResponse}
        return value
    }
}
