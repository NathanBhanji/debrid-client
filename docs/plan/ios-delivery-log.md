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
