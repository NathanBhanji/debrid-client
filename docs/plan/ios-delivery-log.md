# Native delivery evidence

## Foundations and Pocket ID · 2026-11-04

Roadmap 01–06 were independently reviewed and merged as [36](https://github.com/NathanBhanji/debrid-client/pull/36), [37](https://github.com/NathanBhanji/debrid-client/pull/37), [38](https://github.com/NathanBhanji/debrid-client/pull/38), [39](https://github.com/NathanBhanji/debrid-client/pull/39), [40](https://github.com/NathanBhanji/debrid-client/pull/40), [41](https://github.com/NathanBhanji/debrid-client/pull/41). These deliver native design/gallery, isolated transport, Keychain/profile storage, HTTPS discovery, backend native exchange and system-browser/session orchestration. They do not represent a complete daily-use app yet.

Latest auth validation: 51 core Swift Testing functions, five XCTest UI flows and unsigned arm64 app/framework compilation/linking passed. Independent source/security review resolved configuration races, stale input/cancellation and failed secure-removal recovery. Actual native Connect form reviewed in light/dark with fixture-only discovery. Settled light body/primary contrast measured5.39:1/7.56:1; dark8.98:1/7.35:1. A two-second launch frame had appearance fade; ten/twelve-second settled frames were used for final judgement. Physical iPhone/passkey success is still unverified.

## Backend deployment · 2026-11-04

[Release v0.2.2](https://github.com/NathanBhanji/debrid-client/releases/tag/v0.2.2), commit `c3f386a922215856cdc8549e18c6534115ea2e85`, published by the existing tagged release workflow. NAS service `debrid` now pins:

```
ghcr.io/nathanbhanji/debrid-client:0.2.2@sha256:4f197ee219050935773a21bebcccd214daeea7093ceee47338846b341295f7f8
```

Applied only with `nas-agent debrid-client compose-put` then `up debrid`. Parsed compose documents were compared before applying: the image reference was the only field changed. Existing data/download mounts, ports, environment and access boundary were identical. No source checkout, new privileges/mounts, account changes or example transfers. There are no database migrations between v0.2.1 and this release.

Status confirmed the pinned image running; existing tailnet HTTPS health returned HTTP200, ok=true, version0.2.2; public authentication status reported configured OIDC and native_sso=true. Bounded post-recreate logs showed no error/panic lines. Raw compose/log contents were withheld. NAS source-deploy tracking still says unknown because this is an image-based apply; report release commit plus running digest/health instead. The later logout failure-reporting hardening in PR41 is on main and will be included in the next backend release bundle.

## Apple release access

Heimdall verified/created distinct app and share identifiers, enabled APP_GROUPS and staged dedicated distribution profiles, with matching team certificates. Required `group.dev.bhanji.debrid` is not assigned; profiles are not installed or release-ready. Authenticated Apple website access must register/assign that group to both identifiers and create the new Debrid ASC app record. Regenerate and inspect only Debrid profiles afterward. Physical development testing also needs device registration. Existing credentials/profiles for other apps are not substitutes. This blocks final distribution/device acceptance, not ongoing simulator/app development.


## Durable submission backend · 2026-11-04

Roadmap15 ([50](https://github.com/NathanBhanji/debrid-client/pull/50)) and16 ([51](https://github.com/NathanBhanji/debrid-client/pull/51)) are merged. The former adds protected paste/torrent review; the latter supplies multipart settings parity, atomic user activation and durable local submission receipts. Full Go race tests, repeated independent-SQLite-handle races, migration preservation, pinned lint, generated Go/web/native contracts, web checks/build and hosted Swift wire decoding passed. Independent source/test review approved.

[Release v0.2.3](https://github.com/NathanBhanji/debrid-client/releases/tag/v0.2.3), commit `a1f0a57cd6f74ef6fb7eaaa5cbf36733febf0e46`, was published by successful release workflow41789972778. NAS service debrid now pins:

```
ghcr.io/nathanbhanji/debrid-client:0.2.3@sha256:36962617090851b482306caed3ff7a421df3015a01c99e2ee9f92a90fca5fc7b
```

The public registry index and both Linux platform manifests/configs were checked by content digest and version. The image has no revision label; release-workflow SHA and the running binary's `debrid 0.2.3 (a1f0a57)` establish code provenance. Parsed compose documents differed only in the pinned image. The baseline was re-read before compose-put; the resulting candidate was re-read before up debrid. Existing mounts, ports, environment and network boundaries were unchanged.

At07:19:22UTC, status reported running, tailnet HTTPS health returned200/ok/version0.2.3 with both submission capabilities, and Pocket ID stayed configured/native_sso=true. A150-line bounded new-container log check found0errors/0panics. Server startup occurs after Store.Open/migrations succeed; Heimdall independently confirmed schema4 at07:20:44.995UTC using a pinned read-only transaction: the four-column receipt table/constraints are present, integrity_check is ok and foreign_key_check is empty. Every pre-existing table/index/trigger/view definition and application-table rowset matched the immutable pre-migration backup exactly; only the receipt table and expected migration record were added. The backup hash remained unchanged. No production example transfer, account edit or native submission was used for validation. NAS source-deploy tracking remains unknown for this image-based apply; report the digest/commit/health instead.

Before application, Heimdall verified consistent online SQLite backup `pre-v0.2.3-20261104T070941Z`, completed07:09:41.973UTC,221184bytes, SHA256 `d21e1f343be783e8ee52a42a260faa0f93fa8c44309fba7afe3a4c93c36dc925`. A pinned read transaction/SQLite online backup included committed WAL pages. Independent immutable reopen passed integrity and foreign-key checks, matched pinned-source application-table row counts, showed migration3 and no receipt table. The backup is a0600file in a0700existing-admin directory on the same NAS pool; its precise path/provenance is in the private deployment record. It is a migration rollback copy, not an assumed ZFS snapshot or off-NAS disaster backup. Do not overwrite a live database with it. Migration00004 is additive; ordinary image rollback can leave the receipt table intact.

## Native review/add · 2026-11-04

Roadmap17 now implements explicit provider/category/rules review and recoverable magnet/torrent submission against v0.2.3. Protected inbox format3 persists the server, full options and UUID before POST. Reopening checks receipts without automatic submission; retry reuses original bytes/options/key. Definite fresh rejection, unknown result, accepted/removed result, wrong server and mismatch have distinct behavior. See [state contract](ios-review-add.md).

156 hosted core tests and three new native UI flows passed. Final light/dark Add-to-detail flows passed with the cleanup footer assertion; largest Dynamic Type and lost-response receipt recovery passed. Two-process storage recovery probes and unsigned Release arm64 app/framework compilation/linking passed. Independent source/visual review approved after fixes for readiness bypass, one-use rejection proof, connection generation, stale displayed options, action tint, lifecycle warning contrast (6.93:1 on white) and truthful retention copy. Tests used isolated fixture inboxes and no production submission/account mutation. The one shared simulator is shut down; generated build storage stays bounded, with one relevant result and eight temporary screenshots. Signed physical-device checks remain separate.

## Manual selection backend · 2026-11-04

Roadmap18 supplies the backend hold, authoritative provider-file review, revision-checked immutable confirmation and offline replay for native19. It preserves automatic clients and the v0.2.3 automatic intent fingerprint. Receipt/version/incarnation data remains internal in existing settings JSON; no schema migration. Worker checks cover delayed metadata, changed mappings and retries without releasing an unconfirmed hold. See [contract and downgrade gate](ios-file-selection-contract.md).

Full Go race tests passed; final domain/service/engine/API race checks passed after source follow-through. Pinned lint reports0issues. Generated Go/OpenAPI/native fixtures are current; web types were regenerated after schema generation and verified unchanged on regeneration. Web formatting/lint/typecheck/build and three hosted Swift wire-contract checks passed. Independent read-only review resolved stale failure/worker and second-response link-mapping issues, confirmed content identity binding, and approved the precise downgrade gate. No production transfer/account mutation or deployment occurred. One existing simulator, shut down afterward; one relevant result/eight temporary screenshots retained. Native picker and visual acceptance are milestone19; the plan totals30PRs after this backend/native split. [PR52](https://github.com/NathanBhanji/debrid-client/pull/52) merged as927bb0d1921db60e51a951645b450012f8120c6b after all three checks passed on its independently reviewed head.


## 2026-11-04 — v0.2.4 image release and verification

[Release v0.2.4](https://github.com/NathanBhanji/debrid-client/releases/tag/v0.2.4) points at merged PR53 commit `adabed7bb0611e784917994a3954ca871c1f4bd8`. Release workflow41817471266 succeeded on attempt2 after an incomplete unpublished draft was verified and removed, preserving the tag. The first attempt failed uploading one release archive; its image was never applied. All seven final release assets and their published checksums were verified. The successful attempt's multi-platform OCI index, both platform manifests and image version/source labels were verified before application.

NAS service `debrid` now pins:

```
ghcr.io/nathanbhanji/debrid-client:0.2.4@sha256:62b1d3232a8278e3966a1c2f3b1f42e9fa9d22153883e6b3f49db7e996ad1df0
```

The compose comparison changed only `services.debrid.image`; existing mounts, environment, network and port remained intact. No schema migration was introduced. At11:14:28UTC, status confirmed the pinned running image, the executable reported0.2.4/adabed7, tailnet HTTPS health returned200/ok/version0.2.4 with all three native capabilities, and Pocket ID remained configured with native SSO enabled. A bounded150-line log check contained one startup line and zero errors/panics. No production account edit or transfer submission was used. Private provenance and health records are retained locally; temporary raw compose copies were removed after verification.

The manual-selection downgrade gate applies from this release: an older server must not run while any retained transfer or default has `wait_for_selection=true`. See the [file-selection contract](ios-file-selection-contract.md).

## 2026-11-04 — native file review19

PR57 implements the Files-style chooser Nathan selected: separate folder navigation and explicit file selection, search across folders, scoped bulk selection, total selected count and a final confirmation sheet. Add-review manual policy holds NAS downloads until confirmed. Pending Items restores uncertain selections on their bound server. Inbox4/attempt2 preserve the policy and exact opaque file IDs across launches; old readers refuse the new envelope.

Validation:162 core tests passed before adding the final migration case; all six focused selection tests then passed, including migration/corrupt-envelope preservation. Three native UI tests passed: ordinary confirmation, largest Dynamic Type and response-loss recovery. A further dark-mode confirmation run passed with settled screenshots. Cross-process inbox probe and unsigned iPhone Release build passed. These are simulator/source checks, not real-device signing or a complete VoiceOver audit.

CI exposed stale scheduled provider-selection work replaying after a selection timestamp had been recorded. The worker now rereads and skips an already recorded selection before querying the provider. A deterministic regression and10repeated race-enabled Real-Debrid/replay runs passed; full engine race suite and pinned lint passed. No production action occurred for this fix. Existing source review findings were resolved; independent source/light review approved. Screenshots are bounded local fixture artifacts; one shared simulator was reused and shut down.

## 2026-11-04 — native Share Extension20

Implemented a dedicated XcodeGen share-services extension with SwiftUI review, local explicit Save/Cancel, exact URL/text magnet intake and bounded torrent-file copying while the provider callback owns its temporary URL. It shares only the protected App Group, has no credential entitlement, performs no submission and never forces the app to open. The containing app owns the shared framework; the extension links through the expected parent runpath.

Full172core tests and two actual simulator share-sheet tests (ordinary/largest Dynamic Type) passed before the final attachment-free-text addition. All six provider tests then passed after that addition; the model's four tests cover explicit save, exact URI variants, partial retry and cancellation. Final unsigned iPhone Release build passed, with extension embedding/principal class/entitlement/framework-link inspection. Independent source and actual dark-extension visual review approved, including final fallback-text delta. Reviewed issues fixed exact-URI deduplication, incremental payload quotas, global deadline, URL/caption precedence and late callback guards.

Actual extension opening and Cancel are verified; tests did not press Save into the real shared inbox. Isolated persistence tests cover local Save/partial retry. Signed-device App Group Save/reopen, the phone host-app matrix and real protection behavior remain milestone30 integration gates. Apple App Group assignment and new ASC app record still require authenticated website access; no profile was weakened or borrowed. One existing simulator reused/shut down; retained results/screenshots bounded. No production account/transfer mutation, deployment or Apple provisioning action occurred. See [Share Extension contract](ios-share-extension.md).

## 2026-11-04 — retry21 backend draft started

PR58 merged as28278876c60d51f7a1191df2dd1928729d7119f0; post-merge CI185 passed. Milestones01–20 are merged. Initial21 work adds per-torrent retry/scheduler admission, stale scheduled-cleanup protection and abort-on-drain-failure deletion. Service/engine race suites and pinned lint pass; independent source review approved this initial guard scope after corrections. Native receipt/API/UI work is still pending, so21 is not merge-ready. See [retry audit](ios-retry-actions.md). No production action or simulator use.

## 2026-11-04 — retry21 protocol foundation

Added review/request/receipt routes for exact torrent/download IDs, namespaced durable retry keys and atomic receipt/reset commits. Replays precede eligibility and cancellation and survive parent/file-row removal. Parent-scoped receipt fencing covers native and legacy file retries, including identical timestamp/counter cycles. Migration5 adds a parent receipt index; upgrade/tombstone preservation, indexed plan and integrity checks pass. Fresh backup is required before later production deployment; no production change occurred.

Full Go race suite and pinned lint passed. Independent-handle concurrency and HTTP cookie/auth/Origin/mismatch/no-store coverage passed. Go/OpenAPI/native fixtures and web types regenerated; web formatting/lint/typecheck/build passed. New native RetryAPI DTO/client and query support passed23 Go-wire/transport tests. Independent read-only protocol review approved this foundation. The same PR59 remains draft: protected native retry intent, captured-parent validation, model/lifecycle/confirmation/recovery UI and source/visual validation remain pending. Milestones01–20 are merged. No native retry button is enabled yet.


## 2026-11-04 — retry21 native completion

PR59 now includes protected immutable retry intents, whole-transfer and failed-file native confirmation, server/parent binding, explicit same-request recovery and Pending Items restoration. Inbox5 preserves older submissions and selections; unresolved requests survive lease changes without fresh-request rejection authority. Connection changes fence both retry and file-selection mutations. Accepted means a committed retry, not completed work.

188 core tests, three file-selection UI regressions and four final retry UI flows passed. Actual light/dark, unknown, accepted and largest Dynamic Type SwiftUI captures received independent source/visual approval after recovery placement and warning contrast fixes. Extended real two-process storage probes and unsigned iPhone Release build passed. One shared simulator was reused; no new devices or worktrees were created. No production mutations or deployment occurred. Milestones01–20 remain merged until PR59 merges; migration5 requires a fresh consistent verified backup before deployment. See [retry contract and review](ios-retry-actions.md).
