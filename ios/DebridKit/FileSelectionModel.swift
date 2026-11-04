import Foundation
import Observation

public struct FileSelectionProposal:Sendable,CustomStringConvertible,CustomReflectable {
    public let revision:String
    public let fileIDs:[String]
    public let count:Int
    public let bytes:Int64?
    public let hasUnknownSizes:Bool
    @concurrent static func make(review:FileSelectionReview,selected:Set<Data>) async throws -> Self {
        try Task.checkCancellation()
        guard review.response.state=="ready",let revision=review.response.revision,!selected.isEmpty else {throw FileSelectionError.invalidMetadata}
        var ids:[String]=[],bytes:Int64=0,overflow=false,unknown=false
        for id in selected {
            try Task.checkCancellation()
            guard let file=review.filesByID[id] else {throw FileSelectionError.invalidMetadata}
            ids.append(file.providerID);unknown = unknown || file.size==0
            let sum=bytes.addingReportingOverflow(file.size);overflow = overflow || sum.overflow;if !sum.overflow {bytes=sum.partialValue}
        }
        return Self(revision:revision,fileIDs:try FileSelectionIdentity.canonicalIDs(ids),count:ids.count,bytes:overflow ? nil : bytes,hasUnknownSizes:unknown)
    }
    public var description:String {"FileSelectionProposal(redacted)"}
    public var customMirror:Mirror {Mirror(self,children:["contents":"redacted"])}
}

@Observable @MainActor public final class FileSelectionModel {
    public enum Confirmation:Sendable,Equatable {case ours,previous,other}
    public enum Phase:Sendable,Equatable {case loading,waiting,editing,sending,checking,uncertain,rejected,accepted(Confirmation),unavailable,requiresUpdate,blocked}
    public let connectionID:UUID
    public let serverURL:URL
    public let transferID:String
    public let name:String
    public private(set) var phase:Phase = .loading
    public private(set) var message:String?
    public private(set) var review:FileSelectionReview?
    public private(set) var selected=Set<Data>()
    public private(set) var intent:FileSelectionIntent?
    public private(set) var isBusy=false
    public private(set) var requiresSignIn=false
    public var isPreview:Bool {api.isPreview}
    public var canEdit:Bool {active && connectionIsCurrent() && phase == .editing && !isBusy}
    public var canReview:Bool {canEdit && !selected.isEmpty}
    public var canRetry:Bool {active && connectionIsCurrent() && !isBusy && phase == .uncertain && intent?.phase == .prepared && capabilitiesReady}
    private let connectionIsCurrent:@MainActor @Sendable ()->Bool
    private let api:any FileSelectionAPI
    private let store:any FileSelectionIntentStoring
    private var claim:FileSelectionClaim?
    private var active=false
    private var generation=0
    private var capabilitiesReady=false
    @ObservationIgnored private var renewal:Task<Void,Never>?
    @ObservationIgnored private var cleanup:Task<Void,Never>?
    public init(server:AuthenticatedServer,transferID:String,name:String,api:any FileSelectionAPI,store:any FileSelectionIntentStoring,isConnectionCurrent:@escaping @MainActor @Sendable ()->Bool = {true}) throws {
        guard server.profile.serverURL==api.serverURL,SubmissionIdentity.validID(transferID) else {throw APIError.invalidConfiguration}
        connectionID=server.connectionID;serverURL=server.profile.serverURL;self.transferID=transferID;self.name=name;self.api=api;self.store=store;connectionIsCurrent=isConnectionCurrent
    }
    deinit {renewal?.cancel()}
    private func current(_ value:Int)->Bool {active && generation==value && connectionIsCurrent() && !Task.isCancelled}
    private func release(_ claim:FileSelectionClaim) {
        let store=self.store
        // A fresh cleanup task must survive cancellation of the view's structured task.
        cleanup=Task {try? await store.releaseFileSelection(claim.id,token:claim.token)}
    }
    public func stop() {
        active=false;generation &+= 1;renewal?.cancel();renewal=nil;isBusy=false
        if let claim {release(claim)};claim=nil
    }
    public func waitForRelease() async {await cleanup?.value}
    private func finish(_ value:Int) {
        guard generation==value else {return};isBusy=false
        if Task.isCancelled {
            requiresSignIn=false
            switch phase {
            case .sending,.checking:phase = .uncertain;message="This review was interrupted. Check its result before changing the files."
            case .loading:phase = intent?.phase == .prepared ? .uncertain : .unavailable;message="This review was interrupted. Reopen it to continue."
            default:break
            }
        }
    }
    public func open() async {
        guard !isBusy,!Task.isCancelled,connectionIsCurrent() else {return}
        if claim != nil {await refresh();return}
        active=true;generation &+= 1;let value=generation;isBusy=true;defer {finish(value)};phase = .loading;message=nil;requiresSignIn=false
        do {
            await cleanup?.value
            guard current(value) else {return}
            let acquired=try await store.claimFileSelection(server:serverURL,transferID:transferID,name:name)
            guard current(value) else {release(acquired);return}
            claim=acquired;intent=acquired.intent;startRenewal(acquired,generation:value)
            isBusy=false;await refresh()
        } catch {if current(value) {isBusy=false;fail(error)}}
    }
    private func startRenewal(_ claim:FileSelectionClaim,generation value:Int) {
        let store=self.store
        renewal=Task { [weak self] in
            do {
                while !Task.isCancelled {
                    try await Task.sleep(for:.seconds(30))
                    try await store.renewFileSelection(claim.id,token:claim.token)
                }
            } catch {
                guard !Task.isCancelled,let self,self.current(value) else {return}
                self.stop();self.phase = .unavailable;self.message="This review is no longer current. Reopen it to continue."
            }
        }
    }
    private func verifyCapabilities(_ value:Int) async throws -> Bool {
        capabilitiesReady=false
        let health=try await api.health();guard current(value) else {throw CancellationError()}
        guard health.ok else {throw APIError.invalidResponse}
        guard health.capabilities?.contains("manual_file_selection_v1")==true else {phase = .requiresUpdate;message="Update this server before reviewing files. Saved requests remain on this iPhone.";return false}
        capabilitiesReady=true;return true
    }
    public func refresh() async {
        guard !Task.isCancelled,!isBusy else {return}
        guard let claim else {await open();return}
        guard active else {return}
        let value=generation;isBusy=true;defer {finish(value)}
        phase = intent?.phase == .prepared ? .checking : .loading;message=nil;requiresSignIn=false
        do {
            let saved=try await store.fileSelectionClaim(claim.id,token:claim.token)
            guard current(value) else {return};intent=saved.intent
            guard try await verifyCapabilities(value) else {return}
            let response=try await api.fileSelection(id:transferID)
            guard current(value) else {return}
            try await install(response,generation:value)
        } catch {if current(value) {fail(error)}}
    }
    private func install(_ response:FileSelectionDTO,generation value:Int) async throws {
        let built=try await FileSelectionReview.build(response,transferID:transferID)
        guard current(value) else {return}
        let previousRevision=review?.response.revision
        review=built
        if response.state=="accepted" {
            let canonical=try FileSelectionIdentity.canonicalIDs(response.selectedIds,maximumBytes:2*1024*1024)
            let confirmation:Confirmation
            if let intent {confirmation = intent.revision==response.revision && FileSelectionIdentity.sameIDs(intent.fileIDs,canonical) ? .ours : .other}
            else {confirmation = .previous}
            selected=Set(canonical.map {Data($0.utf8)})
            if let intent,let claim {
                do {try await store.resolveFileSelection(claim.id,token:claim.token,intentID:intent.id);if current(value) {self.intent=intent.replacingPhase(.resolved)}}
                catch {if current(value) {message="The server confirmed a selection, but saved tracking could not be updated. It will be checked again later."}}
            }
            guard current(value) else {return};phase = .accepted(confirmation);return
        }
        if intent?.phase == .prepared || intent?.phase == .blocked {
            phase = intent?.phase == .blocked ? .blocked : .uncertain
            message="The earlier request is not confirmed. Its original files are kept; a pending or ready file list does not mean the request failed."
            return
        }
        switch response.state {
        case "ready":
            if previousRevision != response.revision {selected=[]}
            phase = .editing
        case "pending":phase = .waiting
        default:phase = .unavailable
        }
    }
    public func toggle(_ id:Data) {guard canEdit,review?.filesByID[id] != nil else {return};if !selected.insert(id).inserted {selected.remove(id)}}
    public func select(_ files:[FileSelectionReview.File]) {guard canEdit else {return};selected.formUnion(files.map(\.id))}
    public func clear() {guard canEdit else {return};selected=[]}
    public func proposal() async -> FileSelectionProposal? {
        guard canReview,let review else {return nil}
        let value=generation,selected=self.selected;isBusy=true;defer {finish(value)}
        do {let result=try await FileSelectionProposal.make(review:review,selected:selected);return current(value) ? result : nil}
        catch {if current(value) {message=(error as? LocalizedError)?.errorDescription ?? "These files could not be prepared for review."};return nil}
    }
    public func confirm(_ proposal:FileSelectionProposal) async {
        guard canReview,let claim,proposal.revision==review?.response.revision,Set(proposal.fileIDs.map {Data($0.utf8)})==selected else {return}
        let value=generation;isBusy=true;defer {finish(value)}
        phase = .sending;message=nil;requiresSignIn=false
        do {
            guard try await verifyCapabilities(value) else {return}
            let prepared=try await store.prepareFileSelection(claim.id,token:claim.token,revision:proposal.revision,fileIDs:proposal.fileIDs)
            guard current(value) else {return};intent=prepared.intent
            guard prepared.wasCreated else {phase = .uncertain;message="This request was already saved. Check its result before retrying.";return}
            await dispatch(prepared.intent,freshToken:prepared.rejectionToken,generation:value)
        } catch {if current(value) {fail(error)}}
    }
    public func retry() async {
        guard canRetry,let claim,let expected=intent else {return}
        let value=generation;isBusy=true;defer {finish(value)}
        phase = .sending;message=nil;requiresSignIn=false
        do {
            guard try await verifyCapabilities(value) else {return}
            let saved=try await store.fileSelectionClaim(claim.id,token:claim.token)
            guard current(value),let intent=saved.intent,intent.id==expected.id,intent.phase == .prepared else {throw FileSelectionError.unresolved}
            await dispatch(intent,freshToken:nil,generation:value)
        } catch {if current(value) {fail(error)}}
    }
    private func dispatch(_ saved:FileSelectionIntent,freshToken:UUID?,generation value:Int) async {
        guard current(value) else {return}
        do {
            let response=try await api.confirmFileSelection(id:transferID,revision:saved.revision,fileIDs:saved.fileIDs)
            guard current(value) else {return}
            guard response.state=="accepted",response.revision==saved.revision,FileSelectionIdentity.sameIDs(try FileSelectionIdentity.canonicalIDs(response.selectedIds,maximumBytes:2*1024*1024),saved.fileIDs) else {throw APIError.invalidResponse}
            try await install(response,generation:value)
        } catch {
            guard current(value) else {return}
            if let freshToken,let claim,error as? APIError == .http(status:409) || error as? APIError == .http(status:422) {
                do {
                    try await store.rejectFileSelection(claim.id,token:claim.token,intentID:saved.id,freshToken:freshToken)
                    guard current(value) else {return};intent=saved.replacingPhase(.rejected);phase = .rejected;message="The server did not accept this choice. Refresh the file list before reviewing again.";return
                } catch {if current(value) {fail(error)};return}
            }
            fail(error)
        }
    }
    private func fail(_ error:any Error) {
        requiresSignIn=error as? APIError == .unauthorized
        if intent?.phase == .prepared || intent?.phase == .blocked {
            phase = intent?.phase == .blocked ? .blocked : .uncertain
            message=requiresSignIn ? "Sign in to the original server again to check this saved choice." : "The result is not confirmed. The same saved files are kept for checking or retrying."
        } else {
            phase = .unavailable
            message=(error as? LocalizedError)?.errorDescription ?? ServerConnectionModel.message(for:error)
        }
    }
}
