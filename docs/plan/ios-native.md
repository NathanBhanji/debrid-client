# Native iPhone delivery · 30 reviewable PRs

Status: implementation underway; Nathan approved the Debrid name and three tabs, and chose Pocket ID SSO as the primary sign-in on 2026-11-03. He then corrected the visual direction: preserve the existing web identity while feeling native to iOS 27, using Emil’s design skills in boards and code. Milestones 01–20 are merged. They deliver reviewed native gallery, typed API/wire fixtures, server-scoped Keychain storage, HTTPS onboarding and Pocket ID backend/system-browser session lifecycle. Backend v0.2.4 is deployed by pinned image, with independently verified migration/data preservation. The live Transfers list is merged; foreground SSE reconciliation, search and native filters are merged; live detail is merged; file browsing is merged; magnet parsing is merged; protected pending storage is merged; magnet URL delivery and native handoff are merged; paste and torrent document import are merged; PR 16 supplies atomic enqueue/duplicate handling, multipart settings parity and durable submission reconciliation. See [delivery evidence](ios-delivery-log.md). Each number is a delivery unit with a coherent behavior and checks, not a commitment to open everything at once. Dependency numbers are logical prerequisites, not an instruction to create 30 simultaneous branches. Keep at most 2–3 open dependent PRs and one active implementation checkout. Nathan authorized merging validated PRs and deploying needed backend releases autonomously on 2026-11-04. Continue independent reviews and validation; preserve NAS access/data constraints and escalate genuine access/infrastructure blockers.

| PR | Deliverable | Depends | Acceptance checks |
|---|---|---|---|
| 01 | Design system/boards, API audit, roadmap, XcodeGen app/framework, fixture navigation and build workflow | — | Simulator and arm64 compile/link; no network; screen review; address-boundary tests |
| 02 | URLSession transport, exact DTO contract fixtures and errors | 01 | Decode representative Go API responses; unknown enums/null fields; 401/429/5xx; timeout/cancel; redirect credential isolation |
| 03 | Keychain native-session and isolated server profile storage | 02 | Keychain access/failure/logout; no secret defaults/logs; server switching cannot reuse another origin's credential |
| 04 | Server connect/discovery and SSO readiness onboarding | 03 | HTTPS/root prefix, health/auth-status, malformed host, DNS/Tailscale/unreachable, unsupported native SSO, cancellation; no production mutation |
| 05 | Native browser SSO exchange backend | 02 | Threat model review; PKCE/state/expiry/single-use; issuer pinning, redirect allowlist; browser/CLI regressions; no token callback leakage |
| 06 | ASWebAuthenticationSession Pocket ID SSO and session lifecycle | 03,04,05 | Browser cancel, cold callback, replay, app restart, revoked/expired session, logout; real device cookie isolation |
| 07 | Transfer repository and initial list | 06 | Loading/empty/error/refresh; status mapping; cancelled requests cannot overwrite new server; DTO fixtures |
| 08 | SSE invalidation and foreground reconciliation | 07 | Fragmented frames, heartbeat, missed-event resync, reconnect jitter; one stream; background/server-switch cancellation |
| 09 | Search/filter/category/provider lenses | 07,08 | Combined filters, empty results, restored query; keyboard/VoiceOver; bounded work on large fixture list |
| 10 | Transfer detail with two-stage progress and destination | 08,09 | Provider versus NAS states, unknown totals, unpacking, errors/stale snapshots; server paths clearly labeled |
| 11 | File tree and per-file download progress | 10 | Nested/duplicate names, large lists, independent download failures, unavailable metadata; no fake local file open |
| 12 | Exact magnet parser and intake domain | 02 | btih hex/base32 and supported btmh policy versus Go parser; repeated trackers/percent encoding; limits, invalid/control input; raw URI round trip |
| 13 | Protected durable pending inbox | 03,12 | Two-process claims/atomic writes, crash/restart, before-first-unlock, size/count/age limits, migration and dedupe; no silent discard |
| 14 | magnet: cold/warm URL routing and app review handoff | 06,13 | Simulator cold/warm; real Safari/Notes, competing handler and onboarding-pending URL; never auto-submit |
| 15 | Paste and torrent file document import | 12,13 | Explicit paste permission; security-scoped copy; oversized/malformed torrent, multiple/duplicate items; cancellation and cleanup |
| 16 | Backend add parity and duplicate reconciliation contract | 02,12 | Multipart settings parity, simultaneous same-hash adds, ambiguous timeout reconciliation; preserve existing clients/data |
| 17 | Native review/add sheet and submission coordinator | 06,13,15,16 | Provider/category/defaults/manual file policy, exactly one in-flight request, uncertain result, cancellation before/after send; full URI preserved |
| 18 | Backend manual file-selection hold and immutable review contract | 11,16 | Authoritative provider metadata, revision mismatch, one accepted choice, offline replay, held cached transfers, stale workers, restart/retry; preserve automatic clients |
| 19 | Native file-selection review and provider limitations | 11,17,18 | Nonempty selection, stale/ambiguous IDs, submit/reconcile, provider-specific scope, accessibility and fixture simulator review |
| 20 | Native Share Extension and coordinated draft review | 13,17 | URL/text/torrent NSItemProvider fixtures; extension termination, no forced app launch; offline save/reopen; real host app matrix and app-group signing |
| 21 | Retry transfer and failed file actions | 10,11 | Allowed states; retry one file; double tap/timeout; retry progress/history refresh; fixture server only |
| 22 | Backend cancellation semantics and API | 10 | Design safe worker/provider cancellation; distinguish remove/pause; concurrent completion, preserve completed files; data/restart regression tests |
| 23 | Cancel/remove confirmation UX | 21,22 | Cancel races; independent provider/files removal choices default safe; accessibility; never DELETE for pause; reconcile uncertain writes |
| 24 | Provider account list/detail and explicit health checks | 06 | All five kinds; expiry/quota unknown; partial provider failures; no background provider tests |
| 25 | Provider account add/edit/default/disable/removal | 24 | Secure entry/redaction; credential validation; default changes; force-delete consequences; no real account mutation in automation |
| 26 | Backend settings revision preconditions | 02 | Concurrent web/native edits rejected safely; backward-compatible old clients; typed validation, no data migration loss |
| 27 | Settings/categories/organization and per-transfer overrides | 10,26 | Refetch/merge review on conflict; full replacement semantics; template preview; existing download paths unaffected |
| 28 | Offline/stale/auth recovery and diagnostics | 06,08,20,25,27 | Airplane mode, tailnet off, DNS/server/TLS faults, 401/429; no false Tailscale detection; export redacted logs; preserve unsent drafts |
| 29 | Accessibility, light/dark and interaction hardening | 09–11,17–25,27,28 | Largest Dynamic Type, VoiceOver order/actions, contrast, Reduce Motion, landscape, keyboard and touch targets; approved screen comparisons |
| 30 | Signed device/TestFlight delivery and end-to-end acceptance | 29 | Distinct app/extension IDs/group/profiles/ASC record; arm64 link/embed, signed device Keychain/group; real cold/warm/share matrix; privacy/icon metadata; bounded artifact cleanup |

File selection was split into backend 18 and native 19 on 2026-11-04 after auditing worker races and authoritative provider metadata. The backend needs its own review, compatibility checks and deployment before the native picker can enable the capability. This brings the total to 30 coherent PRs within the original 28–30 range.

## Review gates

After 01: review board and runnable fixture app here with Nathan; name and tabs are approved; review the revised web-family/native iOS 27 visual design; prioritize Pocket ID SSO before live use. Confirm phone OS and review the detailed add/share states. PRs 02/03/12 can progress independently of styling while feedback is pending.

After 04: discovery can check public health/auth status only. Use a fixture server first. Confirm 05's native auth contract before building 06; Pocket ID SSO is the live-use gate. Existing API-key support remains compatible for CLI/MCP; a native advanced fallback is deferred, not part of normal onboarding. Never request secrets in chat. Backend changes must be published through the existing image workflow before deployment; Nathan has authorized this delivery work, including validated merges and needed deployments.

After 17/20: demonstrate add/share review using a disposable local server and fixtures. Live submissions require deliberately chosen content; do not send example magnets to production. Review exactly what happens offline and after uncertain submission. Confirm cancellation meaning at 22 rather than invent a pause action.

After 25/27: review account deletion and settings conflict UX. After 29: Nathan accepts accessibility and screen direction. At 30: provision this app's identifiers, verify on physical iPhone, then prepare and deliver the concrete TestFlight build under Nathan’s authorization. Keep release artifacts pinned and validate deployment/device behavior.

## Validation strategy

Pure Swift tests for boundaries, parser, state machines and dedupe. URLProtocol/local HTTP fixtures for transport/SSE and ambiguous writes. Go tests for backend additions with OpenAPI generation checks. Host-driven extension tests and installed-app device checks for platform integration. Simulator success never substitutes for device signing, shared-container/Keychain behavior or browser routing. Record actual evidence and remaining gaps per PR, using the storage policy in ios/README.md. Widgets/push are deferred: current API has no notification service, and transfer management should work first.
