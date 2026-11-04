import Foundation
import Observation

public protocol PendingInboxAccess: Sendable {
    func snapshot() async throws -> [PendingItem]
    func enqueue(_ magnet:MagnetLink,source:PendingSource,target:ServerAddress?) async throws -> PendingReceipt
    func enqueueTorrent(_ file:TorrentFile,source:PendingSource,target:ServerAddress?) async throws -> PendingReceipt
    func reviewItem(_ id:UUID,token:UUID) async throws -> PendingItem
    func prepareSubmission(_ id:UUID,token:UUID,server:ServerAddress,options:SubmissionOptions,confirmedTargetChange:Bool) async throws -> PreparedSubmission
    func recordSubmissionAccepted(_ id:UUID,token:UUID,key:UUID,server:ServerAddress,transferID:String,exists:Bool) async throws
    func recordSubmissionMismatch(_ id:UUID,token:UUID,key:UUID) async throws
    func recordSubmissionRejected(_ id:UUID,token:UUID,key:UUID,freshToken:UUID,reason:SubmissionRejection) async throws
    func loadTorrent(_ id:UUID,token:UUID) async throws -> TorrentFile
    func claim(_ id:UUID) async throws -> PendingClaim
    func renew(_ id:UUID,token:UUID) async throws -> Date
    func release(_ id:UUID,token:UUID) async throws
    func keep(_ id:UUID) async throws
    func discard(_ id:UUID,token:UUID?) async throws
}
extension PendingInbox: PendingInboxAccess {}

/// Routes intake to durable review; the explicit review model owns server submission.
@Observable @MainActor public final class MagnetIntakeCoordinator {
    public private(set) var items: [PendingItem] = []
    public private(set) var savedSelections:[SavedFileSelection]=[]
    public private(set) var savedRetries:[SavedRetry]=[]
    public private(set) var storageMessage: String?
    public private(set) var intakeMessage: String?
    public private(set) var notice: String?
    public private(set) var isLoading = false
    public private(set) var hasSnapshot = false
    public private(set) var savingCount = 0
    public private(set) var isRetrying = false
    public private(set) var activeClaim: PendingClaim?
    public private(set) var reviewMessage: String?
    public private(set) var isReviewLoading = false
    public private(set) var reviewNeedsConfirmation = false
    public var isPresented = false
    public private(set) var focusID: UUID?
    public var failedCount: Int { failed.count }
    private enum Content: Sendable {
        case magnet(MagnetLink), torrent(TorrentFile)
        var key:String { switch self { case .magnet(let link): link.rawURI; case .torrent(let file): file.digest } }
        var byteCount:Int { switch self { case .magnet(let link): link.rawURI.utf8.count; case .torrent(let file): file.data.count } }
    }
    private struct Input: Sendable { let id = UUID(); let content:Content; let source:PendingSource; let target:ServerAddress? }
    public private(set) var isImporting = false
    private var failed: [Input] = []
    public private(set) var connection:AuthenticatedServer?
    public struct NavigationRequest:Identifiable,Sendable { public let id=UUID();public let serverURL:URL;public let transferID:String }
    public private(set) var navigationRequest:NavigationRequest?
    private var unauthorized:(@MainActor (UUID) async -> Void)?
    private let makeSubmissionAPI:@MainActor @Sendable (AuthenticatedServer) throws -> any TransferSubmissionAPI
    private var target: ServerAddress?
    private var lastReceiptID: UUID?
    private var snapshotRevision = 0
    private var reviewRevision = 0
    private var reviewOwner: UUID?
    private var reviewID: UUID?
    private var cleanupClaim: PendingClaim?
    private let loadDocument: @Sendable (URL) async throws -> TorrentFile
    private let openInbox: @Sendable () throws -> any PendingInboxAccess
    private var cachedInbox: (any PendingInboxAccess)?
    @ObservationIgnored private var renewal: Task<Void,Never>?
    public init(openInbox: @escaping @Sendable () throws -> any PendingInboxAccess = { try PendingInbox.shared() }, loadDocument:@escaping @Sendable (URL) async throws -> TorrentFile = { try await TorrentDocument.load($0) },makeSubmissionAPI:@escaping @MainActor @Sendable (AuthenticatedServer) throws -> any TransferSubmissionAPI = { try DebridAPIClient(server:ServerAddress($0.profile.serverURL.absoluteString),credential:$0.credential) }) { self.openInbox = openInbox; self.loadDocument = loadDocument;self.makeSubmissionAPI=makeSubmissionAPI }
    public convenience init(inbox:any PendingInboxAccess) { self.init(openInbox:{ inbox }) }
    deinit { renewal?.cancel() }
    private func inbox() throws -> any PendingInboxAccess {
        if let cachedInbox { return cachedInbox }; let store = try openInbox(); cachedInbox = store; return store
    }
    public func setConnection(_ server:AuthenticatedServer?,onUnauthorized:(@MainActor (UUID) async -> Void)? = nil) {
        if connection?.connectionID != server?.connectionID { navigationRequest=nil }
        connection=server;unauthorized=onUnauthorized;setTargetServer(server?.profile.serverURL)
    }
    public func rejectConnection(_ id:UUID) async {
        guard connection?.connectionID==id else { return };await unauthorized?(id)
    }
    public func submissionModel(claim:PendingClaim) throws -> TransferAddModel? {
        guard let connection else { return nil }
        return try TransferAddModel(claim:claim,server:connection,store:inbox(),api:makeSubmissionAPI(connection))
    }
    public func fileSelectionModel(transferID:String,name:String,serverURL:URL? = nil) throws -> FileSelectionModel? {
        guard let connection,serverURL == nil || serverURL==connection.profile.serverURL else {return nil}
        guard let store=try inbox() as? any FileSelectionIntentStoring else {throw FileSelectionError.unsupported}
        let api=try DebridAPIClient(server:ServerAddress(connection.profile.serverURL.absoluteString),credential:connection.credential)
        return try FileSelectionModel(server:connection,transferID:transferID,name:name,api:api,store:store,isConnectionCurrent:{[weak self] in self?.connection?.connectionID==connection.connectionID})
    }
    public func retryModel(kind:RetryResourceKind,resourceID:String,torrentID:String,name:String,serverURL:URL? = nil) throws -> RetryModel? {
        guard let connection,serverURL==nil || serverURL==connection.profile.serverURL else {return nil}
        guard let store=try inbox() as? any RetryIntentStoring else {throw APIError.invalidConfiguration}
        let api=try DebridAPIClient(server:ServerAddress(connection.profile.serverURL.absoluteString),credential:connection.credential)
        return try RetryModel(server:connection,kind:kind,resourceID:resourceID,torrentID:torrentID,name:name,api:api,store:store,isConnectionCurrent:{[weak self] in self?.connection?.connectionID==connection.connectionID})
    }
    public func finishNavigation(_ id:UUID) { if navigationRequest?.id==id { navigationRequest=nil } }
    public func viewTransfer(_ id:String,serverURL:URL) {
        guard connection?.profile.serverURL==serverURL,SubmissionIdentity.validID(id) else { return }
        navigationRequest=NavigationRequest(serverURL:serverURL,transferID:id);isPresented=false
    }
    public func setTargetServer(_ url:URL?) { target = url.flatMap { try? ServerAddress($0.absoluteString) } }
    public func present(focus:UUID? = nil) { focusID = focus; isPresented = true }
    public func reviewReceivedLink() { present(focus:lastReceiptID); notice = nil }
    public func dismissNotice() { notice = nil; if failed.isEmpty { intakeMessage = nil } }
    public func refresh() async {
        guard !Task.isCancelled else { return }
        snapshotRevision &+= 1; let revision = snapshotRevision; isLoading = true
        defer { if revision == snapshotRevision { isLoading = false } }
        do {
            let store=try inbox()
            let result = try await store.snapshot()
            let selections=try await (store as? any FileSelectionIntentStoring)?.savedFileSelections() ?? []
            let retries=try await (store as? any RetryIntentStoring)?.savedRetries() ?? []
            guard !Task.isCancelled, revision == snapshotRevision else { return }
            items = result;savedSelections=selections;savedRetries=retries; hasSnapshot = true; storageMessage = nil
        } catch {
            guard !Task.isCancelled, revision == snapshotRevision else { return }
            storageMessage = Self.message(error)
        }
    }
    /// Ignore authentication and unrelated schemes; ASWebAuthenticationSession owns its callback.
    @discardableResult public func receive(_ url:URL) async -> Bool {
        if url.isFileURL { await importDocument(url); return true }
        guard url.scheme?.lowercased() == "magnet" else { return false }
        guard !Task.isCancelled else { return true }
        do { await save(Input(content:.magnet(try MagnetLink(url.absoluteString)),source:.url,target:target)) }
        catch { intakeMessage = Self.message(error) }; return true
    }
    @discardableResult public func paste(_ text:String) async -> Bool {
        let capturedTarget = target
        do {
            let links = try await MagnetPaste.parse(text)
            var allSaved = true
            for link in links { try Task.checkCancellation(); if await !save(Input(content:.magnet(link),source:.paste,target:capturedTarget)) { allSaved = false } }
            return allSaved
        } catch { if !Task.isCancelled { intakeMessage = Self.message(error) }; return false }
    }
    public func importDocument(_ url:URL) async {
        guard !isImporting else { intakeMessage = "A file is being imported. Try again when it finishes."; return }
        isImporting = true; defer { isImporting = false }; let capturedTarget = target
        do { let file = try await loadDocument(url); try Task.checkCancellation(); await save(Input(content:.torrent(file),source:.file,target:capturedTarget)) }
        catch { if !Task.isCancelled { intakeMessage = Self.message(error) } }
    }
    private func enqueue(_ input:Input) async throws -> PendingReceipt {
        switch input.content {
        case .magnet(let link): try await inbox().enqueue(link,source:input.source,target:input.target)
        case .torrent(let file): try await inbox().enqueueTorrent(file,source:input.source,target:input.target)
        }
    }
    @discardableResult private func save(_ input:Input) async -> Bool {
        guard !Task.isCancelled else { return false }
        guard savingCount < PendingInbox.itemLimit else { intakeMessage = "Too many items are arriving. Try opening this item again shortly."; return false }
        savingCount += 1; defer { savingCount -= 1 }
        do {
            let receipt = try await enqueue(input)
            failed.removeAll { $0.content.key == input.content.key && $0.target == input.target }
            acknowledge(receipt); await refresh(); return true
        } catch { retainForRetry(input,error:error); return false }
    }
    private func acknowledge(_ receipt:PendingReceipt) {
        lastReceiptID = receipt.id
        notice = receipt.wasDuplicate ? "Item is already saved" : "Item saved for review"
        if failed.isEmpty { intakeMessage = nil }
    }
    private func retainForRetry(_ input:Input,error:any Error) {
        if !failed.contains(where:{ $0.content.key == input.content.key && $0.target == input.target }) {
            guard failed.count < PendingInbox.itemLimit, failed.reduce(0,{ $0+$1.content.byteCount })+input.content.byteCount <= TorrentFile.byteLimit else { intakeMessage = "Unsaved items are full. Retry them, then import this item again."; return }
            failed.append(input)
        }
        intakeMessage = Self.message(error)+" Keep Debrid open and retry to save the received item."
    }
    public func retryFailed() async {
        guard !isRetrying, !Task.isCancelled else { return }; isRetrying = true; defer { isRetrying = false }
        for input in failed {
            guard !Task.isCancelled else { break }
            do {
                let receipt = try await enqueue(input)
                failed.removeAll { $0.id == input.id }; acknowledge(receipt)
            } catch { intakeMessage = Self.message(error); break }
        }
        await refresh()
    }
    private func releaseTask(_ claim:PendingClaim,store:any PendingInboxAccess) -> Task<Void,Never> {
        Task.detached(priority:.utility) { try? await store.release(claim.item.id,token:claim.token) }
    }
    private func stopReview() -> Task<Void,Never>? {
        reviewRevision &+= 1; renewal?.cancel(); renewal = nil; reviewOwner = nil; reviewID = nil; isReviewLoading = false
        let claim = activeClaim ?? cleanupClaim; activeClaim = nil; cleanupClaim = nil
        guard let claim, let cachedInbox else { return nil }; return releaseTask(claim,store:cachedInbox)
    }
    @discardableResult public func endReview(owner:UUID) -> Task<Void,Never>? { guard reviewOwner == owner else { return nil }; return stopReview() }
    public func beginReview(_ id:UUID,owner:UUID) async {
        guard !Task.isCancelled else { return }
        if reviewOwner == owner, activeClaim?.item.id == id { return }
        let cleanup = stopReview(); reviewOwner = owner; reviewID = id
        let revision = reviewRevision; reviewMessage = nil; reviewNeedsConfirmation = false; isReviewLoading = true
        await cleanup?.value
        guard !Task.isCancelled, reviewRevision == revision, reviewOwner == owner else {
            if reviewRevision == revision, reviewOwner == owner { isReviewLoading = false }; return
        }
        do {
            let store = try inbox(), claim = try await store.claim(id)
            guard !Task.isCancelled, reviewRevision == revision, reviewOwner == owner else {
                if reviewRevision == revision, reviewOwner == owner { isReviewLoading = false }
                await releaseTask(claim,store:store).value; return
            }
            activeClaim = claim; cleanupClaim = claim; isReviewLoading = false
            renewal = Task { [weak self] in
                while !Task.isCancelled {
                    do { try await Task.sleep(for:.seconds(30)) } catch { return }
                    guard let self, self.reviewRevision == revision, self.reviewOwner == owner else { return }
                    do {
                        let expiry = try await store.renew(id,token:claim.token)
                        guard !Task.isCancelled, self.reviewRevision == revision, self.reviewOwner == owner else { return }
                        self.activeClaim = PendingClaim(item:claim.item,token:claim.token,expiresAt:expiry)
                        self.cleanupClaim = self.activeClaim
                    }
                    catch {
                        guard !Task.isCancelled, self.reviewRevision == revision else { return }
                        self.activeClaim = nil; self.reviewMessage = Self.message(error); return
                    }
                }
            }
        } catch {
            guard !Task.isCancelled, reviewRevision == revision, reviewOwner == owner else { return }
            isReviewLoading = false; reviewMessage = Self.message(error); reviewNeedsConfirmation = error as? PendingInboxError == .expired
        }
    }
    @discardableResult public func confirmKeep(_ id:UUID) async -> Bool {
        guard !Task.isCancelled else { return false }
        do { try await inbox().keep(id); await refresh(); return true }
        catch { if !Task.isCancelled { storageMessage = Self.message(error) }; return false }
    }
    public func discardSaved(_ id:UUID) async -> Bool {
        guard !Task.isCancelled else { return false }
        do { try await inbox().discard(id,token:nil); clearReceipt(id); await refresh(); return true }
        catch { if !Task.isCancelled { storageMessage = Self.message(error) }; return false }
    }
    public func discardReview(owner:UUID) async -> Bool {
        guard !Task.isCancelled, reviewOwner == owner, let claim = activeClaim else { return false }
        let revision = reviewRevision
        do {
            try await inbox().discard(claim.item.id,token:claim.token)
            guard reviewRevision == revision, reviewOwner == owner else { return false }
            clearReceipt(claim.item.id); activeClaim = nil; cleanupClaim = nil; _ = stopReview(); await refresh(); return true
        } catch { if !Task.isCancelled, reviewRevision == revision { reviewMessage = Self.message(error) }; return false }
    }
    private func clearReceipt(_ id:UUID) { if lastReceiptID == id { lastReceiptID = nil; notice = nil } }
    private static func message(_ error:any Error) -> String {
        if let error = error as? TorrentFileError { return error.errorDescription ?? "The torrent file could not be read." }
        if let error = error as? MagnetPasteError { return error.errorDescription ?? "The pasted text is invalid." }
        if let error = error as? MagnetError { return error.errorDescription ?? "This magnet link is invalid." }
        if let error = error as? PendingInboxError { return error.errorDescription ?? "Pending links are unavailable." }
        return "Pending links are unavailable. Try again."
    }
}
