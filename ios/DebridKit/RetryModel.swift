import Foundation
import Observation

@Observable @MainActor public final class RetryModel {
    public enum Phase:Equatable,Sendable {case loading,review,sending,checking,uncertain,rejected,accepted(Bool),unavailable,requiresUpdate,blocked}
    public let serverURL:URL
    public let connectionID:UUID
    public let kind:RetryResourceKind
    public let resourceID:String
    public let torrentID:String
    public private(set) var name:String
    public private(set) var phase:Phase = .loading
    public private(set) var review:RetryReviewDTO?
    public private(set) var intent:RetryIntent?
    public private(set) var message:String?
    public private(set) var isBusy=false
    public private(set) var requiresSignIn=false
    public var isPreview:Bool {api.isPreview}
    public var canConfirm:Bool {active && connectionIsCurrent() && !isBusy && phase == .review && capabilitiesReady && review?.allowed == true && intent?.phase != .prepared && intent?.phase != .blocked}
    public var canRetry:Bool {active && connectionIsCurrent() && !isBusy && phase == .uncertain && capabilitiesReady && intent?.phase == .prepared}
    public var waitForSelection:Bool {if let intent,intent.phase != .rejected {return intent.waitForSelection};return review?.waitForSelection ?? false}
    private let connectionIsCurrent:@MainActor @Sendable ()->Bool
    private let api:any RetryAPI
    private let store:any RetryIntentStoring
    private var claim:RetryClaim?
    private var acceptedProof:RetryAcceptanceDTO?
    private var active=false
    private var generation=0
    private var capabilitiesReady=false
    @ObservationIgnored private var renewal:Task<Void,Never>?
    @ObservationIgnored private var cleanup:Task<Void,Never>?
    public init(server:AuthenticatedServer,kind:RetryResourceKind,resourceID:String,torrentID:String,name:String,api:any RetryAPI,store:any RetryIntentStoring,isConnectionCurrent:@escaping @MainActor @Sendable ()->Bool = {true}) throws {
        guard api.serverURL==server.profile.serverURL,SubmissionIdentity.validID(resourceID),SubmissionIdentity.validID(torrentID),kind != .torrent || resourceID==torrentID else {throw APIError.invalidConfiguration}
        serverURL=server.profile.serverURL;connectionID=server.connectionID;self.kind=kind;self.resourceID=resourceID;self.torrentID=torrentID;self.name=FileSelectionReview.display(name);self.api=api;self.store=store;connectionIsCurrent=isConnectionCurrent
    }
    deinit {renewal?.cancel()}
    private func current(_ value:Int)->Bool {active && generation==value && connectionIsCurrent() && !Task.isCancelled}
    private func release(_ claim:RetryClaim) {let store=self.store;cleanup=Task {try? await store.releaseRetry(claim.id,token:claim.token)}}
    public func stop() {
        active=false;generation &+= 1;renewal?.cancel();renewal=nil;isBusy=false;requiresSignIn=false
        if let claim {release(claim)};claim=nil
    }
    public func waitForRelease() async {await cleanup?.value}
    private func finish(_ value:Int) {
        guard generation==value else {return};isBusy=false
        if Task.isCancelled {
            requiresSignIn=false
            if let proof=acceptedProof {phase = .accepted(proof.torrentExists)}
            else if intent?.phase == .prepared {phase = .uncertain;message="This retry was interrupted. Its original request is kept for checking."}
            else {phase = .unavailable;message="This review was interrupted. Reopen it to continue."}
        }
    }
    public func open() async {
        guard !isBusy,!Task.isCancelled,connectionIsCurrent() else {return}
        if let proof=acceptedProof {active=true;phase = .accepted(proof.torrentExists);return}
        if claim != nil {await refresh();return}
        active=true;generation &+= 1;let value=generation;isBusy=true;defer {finish(value)}
        phase = .loading;message=nil;requiresSignIn=false
        do {
            await cleanup?.value;guard current(value) else {return}
            let acquired=try await store.claimRetry(server:serverURL,kind:kind,resourceID:resourceID,torrentID:torrentID,name:name)
            guard current(value) else {release(acquired);return}
            claim=acquired;intent=acquired.intent;name=acquired.name;startRenewal(acquired,generation:value)
            isBusy=false;await refresh()
        } catch {if current(value) {fail(error)}}
    }
    private func startRenewal(_ claim:RetryClaim,generation value:Int) {
        let store=self.store
        renewal=Task {[weak self] in
            do {while !Task.isCancelled {try await Task.sleep(for:.seconds(30));try await store.renewRetry(claim.id,token:claim.token)}}
            catch {guard !Task.isCancelled,let self,self.current(value) else {return};self.stop();if let proof=self.acceptedProof {self.phase = .accepted(proof.torrentExists);self.message="This retry is already accepted. Refresh the transfer for current progress."} else {self.phase = .unavailable;self.message="This review is no longer current. Reopen it to check the saved request."}}
        }
    }
    private func verifyCapabilities(_ value:Int) async throws -> Bool {
        capabilitiesReady=false
        let health=try await api.health();guard current(value) else {throw CancellationError()}
        guard health.ok else {throw APIError.invalidResponse}
        guard health.capabilities?.contains("native_retry_v1")==true else {phase = .requiresUpdate;message="Update this server before retrying. Saved requests remain on this iPhone.";return false}
        capabilitiesReady=true;return true
    }
    public func refresh() async {
        guard !isBusy,!Task.isCancelled else {return}
        if let proof=acceptedProof {phase = .accepted(proof.torrentExists);return}
        guard let claim else {await open();return};guard active else {return}
        let value=generation;isBusy=true;defer {finish(value)};message=nil;requiresSignIn=false
        do {
            let saved=try await store.retryClaim(claim.id,token:claim.token);guard current(value) else {return};intent=saved.intent
            phase = intent?.phase == .prepared || intent?.phase == .blocked ? .checking : .loading
            guard try await verifyCapabilities(value) else {return}
            if let intent,intent.phase == .prepared || intent.phase == .blocked {
                let receipt=try await api.retryAcceptance(kind:kind,id:resourceID,key:intent.key,revision:intent.revision)
                guard current(value) else {return}
                if let receipt {try await accept(receipt,generation:value)}
                else {phase = intent.phase == .blocked ? .blocked : .uncertain;message="The earlier retry may already be accepted. Its original action is kept until the result is confirmed."}
                return
            }
            let response=try await api.retryReview(kind:kind,id:resourceID);guard current(value) else {return}
            guard response.kind==kind.rawValue,response.resourceId==resourceID,response.torrentId==torrentID,FileSelectionIdentity.revisionIsValid(response.revision) else {throw RetryIntentError.parentChanged}
            review=response;phase = response.allowed ? .review : .unavailable
            if !response.allowed {message="The server no longer allows this retry. Refresh the transfer to see its current state."}
        } catch {if current(value) {await handle(error,generation:value)}}
    }
    public func confirm() async {
        guard canConfirm,let review,let claim,!Task.isCancelled else {return}
        let value=generation;isBusy=true;defer {finish(value)};phase = .sending;message=nil;requiresSignIn=false
        do {
            guard try await verifyCapabilities(value) else {return}
            let prepared=try await store.prepareRetry(claim.id,token:claim.token,review:review)
            guard current(value) else {return};intent=prepared.intent
            guard prepared.wasCreated else {phase = .uncertain;message="This retry is already saved. Check its result before retrying.";return}
            await dispatch(prepared.intent,freshToken:prepared.rejectionToken,generation:value)
        } catch {if current(value) {await handle(error,generation:value)}}
    }
    public func retry() async {
        guard canRetry,let expected=intent,let claim,!Task.isCancelled else {return}
        let value=generation;isBusy=true;defer {finish(value)};phase = .sending;message=nil;requiresSignIn=false
        do {
            guard try await verifyCapabilities(value) else {return}
            let saved=try await store.retryClaim(claim.id,token:claim.token)
            guard current(value),let intent=saved.intent,intent.key==expected.key,intent.revision==expected.revision,intent.torrentID==torrentID,intent.phase == .prepared else {throw RetryIntentError.unresolved}
            await dispatch(intent,freshToken:nil,generation:value)
        } catch {if current(value) {await handle(error,generation:value)}}
    }
    private func dispatch(_ saved:RetryIntent,freshToken:UUID?,generation value:Int) async {
        guard current(value) else {return}
        do {
            let receipt=try await api.requestRetry(kind:kind,id:resourceID,key:saved.key,revision:saved.revision)
            guard current(value) else {return};try await accept(receipt,generation:value)
        } catch {
            guard current(value) else {return}
            if let freshToken,let claim,error as? APIError == .http(status:409) || error as? APIError == .http(status:422) {
                do {try await store.rejectRetry(claim.id,token:claim.token,key:saved.key,freshToken:freshToken);guard current(value) else {return};intent=saved.replacingPhase(.rejected);phase = .rejected;message="The server did not accept this retry. Refresh its review before creating another request."}
                catch {if current(value) {fail(error)}}
                return
            }
            await handle(error,generation:value)
        }
    }
    private func accept(_ receipt:RetryAcceptanceDTO,generation value:Int) async throws {
        guard receipt.kind==kind.rawValue,receipt.resourceId==resourceID,receipt.torrentId==torrentID,receipt.acceptedAt.timeIntervalSince1970.isFinite else {throw RetryIntentError.parentChanged}
        guard current(value),let intent,let claim else {return}
        acceptedProof=receipt;phase = .accepted(receipt.torrentExists)
        do {try await store.acceptRetry(claim.id,token:claim.token,key:intent.key,receipt:receipt);if current(value) {self.intent=intent.replacingPhase(.accepted,receipt:receipt)}}
        catch {if current(value) {message="The server accepted the retry, but local tracking could not be updated. It will be checked again later."}}
    }
    private func handle(_ error:any Error,generation value:Int) async {
        guard current(value) else {return}
        if error as? SubmissionFailure == .keyMismatch,let intent,let claim {
            try? await store.blockRetry(claim.id,token:claim.token,key:intent.key)
            guard current(value) else {return};self.intent=intent.replacingPhase(.blocked);phase = .blocked;message="The server’s record does not match this saved retry. It has been kept for checking.";return
        }
        fail(error)
    }
    private func fail(_ error:any Error) {
        requiresSignIn=error as? APIError == .unauthorized
        if intent?.phase == .prepared || intent?.phase == .blocked {
            phase = intent?.phase == .blocked ? .blocked : .uncertain
            message=requiresSignIn ? "Sign in to the original server again to check this retry." : "The result is not confirmed. The original retry is kept for checking or retrying."
        } else {phase = .unavailable;message=(error as? LocalizedError)?.errorDescription ?? ServerConnectionModel.message(for:error)}
    }
}
