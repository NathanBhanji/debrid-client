# Retry actions — milestone21

The native UI will distinguish retrying one failed NAS download from restarting a failed transfer. Whole-transfer retry clears provider identity and download rows, retains disk files and resets manual-selection acceptance while keeping the manual hold. Single-file retry requeues that failed download and may return its errored parent to finished/provider-ready state. Confirmation must explain this difference and preserve completed files.

Audit findings before implementation:

- POST torrent retry accepts only terminal transfers; the existing service calls engine cancellation before checking the state in its transaction. A stale or duplicate native request must not cancel active work before returning conflict. Review the worker cancellation/commit ordering before enabling the action.
- Whole retry increments torrent RetryCount. File retry resets its retry counter to zero, so that counter cannot reconcile an uncertain user retry. A fresh GET showing error is not proof the first request failed.
- Both POSTs lack a native durable idempotency receipt. Native actions must persist uncertainty, avoid automatic resending and prohibit blind second mutations after timeout. Decide the focused backend precondition/receipt contract before implementing a retry button that could act on a later failure.
- Manual selections are tied to retry incarnation; never carry an old accepted revision into the new attempt. New provider metadata requires a new explicit selection.

Validation will use fixture server/core worker tests only: stale action, duplicate tap, response loss, app interruption, single-file identity and parent binding, source/default compatibility, provider re-add, unchanged completed files, new manual-selection review and history/progress refresh. No production retry or example submission.

## Backend admission guard

The engine now provides a per-torrent operation slot. Service retry waits for the slot, rereads eligibility, stops/drains old work, and commits its reset while scheduler admission for that torrent stays frozen. Scheduler passes skip busy slots and reread each eligible torrent after admission; unrelated transfers continue. Slots are released and removed after cancellation/completion.

Delete-on-error cleanup owns the same slot and rereads error status, retry incarnation, completion timestamp and due policy before deletion. Its wait has a one-minute context bound. DeleteTorrent now returns cancellation failure before any provider/file/database deletion; the prior log-and-continue behavior could remove data underneath an undrained worker. This is a safety prerequisite, not the separate user-facing cancellation feature planned at22/23.

Regression coverage includes ineligible retry causing no cancellation, drain failure retaining the transfer/download rows, failed delete drain preserving provider/file bytes/DB record, independent scheduler admission while an earlier torrent is busy, cancellation/reference cleanup, and stale scheduled cleanup after retry. Full service/engine race suites and pinned lint pass. Independent read-only follow-through resolved blocking scheduler admission and failed-drain deletion findings. No production operations or simulator use in this backend draft.

The native protocol and protected confirmation/recovery flow are implemented below. POST retry is never automatically repeated after an uncertain response. Direct user delete/cancel semantics remain the separate22/23 scope.

## Durable retry protocol

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

Service/HTTP tests cover native/legacy counter ABA, same-key concurrency through independent SQLite handles, stale rules before cancellation, atomic rollback, tombstones and later download-row removal, cookie/auth/Origin boundaries, key/kind/resource/revision mismatches and no-store. Full Go race tests pass. Go-generated OpenAPI/clients/wire fixtures and web types are regenerated. Native RetryAPI validates binding, scopes URLs/credentials, encodes the receipt query and treats404 as unknown;23 wire/transport tests pass. Independent read-only protocol review approved this foundation. The native model and protected storage enforce captured parent binding as described below.


## Protected native confirmation and recovery

Failed transfer detail and failed file rows open a native review sheet. Whole-transfer confirmation explains the reset and a new file review when the transfer is held for manual selection; single-file confirmation preserves completed files. These are explicit user actions, never background retry loops.

Inbox format5 migrates formats1–4 while preserving existing drafts, payloads, submissions and file-selection intents. It adds bounded retry rows under the existing protected atomic cross-process store. Before POST, an immutable intent binds the original server, resource kind/ID, parent ID, revision and UUID key. Unknown intents are never evicted for capacity. Missing/corrupt new envelope fields fail closed before cleanup. Lease reclamation clears fresh-request rejection authority.

Reopening an unresolved retry performs receipt lookup only. A missing receipt remains unknown; explicit retry uses the same saved key and revision. An uncertain conflict does not authorize a new key. Only a definite rejection of a fresh request can retire that request. Mismatch blocks reuse; receipt acceptance validates the captured parent. Accepted proof survives local tracking failure and means the server committed a retry, not that provider or NAS work completed. Parent existence does not imply that a historical file row still exists.

The model checks current connection identity after awaits and before mutation. Changing server dismisses the sheet and prevents delayed work from acting on the new connection. The same connection guard now protects file-selection confirmation. Closing a sheet cancels local work without claiming to cancel server work; saved uncertainty remains in Pending Items on its original server.

## Native review and validation

| Before | After | Why |
| --- | --- | --- |
| Recovery controls followed the full original review | Current result and recovery controls precede a collapsed original-review section | Keep recovery reachable at ordinary and large text sizes |
| Manual-hold warning used dark lamp ink on a dark surface | Semantic amber warning text | Maintain readable light/dark contrast |
| Generic refresh label across phases | Review again, Check server or Check result as appropriate | Describe the action the user will actually take |

188 core tests passed, including durable reclaim, migration preservation, quota, parent binding, capability recheck, connection changes, duplicate confirmation, lost responses and cancellation. Three file-selection UI regressions and four retry UI flows passed on the one shared simulator. Actual SwiftUI captures cover light/dark confirmation, unknown result, accepted result and largest Dynamic Type file confirmation. Independent source and final five-capture review approved. Extended two-process probes passed claim exclusion, exact-key reclaim and preservation by an unrelated writer. Unsigned iPhone Release app/framework/extension build passed.

Fixture APIs use isolated Debug inboxes and never send production retries. Simulator success does not certify signed-device linking, locked-device protection, Pocket ID on a real phone or a complete VoiceOver audit. Those remain final device acceptance checks. No production action occurred for this milestone; migration5 deployment requires a fresh verified consistent backup and a published pinned image.
