# Native cancellation and removal — milestone23

Status: conditional-removal API/transport foundation in progress. Protected native intents, models, confirmation/recovery UI and simulator review remain pending in this same milestone. Do not merge the foundation alone or count23 complete.

The cancellation backend and stopped-state readers ship in v0.2.6; see [cancellation contract](ios-cancellation.md). This milestone adds native explicit management without treating sheet dismissal as cancellation or DELETE as pause.

## Native review and recovery contract

Detail offers separate Cancel transfer and Remove transfer actions. Cancel stops local work, retains records/files/provider copy and warns that provider activity may continue. Its confirmation persists the original server/ID/incarnation/key before POST. Reopening checks the durable receipt without sending. An explicit resend uses the same saved key/revision; a historical accepted receipt never claims a later retry is currently stopped.

Remove defaults to keeping both NAS files and the known provider copy. Cleanup options are independent, with a final review before sending. Shared/untracked files or unknown remote submissions may remain even when cleanup is requested.204 means the record was removed; it is not a promise that every provider copy or byte is gone. The native implementation must persist original server/ID/revision/both options before DELETE and freeze them after any uncertain result.

Reconciliation is GET-only. A typed successful review with exists:false means the transfer is no longer listed; after an uncertain request the sheet must say cleanup cannot be confirmed, not that the original DELETE succeeded. A missing route/HTTP404 is an error, never absence proof. A still-present matching incarnation permits only an explicit resend of the original conditional DELETE/options. A changed incarnation blocks that saved action. Never automatically resend, replace options, mint a fresh action or delete a later retry.

Both flows check capabilities and captured connection identity before requests and after awaits, retain unknown actions in protected Pending Items on the original server, renew bounded claims, and stop local tasks on dismissal/background/connection changes. Closing a sheet does not cancel a server operation or discard its uncertainty. Double taps and competing launches share the same immutable intent/claim.

## Conditional removal API foundation

`native_removal_precondition_v1` advertises GET `/api/v1/torrents/{id}/removal-review` and DELETE `/api/v1/torrents/{id}/removal` with mandatory strong If-Match. The original DELETE route also enforces a supplied precondition while retaining legacy omission behavior. Review uses exact IDs and returns HTTP200 with torrent_id, exists, optional revision and provider_copy_known. Missing records explicitly return exists:false with no revision; a route404 remains distinguishable.

The native removal route requires exactly one strong quoted64hex revision; missing, present-empty, weak, wildcard, list and duplicate raw header values are refused before any worker/provider/file/record action. Native transport always uses that route, so a missing/stripped header cannot fall back to unconditional deletion. Only the original legacy DELETE route permits a truly absent header. Reviewed deletion requires an exact ID, validates the incarnation before stopping workers and again afterward under per-transfer admission, and returns412 on mismatch before cleanup. The incarnation fence deliberately tolerates normal drain metadata changes; cancellation/retry receipts invalidate older reviews. Responses use no-store.

The existing provider/filesystem/SQLite side effects are not atomic. A provider deletion followed by a record-delete failure leaves a listed record and potentially partial cleanup. Explicit repetition of unchanged options/incarnation can finish the operation; existing provider-not-found and missing-file handling are idempotent. No new backend receipt is claimed or needed for the conservative native contract above.

## Review and validation

Architecture review approved conditional DELETE without a new receipt provided the UI distinguishes record removal, historical cancellation acceptance and cleanup uncertainty. Source review caught empty/duplicate header fallback; raw cardinality validation and no-side-effect regressions address that boundary. A dedicated mandatory-precondition native route also rejects a missing header instead of falling back to legacy behavior. Final foundation source approval and checks are recorded in the delivery log.

Focused tests cover stale incarnation before/during drain, cancellation invalidating prior removal review, default preservation, exact IDs, partial cleanup and explicit identical retry, typed absence, legacy omission, cookie/Origin boundaries and malformed/empty/duplicate headers and mandatory native header absence. Native transport checks proxy prefix, scoped cookie, exact ID/revision/options, expected204 and route404 vs typed absence. Later native tests must cover ordinary/largest text/light/dark confirmations, cancellation response loss, removal response loss/partial cleanup, server changes, interruption and Pending Items restore. Fixture-only data; no production transfer/account mutations.
