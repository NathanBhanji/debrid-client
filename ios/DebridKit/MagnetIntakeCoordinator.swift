import Foundation
import Observation

public protocol PendingInboxAccess: Sendable {
    func snapshot() async throws -> [PendingMagnet]
    func enqueue(_ magnet:MagnetLink,source:PendingSource,target:ServerAddress?) async throws -> PendingReceipt
    func claim(_ id:UUID) async throws -> PendingClaim
    func renew(_ id:UUID,token:UUID) async throws -> Date
    func release(_ id:UUID,token:UUID) async throws
    func keep(_ id:UUID) async throws
    func discard(_ id:UUID,token:UUID?) async throws
}
extension PendingInbox: PendingInboxAccess {}

/// Routing ends at durable review. This type has no server mutation capability.
@Observable @MainActor public final class MagnetIntakeCoordinator {
    public private(set) var items: [PendingMagnet] = []
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
    private struct Input: Sendable { let id = UUID(); let magnet:MagnetLink; let target:ServerAddress? }
    private var failed: [Input] = []
    private var target: ServerAddress?
    private var lastReceiptID: UUID?
    private var snapshotRevision = 0
    private var reviewRevision = 0
    private var reviewOwner: UUID?
    private var reviewID: UUID?
    private var cleanupClaim: PendingClaim?
    private let openInbox: @Sendable () throws -> any PendingInboxAccess
    private var cachedInbox: (any PendingInboxAccess)?
    @ObservationIgnored private var renewal: Task<Void,Never>?
    public init(openInbox: @escaping @Sendable () throws -> any PendingInboxAccess = { try PendingInbox.shared() }) { self.openInbox = openInbox }
    public convenience init(inbox:any PendingInboxAccess) { self.init(openInbox:{ inbox }) }
    deinit { renewal?.cancel() }
    private func inbox() throws -> any PendingInboxAccess {
        if let cachedInbox { return cachedInbox }; let store = try openInbox(); cachedInbox = store; return store
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
            let result = try await inbox().snapshot()
            guard !Task.isCancelled, revision == snapshotRevision else { return }
            items = result; hasSnapshot = true; storageMessage = nil
        } catch {
            guard !Task.isCancelled, revision == snapshotRevision else { return }
            storageMessage = Self.message(error)
        }
    }
    /// Ignore authentication and unrelated schemes; ASWebAuthenticationSession owns its callback.
    @discardableResult public func receive(_ url:URL) async -> Bool {
        guard url.scheme?.lowercased() == "magnet" else { return false }
        guard !Task.isCancelled else { return true }
        do {
            let input = Input(magnet:try MagnetLink(url.absoluteString),target:target)
            guard savingCount < PendingInbox.itemLimit else { intakeMessage = "Too many links are arriving. Try opening this link again shortly."; return true }
            savingCount += 1; defer { savingCount -= 1 }
            do {
                let receipt = try await inbox().enqueue(input.magnet,source:.url,target:input.target)
                failed.removeAll { $0.magnet.rawURI == input.magnet.rawURI && $0.target == input.target }
                acknowledge(receipt); await refresh()
            } catch { retainForRetry(input,error:error) }
        } catch { intakeMessage = Self.message(error) }
        return true
    }
    private func acknowledge(_ receipt:PendingReceipt) {
        lastReceiptID = receipt.id
        notice = receipt.wasDuplicate ? "Link is already saved" : "Link saved for review"
        if failed.isEmpty { intakeMessage = nil }
    }
    private func retainForRetry(_ input:Input,error:any Error) {
        if !failed.contains(where:{ $0.magnet.rawURI == input.magnet.rawURI && $0.target == input.target }) {
            guard failed.count < PendingInbox.itemLimit else { intakeMessage = "Unsaved links are full. Retry them before opening another link."; return }
            failed.append(input)
        }
        intakeMessage = Self.message(error)+" Keep Debrid open and retry to save the received link."
    }
    public func retryFailed() async {
        guard !isRetrying, !Task.isCancelled else { return }; isRetrying = true; defer { isRetrying = false }
        for input in failed {
            guard !Task.isCancelled else { break }
            do {
                let receipt = try await inbox().enqueue(input.magnet,source:.url,target:input.target)
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
        if let error = error as? MagnetError { return error.errorDescription ?? "This magnet link is invalid." }
        if let error = error as? PendingInboxError { return error.errorDescription ?? "Pending links are unavailable." }
        return "Pending links are unavailable. Try again."
    }
}
