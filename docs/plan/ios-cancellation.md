# Cancellation and removal — milestone22

Status: draft safety foundation; cancellation endpoints and persisted cancelled states are not enabled. Do not merge the foundation alone or count milestone22 complete.

## Product contract

Cancel means stop this transfer's local orchestration and NAS work, retain the transfer record and completed/partial files as found after workers drain, and prevent automatic restart. It does not promise to pause or cancel remote provider activity. A provider request already accepted remotely may continue, including a request whose result was lost. Completed files are retained; in-flight unpacking may already have applied the user's existing archive policy before the stop completes. This is whole-transfer cancellation, not a new per-file cancel API.

Remove is a separate operation. Removing the record defaults to keeping NAS files and the provider copy. Deleting NAS files and removing the provider copy require independent explicit choices. DELETE is never a pause action. Provider/filesystem side effects cannot be made transactional with SQLite; the native flow must expose uncertainty and partial failures rather than inventing a successful cancellation.

## Required backend contract before native controls

- Explicit terminal cancelled parent and unfinished-child states; finished file rows/bytes remain. No provider delete or finished/delete-on-error cleanup as part of cancel.
- Fresh review bound to transfer and retry incarnation, plus a protected immutable request key and durable acceptance receipt. Acceptance commits atomically with the cancelled state only after successful worker drain.
- Validate the review before drain under per-transfer admission. After drain, allow expected child timestamp/state changes while retaining incarnation checks and explicitly handling a completion/error race. Do not blindly recheck a digest of changing progress after stopping workers.
- A lost response or process exit before state/receipt commit is unconfirmed. A missing receipt is not proof the request failed. Reopening performs lookup only; an explicit resend uses the same intent/key.
- Cancelling a retained record remains idempotent; an earlier cancellation key never cancels a new explicit retry. Original receipt lookup survives record removal. Foreign cookie Origins remain rejected; responses are no-store.
- Explicit whole-transfer retry can restart a cancelled record, preserving the manual-selection hold and requiring a new file review. Automatic provider/file retries cannot release cancellation.
- Web/native/CLI contract generation and presentation recognize cancelled as stopped, not complete/error/active. Older clients' existing endpoints retain their semantics.
- Downgrade gate: older backends must not run while retained cancelled parent/child states exist. Their pollers do not recognize this terminal state and can resurrect or delete work. Document resolution before any rollback.

## Safety foundation implemented

Deletion now owns per-transfer admission in the service. It rereads data before drain and again afterward, so late add identity, organized paths and extraction metadata are not lost. Failed drain still aborts all removal. Scheduled error cleanup delegates its expected incarnation/due condition to that service-owned flow and checks eligibility before and after drain, avoiding a nested nonreentrant operation lock.

Provider polling captures local incarnation before its network listing. Reconciliation uses nonblocking per-transfer admission, rereads current state, and skips changed account/provider/retry identity or terminal/queued/adding state. A busy transfer is left to the next fresh poll. Error, missing and relink results all pass through the same guard. Listing cache publication also checks an account invalidation generation, including invalidation with no cached snapshot; a late response cannot repopulate a deleted provider identity for a later same-hash add. Actual provider deletion invalidates again after successful removal, covering late identities committed during drain. Registered workers never wait on that admission slot while cancellation/deletion drains them. Provider-error application checks terminal/incarnation before its destructive retry branch; a delayed generic failure cannot overwrite a terminal result.

| Before | After | Why |
| --- | --- | --- |
| Delayed provider listing could act on a later retry | Capture incarnation before network and reread under admission | Old remote responses cannot remove or requeue new work |
| Delete used pre-drain provider identity and file paths | Reload final metadata after workers finish | Include late committed identity and paths |
| Cleanup owned admission before calling deletion | Service owns admission and checks cleanup predicate twice | Avoid nested-lock deadlock and stale cleanup |
| Generic delayed failure could replace completed state | Preserve terminal state | Do not turn a completed race into failure |

Full Go race suite, pinned lint, go mod tidy and generated-contract freshness checks pass. Independent source review approved the foundation after resolving delayed cache publication; cancellation protocol approval remains pending. Focused race tests cover delayed error/missing/relink listing after a same-provider-ID retry incarnation, terminal provider error/failure, busy deletion admission, late identity/path commits, stale cleanup after retry and cleanup eligibility changing during drain. All tests use isolated stores/providers. No production transfer mutation, new simulator or additional worktree.

Still required in this same milestone: cancellation protocol/states, restart and real worker drain regressions, receipt/auth/Origin/concurrency tests, current-client presentation compatibility, generated contracts, final independent review and full checks. Native confirmation/removal UI follows in23.
