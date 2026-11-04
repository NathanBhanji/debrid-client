# Debrid for iPhone

Native SwiftUI app, Swift 6 complete concurrency checking. **Current milestone: Pocket ID connection, live Transfers, search, filters and details.** Normal launch restores a selected server session or opens Connect. Public discovery advertises native SSO on servers with the exchange addition (v0.2.2+). The live Transfers list uses foreground SSE reconciliation, native search and combined status/category/account filters; intake and provider/settings management follow in subsequent milestones. This is not ready for daily use. Debug `-Demo` or the native design gallery use fixture data without network or mutations.

XcodeGen `project.yml` is the source of truth. Do not edit generated projects/Info.plists/entitlements. Xcode 27.1 (27A9275), iPhoneOS/Simulator SDK 27.1 and installed iOS 27.0 runtime verified on 2026-11-03. Minimum iOS **27.0**, provisionally matching Nathan's app fleet and the available runtime; confirm the phone's OS before device delivery. No third-party packages. Design and Swift implementation follow Emil’s `emil-design-eng`, `apple-design` and `write-swift` skills; see the [research/review](../docs/design/ios/research.md). Nathan requested the existing web identity within native iOS 27 navigation and controls, and chose Pocket ID SSO as the primary login; this requires a secure native exchange addition to the backend before live use.

```sh
cd ios
xcodegen generate
python3 scripts/check.py build
python3 scripts/check.py device # arm64 device compilation/linking, signing disabled
python3 scripts/check.py test --simulator <existing-Debrid-simulator-UUID>
open Debrid.xcodeproj
```

`dev.bhanji.debrid`, framework `dev.bhanji.debrid.kit`; proposed extension `dev.bhanji.debrid.share`, App Group `group.dev.bhanji.debrid`, team `ZTN995BU9H`. Only app/framework/test targets exist today. The app has an explicit private Keychain access group; there are no copied profiles, extension-shared secrets or APNs capabilities. Device builds in Xcode use automatic signing; simulator checks use ad-hoc signing and a hosted test bundle so actual Keychain operations are exercised; unsigned device checks compile/link separately. Device installation, App Group/Keychain verification, app icon, distribution profiles and App Store Connect registration are future gates. Never reuse Flux/Heimdall profiles. No TestFlight upload is possible yet.

`DebridKit` is extension-safe and owns domain/transport/storage boundaries as they are added. `TransferPreview` is presentation data, not a speculative API schema. `ServerAddress` validates HTTPS roots without credentials/query fragments and preserves proxy prefixes. API DTOs, URLSession client, Keychain and protected inbox arrive separately with focused tests. Never persist secrets in defaults, design fixtures or logs.

## Resource policy

Never create a simulator per PR or reviewer. Check `xcrun simctl list devices available`, runtimes, `pgrep -fl xcodebuild` and disk space first. Reuse one named `Debrid Shared iPhone` device on iOS 27.0; record its UUID locally, pass it explicitly, and serialize tests. Do not borrow a booted device from another project. Do not install runtimes automatically. If no Debrid device exists, create exactly one after checking free space (at least 20 GB). A busy device is a reason to wait, not clone it.

Checks have a 15 minute process-group timeout, one derived-data directory, no parallel simulator clones, no explicit xcresult archive, and one log per operation. Logs retain the final 2 MB after completion. Xcode activity/results can still grow under `.build`: after each milestone inspect `du -sh .build .artifacts`, retain at most one relevant result and 8 screenshots, and remove only this checkout's generated artifacts once no owned test is running. Never erase unrelated caches/devices or delete a runtime. Shut down the Debrid simulator after use; retain it for reuse. Use this worktree for the train; only create another when independent work actually requires it. Remove completed worktrees only after checking clean state, pushed branch and PR safety. Never delete uncommitted changes.

Design: [spec](../docs/design/ios/README.md), [board](../docs/design/ios/board.html). Delivery: [29-PR plan](../docs/plan/ios-native.md). Audit: [API/platform evidence](../docs/plan/ios-audit.md).

## Native design gallery

Nathan requested native simulator evidence for the complete screen family. Debug builds expose `-DesignGallery` (browse sheets) and `-DesignScreen <slug>` (standalone capture). Screens use real SwiftUI forms/navigation/pickers and shared design components, with fictional data and honest preview-only action alerts. The gallery is compiled out of Release; it does not implement live features ahead of their roadmap milestones.

```sh
python3 scripts/check.py build
python3 scripts/capture-gallery.py --simulator <existing-Debrid-simulator-UUID>
# Optional small recapture batch:
python3 scripts/capture-gallery.py --simulator <UUID> --screens connect sign-in add
```

Check/test/capture share a checkout lock; test and capture additionally acquire a canonical UUID lock under `~/Library/Caches/dev.bhanji.debrid/locks`, spanning worktrees. Capture refuses a booted device, creates no devices, has bounded subprocesses and restores/shuts down only the shared device it booted. Capture and tests must run serially.

Gallery retention is one catalog of **at most 32 screenshots**, one per route, plus the existing two primary appearance references. This explicitly replaces the eight-image local limit for Nathan’s requested all-screen catalog; keep it below 15 MB, overwrite the same route files rather than keeping per-PR sets, and retain at most eight temporary captures. Never copy derived data or make a simulator per screen. Future state permutations are interactive studies, not an assertion that every state has been captured.

## Typed API transport

`DebridAPIClient` is a server-bound URLSession client. The app now constructs anonymous discovery/auth clients and explicit credential-bound session clients. Native auth/logout are bounded POST requests; transfer/provider mutations arrive separately. DTOs decode the Go wire contract; credentials are explicit per-client values with redacted descriptions, with server-bound Keychain persistence. See [transport boundaries](../docs/plan/ios-api-transport.md).

```sh
make generate-check # repository root; includes Go-produced native fixtures
python3 ios/scripts/check.py test --suite core --simulator <UUID>
```

Use core-only tests when UI is unchanged; the default suite still includes native interaction tests. All tests/captures use the same locks/device policy. Test diagnostics collection is disabled to avoid large automatic sysdiagnose archives; one latest result and bounded log are retained.

## Server profiles and session storage

`SessionStore` serializes one app-owned namespace. `ServerProfile` canonicalizes HTTPS origins/proxy prefixes and hashes the canonical URL into the credential account. Non-secret profiles/selection live in defaults; each versioned session is bound again to its exact URL inside Keychain. The Keychain adapter requires an explicit entitled group on every operation, is non-synchronizable and uses `WhenUnlockedThisDeviceOnly`. There is no plaintext fallback when locked, corrupt or missing entitlements. Local logout deletes only that server’s secret; server-side revocation belongs to the live session coordinator.

Metadata and Keychain are separate stores, not a two-store transaction. Removal deletes the secret first: if metadata then fails, the visible profile can remain signed out, while other servers are untouched. Own exactly one `SessionStore` per namespace. A future extension inbox coordinates files separately and does not need session credentials.

Validation: 28 Swift Testing functions (including real hosted simulator Keychain read/update/delete, accessibility attributes, namespace isolation, expiry, storage failures and proxy-path boundaries) passed on the shared iOS 27.0 device. Physical-device entitlement/protection behavior remains a delivery check. Apple’s [Keychain access-group guidance](https://developer.apple.com/documentation/security/sharing-access-to-keychain-items-among-a-collection-of-apps) explains why the adapter never searches implicitly across entitled groups.

## Server onboarding

Normal launch opens the native Connect form. It requires a full HTTPS URL, checks anonymous `/health` and `/auth/status`, then offers explicit non-secret server saving. Health must be healthy; authentication setup/mode and optional `native_sso` capability determine readiness. An older OIDC server reports that an update is needed, rather than falling back to copying an API key. Password-only setups are described honestly; changing a configured password server into OIDC is an administrator operation.

Discovery constructs an anonymous, redirect-refusing transport per server. Editing/cancelling invalidates results, including tasks cancelled before they start. No auto-discovery on launch, provider probes, real transfers, credentials or TLS bypass. Saved metadata restores the address only while input remains untouched; the session coordinator independently validates saved identity before connecting. DNS/network messages suggest checking Tailscale without claiming to detect VPN status.

## Pocket ID and session recovery

The app uses the current `ASWebAuthenticationSession` callback API, an active scene window, platform secure randomness and app-owned S256 verifier/state. The server keeps its existing OIDC callback registration. The app verifies the exact callback target, two unique query fields, state and canonical grant before exchanging with that same server. No ordinary URL-open path can authenticate an unsolicited callback. Attempt data is transient; app death restarts sign-in. See [protocol and threat model](../docs/plan/ios-native-auth.md).

One coordinator serializes sign-in/restore. Cancellation generations reject late responses and overlapping attempts; old browser completion/cancellation cannot complete a newer session. Startup checks address intent across metadata/credential/identity awaits. Stored sessions are validated through `/auth/me`; 401 removes only that server’s secret, while offline failures retain it for reconnect.

Sign-out attempts server revocation and removes the local session. Offline revocation is reported honestly; an interrupted exchange can create an unreachable server session that expires under existing server policy. If secure deletion fails, Connect retains the exact cleanup profile and offers a local-only **Remove saved session** retry; other servers remain untouched. Backend logout now returns an opaque failure if deletion fails rather than reporting success and clearing the cookie.

Debug `-ConnectionPreview` composes the **actual Connect form** with anonymous sample discovery, a visible preview label and explicit preview action notices. It skips all credential setup/network; this is a visual/UI-test fixture, compiled out of Release routing. It supports real-form light/dark captures without production data.

Validation for this milestone: 51 core Swift Testing functions and five native UI tests passed on the shared iOS 27.0 device, including RFC PKCE vector, exact/malformed callbacks, request bodies, empty logout, cancellation, delayed metadata restore, revoked/offline recovery, failed removal/retry and other-server preservation. System browser/passkey success and physical-device Keychain behavior still require the signed iPhone acceptance pass. Simulator identity and unsigned arm64 linking do not certify distribution provisioning.

Apple setup: distinct Debrid app/share identifiers and dedicated AppStore profiles were prepared by Heimdall. Final signing/upload remains blocked on authenticated Apple website steps: register/assign `group.dev.bhanji.debrid` to both IDs and create the new Debrid App Store Connect record. Prepared profiles lack the required group and must be regenerated/validated after assignment. Never substitute another app’s profiles/record or weaken the group.

## Live transfer snapshots

`TransferListModel` owns an immutable server-bound client and one cancellable request. Refresh replaces/cancels its predecessor; revision/cancellation guards discard old successes and failures. A cancelled late401 cannot clear credentials; the session coordinator also checks the exact current server before cleanup. A failed refresh preserves the last successful snapshot and timestamp, with an explicit stale notice. Initial errors never appear as a successful empty list.

`TransferSummary` derives presentation from both provider and local-download states. Provider `finished` means available remotely, not complete on the NAS; pending/downloading/unpacking/finishing local phases remain distinct. Unknown future states remain unknown. Progress is bounded, uses the correct provider/local field and stays indeterminate when totals/progress are unknown. Provider speed is not misrepresented as NAS speed. Display names are scalar/character-bounded without altering their raw DTO value.

Debug `-TransfersPreview` uses the actual list with in-memory sample DTOs and no auth, network or credential storage. `-TransfersEmpty` and `-TransfersOffline` select explicit states. It exposes no mutation actions; its server sheet states that no session exists. The connected app currently opens Transfers with a server/session sheet; the complete Providers/Settings tab behaviors arrive in their roadmap increments. No detail/file actions or intake controls are presented before they work.

This milestone passed 62 core tests and seven native UI flows, plus real-list light/dark source/visual review. The readout’s actual PNG pixels are present at13.36:1 contrast in both appearances; a viewer displayed the dark crop incorrectly, so source was not changed from that display artifact. No production transfer was submitted, modified or removed in validation. [Delivery/deployment evidence](../docs/plan/ios-delivery-log.md) tracks the released backend separately.

## Foreground events

The connected list now owns one foreground SSE subscription, bounded parsing/buffering, jittered reconnect and periodic reconciliation. Events are hints; snapshots are fetched on connect/resume and coalesced changes, with a45-second foreground safety resync. An event arriving during a GET waits for it rather than starving it with cancellation. Scene inactivity/disappearance stops network work; stale epochs and cancelled errors cannot update state or delete credentials. Read [protocol, timing and validation boundaries](../docs/plan/ios-live-updates.md).

## Transfer search and filters

Native searchable navigation and a Filters form combine names, hashes, category and provider-account labels. Results retain server order and display explicit empty matches. Filter/query state is server-bound and atomically saved to a bounded protected app file, with no defaults or credential sharing. Delayed restoration cannot overwrite new typing. See [search boundaries and validation](../docs/plan/ios-search.md).

## Live transfer detail

Each live row opens native detail with independent provider availability and NAS progress, selected download byte counts, reported server folder and transfer metadata. List and detail share one foreground stream; search does not remove an open detail. Failed refreshes preserve stale information, and successful snapshots that omit the transfer show an explicit removed state. See [detail semantics and checks](../docs/plan/ios-transfer-detail.md).
