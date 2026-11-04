# Cancellation and removal — milestone22

Status: backend cancellation, terminal states and typed native transport are implemented. Native persisted confirmation/recovery and cancel/remove controls follow in milestone23. Milestone22 is complete only after final review/checks and PR60 merge.

## Product contract

Cancel means stop this transfer's local orchestration and NAS work, retain the transfer record and completed/partial files as found after workers drain, and prevent automatic restart. It does not promise to pause or cancel remote provider activity. A provider request already accepted remotely may continue, including a request whose result was lost. Completed files are retained; in-flight unpacking may already have applied the user's existing archive policy before the stop completes. This is whole-transfer cancellation, not a new per-file cancel API.

Remove is a separate operation. Removing the record defaults to keeping NAS files and the provider copy. Deleting NAS files and removing the provider copy require independent explicit choices. DELETE is never a pause action. Provider/filesystem side effects cannot be made transactional with SQLite; the native flow must expose uncertainty and partial failures rather than inventing a successful cancellation.

## Backend contract

- Explicit terminal `cancelled` parent and unfinished-child states; finished file rows/bytes remain. No provider delete or finished/delete-on-error cleanup as part of cancel.
- Fresh review bound to transfer and retry incarnation, plus a protected immutable request key and durable acceptance receipt. Acceptance commits atomically with the cancelled state only after successful worker drain.
- Validate the review before drain under per-transfer admission. After drain, allow expected child timestamp/state changes while retaining incarnation checks and explicitly handling a completion/error race. Do not blindly recheck a digest of changing progress after stopping workers.
- A lost response or process exit before state/receipt commit is unconfirmed. A missing receipt is not proof the request failed. Reopening performs lookup only; an explicit resend uses the same intent/key.
- Cancelling a retained record remains idempotent; an earlier cancellation key never cancels a new explicit retry. Original receipt lookup survives record removal. Foreign cookie Origins remain rejected; responses are no-store.
- Explicit whole-transfer retry can restart a cancelled record, preserving the manual-selection hold and requiring a new file review. Automatic provider/file retries cannot release cancellation.
- Web/native/CLI contract generation and presentation recognize cancelled as stopped, not complete/error/active. Older clients' existing endpoints retain their semantics.
- Downgrade gate: older backends must not run while retained cancelled parent/child states exist. Their pollers do not recognize this terminal state and can resurrect or delete work. Document resolution before any rollback.

## Admission, polling and deletion safety

Deletion now owns per-transfer admission in the service. It rereads data before drain and again afterward, so late add identity, organized paths and extraction metadata are not lost. Failed drain still aborts all removal. Scheduled error cleanup delegates its expected incarnation/due condition to that service-owned flow and checks eligibility before and after drain, avoiding a nested nonreentrant operation lock.

Provider polling captures local incarnation before its network listing. Reconciliation uses nonblocking per-transfer admission, rereads current state, and skips changed account/provider/retry identity or terminal/queued/adding state. A busy transfer is left to the next fresh poll. Error, missing and relink results all pass through the same guard. Listing cache publication also checks an account invalidation generation, including invalidation with no cached snapshot; a late response cannot repopulate a deleted provider identity for a later same-hash add. Actual provider deletion invalidates again after successful removal, covering late identities committed during drain. Registered workers never wait on that admission slot while cancellation/deletion drains them. Provider-error application checks terminal/incarnation before its destructive retry branch; a delayed generic failure cannot overwrite a terminal result.

| Before | After | Why |
| --- | --- | --- |
| Delayed provider listing could act on a later retry | Capture incarnation before network and reread under admission | Old remote responses cannot remove or requeue new work |
| Delete used pre-drain provider identity and file paths | Reload final metadata after workers finish | Include late committed identity and paths |
| Cleanup owned admission before calling deletion | Service owns admission and checks cleanup predicate twice | Avoid nested-lock deadlock and stale cleanup |
| Generic delayed failure could replace completed state | Preserve terminal state | Do not turn a completed race into failure |

Full Go race suite, pinned lint, go mod tidy and generated-contract freshness checks pass. Independent source review approved the foundation after resolving delayed cache publication; cancellation protocol approval remains pending. Focused race tests cover delayed error/missing/relink listing after a same-provider-ID retry incarnation, terminal provider error/failure, busy deletion admission, late identity/path commits, stale cleanup after retry and cleanup eligibility changing during drain. All tests use isolated stores/providers. No production transfer mutation, new simulator or additional worktree.

## API and receipt rules

Servers advertise `native_cancellation_v1`. Exact API transfer IDs are required; prefix/hash aliases are not used for review or receipt requests.

| Action | Route | Meaning |
| --- | --- | --- |
| Review | GET `/api/v1/torrents/{id}/cancellation-review` | Eligibility, original incarnation revision, provider-may-continue warning |
| Request | POST `/api/v1/torrents/{id}/cancellation-requests` | JSON UUIDv4 `key` and original64hex `revision`; drain then atomically stop and record |
| Reconcile | GET `/api/v1/torrents/{id}/cancellation-requests/{key}?revision=…` | Check the original accepted intent without stopping anything again |

The revision binds account/hash/ID/added time/retry incarnation and a durable parent receipt count. It deliberately excludes progress, child timestamps and late provider identity, because stopping active workers can commit those changes. Eligibility is checked before drain and again inside the final transaction. A completion/error race stays terminal and returns conflict without a cancellation receipt. Failed drain or receipt insertion produces no committed stopped state. A stale review is rejected before stopping workers. Receipt count prevents counter/timestamp ABA.

A separate cancellation key namespace/fingerprint prevents replay through add/retry APIs. Replay comes before current eligibility and does not stop a later explicit retry. Original acceptance survives deletion; `torrent_exists` means existence only and does not promise the current incarnation is still stopped.404 means unconfirmed,422 mismatch must not be silently replaced with a new key, and409 requires reconciliation of any uncertain original request before a new review. Native cookie requests without Origin remain supported; foreign/null cookie Origins are refused. Private URLs/errors/settings are excluded from review/receipt output. Responses use no-store.

No schema migration is added: existing text state fields and the durable receipt table/index are reused. This does not make downgrade safe. **Do not run an older backend while any retained parent or child has `cancelled` state.** Review retained transfers, explicitly retry or remove them using the current backend as appropriate, verify both parent and child states are absent, and respect the existing manual-selection downgrade gate before considering rollback. Do not rewrite live records or claim an image-only rollback is automatically compatible.

## Client compatibility and evidence

Generated Go/OpenAPI/web contracts include both cancelled enums and the routes. Web readouts show STOP without active blinking; whole retry remains explicit. Native list/detail/files show Cancelled locally, exclude it from active/completed filters, provide a Cancelled filter and retain the provider warning. Typed URLSession review/request/receipt methods preserve proxy prefixes, scoped credentials, exact IDs and original revision query; missing receipts remain unknown. The model, protected cancellation intent, confirmation/recovery UI and removal-choice UI are deliberately milestone23.

Focused tests cover durable replay after explicit retry/removal, namespace/ID/revision mismatch, native cookie/Origin boundaries, atomic receipt rollback, failed drain, expected drain-induced metadata changes, completed/error races, stale incarnation rejection, independent SQLite handles, and a counter/timestamp ABA fence. A real engine test cancels a held fetch, verifies its partial bytes remain, then performs recovery/poll/tick with a remote provider error and confirms no restart or cleanup.191 core Swift tests and unsigned iPhone Release build pass; web formatting/lint/typecheck/build pass. Final full Go race/lint/generated checks and independent source review are recorded in the delivery log. No simulator result certifies physical protection/signing; native interactive cancellation acceptance follows in23.

Full testing also exposed unescaped SQLite URI punctuation in a test directory: `#` truncated the path and could alias another database. Store open and read-only access now use the same escaped file-URL builder. A regression checks literal `#`, `%`, spaces and supported `?` filenames against actual opened-file identity and read-only values; normal database paths are unchanged. No production database or transfer mutation was used in these tests.
