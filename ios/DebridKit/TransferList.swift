import Foundation
import Observation

public protocol TransferListing: Sendable { func torrents() async throws -> [TorrentDTO] }
extension DebridAPIClient: TransferListing {}

public enum TransferStage: Sendable, Equatable {
    case queued, adding, processing, selecting, providerDownloading, providerUploading, providerReady
    case nasQueued, nasDownloading, nasFinishing, unpacking, completed, failed, unknown
    public var label: String {
        switch self {
        case .queued: "Queued"
        case .adding: "Adding at provider"
        case .processing: "Processing at provider"
        case .selecting: "Choose files"
        case .providerDownloading: "Downloading at provider"
        case .providerUploading: "Preparing at provider"
        case .providerReady: "Ready at provider"
        case .nasQueued: "Queued for NAS"
        case .nasDownloading: "Downloading to NAS"
        case .nasFinishing: "Finishing on NAS"
        case .unpacking: "Unpacking on NAS"
        case .completed: "Complete"
        case .failed: "Needs attention"
        case .unknown: "Unknown state"
        }
    }
    public var needsAttention: Bool { self == .failed || self == .selecting || self == .unknown }
    public var isActive: Bool { self != .completed && self != .failed && self != .selecting && self != .unknown }
}

/// Presentation derives from the two distinct server lifecycles; finished is not local completion.
public struct TransferSummary: Identifiable, Sendable {
    public let transfer: TorrentDTO
    public var id: String { transfer.id }
    public let stage: TransferStage
    public let progress: Double?
    public let progressLabel: String
    public var name: String {
        var bounded = ""
        bounded.unicodeScalars.append(contentsOf: transfer.name.unicodeScalars.prefix(1024))
        let name = bounded.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? "Unnamed transfer" : String(name.prefix(256))
    }
    public init(_ transfer: TorrentDTO) {
        self.transfer = transfer
        switch transfer.status {
        case "queued": stage = .queued
        case "adding": stage = .adding
        case "processing": stage = .processing
        case "waiting_selection": stage = .selecting
        case "downloading": stage = .providerDownloading
        case "uploading": stage = .providerUploading
        case "completed": stage = .completed
        case "error": stage = .failed
        case "finished":
            let states = Set(transfer.downloads.map(\.state))
            if states.contains("error") { stage = .failed }
            else if states.contains("unpacking") { stage = .unpacking }
            else if states.contains("downloading") { stage = .nasDownloading }
            else if states.contains("unrestricting") || states.contains("pending") { stage = .nasQueued }
            else if states.contains("downloaded") || states.contains("done") { stage = .nasFinishing }
            else { stage = transfer.downloads.isEmpty ? .providerReady : .unknown }
        default: stage = .unknown
        }
        let isProvider = stage == .providerDownloading || stage == .providerUploading
        progressLabel = isProvider ? "Provider progress" : "NAS progress"
        let raw = isProvider ? transfer.providerProgress : transfer.localProgress
        if stage == .completed { progress = 1 }
        else if isProvider || stage == .nasDownloading || stage == .nasFinishing {
            progress = raw.isFinite && (transfer.size > 0 || raw > 0) ? min(1,max(0,raw)) : nil
        } else { progress = nil }
    }
}

@Observable @MainActor public final class TransferListModel {
    public enum Phase: Equatable { case idle, loading, ready, failed(String) }
    public enum LiveStatus: Equatable {
        case paused, connecting, connected, reconnecting, unavailable
        public var label: String {
            switch self {
            case .paused: "Updates paused"
            case .connecting: "Connecting live updates"
            case .connected: "Live updates connected"
            case .reconnecting: "Reconnecting updates"
            case .unavailable: "Refreshes periodically"
            }
        }
    }
    public private(set) var liveStatus: LiveStatus = .paused
    public private(set) var isLiveActive = false
    public private(set) var phase: Phase = .idle
    public private(set) var items: [TransferSummary] = []
    public private(set) var visibleItems: [TransferSummary] = []
    public var search = TransferSearch() { didSet {
        guard search != oldValue else { return }
        searchRevision &+= 1; applySearch()
        if !restoringSearch { schedulePreferenceSave() }
    } }
    public private(set) var accounts: [AccountDTO] = []
    public private(set) var accountsMessage: String?
    public private(set) var preferencesMessage: String?
    private var searchIndex = TransferSearchIndex(items: [], accounts: [])
    private var searchRevision = 0
    private var restoringSearch = false
    private var didRestoreSearch = false
    private let preferences: (any TransferPreferenceStoring)?
    @ObservationIgnored private var preferenceTask: Task<Void, Never>?
    @ObservationIgnored private var accountsTask: Task<Void, Never>?
    private var needsAccounts = false

    public private(set) var updatedAt: Date?
    public private(set) var requiresSignIn = false
    public let profile: ServerProfile
    private let api: any TransferListing
    private let events: (any ServerEventStreaming)?
    private let timing: LiveUpdateTiming
    @ObservationIgnored private var activeRequest: Task<[TorrentDTO], any Error>?
    @ObservationIgnored private var eventTask: Task<Void, Never>?
    @ObservationIgnored private var periodicTask: Task<Void, Never>?
    @ObservationIgnored private var refreshTask: Task<Void, Never>?
    private var needsReconciliation = false
    private var liveEpoch = 0
    private var revision = 0
    public convenience init(profile: ServerProfile, api: any TransferListing, events: (any ServerEventStreaming)? = nil, preferences: (any TransferPreferenceStoring)? = nil) {
        self.init(profile: profile, api: api, events: events, timing: LiveUpdateTiming(), preferences: preferences)
    }
    init(profile: ServerProfile, api: any TransferListing, events: (any ServerEventStreaming)?, timing: LiveUpdateTiming, preferences: (any TransferPreferenceStoring)? = nil) {
        self.profile = profile; self.api = api; self.events = events; self.timing = timing; self.preferences = preferences
    }
    public convenience init(server: AuthenticatedServer) throws {
        let client = try DebridAPIClient(server: ServerAddress(server.profile.serverURL.absoluteString), credential: server.credential)
        self.init(profile: server.profile, api: client, events: client, preferences: try? ProtectedTransferPreferences.applicationStore())
    }
    deinit { activeRequest?.cancel(); eventTask?.cancel(); periodicTask?.cancel(); refreshTask?.cancel(); accountsTask?.cancel() }
    public func reload() async {
        guard !Task.isCancelled else { return }
        revision &+= 1
        let request = revision
        phase = .loading; requiresSignIn = false
        activeRequest?.cancel()
        let api = self.api
        let task = Task { try Task.checkCancellation(); return try await api.torrents() }
        activeRequest = task
        defer {
            if request == revision {
                activeRequest = nil
                if needsReconciliation { requestReconciliation() }
            }
        }
        do {
            let transfers = try await withTaskCancellationHandler {
                try await task.value
            } onCancel: { task.cancel() }
            try Task.checkCancellation()
            guard request == revision else { return }
            guard Set(transfers.map(\.id)).count == transfers.count, transfers.allSatisfy({ !$0.id.isEmpty }) else { throw APIError.invalidResponse }
            items = transfers.map(TransferSummary.init)
            rebuildSearchIndex()
            updatedAt = Date(); phase = .ready
        } catch is CancellationError {
            if request == revision { phase = items.isEmpty ? .idle : .ready }
        } catch {
            guard request == revision else { return }
            guard !Task.isCancelled else { phase = items.isEmpty ? .idle : .ready; requiresSignIn = false; return }
            let unauthorized = error as? APIError == .unauthorized
            if unauthorized { stopLiveUpdates() }
            requiresSignIn = unauthorized
            let message = requiresSignIn ? "Your session has expired. Sign in again." :
                (error as? APIError == .forbidden ? "This server did not permit access to transfers." : ServerConnectionModel.message(for: error))
            phase = .failed(message)
        }
    }
    public func invalidate() { revision &+= 1; activeRequest?.cancel(); activeRequest = nil; requiresSignIn = false; phase = items.isEmpty ? .idle : .ready }

    public func refreshWhileActive() async {
        guard isLiveActive, !Task.isCancelled else { return }
        await reload()
    }

    /// Foreground ownership: one event loop, one periodic resync, and at most one scheduled refresh.
    public func startLiveUpdates() {
        guard !Task.isCancelled, !isLiveActive, !requiresSignIn else { return }
        liveEpoch &+= 1
        let epoch = liveEpoch
        isLiveActive = true
        liveStatus = events == nil ? .unavailable : .connecting
        if items.isEmpty { phase = .loading }
        requestReconciliation(after: .zero)
        reloadAccounts()
        let timing = self.timing
        periodicTask = Task { [weak self] in
            while !Task.isCancelled {
                do { try await timing.sleep(timing.periodic) } catch { return }
                guard self?.isCurrent(epoch) == true else { return }
                self?.requestReconciliation()
            }
        }
        guard let events else { return }
        eventTask = Task { [weak self] in
            var failures = 0
            while !Task.isCancelled {
                guard self?.isCurrent(epoch) == true else { return }
                let began = ContinuousClock.now
                var retryAfter: TimeInterval?
                do {
                    for try await event in events.events() {
                        guard !Task.isCancelled, self?.isCurrent(epoch) == true else { return }
                        self?.liveStatus = .connected
                        switch event {
                        case .connected: self?.requestReconciliation(); self?.reloadAccounts()
                        case .changed(let name):
                            self?.requestReconciliation()
                            if name == "account.changed" { self?.reloadAccounts() }
                        }
                    }
                } catch is CancellationError { return }
                catch {
                    guard !Task.isCancelled, self?.isCurrent(epoch) == true else { return }
                    if error as? APIError == .unauthorized {
                        self?.stopLiveUpdates()
                        self?.requiresSignIn = true
                        self?.phase = .failed("Your session has expired. Sign in again.")
                        return
                    }
                    switch error {
                    case APIError.forbidden, APIError.redirectRefused, APIError.invalidContentType,
                         APIError.invalidResponse, APIError.responseTooLarge, APIError.http(status: 404):
                        self?.liveStatus = .unavailable
                        return // Keep periodic reconciliation; retry capability on next foreground activation.
                    case APIError.rateLimited(let delay): retryAfter = delay
                    default: break
                    }
                }
                guard !Task.isCancelled, self?.isCurrent(epoch) == true else { return }
                if began.duration(to: .now) >= .seconds(30) { failures = 0 }
                failures = min(32,failures + 1)
                self?.liveStatus = .reconnecting
                let delay = SSEReconnectPolicy.delay(failures: failures, jitter: timing.jitter(), retryAfter: retryAfter)
                do { try await timing.sleep(delay) } catch { return }
            }
        }
    }
    private func isCurrent(_ epoch: Int) -> Bool { isLiveActive && liveEpoch == epoch }
    private func requestReconciliation(after delay: Duration? = nil) {
        guard isLiveActive, !requiresSignIn else { return }
        needsReconciliation = true
        guard activeRequest == nil, refreshTask == nil else { return }
        let epoch = liveEpoch, timing = self.timing
        refreshTask = Task { [weak self] in
            do { try await timing.sleep(delay ?? timing.coalesce) }
            catch { if self?.isCurrent(epoch) == true { self?.refreshTask = nil }; return }
            guard !Task.isCancelled, self?.isCurrent(epoch) == true else { return }
            self?.refreshTask = nil
            guard self?.activeRequest == nil else { return } // Its completion schedules the pending reconciliation.
            self?.needsReconciliation = false
            await self?.reload()
        }
    }
    public func stopLiveUpdates() {
        liveEpoch &+= 1; isLiveActive = false
        eventTask?.cancel(); eventTask = nil
        periodicTask?.cancel(); periodicTask = nil
        refreshTask?.cancel(); refreshTask = nil
        needsReconciliation = false
        accountsTask?.cancel(); accountsTask = nil; needsAccounts = false
        flushSearchPreferences()
        invalidate()
        liveStatus = .paused
    }


    public var categories: [String] {
        var values = Set(items.compactMap { $0.transfer.category }.filter { !$0.isEmpty })
        if case .named(let selected) = search.category { values.insert(selected) }
        return values.sorted { $0.localizedStandardCompare($1) == .orderedAscending }
    }
    public var accountOptions: [AccountFilterOption] {
        var labels: [String:String] = [:]
        for account in accounts {
            let name = TransferSearchIndex.bounded(account.name,limit:128)
            labels[account.id] = name.isEmpty ? ProviderNames.label(account.kind) : name+" · "+ProviderNames.label(account.kind)
        }
        var ids = Set(items.map { $0.transfer.accountId }); ids.formUnion(accounts.map(\.id))
        if let selected = search.accountID { ids.insert(selected) }
        let counts = Dictionary(grouping:labels.values,by:{ $0 }).mapValues(\.count)
        return ids.map { id in
            let base = labels[id] ?? "Unavailable account"
            let label = labels[id] == nil || (counts[base] ?? 0) > 1 ? base+" · "+String(id.prefix(6)) : base
            return AccountFilterOption(id:id,label:label)
        }.sorted { $0.label.localizedStandardCompare($1.label) == .orderedAscending }
    }
    public func setQuery(_ value: String) { search.query = TransferSearchIndex.bounded(value,limit:256) }
    public func resetSearch() { search = TransferSearch() }
    private func rebuildSearchIndex() { searchIndex = TransferSearchIndex(items:items,accounts:accounts); applySearch() }
    private func applySearch() { visibleItems = searchIndex.results(for:search) }

    public func restoreSearchIfNeeded() async {
        guard !didRestoreSearch, searchRevision == 0, let preferences else { return }
        let revision = searchRevision
        do {
            let saved = try await preferences.load(for:profile)
            try Task.checkCancellation()
            guard searchRevision == revision else { didRestoreSearch = true; return }
            if let saved {
                try saved.validate()
                restoringSearch = true; search = saved; restoringSearch = false
            }
            didRestoreSearch = true; preferencesMessage = nil
        } catch {
            guard !Task.isCancelled, searchRevision == revision else { return }
            preferencesMessage = "Saved search could not be restored. You can still search and filter here."
        }
    }
    private func schedulePreferenceSave() {
        preferenceTask?.cancel()
        guard let preferences else { return }
        let search = self.search, profile = self.profile, revision = searchRevision
        preferenceTask = Task { [weak self] in
            do {
                try await Task.sleep(for:.milliseconds(400))
                try await preferences.save(search,for:profile)
                if self?.searchRevision == revision { self?.preferencesMessage = nil }
            } catch {
                guard !Task.isCancelled, self?.searchRevision == revision else { return }
                self?.preferencesMessage = "Search could not be saved. It remains available while this screen is open."
            }
        }
    }
    public func flushSearchPreferences() {
        preferenceTask?.cancel()
        guard searchRevision > 0, !restoringSearch, let preferences else { return }
        let search = self.search, profile = self.profile, revision = searchRevision
        preferenceTask = Task { [weak self] in
            do {
                try Task.checkCancellation(); try await preferences.save(search,for:profile)
                if self?.searchRevision == revision { self?.preferencesMessage = nil }
            } catch {
                guard !Task.isCancelled, self?.searchRevision == revision else { return }
                self?.preferencesMessage = "Search could not be saved. It remains available while this screen is open."
            }
        }
    }
    public func reloadAccounts() {
        guard isLiveActive, !requiresSignIn, let api = api as? any AccountListing else { return }
        needsAccounts = true
        guard accountsTask == nil else { return }
        needsAccounts = false
        let epoch = liveEpoch
        accountsTask = Task { [weak self] in
            defer {
                if self?.isCurrent(epoch) == true {
                    self?.accountsTask = nil
                    if self?.needsAccounts == true { self?.reloadAccounts() }
                }
            }
            do {
                let accounts = try await api.accounts()
                try Task.checkCancellation()
                guard self?.isCurrent(epoch) == true else { return }
                guard Set(accounts.map(\.id)).count == accounts.count, accounts.allSatisfy({ !$0.id.isEmpty }) else { throw APIError.invalidResponse }
                self?.accounts = accounts; self?.accountsMessage = nil; self?.rebuildSearchIndex()
            } catch {
                guard !Task.isCancelled, self?.isCurrent(epoch) == true else { return }
                if error as? APIError == .unauthorized {
                    self?.stopLiveUpdates(); self?.requiresSignIn = true
                    self?.phase = .failed("Your session has expired. Sign in again.")
                } else { self?.accountsMessage = "Provider account labels are unavailable. Existing transfers can still be filtered by account." }
            }
        }
    }

}
