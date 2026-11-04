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


## 2026-11-04 — retry21 merged and v0.2.5 deployed

PR59 merged as `c0930510b60a29689dea298a8d89f1523c3ce70b`; main CI189 and Release7 passed. Milestones01–21 are merged. All seven release assets/checksums and the OCI index/both platform manifests/configs/version/source labels were verified before an image-only compose update. NAS debrid now pins `ghcr.io/nathanbhanji/debrid-client:0.2.5@sha256:6ddb94289672b27c3ebd4908a8c7e1d71a9ed43a9df32ed4c41351c7d1729e9e`. Existing mounts, ports, environment and network were preserved; the actual running binary reports0.2.5/c093051. At14:41:36UTC HTTPS health returned200/ok with native_retry_v1 and previous capabilities; Pocket ID/native SSO remained enabled, bounded logs had one startup line and no errors/panics.

Fresh online SQLite backup pre-v0.2.5-20261104T143058Z was independently verified at14:30:58UTC from a pinned read transaction, then reopened immutable: integrity/FK clean, schema/rowsets equal, schema4,229376bytes,SHA25619608e36bed4468466c53086441bacb0582b7b770aeab8901ce7779f619930d1. It is a private same-NAS rollback copy, not off-NAS disaster recovery. At14:42:20.598UTC independent read-only comparison confirmed schema5, only the intended receipt parent index added, all seven application-table counts/rowset hashes unchanged, old migration records preserved plus one applied5 record, integrity/FK clean, backup hash unchanged. No example account/transfer mutations were performed. Private provenance is retained locally; raw compose copies were removed after verification.

## 2026-11-04 — cancellation22 safety foundation

Started the same milestone22 implementation with admission and stale-poller prerequisites. Service deletion now owns the operation slot and reloads final worker metadata; scheduled cleanup passes its predicate into the service and rechecks after drain. Provider listings capture incarnation before network and reconcile under nonblocking admission; terminal failures cannot be overwritten. Focused race tests pass for delayed error/missing/relink, changed incarnation, busy delete, late provider/path commits and post-drain cleanup changes. A further cache invalidation generation prevents delayed listings from republishing deleted provider IDs; a held-response/new-same-hash regression passed. Full Go race suite, pinned lint, tidy and generated-contract checks passed. Independent source review approved this foundation only. Cancellation states/protocol are still pending; this is not a completed milestone or an enabled native control. See [cancellation contract](ios-cancellation.md).


## 2026-11-04 — cancellation22 backend and transport complete

PR60 now implements explicit terminal cancelled parent/unfinished-child states and capability-gated review/request/receipt routes. Cancellation drains local jobs before atomically committing stopped state and durable acceptance; it retains records, provider copies and files as found after drain. Stable incarnation fencing tolerates normal worker metadata changes but rejects stale retry cycles; completion/error races keep their terminal outcome. Receipt replay is historical, survives retry/removal and cannot stop a later incarnation. Whole retry is explicit and preserves manual hold; single-file retry cannot release cancellation. No new schema migration. Older backends must not run while retained cancelled parents/children exist; the existing manual-selection downgrade gate also remains.

Full Go race suite, pinned lint and tidy pass. Focused tests cover real worker drain/partial-file preservation/recovery/remote-error stability, completed/error races, failed drain/receipt rollback, ABA, independent SQLite handles and HTTP auth/cookie/Origin/no-store/mismatch boundaries.191 native core tests and unsigned iPhone Release build pass; generated Go/OpenAPI/native fixtures/web types are current, with web formatting/lint/typecheck/build passing. Native/web readers distinguish stopped from completed/active and native transport preserves exact receipt binding. Independent final source review approved backend/transport and downgrade documentation; physical-device behavior remains separate. Native protected confirmation/recovery and cancel/remove controls follow in23.

Full testing exposed raw SQLite URI concatenation aliasing a `#` test directory to a sibling file. Open/read-only now share escaped file URLs; literal punctuation and actual file identity/read-only regressions pass. Windows drive handling is source-reviewed, not Windows runtime-certified. One shared simulator was reused; two owned failed result bundles and the verified fixture-only alias database were removed, preserving the passing result and existing native captures. No new simulator/worktree, production mutation or deployment occurred for PR60. Current production remains verified v0.2.5/c093051.


## 2026-11-04 — cancellation22 merged and v0.2.6 deployed

PR60 merged as `77ad7d48af9d98b68fe34c531479c1bca6b28e6c`; main CI192 and Release8 passed. Milestones01–22 are merged. All seven release asset checksums and the OCI index/both Linux platform manifests/config version/source labels were verified before applying the pinned image `ghcr.io/nathanbhanji/debrid-client:0.2.6@sha256:ae9b316160ec887aa29936f4ff0ea1c86618c97fd8d39e48810a4afc058cf115`. Parsed compose differed only in services.debrid.image, with baseline and candidate readbacks; mounts, ports, environment and network were preserved.

At15:30:39UTC the running binary reported0.2.6/77ad7d4, tailnet HTTPS health returned200/ok with native_cancellation_v1 and prior capabilities, Pocket ID/native SSO remained configured, and a150-line bounded log request returned one startup line and zero errors/panics. No schema migration was added; prior verified schema5 baseline remains on record. No example production account/transfer mutation was used. Private deployment provenance retained; temporary raw compose/log copies removed after verification. Retained cancelled states and manual-selection holds still impose the documented downgrade restrictions.


## 2026-11-04 — native management23 conditional-removal foundation

Started23 with the server/transport boundary needed for a safe native removal sheet. Exact-ID removal review returns typed absence; a missing route is never absence proof. A dedicated native DELETE/removal route requires one strong quoted revision before and after worker drain under admission, while legacy DELETE retains omission compatibility. Empty/weak/wildcard/list/duplicate/missing native headers are rejected before cancellation/provider/file/record actions. Stale incarnation returns412; cancellation/retry invalidates older reviews. Native transport freezes explicit cleanup booleans and targets the mandatory route. No schema migration or new backend removal receipt; partial cleanup remains honestly unconfirmed until a direct204 record-removal response.

Full Go race, pinned lint, tidy and generated contracts pass; web formatting/lint/typecheck/build pass.192 core Swift tests passed before the final dedicated-route refinement; all22 focused transport tests passed afterward. Focused HTTP/service race tests cover pre/post-drain changes, partial provider cleanup followed by database failure and identical explicit retry, default preservation, native cookie/Origin/auth, typed absence and malformed/empty/duplicate/missing headers with actual provider/file/record preservation and zero cancellation calls. Independent source review approved the final foundation. One shared simulator was reused. Protected native intents/models/sheets/recovery and real SwiftUI screenshots remain pending in this SAME23 PR, which stays draft. Milestones01–22 remain merged;23 is not complete.


## 2026-11-04 — CI61 admission liveness follow-through

CI on b860d41 stalled TestFiltersApplyAndAllExcludedFails at provider-finished with no error. The exact failure did not recur in15 ordinary and20 two-core race-enabled focused runs. Source review identified a liveness gap: poller and scheduler both skipped busy per-transfer admission without guaranteeing the scheduler an opportunity. A successful poll now reconciles and runs the existing scheduling pass while still owning admission. Repeated poller wins therefore advance ready work; cancellation/drain owners remain nonblocking exclusions. No timeout was loosened.

A stopped-loop regression advances link creation, fetch and finalization solely through three explicit poll/job barriers; it exercises the progress guarantee without timing contention. Existing stopped-loop tests were audited for job cleanup. Three focused race repetitions and the full engine race suite passed. Timeout diagnostics now include bounded child-state/job/admission counts. This fixes the demonstrated admission gap; it is not a claim that the single CI stall's root cause was independently reproduced. Native management implementation continues in this same PR while hosted checks run.


## 2026-11-04 — native management23 complete

Implemented native cancel/remove review and uncertainty recovery, protected immutable management intents, original-server Pending Items restoration and connection fencing. Inbox6 preserves earlier formats and requires its new management array; unknown actions retain their original key/revision/cleanup choices. Removal defaults to preserving files/provider copies, uses the mandatory conditional route, and distinguishes direct204 record removal from observed absence with cleanup unconfirmed. Cancellation receipts remain historical. Source fixes included renewal deinit cancellation, immediate name sanitization, precise storage errors, action contrast and status-first result feedback.

204 core tests passed. All five native UI flows passed at17:04:16; final largest dialog copy/dismissal refinement passed at17:11:58, verifying Keep reviewing sends nothing before a later explicit confirmation. Eight actual SwiftUI captures retained in docs/design/ios/management received independent source/visual review, including the final complete selected-effect dialog and safe alternative. Cross-process probes passed new management claim/key preservation and unrelated-writer recovery, alongside previous duplicate/lock/killed-writer checks. Unsigned Release app/framework/extension build passed. Full Go race suite passed after admission follow-through; no timeout was loosened. Generated contracts/pinned lint/web validation were already checked for the backend foundation. Final CI must validate the complete commit before merge.

One shared simulator and the existing checkout/build outputs were reused; temporary failure exports/videos and superseded captures were removed. Production remains v0.2.6 with no example transfer/account actions. Signed-device/linking/App Group/real protection and full VoiceOver acceptance remain final gates. Milestones01–22 stay merged until PR61 merges. Nathan requested faster delivery: continue independent implementation while CI runs and group later backend releases where practical, retaining review before merge.

## 2026-11-04 — native management23 merged

PR61 merged as `6638c273be9c95e37bb1ad130fcf55b51812d089` after all three checks passed on the reviewed final head e6e402e. Main CI196 passed. Milestones01–23 are merged. Production remains v0.2.6; conditional native removal awaits the next grouped backend release. Provider overview24 is being built as a complete native feature rather than a separate foundation checkpoint.

## 2026-11-04 — provider overview24 complete

Implemented the connected Transfers/Providers/Settings tabs and native account list/detail/search, with default/disabled presentation and explicit credential checks through the existing API. Browsing never runs provider tests; checks show dated membership/expiry and honest missing quota/expiry. Read-before/read-after account binding avoids publishing results across an observed edit. Failed checks retain prior dated success; offline list refresh retains the last snapshot. Existing session coordinator supplies confirmed sign-out. App/extension Info.plist versions now take the configured project versions instead of XcodeGen’s default.

Independent review found and fixed check/refresh ownership and queued stopped-action races; actual in-flight ownership survives generation changes. Simulator review fixed action colors, fixture labels, accessibility-size badge wrapping/decorative icons and search results hidden behind the overview/keyboard. Final offline polish hides empty Accounts chrome. Initial full core213 passed; final focused provider/transport32 passed. All four native UI flows passed across focused reruns, including largest accessibility text. Unsigned iPhone Release build passed. Nine actual SwiftUI images and capture provenance are retained in docs/design/ios/providers; independent visual review approved the screens. Physical-device/signing/full VoiceOver remain final gates. One existing shared simulator and checkout were reused; no production account/transfer mutations, backend changes, migration or deploy occurred. Milestones01–23 remain merged until this24 PR passes final hosted checks and merges.

## 2026-11-04 — provider overview24 merged

PR62 merged as 1f7be71a73856cfe5ccf2d40fc0dbf77a1ab6897 after all three checks passed on a135ad9. Main CI198 passed. Milestones01–24 are merged. Production remains v0.2.6; the next backend release groups conditional transfer removal with the account-removal safety fixes from25.

## 2026-11-04 — provider management25 complete

Added native account add/edit/default/disable/removal, empty masked opt-in credential replacement and observation-only recovery after unknown saves/removals. Secret entry is never persisted and is cleared before submission/background/dismissal. One synchronous task owner prevents a double tap from replacing the request's cancellation handle. The sheet performs best-effort preflight timestamp review; it does not claim transactional account-update revisions or durable operation receipts.

Backend audit found forced account removal ignored worker-stop errors. It now uses existing guarded per-transfer deletion and aborts on failed drain, preserving files/provider copies by request; multi-transfer failure can be partial. Default promotion now reads the current account inside its final transaction, preserving other clients' changes during drain. A new exact-ID removal route/capability keeps native force requests away from unsafe older backends. No schema change. Production remains v0.2.6 until a later verified release.

Full core222 passed; final focused8 editor tests passed after the added held-preflight regression. Five native UI flows passed, including final largest-text safe dismissal/reopen/removal. Nine captures were independently reviewed. Actual visual review rejected the narrow largest-text popover despite a passing tap test; accessibility sizes now use a scrollable native confirmation sheet with persistent Cancel and full-width safe/destructive actions. Full Go race passed before the final default re-read fix; service/API race suites and pinned lint passed after it. Generated contracts/tidy/web formatting/lint/types/build and final unsigned iPhone Release build passed. One shared simulator/checkout/build directory was reused; no production account or transfer action was used. Milestones01–24 remain merged pending this25 PR's hosted checks and merge; signed-device/full VoiceOver gates remain30.

## 2026-11-04 — v0.2.7 deployed

Release9 passed on b1f007730ca9b3d7b30b1c4f8b5e70a9444d7e63. Verified all seven downloaded release assets against their published SHA256 digests/checksums, plus the OCI index and both Linux platform manifests/config version/source labels. Image-only parsed compose replacement pinned ghcr.io/nathanbhanji/debrid-client:0.2.7@sha256:b5f9e2e1a97962570a23b97417e7e02b8586abf79661283c28e60a7ce2106a44; baseline/candidate readbacks matched and all mounts/ports/environment/network were preserved.

At18:55:41UTC the running binary reported0.2.7/b1f0077 and HTTPS health returned200/ok with native_removal_precondition_v1 and native_account_removal_drain_v1 plus prior capabilities. Pocket ID/native SSO remained configured; a150-line bounded log request yielded one line and zero error/panic/fatal matches. No new migration; previous schema5 verification remains on record, not a new independent rowset comparison. No example provider/account/transfer mutation was submitted. Private provenance retained and temporary raw compose/log files removed after verification. Milestones01–25 are merged; settings revision26 follows.

## 2026-11-04 — settings revisions26 complete

Implemented dedicated no-store settings review/conditional replacement with matching ETag/body revision and exactly-one strong If-Match. Missing/empty/weak/wildcard/list/duplicate headers reject before mutation; stale state returns412 inside the same transaction as value/nonce replacement. Existing web/CLI/MCP full replacements remain available and rotate the new reserved KV nonce atomically. Default reads allocate no rows, normalization remains compatible, ABA/same-clock saves invalidate older reviews, rollback retains both settings/metadata, and settings events occur only after commit. No schema migration. Older-binary/direct database writers are outside the ABA guarantee.

Go-generated wire fixtures and typed Swift transport preserve snake-case settings, current-server credentials and required revision without legacy fallback or replay. Lost responses require observation and fresh review; no durable acceptance receipt is claimed. Independent source review approved the contract. Full Go race suite passed; focused service concurrency/rollback/ABA repetitions and HTTP/native-cookie/Origin/header tests passed. Pinned lint, generated check, tidy, web format/lint/types/build,225 native core tests and unsigned iPhone Release build passed. No new UI or screenshot set for this contract milestone; the native forms follow27. One existing shared simulator/checkout was reused and no production settings/account/transfer writes were used for validation. Production remains verified v0.2.7 until this next backend capability is published/deployed. Milestones01–25 remain merged pending26 hosted CI/merge.

## 2026-11-04 — CI202 shutdown-resume test repair

Main CI202 failed in the existing TestShutdownLeavesDownloadResumable at engine_test.go:702: its3s startup polling loop silently fell through with no download rows, then indexed Downloads[0] after cancellation. Settings service/API checks had passed. Twenty two-core race-enabled focused baseline runs did not reproduce the startup stall; its runtime cause is not claimed resolved.

The test now prepares a stable pending-download fixture with stopped background loops and an explicit poll/job barrier, verifies exactly one pending row, then runs the actual Engine.Run with a cancellation-only fetcher. Shutdown begins only after that fetcher signals entry. The same3s entry/shutdown deadlines and2s shutdown limit remain. Assertions verify actual fetch cancellation and retention of the same pending download ID, with checked errors/counts instead of unsafe indexing. Setup, shutdown and deferred cleanup waits are bounded; shared harness cleanup is also bounded to prevent a failed assertion hanging afterward.

Final30 two-core race repetitions passed. Full Go race passed before the cleanup refinement; full engine race and pinned lint passed afterward. Independent source review approved the test guarantee and bounded cleanup. No production code, timeout relaxation, simulator use or NAS action. This is an unplanned CI repair and does not advance native milestone27; native progress remains01–26 merged, with settings UI next.

## 2026-11-04 — PR65 separate filter-test stall investigation

Hosted CI on60e10f9 passed the repaired shutdown test but failed TestFiltersApplyAndAllExcludedFails after5s: parent finished/downloading, one child unrestricting, one registered job, zero operation slots. This differs from the prior never-started shutdown fixture. Forty focused two-core race-enabled filter runs passed locally; the CI stall and its runtime cause remain unconfirmed. Source audit did not establish a matching deadlock or runtime defect, so no production fix or timeout increase was made.

Added failure-only diagnostics to the existing timeout report: first16 job keys/kinds/parent IDs, database pool stats and a goroutine stack capped64KiB, captured after releasing the engine mutex. A repeat hosted failure can now distinguish which worker remains and where it waits. Final full engine race suite and pinned lint passed. Original filter assertions/deadline are unchanged. This evidence improvement stays in PR65; native progress remains26/30 and productionv0.2.7 is unchanged.

## 2026-11-04 — native settings27 complete

Added complete server policy/category/organization/archive forms, explicit replacement review, section-level conflict resolution and observation-only uncertain-result recovery. Exact UTF-8 category identity and untouched manual file policy survive edits. Add-time policy options retain the saved-item claim and Keep Copies/file-review decisions without submission. Organization preview now distinguishes omitted versus explicitly cleared templates; no migration or settings write occurs during preview. See [contract, Emil review and validation](ios-runtime-settings.md).

Independent source/screenshot review approved15 actual native captures. Final236 core tests and eight distinct UI flows across focused runs passed; full Go race, preview regression, pinned lint, generated/tidy/web checks and final unsigned iPhone Release build passed. One shared simulator/checkout/build directory was reused; no production mutation. Milestones01–26 are merged;27 awaits its PR checks/merge. MainCI205 on c0aabf6 passed after the separate CI repair. Production remains verifiedv0.2.7; backend26+27 are intended for the next grouped release. Signed TestFlight remains blocked by the authenticated Apple website App Group/app-record step already reported to Nathan.

## 2026-11-04 — v0.2.8 deployed

After mainCI207 passed on its bounded retry, Release10 succeeded at1f89bc73db32052ce09e6b8e9de246cb3983630c. Both failed engine cases had passed20 focused two-core race repetitions each; source/stack review found no demonstrated deadlock, and the underlying hosted timeout cause remains unconfirmed. No runtime or deadline change was used to get a passing retry.

All seven downloaded release assets matched published SHA256/checksums; OCI index/platform manifests and config version/source labels were verified. Image-only parsed compose replacement pinned ghcr.io/nathanbhanji/debrid-client:0.2.8@sha256:8d33318357459cf4d62d2df2cc96be41335954f5d811d6d2bebef7c5c88f485b. Baseline and candidate readbacks matched; mounts, ports, environment and network were preserved.

At20:39:10UTC the running binary reported0.2.8/1f89bc7 and HTTPS health returned200/ok including native_settings_revision_v1 and prior capabilities. Pocket ID/native SSO remained configured; a150-line bounded log request returned one line with zero error/panic/fatal matches. No new migration or new independent database rowset comparison is claimed; schema5 verification remains on record. Temporary raw compose/log copies were removed after verification; private deployment provenance retained. No example provider/transfer/settings writes were made. Milestones01–27 are merged; recovery/diagnostics28 follows.

## 2026-11-04 — recovery/diagnostics28 complete

Added safe typed connection guidance, explicit anonymous public-server checks, reviewed native sharing of an allowlisted support report, and dated retained transfer snapshots. List429 responses impose a bounded monotonic delay across manual taps, event reconciliation and foreground restarts; actual observed late429s retain the delay even when their UI result is obsolete. No host/identity/transfer/path/raw-error input enters reports. Existing session/inbox/unknown-action rules remain intact. See [behavior, privacy and Emil review](ios-recovery-diagnostics.md).

Independent review caught and verified a repair for cooldown warnings being hidden on restart. Final243 core tests and four native UI flows passed together; nine final actual SwiftUI captures and source were independently approved. Final unsigned arm64 Release build passed. The largest-text test scrolls to lazy List controls rather than requiring offscreen elements to already exist. One shared simulator/checkout/build directory reused; no production mutations. Milestones01–27 are merged;28 awaits hosted checks/merge. Production is verifiedv0.2.8.
