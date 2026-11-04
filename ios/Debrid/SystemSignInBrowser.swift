import AuthenticationServices
import DebridKit
import UIKit

/// Retained by the app coordinator, with exactly one continuation/session at a time.
@MainActor final class SystemSignInBrowser: NSObject, NativeSignInBrowser, ASWebAuthenticationPresentationContextProviding {
    private var session: ASWebAuthenticationSession?
    private var pending: CheckedContinuation<URL, any Error>?
    private var anchor: UIWindow?
    private var generation = 0

    func authenticate(url: URL) async throws -> URL {
        try Task.checkCancellation()
        guard pending == nil,
              let window = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene })
                .filter({ $0.activationState == .foregroundActive }).flatMap(\.windows).first(where: \.isKeyWindow)
        else { throw NativeSignInError.browserUnavailable }
        anchor = window
        generation &+= 1
        let operation = generation
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                guard !Task.isCancelled else { continuation.resume(throwing: CancellationError()); anchor = nil; return }
                pending = continuation
                let auth = ASWebAuthenticationSession(url: url, callback: .customScheme(NativeSignInAttempt.callbackScheme)) { [weak self] url, error in
                    Task { @MainActor in
                        guard self?.generation == operation else { return }
                        if let url { self?.finish(.success(url)) }
                        else if (error as? ASWebAuthenticationSessionError)?.code == .canceledLogin { self?.finish(.failure(CancellationError())) }
                        else { self?.finish(.failure(NativeSignInError.browserUnavailable)) }
                    }
                }
                auth.presentationContextProvider = self
                // Let the system ask for browser consent and reuse Pocket ID's existing browser session.
                auth.prefersEphemeralWebBrowserSession = false
                session = auth
                if !auth.start() { finish(.failure(NativeSignInError.browserUnavailable)) }
            }
        } onCancel: {
            Task { @MainActor [weak self] in
                guard self?.generation == operation else { return }
                self?.cancel()
            }
        }
    }
    func cancel() {
        generation &+= 1
        let active = session
        finish(.failure(CancellationError()))
        active?.cancel()
    }
    private func finish(_ result: Result<URL, any Error>) {
        let continuation = pending
        pending = nil; session = nil; anchor = nil
        continuation?.resume(with: result)
    }
    func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        // The anchor is acquired before starting and retained until completion.
        anchor ?? ASPresentationAnchor()
    }
}
