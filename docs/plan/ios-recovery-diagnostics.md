# Recovery and diagnostics · milestone28

The app distinguishes offline/network access, name resolution, unreachable server, interrupted connection, TLS trust, expired session, denied access, server throttling, server errors, refused redirects and incompatible responses. Messages are drawn from an allowlist: arbitrary error descriptions, HTTP bodies and signed provider URLs never become support text. A DNS failure suggests checking tailnet access but does not claim to detect Tailscale.

Connection help is reached from Settings → Server and sign-in. Checks are explicit and anonymous: only existing public health/auth discovery routes are called. No session, provider credentials, transfer submission or action retry is involved. A successful check means public reachability and advertised native sign-in availability; session acceptance is explicitly not tested. Cancelled/dismissed/background/obsolete checks cannot publish a late report, and one in-flight owner prevents double checks.

The support report is reviewed before the native share action appears. Its allowlist includes timestamp, fixed outcome code, public reachability, advertised native Pocket ID availability and a strictly numeric semantic server version. Other server version strings are omitted entirely. It accepts no URL, username, issuer, file path, transfer name, credential or raw log/error body. Reports are held in memory and cleared on interruption/dismissal; this is a deliberate diagnostic summary, not a raw log export.

Failed transfer refreshes retain the last confirmed snapshot and show its absolute date/time. A confirmed list429 starts a bounded monotonic cooldown, including a conservative30-second delay when Retry-After is absent/invalid. A supplied delay is clamped to1…86400seconds. Manual taps, SSE-driven reconciliation and foreground restarts cannot bypass it; future scheduled refresh waits until the deadline. UI date/time is descriptive; the monotonic deadline controls network admission. Observed429 from an invalidated same-client request still extends the delay without publishing its obsolete data/auth state. Other actions keep their existing durable unknown-result review and observation behavior; connection help never automatically replays them.

## Emil design review

| Before | After | Why |
| --- | --- | --- |
| Generic incompatible response for503/access cases | Specific fixed guidance by failure type | Suggest a useful next step without exposing server bodies |
| Stale transfer warning omitted its age | Absolute last-confirmed timestamp | Old progress must not look freshly confirmed |
| Rate-limit warning disappeared on foreground restart | Visible cooldown survives lifecycle changes | The UI must explain why refresh is waiting |
| Repeated refresh taps could hit a throttled endpoint | Disabled action and monotonic admission delay | Feedback and request behavior should agree |
| Sharing arbitrary logs risks sensitive data | Reviewable allowlisted report and native ShareLink | Make the exact shared content visible and bounded |

One existing shared simulator/check-out/build directory is reused. Signed device, full VoiceOver and the real host-app URL/share matrix remain milestone30; no simulator result establishes them. Productionv0.2.8 is already verified; this milestone changes only the native app and documentation.

## Validation

Final combined run passed243 core tests and four native UI flows. Tests cover each network/server error family, adversarial raw error/version redaction, explicit single-flight anonymous checks, cancellation/dismissal/current-connection fences, empty and preloaded429 snapshots across foreground restart, observed late429 after cancellation and refresh after monotonic cooldown expiry. Existing session, protected inbox, unknown-action and URL/share tests run in the core suite and preserve their guarantees. No diagnostics action mutates pending items.

The first combined UI run exposed a test assumption: controls below the viewport in a lazy List cannot be found by waiting alone at largest Dynamic Type. The final flow scrolls to the native controls and verifies the result and share action are reachable; no font size or content was reduced. The final nine captures replace the initial partial set and all share one passing run.

Independent source and all-nine-image visual review approved the final implementation. The final unsigned arm64 iPhone Release compile/link passed. No backend source changed in this milestone, so validation focused on the native changes. Physical-device networking and full VoiceOver remain unverified release gates.
