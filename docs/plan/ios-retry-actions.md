# Retry actions — milestone21 audit

The native UI will distinguish retrying one failed NAS download from restarting a failed transfer. Whole-transfer retry clears provider identity and download rows, retains disk files and resets manual-selection acceptance while keeping the manual hold. Single-file retry requeues that failed download and may return its errored parent to finished/provider-ready state. Confirmation must explain this difference and preserve completed files.

Audit findings before implementation:

- POST torrent retry accepts only terminal transfers; the existing service calls engine cancellation before checking the state in its transaction. A stale or duplicate native request must not cancel active work before returning conflict. Review the worker cancellation/commit ordering before enabling the action.
- Whole retry increments torrent RetryCount. File retry resets its retry counter to zero, so that counter cannot reconcile an uncertain user retry. A fresh GET showing error is not proof the first request failed.
- Both POSTs lack a native durable idempotency receipt. Native actions must persist uncertainty, avoid automatic resending and prohibit blind second mutations after timeout. Decide the focused backend precondition/receipt contract before implementing a retry button that could act on a later failure.
- Manual selections are tied to retry incarnation; never carry an old accepted revision into the new attempt. New provider metadata requires a new explicit selection.

Validation will use fixture server/core worker tests only: stale action, duplicate tap, response loss, app interruption, single-file identity and parent binding, source/default compatibility, provider re-add, unchanged completed files, new manual-selection review and history/progress refresh. No production retry or example submission.

## Initial backend guard implemented (draft; native retry not enabled)

The engine now provides a per-torrent operation slot. Service retry waits for the slot, rereads eligibility, stops/drains old work, and commits its reset while scheduler admission for that torrent stays frozen. Scheduler passes skip busy slots and reread each eligible torrent after admission; unrelated transfers continue. Slots are released and removed after cancellation/completion.

Delete-on-error cleanup owns the same slot and rereads error status, retry incarnation, completion timestamp and due policy before deletion. Its wait has a one-minute context bound. DeleteTorrent now returns cancellation failure before any provider/file/database deletion; the prior log-and-continue behavior could remove data underneath an undrained worker. This is a safety prerequisite, not the separate user-facing cancellation feature planned at22/23.

Regression coverage includes ineligible retry causing no cancellation, drain failure retaining the transfer/download rows, failed delete drain preserving provider/file bytes/DB record, independent scheduler admission while an earlier torrent is busy, cancellation/reference cleanup, and stale scheduled cleanup after retry. Full service/engine race suites and pinned lint pass. Independent read-only follow-through resolved blocking scheduler admission and failed-drain deletion findings. No production operations or simulator use in this backend draft.

The native protocol/capability/wire foundation is now implemented below. Still required before milestone21 is merge-ready: protected immutable native action intent, single-file and whole-transfer confirmation/reconciliation, native source/visual review and appropriate full validation. POST retry must not be automatically repeated after an uncertain response. The initial guard alone does not provide durable retry idempotency. Direct user delete/cancel semantics remain the separate22/23 scope.

## Durable protocol foundation implemented (native UI still pending)

`native_retry_v1` advertises a separate API without changing legacy POST paths. Collections are `torrents` or `downloads`, with exact API resource IDs:

| Action | Route | Behavior |
| --- | --- | --- |
| Review | GET `/api/v1/{collection}/{id}/retry-review` | Eligibility, parent ID, opaque64hex revision and manual-hold flag |
| Request | POST `/api/v1/{collection}/{id}/retry-requests` | JSON `key` (UUIDv4) and original `revision`; one accepted local mutation |
| Reconcile | GET `/api/v1/{collection}/{id}/retry-requests/{key}?revision=…` | Check the exact saved intent without mutation;404 means no committed receipt observed, not proof the POST failed |

Authenticated native cookies without Origin are supported; foreign/null cookie Origins remain rejected. Successful responses and handled missing/conflict responses use no-store. Output excludes settings, provider URLs and private error text. Acceptance means a local retry was committed, not provider/NAS success. `torrent_exists` means parent existence only: a file receipt remains accepted even if a later whole retry removed that download row.

The existing durable receipt table stores a separate retry key namespace and a fingerprint binding kind/exact resource/original revision. Replay is checked before current eligibility or engine cancellation and returns the original acceptance, including after parent removal. The receipt and state/download reset commit in one SQLite transaction. A failed receipt insert rolls back the mutation. Native request key mismatch returns422; changed review with a fresh key returns409.

Revision includes parent state/settings/incarnation, download snapshot where applicable and a parent-scoped committed receipt count. Native retry and legacy file retry advance that count atomically. This prevents counter/timestamp ABA and conservatively invalidates other outstanding reviews on the same parent. Receipts are retained indefinitely; deletion does not remove them. Add and retry key namespaces cannot replay each other.

Migration00005 adds only a parent-ID index so retained history elsewhere does not cause a global count scan. It does not alter existing table columns or application rows. The upgrade test starts at schema4, preserves an accepted tombstone, verifies index use and integrity. Fresh consistent backup and migration verification are mandatory before any later production deployment; no deployment has occurred. Assess the existing manual-hold downgrade gate and migration compatibility before any rollback.

Service/HTTP tests cover native/legacy counter ABA, same-key concurrency through independent SQLite handles, stale rules before cancellation, atomic rollback, tombstones and later download-row removal, cookie/auth/Origin boundaries, key/kind/resource/revision mismatches and no-store. Full Go race tests pass. Go-generated OpenAPI/clients/wire fixtures and web types are regenerated. Native RetryAPI validates binding, scopes URLs/credentials, encodes the receipt query and treats404 as unknown;23 wire/transport tests pass. Independent read-only protocol review approved this foundation. Captured parent validation in the forthcoming model, immutable protected intent, lifecycle behavior and final native UI/source/visual validation remain pending.
