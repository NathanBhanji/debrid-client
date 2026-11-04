# PR 02 · API transport and wire contract

This milestone supplies a read-only typed client for health, auth capability/identity, torrents, accounts, settings and system status. It does not wire the app to a server, persist credentials, implement native OIDC or submit any mutations. Pocket ID remains the primary planned login; existing Bearer and explicit session-cookie formats are supported by transport for compatibility/testing.

## Boundaries

- HTTPS server root, optional reverse-proxy prefix, validated individual path components. Every client instance binds to one server and immutable credential.
- An isolated ephemeral URLSession clears automatic cookies, credential storage, caches and caller-provided default headers. Credentials go only on explicitly built requests, never query strings. Credential descriptions redact secrets. No custom TLS trust handler or ATS exception.
- All redirects are refused, including same-origin redirects outside a configured prefix. A redirect is a server configuration error for this API client. Native browser SSO is a separate future browser/exchange flow.
- Bounded response collection (8 MiB default, maximum 16 MiB), finite request/resource timeouts and no automatic retries. Body collection/decoding runs with `@concurrent` so it cannot monopolize a calling UI actor. Abandoned/oversized/error streams cancel their underlying task.
- 401/403/429/3xx, other HTTP failures, content type, malformed JSON, byte limits and URL errors have typed errors. Diagnostics do not retain raw response bodies or credential-bearing requests. Retry-After seconds/HTTP-date values are hints only, capped at one day; the client does not schedule retries.
- Required arrays follow the Go wire contract; optional/null fields are modelled explicitly. Raw status/provider strings preserve unknown server versions for later domain mapping. Fractional and whole RFC3339 timestamps decode without sharing mutable formatters.

## Contract evidence

`tools/iosfixtures` marshals the actual public Go apimodel types and invokes the actual health/auth handlers against a disposable local database with an explicitly fictional key. No production server/provider is contacted. The generated `testdata/ios-api/contract.json` is bundled into native tests, and `make generate-check` enforces freshness alongside the existing generated clients. No release/client schema or existing data changed.

Swift tests cover Go fixture decoding, 64-bit sizes, dates/nulls/unknown fields/statuses, required-array failures, prefix/header behavior, cookie/header isolation, redaction, error classes, byte limits, cancellation/timeouts and redirect policy. Tests intercept URLSession through a locked URLProtocol fixture; no network call can reach the NAS. The independent reviewer specifically requested actual in-flight task cancellation and real redirect callbacks in addition to direct error/delegate unit checks.

Apple primary references: [URLSession configuration](https://developer.apple.com/documentation/foundation/urlsessionconfiguration), [cookie handling](https://developer.apple.com/documentation/foundation/urlsessionconfiguration/httpshouldsetcookies), [redirect delegate](https://developer.apple.com/documentation/foundation/urlsessiontaskdelegate/urlsession(_:task:willperformhttpredirection:newrequest:completionhandler:)), [AsyncBytes task](https://developer.apple.com/documentation/foundation/urlsession/asyncbytes/task). Emil's write-swift skill guides value DTOs, explicit Sendable boundaries, isolated request work and Swift Testing. No third-party native dependency was added.

Full TLS/real-network validation, Keychain/device persistence, native OIDC exchange and foreground management belong to later milestones. Simulator tests do not certify signed-device trust/session behavior.

## Review and validation · 2026-11-04

Independent source review found no production transport/DTO blocker. It requested stronger test evidence; the final harness cancels a real held-open request and requires `stopLoading`, and emits real redirect proposals before observing that neither cross-origin nor same-origin/out-of-prefix targets receive a request. The redirect fixture requires proof the original proposal started, then cancels the deliberately suspended custom-protocol original; this avoids racing a fabricated finish callback. HTTP 3xx classification is tested separately. No production test hooks were added.

Validation uses the same existing iOS 27.0 simulator and core-only suite (18 Swift Testing functions plus parameter cases). Foundation's 3 native UI flows passed on the parent revision; this PR changes no views and does not repeat those UI runs. Go tidy/lint/generation freshness checks and internal/api tests pass; existing Go/TypeScript generated clients and go.mod/go.sum are unchanged. Simulator tests remain subject to the harmless Xcode runtime build-version warning already documented for the foundation.

Final independent review approved source and fixture-test design with no remaining actionable blocker. The final core rerun passed all 18 functions and parameter cases. The reviewer did not run tests or access devices; the main agent owns this validation evidence.
