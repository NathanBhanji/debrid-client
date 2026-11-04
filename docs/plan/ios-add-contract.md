# Native add and reconciliation contract

Milestone 16 makes JSON magnets and multipart torrent documents share settings and durable submission semantics. Existing web/CLI/MCP requests without a submission key retain their body shapes, defaults, response body and 201 status. Generated internal Go client callers now pass nil for the new optional header parameters. No provider call happens during local add.

## Feature discovery and options

Public health adds capabilities `torrent_submissions_v1` and `multipart_torrent_settings_v1`. Native submission must check support before posting; a missing capability is not permission to fall back to an unkeyed request. Protected intake remains available on older/offline servers.

JSON add remains POST /api/v1/torrents with magnet/account/category/settings. Multipart POST /api/v1/torrents/file accepts file/account/category and now an optional **text** settings field containing the same JSON TorrentSettings object, bounded to16 KiB. Its schema validation uses the same Huma schema as JSON add. Repeated settings fields, a settings file part, whole null, malformed/trailing JSON, unknown fields, invalid types/enums/durations/regexes and negative bounds are rejected without an enqueue. Optional-property null handling follows the existing JSON/Huma behavior; native clients should send explicit typed values, not null arrays.

Settings are a full replacement, never a partial merge. Omitted settings use the configured defaults at the first successful enqueue; `{}` intentionally supplies the model's zero values/keep action. Native review should freeze a full reviewed settings snapshot if it wants the exact displayed rules. The current backend automatically selects using its filters; manual_files means already-known provider IDs. A pause to choose files when metadata becomes available requires the focused selection-policy addition in milestone18; this contract must not present it as already supported.

## Immutable submission intent

Both add routes accept optional Idempotency-Key: a fresh UUIDv4, bare or quoted. Case/quotes normalize to one key. The server stores only its SHA-256 and a versioned SHA-256 fingerprint of payload kind, exact effective original payload bytes, submitted account selector, category and optional full settings. Magnet surrounding whitespace is removed as before; URI parameter encoding/order and torrent bytes are preserved. The fingerprint excludes current defaults/resolved account configuration. Multipart boundaries and source filenames do not change the intent. A golden test protects fingerprint compatibility when the settings model evolves.

Inside one immediate SQLite transaction, the server first looks up an accepted key; otherwise it resolves account/defaults, validates options, checks active same-account/hash duplicates and inserts the torrent **and receipt**. Parse/payload hashing happens outside the lock. Every query inside WithTx uses its passed q handle, avoiding the single-connection-pool deadlock. Simultaneous legacy adds are guarded too. New requests do not rewrite/remove preexisting duplicate rows.

An accepted identical replay returns201 and the current torrent representation with X-Debrid-Submission-Replayed:true. It does not requeue, publish a second add event or wake the engine again. The original response has false. This is durable local enqueue deduplication; provider execution/reconciliation remains the engine's responsibility.

| Outcome | HTTP | X-Debrid-Error-Code | Reconciliation |
| --- | --- | --- | --- |
| Different input reuses an accepted key | 422 | idempotency_mismatch | Preserve the original attempt; never adopt it as the changed request |
| Another active torrent has the hash/account | 409 | active_torrent | X-Debrid-Existing-Torrent-ID identifies it; offer to open it |
| The accepted key's torrent was removed | 409 | submission_removed | Receipt still proves acceptance; never recreate with that key |

Errors keep the existing application/problem+json shape; stable headers remove the need to parse human messages. The existing-ID header is emitted only for an active duplicate. No key, original payload, tracker URL or credentials are echoed in these errors.

## Uncertain responses and deletion

Authenticated GET /api/v1/torrent-submissions/{key} returns torrent_id, accepted_at and torrent_exists. The ID remains after deletion; false means the accepted torrent has since been removed. Receipt200 and404 responses use Cache-Control:no-store.

A404 means no committed receipt was visible at that read. An original request can still be in flight. After timeout/disconnection/cancellation after send, retain the exact server/key/body and query the receipt. If a retry is needed, send the **same immutable intent with the same key**. Never mint a new key automatically or infer acceptance from an info hash alone. New user intent needs a new explicit review. A transaction/response error can be ambiguous; a durable receipt is authoritative for accepted local work.

Receipts are lightweight, persistent and not automatically expired or evicted. They have no foreign key to torrents/accounts, so deletion cannot erase evidence and resurrect a late retry. Retention grows with accepted keyed adds, without duplicating magnet/torrent payloads. Restoring an earlier database can also restore earlier receipt history; no claim survives losing/rolling back the authoritative database.

Cookie-authenticated native writes already support absent Origin. They use the same session validation as browser requests; foreign or null Origin remains rejected. Receipt lookup requires the existing service authentication and shares its existing server-wide management authorization, not a new per-user ownership model. Keys are operation identifiers, not authentication credentials.

## Duplicate activation and migration

Add, whole-torrent retry and failed-file retry check same-account/hash activation within immediate transactions. Failed-file retry now commits the parent reactivation and child reset together; any conflict/error rolls both back. A parent change emits its own update event. These guards cover user activation; they neither repair legacy duplicates nor claim to redesign the engine's existing provider-worker retry races.

Migration00004 only creates the receipt table. It adds no uniqueness constraint to existing torrents and changes no accounts, credentials, payloads, downloads or filesystem paths. Old binaries ignore the extra table; do not run a down migration/delete receipts as a retry workaround. Signed iPhone delivery is independent of this backend schema change.

## Validation

Race tests use16 concurrent callers across two real SQLite handles for keyed/unkeyed adds, plus independent-handle Add-versus-whole/file-retry races. Repeated runs verify one accepted activation and rollback of rejected child changes. Tests cover replay across service recreation/default/account changes, key mismatch, deleted tombstones, invalid requests, failed receipt-insert rollback, exact URI preservation, full/omitted multipart settings parity, native cookie writes and foreign/unauthenticated rejection. A simulated proxy504 after a successful commit reconciles and replays without a second insert. A real version3→4 database migration preserves existing duplicate rows, credentials, original payloads and download progress.

The full Go race suite, pinned linter, regenerated OpenAPI/sqlc/internal client/native wire fixtures, and hosted Swift wire-decoding tests are required before merge. OpenAPI changes also require `cd web && npm run gen:api` and the web format/lint/typecheck/build pipeline: root `make generate-check` does not generate or verify the TypeScript client. No production/provider mutation is used in these tests. Backend release/image publication and NAS application follow validated merge through the existing release workflow; no source-based NAS deployment is assumed.

Protocol reference: [IETF Idempotency-Key draft08](https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-idempotency-key-header-08), consulted for fingerprint/key-reuse guidance. This service explicitly documents its optional bare-or-quoted UUID form and current-resource replay behavior rather than claiming complete draft conformance.
