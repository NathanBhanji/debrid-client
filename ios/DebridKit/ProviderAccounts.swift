import Foundation
import Observation

public protocol ProviderAccountAPI: AccountListing {
    var serverURL: URL { get }
    func account(id: String) async throws -> AccountDTO
    func checkAccount(id: String) async throws -> ProviderUserDTO
}
extension DebridAPIClient: ProviderAccountAPI {
    public func account(id: String) async throws -> AccountDTO {
        let account: AccountDTO = try await send(["accounts", id], method: "GET", expectedStatus: 200)
        guard account.id == id else { throw APIError.invalidResponse }
        return account
    }
    public func checkAccount(id: String) async throws -> ProviderUserDTO {
        try await send(["accounts", id, "test"], method: "POST", expectedStatus: 200)
    }
}

/// A successful check verifies credentials at that moment, not download availability or quota.
public struct ProviderCheck: Sendable {
    public let user: ProviderUserDTO
    public let checkedAt: Date
    public let accountUpdatedAt: Date
}

@Observable @MainActor public final class ProviderAccountsModel {
    public let connectionID: UUID
    public let serverURL: URL
    public private(set) var accounts: [AccountDTO] = []
    public private(set) var checks: [String: ProviderCheck] = [:]
    public private(set) var checkErrors: [String: String] = [:]
    public private(set) var checkingID: String?
    public private(set) var isChecking = false
    public private(set) var isActive = true
    public private(set) var isLoading = false
    public private(set) var hasLoaded = false
    public private(set) var isStale = true
    public private(set) var message: String?
    public private(set) var requiresSignIn = false
    private let api: any ProviderAccountAPI
    private let isCurrent: @MainActor () -> Bool
    private var generation = 0

    public init(server: AuthenticatedServer, api: any ProviderAccountAPI,
                isCurrent: @escaping @MainActor () -> Bool = { true }) throws {
        guard api.serverURL == server.profile.serverURL else { throw APIError.invalidConfiguration }
        self.connectionID = server.connectionID; self.serverURL = api.serverURL
        self.api = api; self.isCurrent = isCurrent
    }
    private func valid(_ operation: Int) -> Bool {
        isActive && operation == generation && !Task.isCancelled && isCurrent()
    }
    public func start() {
        guard !Task.isCancelled, isCurrent() else { return }
        isActive = true
    }
    public func stop() {
        isActive = false
        generation &+= 1; isLoading = false; checkingID = nil; isStale = true
    }
    public func refresh() async {
        guard isActive, !isLoading, !isChecking, !Task.isCancelled, isCurrent() else { return }
        generation &+= 1
        let operation = generation
        isLoading = true; checkingID = nil; message = nil
        defer { if generation == operation { isLoading = false } }
        do {
            let rows = try await api.accounts()
            guard valid(operation) else { return }
            guard Set(rows.map(\.id)).count == rows.count,
                  rows.allSatisfy({ !$0.id.isEmpty && $0.createdAt.timeIntervalSince1970.isFinite && $0.updatedAt.timeIntervalSince1970.isFinite })
            else { throw APIError.invalidResponse }
            checks = checks.filter { id, check in rows.contains { $0.id == id && $0.updatedAt == check.accountUpdatedAt } }
            checkErrors = checkErrors.filter { id, _ in rows.contains { $0.id == id } }
            accounts = rows; hasLoaded = true; isStale = false; requiresSignIn = false
        } catch {
            guard valid(operation) else { return }
            isStale = true; message = Self.message(error)
            requiresSignIn = error as? APIError == .unauthorized
        }
    }
    /// Never invoked by loading, opening detail, a timer, or an automatic retry.
    public func check(id: String) async {
        guard isActive, !isLoading, !isChecking, !Task.isCancelled, isCurrent(), accounts.contains(where: { $0.id == id }) else { return }
        let operation = generation
        checkingID = id; isChecking = true; checkErrors[id] = nil
        defer { isChecking = false; checkingID = nil }
        do {
            let before = try await api.account(id: id)
            guard valid(operation) else { return }
            guard before.id == id else { throw APIError.invalidResponse }
            replace(before)
            let user = try await api.checkAccount(id: id)
            guard valid(operation) else { return }
            let after = try await api.account(id: id)
            guard valid(operation) else { return }
            guard after.id == id else { throw APIError.invalidResponse }
            replace(after)
            guard before.updatedAt == after.updatedAt, before.kind == after.kind else {
                checks[id] = nil
                checkErrors[id] = "This account changed during the check. Check it again."
                return
            }
            checks[id] = ProviderCheck(user: user, checkedAt: Date(), accountUpdatedAt: after.updatedAt)
            requiresSignIn = false
        } catch {
            guard valid(operation) else { return }
            // Preserve the prior successful result, visibly dated, alongside the new error.
            checkErrors[id] = Self.message(error)
            requiresSignIn = error as? APIError == .unauthorized
        }
    }
    private func replace(_ row: AccountDTO) {
        guard let index = accounts.firstIndex(where: { $0.id == row.id }) else { return }
        if checks[row.id]?.accountUpdatedAt != row.updatedAt { checks[row.id] = nil }
        accounts[index] = row
    }
    private static func message(_ error: any Error) -> String {
        switch error {
        case APIError.unauthorized: "Your session has expired. Sign in with Pocket ID again."
        case APIError.http(status: 404): "This account is no longer available. Refresh the list."
        case APIError.http(status: 422): "The provider could not verify this account. Check its credentials and try again."
        case APIError.forbidden: "This session does not have permission to check this account."
        case APIError.rateLimited: "Too many requests. Wait before checking again."
        default: ServerConnectionModel.message(for: error)
        }
    }
}
