import Foundation
import Observation

/// One review owns one immutable, credential-free attempt. Only explicit user actions POST.
@Observable @MainActor public final class TransferAddModel {
    public enum Phase:Equatable, Sendable {
        case loading, review, sending, checking, uncertain, mismatched, rejected, duplicate(String), accepted(String,Bool), wrongServer(URL), requiresUpdate, unavailable
    }
    public enum Organization:String, CaseIterable { case inherit, on, off }
    public enum FilePolicy:String, CaseIterable {case rules,all,choose}
    public private(set) var phase:Phase = .loading
    public private(set) var message:String?
    public private(set) var requiresSignIn = false
    public private(set) var attempt:PendingSubmission?
    public private(set) var accounts:[AccountDTO] = []
    public private(set) var categories:[String] = []
    public private(set) var item:PendingItem
    public private(set) var durableAcceptance = false
    public private(set) var capabilityReady = false
    public var accountID = ""
    public var category = ""
    public var filePolicy:FilePolicy = .rules
    public var allFiles:Bool {get {filePolicy == .all} set {filePolicy = newValue ? .all : .rules}}
    public var unpack = false
    public var organization:Organization = .inherit
    public var keepCopies = false
    public var confirmedTargetChange = false
    public let connectionID:UUID
    public let serverURL:URL
    public var isPreview:Bool { api.isPreview }
    public private(set) var isBusy = false
    public var canEdit:Bool { phase == .review && !isBusy }
    public var canSubmit:Bool { canEdit && capabilityReady && baseSettings != nil && accounts.contains { $0.id==accountID && $0.enabled } && (!needsTargetConfirmation || confirmedTargetChange) }
    public var canRetry:Bool { !isBusy && phase == .uncertain && capabilityReady && attempt?.phase == .prepared }
    public var needsTargetConfirmation:Bool {
        let target=item.targetServerURL ?? (attempt?.phase == .rejected ? attempt?.serverURL : nil)
        return target != nil && target != serverURL
    }
    public var canDiscard:Bool { attempt?.phase != .prepared && attempt?.phase != .blocked && !isBusy }
    public var hasAutomaticDeletion:Bool { baseSettings?.finishedAction=="remove_from_provider" || !(baseSettings?.deleteOnError ?? "").isEmpty }
    public var reviewedSettings:TorrentSettingsDTO? { baseSettings.map { adjusted($0) } }
    public var visibleSettings:TorrentSettingsDTO? {
        if let inFlightOptions { return inFlightOptions.settings }
        if !canEdit,let attempt { return attempt.options.settings }
        return reviewedSettings
    }
    public var visibleOptions:SubmissionOptions? { inFlightOptions ?? attempt?.options }
    public var canViewPriorDuplicate:Bool { attempt?.serverURL==serverURL }
    private var inFlightOptions:SubmissionOptions?
    private var baseSettings:TorrentSettingsDTO?
    private let token:UUID
    private let store:any PendingInboxAccess
    private let api:any TransferSubmissionAPI
    public init(claim:PendingClaim,server:AuthenticatedServer,store:any PendingInboxAccess,api:any TransferSubmissionAPI) throws {
        guard api.serverURL==server.profile.serverURL else { throw APIError.invalidConfiguration }
        item=claim.item;token=claim.token;connectionID=server.connectionID;serverURL=api.serverURL;self.store=store;self.api=api
    }
    private func install(_ saved:PendingSubmission) {
        attempt=saved;accountID=saved.options.accountID;category=saved.options.category;baseSettings=saved.options.settings
        unpack=saved.options.settings.unpack
        organization=saved.options.settings.organize.map { $0 ? .on : .off } ?? .inherit
        filePolicy=saved.options.settings.waitForSelection == true ? .choose : .rules;keepCopies=false
    }
    public func load() async {
        guard !Task.isCancelled,!isBusy else { return }
        isBusy=true;defer { isBusy=false }
        phase = .loading;message=nil;requiresSignIn=false;capabilityReady=false
        do {
            item=try await store.reviewItem(item.id,token:token);attempt=item.submission
            if let saved=attempt {
                install(saved)
                if saved.phase != .rejected && saved.serverURL != serverURL { phase = .wrongServer(saved.serverURL);return }
                if saved.phase == .blocked { phase = .mismatched;return }
                if saved.phase == .accepted, let id=saved.acceptedID, let exists=saved.torrentExists { durableAcceptance=true;phase = .accepted(id,exists);return }
            }
            guard try await verifyCapabilities() else { return }
            if attempt?.phase == .prepared {
                // Names improve recovery review; a failed name lookup must not prevent receipt recovery.
                if let current=try? await api.accounts(),current.count<=100,Set(current.map(\.id)).count==current.count,current.allSatisfy({ SubmissionIdentity.validID($0.id) }) { accounts=current }
                try Task.checkCancellation();phase = .checking;await checkReceipt();return
            }
            async let providerAccounts=api.accounts()
            async let serverSettings=api.settings()
            let (accounts,settings)=try await (providerAccounts,serverSettings);try Task.checkCancellation()
            guard accounts.count<=100,Set(accounts.map(\.id)).count==accounts.count,accounts.allSatisfy({ SubmissionIdentity.validID($0.id) }), (settings.categories?.count ?? 0)<=256 else { throw APIError.responseTooLarge }
            self.accounts=accounts.filter { $0.enabled };categories=RuntimeSettingsDraft.byteDistinct((settings.categories ?? []).filter { !$0.isEmpty })
            if attempt == nil {
                baseSettings=settings.torrentDefaults;unpack=settings.torrentDefaults.unpack
                filePolicy=settings.torrentDefaults.waitForSelection == true ? .choose : .rules
                organization=settings.torrentDefaults.organize.map { $0 ? .on : .off } ?? .inherit
                accountID=self.accounts.first(where: \.isDefault)?.id ?? ""
            }
            if let rejection=attempt?.rejection {
                switch rejection { case .activeTorrent(let id):phase = .duplicate(id);case .options:phase = .rejected }
            } else { phase = .review }
        } catch { fail(error) }
    }
    public func editRejectedOptions() {
        guard !isBusy,capabilityReady,attempt?.phase == .rejected else { return }
        switch phase { case .rejected,.duplicate:phase = .review;message=nil;default:break }
    }
    private func verifyCapabilities() async throws -> Bool {
        capabilityReady=false
        let health=try await api.health();try Task.checkCancellation()
        guard health.ok else { throw APIError.invalidResponse }
        let features=Set(health.capabilities ?? [])
        guard features.contains("torrent_submissions_v1"),item.torrent == nil || features.contains("multipart_torrent_settings_v1") else { phase = .requiresUpdate;return false }
        if (inFlightOptions?.settings.waitForSelection ?? attempt?.options.settings.waitForSelection ?? (filePolicy == .choose)) && !features.contains("manual_file_selection_v1") {phase = .requiresUpdate;return false}
        capabilityReady=true;return true
    }
    public var editableOverrides: TorrentSettingsDTO? { canEdit ? reviewedSettings : nil }
    public func applyOverrides(_ settings: TorrentSettingsDTO) {
        guard canEdit else { return }
        baseSettings = settings
        unpack = settings.unpack
        organization = settings.organize.map { $0 ? .on : .off } ?? .inherit
        keepCopies = false
    }
    private func adjusted(_ base:TorrentSettingsDTO) -> TorrentSettingsDTO {
        let organize:Bool? = switch organization { case .inherit:nil;case .on:true;case .off:false }
        return TorrentSettingsDTO(minFileSize:allFiles ? 0 : base.minFileSize,includeRegex:allFiles ? nil : base.includeRegex,excludeRegex:allFiles ? nil : base.excludeRegex,manualFiles:filePolicy != .rules ? nil : base.manualFiles,finishedAction:keepCopies ? "keep" : base.finishedAction,finishedDelay:keepCopies ? nil : base.finishedDelay,downloadRetries:base.downloadRetries,torrentRetries:base.torrentRetries,deleteOnError:keepCopies ? nil : base.deleteOnError,lifetime:base.lifetime,unpack:unpack,organize:organize,waitForSelection:filePolicy == .choose ? true : (base.waitForSelection == nil ? nil : false))
    }
    public func submit() async {
        guard !Task.isCancelled,canSubmit,let settings=reviewedSettings else { return }
        isBusy=true;defer { isBusy=false;inFlightOptions=nil }
        phase = .sending;message=nil;requiresSignIn=false
        do {
            let options=try SubmissionOptions(accountID:accountID,category:category,settings:settings)
            let confirmed=confirmedTargetChange;inFlightOptions=options
            guard try await verifyCapabilities() else { return }
            let payload=try await payload();try Task.checkCancellation()
            let prepared=try await store.prepareSubmission(item.id,token:token,server:ServerAddress(serverURL.absoluteString),options:options,confirmedTargetChange:confirmed)
            attempt=prepared.attempt
            // This nonce is local to this single dispatch; retries/restoration never receive it.
            await dispatch(payload,attempt:prepared.attempt,freshToken:prepared.rejectionToken)
        } catch { fail(error) }
    }
    public func retrySameSubmission() async {
        guard !Task.isCancelled,canRetry else { return }
        isBusy=true;defer { isBusy=false }
        phase = .sending;message=nil;requiresSignIn=false
        do {
            guard try await verifyCapabilities() else { return }
            item=try await store.reviewItem(item.id,token:token)
            guard let saved=item.submission,saved.phase == .prepared,saved.serverURL==serverURL else { throw PendingInboxError.submissionPending }
            attempt=saved;let payload=try await payload();try Task.checkCancellation()
            await dispatch(payload,attempt:saved,freshToken:nil)
        } catch { fail(error) }
    }
    private func payload() async throws -> SubmissionPayload {
        if let magnet=item.magnet { return .magnet(magnet) }
        return .torrent(try await store.loadTorrent(item.id,token:token))
    }
    private func dispatch(_ payload:SubmissionPayload,attempt saved:PendingSubmission,freshToken:UUID?) async {
        do {
            try Task.checkCancellation()
            let torrent=try await api.submit(payload,options:saved.options,key:saved.key)
            guard SubmissionIdentity.validID(torrent.id),torrent.hash.lowercased()==item.infoHash,torrent.accountId==saved.options.accountID else { throw APIError.invalidResponse }
            await accept(torrent.id,exists:true,attempt:saved)
        } catch {
            if error as? SubmissionFailure == .keyMismatch {
                do {
                    try await store.recordSubmissionMismatch(item.id,token:token,key:saved.key)
                    attempt=PendingSubmission(key:saved.key,serverURL:saved.serverURL,options:saved.options,createdAt:saved.createdAt,phase:.blocked)
                } catch { }
                phase = .mismatched;message="This saved request does not match the server's receipt. It is kept without resubmitting or replacing its key.";return
            }
            let reason:SubmissionRejection?
            switch error {
            case SubmissionFailure.activeTorrent(let id):reason = .activeTorrent(id)
            case APIError.http(status:422):reason = .options
            default:reason=nil
            }
            if let reason,let freshToken,!Task.isCancelled {
                do {
                    try await store.recordSubmissionRejected(item.id,token:token,key:saved.key,freshToken:freshToken,reason:reason)
                    item=try await store.reviewItem(item.id,token:token);attempt=item.submission
                    switch reason { case .activeTorrent(let id):phase = .duplicate(id);case .options:phase = .rejected }
                    return
                } catch { fail(error);return }
            }
            // A removed receipt is still acceptance; never replace its key with a new one.
            if error as? SubmissionFailure == .removed,!Task.isCancelled { phase = .checking;await checkReceipt();return }
            fail(error)
        }
    }
    public func reconcile() async {
        guard !Task.isCancelled,!isBusy,phase == .uncertain,attempt?.phase == .prepared else { return }
        isBusy=true;defer { isBusy=false };phase = .checking;message=nil;requiresSignIn=false
        do { guard try await verifyCapabilities() else { return };await checkReceipt() } catch { fail(error) }
    }
    private func checkReceipt() async {
        guard let saved=attempt,saved.phase == .prepared,saved.serverURL==serverURL else { phase = .unavailable;return }
        do {
            if let receipt=try await api.submissionReceipt(key:saved.key) {
                guard SubmissionIdentity.validID(receipt.torrentId),receipt.acceptedAt.timeIntervalSince1970.isFinite else { throw APIError.invalidResponse }
                await accept(receipt.torrentId,exists:receipt.torrentExists,attempt:saved)
            } else {
                phase = .uncertain;message="The server has not confirmed this request yet. The earlier request may still be running. Check again or retry the same saved request."
            }
        } catch { fail(error) }
    }
    private func accept(_ id:String,exists:Bool,attempt saved:PendingSubmission) async {
        phase = .accepted(id,exists);durableAcceptance=false
        do {
            try await store.recordSubmissionAccepted(item.id,token:token,key:saved.key,server:ServerAddress(serverURL.absoluteString),transferID:id,exists:exists)
            attempt=PendingSubmission(key:saved.key,serverURL:saved.serverURL,options:saved.options,createdAt:saved.createdAt,phase:.accepted,acceptedID:id,torrentExists:exists)
            durableAcceptance=true;message=nil
        } catch { message="The server confirmed acceptance, but this iPhone could not update saved tracking. Keep it for later and check the receipt again." }
    }
    private func fail(_ error:any Error) {
        requiresSignIn = error as? APIError == .unauthorized
        if attempt?.phase == .prepared {
            phase = .uncertain
            if error as? SubmissionFailure == .keyMismatch { message="The server reports different input for this saved key. Keep this attempt; it cannot safely be replaced automatically." }
            else if requiresSignIn { message="Sign in to this server again to check the saved submission." }
            else { message="The result is not confirmed. The same saved request is kept for checking or retrying." }
        } else {
            phase = .unavailable
            if let typed=error as? PendingInboxError { message=typed.errorDescription }
            else if let typed=error as? SubmissionIntentError { message=typed.errorDescription }
            else { message=ServerConnectionModel.message(for:error) }
        }
    }
}
