import Foundation
import Observation

public struct ManagementReview:Sendable {
    public let resourceID:String
    public let revision:String
    public let allowed:Bool
    public let providerCopyKnown:Bool
}
@Observable @MainActor public final class ManagementModel {
    public enum Phase:Equatable,Sendable {case loading,review,sending,checking,uncertain,rejected,accepted(ManagementOutcome),unavailable,requiresUpdate,blocked}
    public let serverURL:URL
    public let connectionID:UUID
    public let kind:ManagementAction
    public let resourceID:String
    public private(set) var name:String
    public private(set) var phase:Phase = .loading
    public private(set) var review:ManagementReview?
    public private(set) var intent:ManagementIntent?
    public private(set) var message:String?
    public private(set) var isBusy=false
    public private(set) var requiresSignIn=false
    public private(set) var draftOptions=RemovalOptions()
    public var options:RemovalOptions {intent?.phase != .rejected ? intent?.options ?? draftOptions:draftOptions}
    public var isPreview:Bool {api.isPreview}
    public var canConfirm:Bool {active && connectionIsCurrent() && !isBusy && phase == .review && capabilitiesReady && review?.allowed == true && intent?.phase != .prepared && intent?.phase != .blocked}
    public var canEditOptions:Bool {kind == .remove && canConfirm}
    public var canRetry:Bool {active && connectionIsCurrent() && !isBusy && phase == .uncertain && capabilitiesReady && intent?.phase == .prepared}
    private let connectionIsCurrent:@MainActor @Sendable ()->Bool
    private let api:any ManagementAPI
    private let store:any ManagementIntentStoring
    private var claim:ManagementClaim?
    private var acceptedProof:ManagementOutcome?
    private var active=false
    private var generation=0
    private var capabilitiesReady=false
    @ObservationIgnored private var renewal:Task<Void,Never>?
    @ObservationIgnored private var cleanup:Task<Void,Never>?
    public init(server:AuthenticatedServer,kind:ManagementAction,resourceID:String,name:String,api:any ManagementAPI,store:any ManagementIntentStoring,isConnectionCurrent:@escaping @MainActor @Sendable ()->Bool = {true}) throws {
        guard api.serverURL==server.profile.serverURL,SubmissionIdentity.validID(resourceID) else {throw APIError.invalidConfiguration}
        serverURL=server.profile.serverURL;connectionID=server.connectionID;self.kind=kind;self.resourceID=resourceID;self.name=FileSelectionReview.display(name);self.api=api;self.store=store;connectionIsCurrent=isConnectionCurrent
    }
    deinit {renewal?.cancel()}
    public func setDeleteFiles(_ value:Bool) {guard canEditOptions else {return};draftOptions=RemovalOptions(deleteFiles:value,deleteFromProvider:draftOptions.deleteFromProvider)}
    public func setDeleteProvider(_ value:Bool) {guard canEditOptions else {return};draftOptions=RemovalOptions(deleteFiles:draftOptions.deleteFiles,deleteFromProvider:value)}
    private func current(_ value:Int)->Bool {active && generation==value && connectionIsCurrent() && !Task.isCancelled}
    private func release(_ claim:ManagementClaim) {let store=self.store;cleanup=Task {try? await store.releaseManagement(claim.id,token:claim.token)}}
    public func stop() {
        active=false;generation &+= 1;renewal?.cancel();renewal=nil;isBusy=false;requiresSignIn=false
        if let claim {release(claim)};claim=nil
    }
    public func waitForRelease() async {await cleanup?.value}
    private func finish(_ value:Int) {
        guard generation==value else {return};isBusy=false
        if Task.isCancelled {
            requiresSignIn=false
            if let proof=acceptedProof {phase = .accepted(proof)}
            else if intent?.phase == .prepared || intent?.phase == .blocked {phase = intent?.phase == .blocked ? .blocked:.uncertain;message="This action was interrupted. Its original request is kept for checking."}
            else {phase = .unavailable;message="This review was interrupted. Reopen it to continue."}
        }
    }
    public func open() async {
        guard !isBusy,!Task.isCancelled,connectionIsCurrent() else {return}
        if let proof=acceptedProof {active=true;phase = .accepted(proof);return}
        if claim != nil {await refresh();return}
        active=true;generation &+= 1;let value=generation;isBusy=true;defer {finish(value)}
        phase = .loading;message=nil;requiresSignIn=false
        do {
            await cleanup?.value;guard current(value) else {return}
            let acquired=try await store.claimManagement(server:serverURL,kind:kind,resourceID:resourceID,name:name)
            guard current(value) else {release(acquired);return}
            claim=acquired;intent=acquired.intent;name=acquired.name;startRenewal(acquired,generation:value)
            isBusy=false;await refresh()
        } catch {if current(value) {fail(error)}}
    }
    private func startRenewal(_ claim:ManagementClaim,generation value:Int) {
        let store=self.store
        renewal=Task {[weak self] in
            do {while !Task.isCancelled {try await Task.sleep(for:.seconds(30));try await store.renewManagement(claim.id,token:claim.token)}}
            catch {guard !Task.isCancelled,let self,self.current(value) else {return};self.stop();if let proof=self.acceptedProof {self.phase = .accepted(proof);self.message="This action already has a result. Refresh the transfer for its current state."} else {self.phase = .unavailable;self.message="This review is no longer current. Reopen it to check the saved action."}}
        }
    }
    private func verifyCapabilities(_ value:Int) async throws -> Bool {
        capabilitiesReady=false
        let health=try await api.health();guard current(value) else {throw CancellationError()}
        guard health.ok else {throw APIError.invalidResponse}
        let capability=kind == .cancel ? "native_cancellation_v1":"native_removal_precondition_v1"
        guard health.capabilities?.contains(capability)==true else {phase = .requiresUpdate;message="Update the original server before using this action. Saved requests remain on this iPhone.";return false}
        capabilitiesReady=true;return true
    }
    public func refresh() async {
        guard !isBusy,!Task.isCancelled,connectionIsCurrent() else {return}
        if let proof=acceptedProof {phase = .accepted(proof);return}
        guard let claim else {await open();return};guard active else {return}
        let value=generation;isBusy=true;defer {finish(value)};message=nil;requiresSignIn=false
        do {
            let saved=try await store.managementClaim(claim.id,token:claim.token);guard current(value) else {return};intent=saved.intent
            phase = intent?.phase == .prepared || intent?.phase == .blocked ? .checking:.loading
            guard try await verifyCapabilities(value) else {return}
            if let intent,intent.phase == .prepared || intent.phase == .blocked {try await reconcile(intent,generation:value);return}
            if kind == .cancel {
                let response=try await api.cancellationReview(id:resourceID);guard current(value) else {return}
                guard response.torrentId==resourceID,response.providerMayContinue,FileSelectionIdentity.revisionIsValid(response.revision) else {throw APIError.invalidResponse}
                review=ManagementReview(resourceID:resourceID,revision:response.revision,allowed:response.allowed,providerCopyKnown:false)
                phase = response.allowed ? .review:.unavailable
                if !response.allowed {message="This transfer has already stopped or finished. Refresh it to see the current state."}
            } else {
                let response=try await api.removalReview(id:resourceID);guard current(value) else {return}
                try validateRemovalReview(response)
                guard response.exists,let revision=response.revision else {acceptedProof = .absenceObserved;phase = .accepted(.absenceObserved);return}
                review=ManagementReview(resourceID:resourceID,revision:revision,allowed:true,providerCopyKnown:response.providerCopyKnown);phase = .review
            }
        } catch {if current(value) {await handle(error,generation:value)}}
    }
    private func validateRemovalReview(_ value:RemovalReviewDTO) throws {
        guard value.torrentId==resourceID else {throw APIError.invalidResponse}
        if value.exists {guard let revision=value.revision,FileSelectionIdentity.revisionIsValid(revision) else {throw APIError.invalidResponse}}
        else {guard value.revision==nil,!value.providerCopyKnown else {throw APIError.invalidResponse}}
    }
    private func reconcile(_ saved:ManagementIntent,generation value:Int) async throws {
        if kind == .cancel {
            let receipt=try await api.cancellationAcceptance(id:resourceID,key:saved.key,revision:saved.revision);guard current(value) else {return}
            if let receipt {try await acceptCancellation(receipt,generation:value)}
            else {phase = saved.phase == .blocked ? .blocked:.uncertain;message="The earlier cancellation may already be accepted. Check again or retry only its saved request."}
        } else {
            let response=try await api.removalReview(id:resourceID);guard current(value) else {return};try validateRemovalReview(response)
            if !response.exists {try await accept(.absenceObserved,generation:value)}
            else if response.revision != saved.revision {try await block(generation:value);message="This transfer changed after the saved removal. The old action is kept and cannot remove the new incarnation."}
            else {phase = saved.phase == .blocked ? .blocked:.uncertain;message="The transfer is still listed. Earlier file or provider cleanup may already have happened. The original removal choices are kept."}
        }
    }
    public func confirm() async {
        guard canConfirm,let review,let claim,!Task.isCancelled else {return}
        let value=generation;isBusy=true;defer {finish(value)};phase = .sending;message=nil;requiresSignIn=false
        do {
            guard try await verifyCapabilities(value) else {return}
            let prepared=try await store.prepareManagement(claim.id,token:claim.token,resourceID:review.resourceID,revision:review.revision,options:kind == .remove ? draftOptions:nil)
            guard current(value) else {return};intent=prepared.intent
            guard prepared.wasCreated else {phase = .uncertain;message="This action is already saved. Check its result before retrying.";return}
            await dispatch(prepared.intent,freshToken:prepared.rejectionToken,generation:value)
        } catch {if current(value) {await handle(error,generation:value)}}
    }
    public func retry() async {
        guard canRetry,let expected=intent,let claim,!Task.isCancelled else {return}
        let value=generation;isBusy=true;defer {finish(value)};phase = .sending;message=nil;requiresSignIn=false
        do {
            guard try await verifyCapabilities(value) else {return}
            let saved=try await store.managementClaim(claim.id,token:claim.token)
            guard current(value),let intent=saved.intent,intent.key==expected.key,intent.revision==expected.revision,intent.options==expected.options,intent.phase == .prepared else {throw ManagementIntentError.unresolved}
            await dispatch(intent,freshToken:nil,generation:value)
        } catch {if current(value) {await handle(error,generation:value)}}
    }
    private func dispatch(_ saved:ManagementIntent,freshToken:UUID?,generation value:Int) async {
        guard current(value) else {return}
        do {
            if kind == .cancel {
                let receipt=try await api.requestCancellation(id:resourceID,key:saved.key,revision:saved.revision);guard current(value) else {return}
                try await acceptCancellation(receipt,generation:value)
            } else {
                guard let options=saved.options else {throw ManagementIntentError.invalid}
                try await api.removeTransfer(id:resourceID,revision:saved.revision,options:options);guard current(value) else {return}
                try await accept(.removalConfirmed,generation:value)
            }
        } catch {
            guard current(value) else {return}
            let status:Int?;if case APIError.http(let code)=error {status=code}else{status=nil}
            if let freshToken,let claim,status==409 || status==412 || status==422 {
                do {try await store.rejectManagement(claim.id,token:claim.token,key:saved.key,freshToken:freshToken);guard current(value) else {return};intent=saved.replacingPhase(.rejected);phase = .rejected;message="The server did not accept this action. Review the transfer again before creating another request."}
                catch {if current(value) {fail(error)}}
                return
            }
            await handle(error,generation:value)
        }
    }
    private func acceptCancellation(_ receipt:CancellationAcceptanceDTO,generation value:Int) async throws {
        guard kind == .cancel,receipt.torrentId==resourceID,receipt.acceptedAt.timeIntervalSince1970.isFinite else {throw APIError.invalidResponse}
        try await accept(.cancellation(acceptedAt:receipt.acceptedAt,exists:receipt.torrentExists),generation:value)
    }
    private func accept(_ outcome:ManagementOutcome,generation value:Int) async throws {
        guard current(value),let intent,let claim else {return}
        acceptedProof=outcome;phase = .accepted(outcome)
        do {
            switch outcome {
            case .cancellation(let date,let exists):try await store.acceptCancellation(claim.id,token:claim.token,key:intent.key,receipt:CancellationAcceptanceDTO(torrentId:resourceID,acceptedAt:date,torrentExists:exists))
            case .removalConfirmed:try await store.completeRemoval(claim.id,token:claim.token,key:intent.key,confirmed:true)
            case .absenceObserved:try await store.completeRemoval(claim.id,token:claim.token,key:intent.key,confirmed:false)
            }
            if current(value) {self.intent=intent.replacingPhase(.accepted,outcome:outcome)}
        } catch {if current(value) {message="The result is known, but local tracking could not be updated. It will be checked again later."}}
    }
    private func block(generation value:Int) async throws {
        guard current(value),let intent,let claim else {return}
        if intent.phase == .prepared {try await store.blockManagement(claim.id,token:claim.token,key:intent.key)}
        guard current(value) else {return};self.intent=intent.replacingPhase(.blocked);phase = .blocked
    }
    private func handle(_ error:any Error,generation value:Int) async {
        guard current(value) else {return}
        if error as? SubmissionFailure == .keyMismatch || (kind == .remove && error as? APIError == .http(status:412)),intent != nil {
            do {try await block(generation:value);if current(value) {message="The server’s state does not match this saved action. It has been kept for checking."}}catch{if current(value){fail(error)}}
            return
        }
        fail(error)
    }
    private func fail(_ error:any Error) {
        requiresSignIn=error as? APIError == .unauthorized
        if intent?.phase == .prepared || intent?.phase == .blocked {
            phase = intent?.phase == .blocked ? .blocked:.uncertain
            message=requiresSignIn ? "Sign in to the original server again to check this action.":"The result is not confirmed. The original action and choices are kept for checking."
        } else {phase = .unavailable;message=(error as? LocalizedError)?.errorDescription ?? ServerConnectionModel.message(for:error)}
    }
}
