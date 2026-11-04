# Native API and platform audit · 2026-11-03

Repository HEAD and remote main both c0b3a9b (security update #35), not the handoff's 2c4a615. No local AGENTS.md found; supplied memory instructions apply. Existing docs/DESIGN.md describes historical phase 1; actual routes and DTOs govern native behavior.

## Server contract

Sources: `internal/api/api.go`, `routes.go`, `auth.go`, `auth_oidc.go`, `internal/apimodel/model.go`, `internal/events/events.go`, `internal/service`, checked-in OpenAPI 3.1 at `internal/apiclient/openapi.yaml`.

- Public health `/api/v1/health` returns ok/version. `/auth/status` reports configured/mode/issuer when auth manager exists. Protected `/auth/me` verifies credentials. Capability detection must tolerate old/missing auth endpoints.
- Authorization checks Bearer API key first, query key only on SSE, then `debrid_session` cookie. Do not follow README's broad query-key description: native requests always use a header. Password login returns an HttpOnly session cookie. Cookie writes accept absent Origin (nonbrowser); reject foreign/null Origin. Sessions are sliding 30-day server records. Password change requires session and revokes sessions.
- OIDC start is a browser redirect; server callback sets its own cookie and redirects to `/`. ASWebAuthenticationSession does not hand that browser cookie jar to URLSession. Never scrape cookies or put API keys/session tokens in callback URLs. Plan a separate one-time, short-lived native exchange bound to state + PKCE, approved redirect, issuer/subject pinning, replay rejection and explicit expiry. Keep current browser and CLI behavior intact.
- API key is compatible with deployed 0.2.1 but grants broad server authority. Store only in Keychain, tied to canonical server origin/prefix. HTTPS, no cross-origin redirects carrying credentials, ephemeral URLSession cookie isolation, redacted diagnostics. Nathan chose Pocket ID SSO as the primary native login. Native API-key fallback is deferred; it is not a prerequisite for normal iPhone setup.
- Torrents: list (status/account/category filters), get by id/hash, POST magnet, multipart `.torrent`, PATCH category/full settings replacement, PUT files (nonempty IDs), POST retry, DELETE with independent files/provider booleans. There is **no pause or cancel endpoint**. Removal is destructive and must not masquerade as pause. Investigate real cancellation separately before showing it.
- Add magnet includes account/category/settings; settings replace defaults rather than merge. Multipart file add currently has account/category but no settings: focused parity PR required for review choices. Magnets may not expose file metadata until provider processing; ask for manual selection before add, show files later at waiting_selection.
- Accounts: CRUD/default/enabled plus POST test (may make provider calls); test is on demand, never implied by listing accounts. Force-delete also deletes local torrents. Provider credentials are entered only in secure forms, not retained as UI state after save.
- Categories live inside global settings; no standalone category resource. PUT settings replaces the complete document; concurrent edits need a revision/precondition strategy before safe mobile editing. No assumed cancellation, push, cursor paging or event replay capability.
- SSE events: torrent.added/updated/deleted, download.updated, account.changed, settings.changed; small invalidations, not full records. Heartbeat every 15s. Slow subscribers can miss events. Re-fetch on connect/reconnect/foreground; coalesce invalidations, cancel background stream, back off with jitter. Never call cached results live.
- Display provider fetching separately from local download/unpack. Backend progress values and unknown statuses require contract fixtures, not invented percentages. Server download paths are not iPhone files.

## Production boundary

Read-only `nas-agent debrid-client status` confirmed service running pinned 0.2.1 digest from handoff, source checkout not configured. No compose/env/log dumps, mutations, provider tests, release tags, restarts or deployments. The HTTPS health URL in the handoff is tailnet-only; future live tests require Tailscale on phone. No public exposure or ATS bypass. Later approved deployment uses GitHub-published pinned GHCR image, compose-put/up and health/log checks; preserve existing mounts, database and accounts.

## Confirmed product decision

Nathan approved Debrid and Transfers/Providers/Settings on 2026-11-03. The original teal palette is superseded by his correction to preserve the web identity within native iOS 27 design; see docs/design/ios/research.md. Pocket ID SSO is the primary sign-in, using the existing server OIDC integration plus the planned native exchange. Do not ask Nathan to copy a server API key onto the phone as the normal onboarding flow. Production 0.2.1 cannot support that exchange until a separately approved backend deployment.

## Apple evidence and limits

Reviewed Apple's [custom URL scheme documentation](https://developer.apple.com/documentation/xcode/defining-a-custom-url-scheme-for-your-app), [SwiftUI onOpenURL](https://developer.apple.com/documentation/swiftui/view/onopenurl(perform:)), [extension shared containers](https://developer.apple.com/library/archive/documentation/General/Conceptual/ExtensibilityPG/ExtensionScenarios.html), and [activation keys](https://developer.apple.com/library/archive/documentation/General/Reference/InfoPlistKeyReference/Articles/AppExtensionKeys.html).

Registering `magnet` is a candidate integration, not a promise of default handling: multiple registered apps have undefined routing. Browser/host behavior must be tested on Nathan's phone. Test cold and warm app delivery with exact URI/encoding, with another handler installed, Safari link taps/long press, Notes and Files. Custom schemes are untrusted input. Universal links apply to associated HTTPS domains, not ownership of all magnet URIs.

Share Extension uses NSItemProvider URL/plain text/file representations with bounded size/count/time, explicit activation rules (never TRUEPREDICATE in shipping builds). A shared webpage URL is not necessarily the target magnet: show unsupported content and suggest copying the magnet link. Do not scrape arbitrary pages or assume a Share Extension can launch its containing app. Review/save inside the extension; if offline or not configured, save a protected draft and tell the user to open Debrid themselves.

Planned App Group inbox: coordinated per-item atomic files, complete-until-first-unlock protection, size/count/age limits, stable IDs, pending/reviewed/submitting/confirmed/uncertain states, process-safe claims and crash recovery. Keep exact raw input distinct from normalized info-hash identity. No automatic POST on launch or reconnection; a timeout after POST is uncertain until reconciled. Cancellation of URLSession is not proof of server cancellation. Native cold/warm URL handling, extension lifecycle, app-group signing and real browser routing are **not verified by this foundation**.
