import Foundation
import Observation

public struct AuthenticatedServer: Sendable {
    public let profile: ServerProfile
    public let username: String
    public let credential: APICredential
    public init(profile: ServerProfile, username: String, credential: APICredential) {
        self.profile = profile; self.username = username; self.credential = credential
    }
}

@Observable @MainActor public final class SessionCoordinator {
    public enum Phase {
        case signedOut, restoring, signingIn, connected(AuthenticatedServer)
    }
    public private(set) var phase: Phase = .signedOut
    public private(set) var isBusy = false
    public private(set) var message: String?
    public private(set) var pendingLocalRemoval: ServerProfile?
    private let store: SessionStore
    private let browser: any NativeSignInBrowser
    private let makeAPI: @Sendable (ServerProfile, APICredential?) throws -> any NativeSignInAPI
    private var generation = 0

    public init(store: SessionStore, browser: any NativeSignInBrowser) {
        self.store = store; self.browser = browser
        makeAPI = { profile, credential in try DebridAPIClient(server: ServerAddress(profile.serverURL.absoluteString), credential: credential) }
    }
    init(store: SessionStore, browser: any NativeSignInBrowser,
         makeAPI: @escaping @Sendable (ServerProfile, APICredential?) throws -> any NativeSignInAPI) {
        self.store = store; self.browser = browser; self.makeAPI = makeAPI
    }
    private func check(_ operation: Int) throws {
        try Task.checkCancellation()
        guard operation == generation else { throw CancellationError() }
    }
    public func cancel() {
        guard isBusy else { return }
        switch phase { case .signingIn, .restoring: break; default: return }
        generation &+= 1
        browser.cancel()
        phase = .signedOut
        message = nil
    }
    public func signIn(to profile: ServerProfile) async {
        guard !Task.isCancelled, !isBusy, pendingLocalRemoval == nil else { return }
        isBusy = true; generation &+= 1
        let operation = generation
        phase = .signingIn; message = nil
        var storedID: String?
        var received: NativeSessionDTO?
        defer { isBusy = false }
        do {
            let api = try makeAPI(profile, nil)
            let attempt = try NativeSignInAttempt()
            let start = try await api.startNative(challenge: attempt.challenge, state: attempt.state)
            try check(operation)
            let callback = try await browser.authenticate(url: attempt.authorizationURL(start))
            try check(operation)
            let session = try await attempt.complete(callback: callback, using: api)
            received = session
            try check(operation)
            let credential = try APICredential.session(session.sessionToken)
            guard !session.username.isEmpty else { throw APIError.invalidResponse }
            try await store.saveProfile(profile)
            try check(operation)
            try await store.saveSession(token: session.sessionToken, username: session.username, for: profile.id)
            storedID = profile.id
            try check(operation)
            try await store.select(profile.id)
            try check(operation)
            phase = .connected(AuthenticatedServer(profile: profile, username: session.username, credential: credential))
        } catch {
            var cleanupFailed = false
            if let storedID {
                do { try await store.signOut(storedID) }
                catch { cleanupFailed = true }
            }
            // Best effort only: an interrupted exchange may have created a server session whose token never reached us.
            if !Task.isCancelled, let received, let credential = try? APICredential.session(received.sessionToken),
               let api = try? makeAPI(profile, credential) { try? await api.logout() }
            if operation == generation {
                phase = .signedOut
                message = Task.isCancelled || error is CancellationError ? nil : Self.message(for: error)
            }
            if cleanupFailed { pendingLocalRemoval = profile; message = "Sign-in was interrupted, but its saved session could not be removed. Unlock the iPhone, then remove the saved session below." }
        }
    }
    public func restore(connection: ServerConnectionModel? = nil) async {
        if let connection, !connection.address.isEmpty { return }
        guard !Task.isCancelled, !isBusy, pendingLocalRemoval == nil else { return }
        isBusy = true; generation &+= 1
        let operation = generation
        phase = .restoring; message = nil
        defer { isBusy = false }
        var restoringProfile: ServerProfile?
        var inputRevision = connection?.addressRevision
        func checkIntent() throws {
            try check(operation)
            if let connection, connection.addressRevision != inputRevision { throw CancellationError() }
        }
        do {
            guard let profile = try await store.selectedProfile() else { phase = .signedOut; return }
            restoringProfile = profile
            try checkIntent()
            if let connection, let expectedRevision = inputRevision {
                connection.restoreAddress(profile.serverURL.absoluteString, expectedRevision: expectedRevision)
                inputRevision = connection.addressRevision
            }
            guard let saved = try await store.session(for: profile.id) else { phase = .signedOut; return }
            try checkIntent()
            let identity = try await makeAPI(profile, saved.credential).identity()
            try checkIntent()
            guard !identity.apiKey, identity.mode == "oidc", let username = identity.username, !username.isEmpty else { throw APIError.unauthorized }
            phase = .connected(AuthenticatedServer(profile: profile, username: username, credential: saved.credential))
        } catch {
            if operation == generation {
                phase = .signedOut
                guard !Task.isCancelled else { message = nil; return }
                message = error is CancellationError ? nil : Self.message(for: error)
                if error as? APIError == .unauthorized, let restoringProfile {
                    do { try await store.signOut(restoringProfile.id) }
                    catch { pendingLocalRemoval = restoringProfile; message = "This session was revoked, but its saved copy could not be removed. Unlock the iPhone, then remove the saved session below." }
                }
            }
        }
    }
    /// A response from an old server/view can never clear the current server's session.
    public func sessionRejected(for profileID: String) async {
        guard !Task.isCancelled, !isBusy, case .connected(let connected) = phase, connected.profile.id == profileID else { return }
        isBusy = true
        defer { isBusy = false }
        phase = .signedOut
        message = "Your session has expired or was revoked. Sign in with Pocket ID again."
        do { try await store.signOut(profileID) }
        catch {
            pendingLocalRemoval = connected.profile
            message = "The rejected session could not be removed. Unlock the iPhone, then remove the saved session below."
        }
    }
    public func signOut() async {
        guard !isBusy, case .connected(let connected) = phase else { return }
        isBusy = true
        defer { isBusy = false }
        var remoteFailed = false
        do { try await makeAPI(connected.profile, connected.credential).logout() }
        catch { remoteFailed = error as? APIError != .unauthorized }
        do {
            try await store.signOut(connected.profile.id)
            phase = .signedOut
            message = remoteFailed ? "Signed out on this iPhone. The server could not confirm session revocation; it will expire under the server’s session policy." : nil
        } catch {
            pendingLocalRemoval = connected.profile; phase = .signedOut
            message = "The saved session could not be removed. Unlock the iPhone, then remove it below."
        }
    }
    /// Local-only recovery is available even when the server is offline or the session is revoked.
    public func removeSavedSession() async {
        guard !isBusy, let profile = pendingLocalRemoval else { return }
        isBusy = true
        defer { isBusy = false }
        do {
            try await store.signOut(profile.id)
            pendingLocalRemoval = nil; phase = .signedOut
            message = "Saved session removed from this iPhone."
        } catch { message = "The saved session could not be removed. Unlock the iPhone and try again." }
    }
    private static func message(for error: any Error) -> String {
        switch error {
        case NativeSignInError.refused: "Pocket ID sign-in was cancelled or refused. Try again."
        case NativeSignInError.invalidCallback, NativeSignInError.stateMismatch: "The sign-in response did not match this attempt. Start a new sign-in."
        case NativeSignInError.invalidAuthorizationURL: "The server returned an unsafe sign-in address. Check its Pocket ID configuration."
        case NativeSignInError.browserUnavailable: "The sign-in browser could not open. Try again."
        case SessionStorageError.locked: "Unlock the iPhone to access its saved session."
        case is SessionStorageError: "Your sign-in could not be saved securely. Unlock this iPhone and try again."
        case APIError.unauthorized: "Your session has expired or was revoked. Sign in with Pocket ID again."
        case APIError.http(status: 404), APIError.http(status: 409): "Update the server and check that Pocket ID is configured, then try again."
        default: ServerConnectionModel.message(for: error)
        }
    }
}
