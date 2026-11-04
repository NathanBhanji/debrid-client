# Native Pocket ID SSO

The phone authenticates through the configured server’s existing OIDC client. It does not become a Pocket ID OAuth client, need the provider client secret, copy browser cookies, or require an extra provider redirect registration. Existing browser/password/API-key/CLI behavior remains available.

## Protocol

1. After explicit HTTPS server discovery, the app creates a random 32-byte base64url state and PKCE verifier using the platform secure random generator. It retains them only for that sign-in attempt.
2. Anonymous `POST /api/v1/auth/native/start` sends `{challenge, state}`. Challenge is S256; state/challenge must each canonically encode 32 bytes. The server refuses unconfigured/password mode. The response contains `authorization_url` with `Cache-Control: no-store`.
3. The server starts its usual confidential OIDC code flow with independent state/verifier, keeping the existing `/api/v1/auth/oidc/callback` registration. The phone opens the returned HTTPS authorization URL using the current `ASWebAuthenticationSession(url:callback:completionHandler:)` API and `.customScheme("dev.bhanji.debrid")` callback.
4. The server consumes provider state once, verifies the ID token and pinned subject, then creates a one-minute, one-use grant bound to the app challenge, configured issuer/client and local user. Only the grant hash lives in memory. No session exists yet. Callback is fixed to `dev.bhanji.debrid://auth/callback?code=…&state=…`, with no-store/no-referrer. The redirect never contains a session token. Provider denial/failure uses only `error=sign_in_failed` and the original app state.
5. The app checks the exact callback scheme/host/path, unique query fields and its state. It sends `{code, verifier}` to the same immutable server’s `POST /api/v1/auth/native/exchange`. A successful response returns `{session_token, username}` with no-store. No Set-Cookie is needed; the native client explicitly sends that server’s session cookie through its isolated transport and keeps the secret in Keychain.
6. Existing `/auth/me` validates/restores identity; `/auth/logout` revokes this session. Session lifetime follows existing server sliding expiry. Do not represent an initial expiry as a fixed deadline that overrides server sliding renewal.

The app must validate HTTPS authorization URLs and callback structure/state before exchange, discard attempt secrets after cancellation/completion, and never accept a callback as an ordinary unsolicited login. App death during sign-in requires a fresh attempt. This protocol does not change magnet-handler platform limitations.

## Security and failure boundaries

- Fixed callback, no arbitrary return URL parameter. ASWebAuthenticationSession isolates the calling app’s callback even with competing registered schemes; PKCE independently makes an intercepted grant useless without its verifier. See Apple’s [session guidance](https://developer.apple.com/documentation/authenticationservices/aswebauthenticationsession) and [current initializer](https://developer.apple.com/documentation/authenticationservices/aswebauthenticationsession/init(url:callback:completionhandler:)-6nut7).
- Native starts never perform first-run pinning. Existing sole-user OIDC authorization remains authoritative. Setup and login recheck issuer/client/mode inside their final database transactions; native session binding and insertion are also one transaction. Reconfiguration during remote exchange cannot pin an obsolete provider identity or issue a stale native grant session.
- Grants are hashed, consumed even on a wrong-verifier attempt, expire at one minute, disappear on restart, and share a 128-item memory cap. OIDC pending flows retain the existing 128-item/ten-minute bound. A full grant map fails safely rather than growing.
- Start/exchange are bounded POST bodies and independently rate-limited by direct peer address. TLS is required according to existing direct-TLS/trusted-proxy deployment conventions. Do not expose the NAS outside its tailnet or trust arbitrary forwarded headers by expanding the network boundary.
- Invalid/expired/unknown provider state cannot manufacture a native redirect. An expired attempt may end on the existing web error page; cancellation/restart remains available in the system browser. No provider error description enters a native callback. Error bodies and authorization/session values must not enter app diagnostics.
- Existing cookie-authenticated write CSRF checks remain intact. Native URLSession requests send no browser Origin; secrets are explicit per immutable server client, with redirects/cookie storage/cache refused.

## Validation

Go fake-provider and HTTP-route tests cover start/callback/exchange, exact provider redirect reuse, no native callback cookie, no-store/no-referrer, session use through existing middleware without API key, denial, wrong subject, native/browser isolation, wrong verifier, replay, inclusive expiry, concurrent consumption, malformed fields, HTTP rejection, unconfigured setup refusal and actual flow/grant bounds. Deterministic barriers hold provider exchange during issuer replacement and session issuance during same-issuer client replacement; neither writes an obsolete identity/session. No production accounts, magnets or transfers are mutated.

The backend source and both race fixes received independent security review. App orchestration, system-browser cancellation and Keychain/session recovery belong to roadmap 06. Signed physical-device/browser behavior remains a separate acceptance check; a simulator build is not evidence of it.
