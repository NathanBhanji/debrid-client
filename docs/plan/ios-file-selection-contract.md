# Manual file review before NAS downloads

Backend milestone 18 adds an opt-in contract for native 19. Existing automatic transfers and clients keep their behavior. This change has no database schema migration: the hold and versioned acceptance receipt use the existing per-transfer settings JSON. It does not yet add a native picker or activate the capability on production.

## Add and review

`settings.wait_for_selection=true` at add time holds NAS downloads and provider file-selection calls. Any inherited manual IDs/internal receipt are cleared: parsed torrent indices or IDs from another transfer cannot release this hold. Global defaults may opt future transfers into review too. Generic settings replacement preserves a transfer’s hold, manual choice and internal receipt. The legacy files endpoint refuses opt-in transfers; automatic transfers retain its existing behavior.

Authenticated `GET /api/v1/torrents/{id}/file-selection` explicitly fetches authoritative provider metadata, with a 20 second context bound. Responses are no-store and exclude restricted links/internal receipts. Local parsed .torrent files are never treated as provider IDs. A remote content hash, when supplied, must match; providers omitting it remain bound by provider identity and reviewed metadata. Ordinary transfer snapshots add `file_selection_state` (automatic/pending/accepted) without inventing a new provider lifecycle status.

| Review state | Meaning |
| --- | --- |
| automatic | This transfer was added with automatic rules; it cannot be converted through review |
| pending | Provider identity or authoritative metadata is not ready; NAS downloads remain held |
| ready | A bounded unambiguous file list and opaque revision are available for explicit confirmation |
| accepted | An immutable choice is persisted; repeated reads/retries need no provider request |
| unavailable | Provider state/metadata cannot safely support review, or the transfer failed |

Only Real-Debrid supports remote file selection. Its ready state requires waiting_selection; a remotely completed selection cannot safely be retrofitted to an arbitrary subset. TorBox, AllDebrid, Premiumize and Debrid-Link process/fetch all files remotely; review waits until they finish, then controls the files downloaded to the NAS. This is not a pause/cancel guarantee for provider work. Real-Debrid may repack/split its selected results; the NAS downloads the results returned after the explicit provider selection, without guessing synthetic link-ID mappings.

## Confirmation and concurrency

Authenticated `PUT` to the same endpoint takes `{revision, file_ids}`. IDs must be nonempty and unique. Revision binds the transfer/content hash, account, provider incarnation/retry count and canonical sorted file IDs/paths/sizes. Ordering, progress, selected flags and restricted-link refresh do not change it. Both endpoints use the normal API-key/native-session authentication and cookie Origin boundary.

The server refetches authoritative metadata before the first confirmation. It rejects stale revisions, missing/ambiguous IDs, a changed provider incarnation, pre-existing download rows or a previously dispatched selection. One SQLite transaction accepts exactly one choice and freezes its internal version 1 receipt. A different choice conflicts; the identical revision/set replays success even after downloads start or the provider becomes unavailable. No in-memory lock or expiring review lease stands in for this durable boundary.

Provider-file metadata is capped at 20,000 files / 2 MiB encoded canonical metadata, IDs at 8,192 bytes each / 2 MiB total, and paths at 8,192 bytes. HTTP request limits apply as well. Unsupported metadata stays held. First confirmation accepts the complete selected ID set, rather than the legacy filter’s permissive partial match.

## Worker boundaries and retries

Scheduler guards hold both selection and NAS work, including cached/finished torrents without link-timeout failure. Jobs re-read current account/provider/retry/receipt before external work. Provider metadata must still match the reviewed revision. Non-selecting providers must return each chosen link exactly once with the reviewed path/effective size. Download insertion rechecks the same incarnation and acceptance in its transaction; download startup checks again. Delayed mismatch/failure responses can only fail their own still-current receipt, never a newly retried transfer.

Whole-transfer retry/re-add clears the receipt and old manual IDs while retaining the hold. Retry count participates in revisions, so even a provider that reuses its ID needs a new review. The engine’s existing cancellation semantics remain separate work; this contract is not a general cancellation primitive for already-running work from a previously confirmed selection.

## Rollout and downgrade gate

The health capability `manual_file_selection_v1` enables native 19 only on a supporting server. Clients must check this capability before submitting the hold; an older server ignores the unknown flag. Native 19 must advance its durable inbox/attempt format before saving the new policy, so an older app cannot discard the unknown option and submit an automatic request. Release/deploy still requires reviewed code and a pinned published image. No production example transfer or account mutation is part of validation.

**Do not downgrade to v0.2.3 or another unaware backend while any retained transfer OR default enables wait_for_selection.** An old binary ignores the flag and internal receipt, including when a completed transfer is later retried. Absence of a schema migration is not proof of safe runtime downgrade. Before downgrade, authorized maintenance must verify no such flags remain; otherwise keep the supporting image, or explicitly convert/remove affected policy under a reviewed maintenance procedure. Do not silently clear flags, overwrite a live DB or add host mounts/privileges. Ordinary image rollback before feature use remains possible after that check.

## Evidence

Full Go race suite passed, including default automatic clients. Added tests cover authoritative IDs, cached holds beyond link timeout, Real-Debrid wait/repacked results, stale revisions, missing/duplicate IDs, metadata/link changes, competing independent SQLite handles, offline/restart replay, retained hold through generic settings edits, retry invalidation, stale worker completion and API/native cookie/Origin/no-store/redaction boundaries. A golden v0.2.3 automatic intent hash remains unchanged; the new policy produces a different hash. Existing Swift wire decoding passes with the additive response. Independent read-only review resolved two worker race/mapping findings and approved the final source/operational gate. Native picker/fixture visual acceptance belongs to 19.
