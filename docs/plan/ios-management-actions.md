# Native cancellation and removal — milestone23

Status: conditional-removal API, protected native intents, confirmation/recovery sheets and Pending Items integration are implemented and independently reviewed. Milestone23 is complete when PR61 passes final CI and merges.

The cancellation backend and stopped-state readers ship in v0.2.6; see [cancellation contract](ios-cancellation.md). This milestone adds native explicit management without treating sheet dismissal as cancellation or DELETE as pause.

## Native review and recovery contract

Detail offers separate Cancel transfer and Remove transfer actions. Cancel stops local work, retains records/files/provider copy and warns that provider activity may continue. Its confirmation persists the original server/ID/incarnation/key before POST. Reopening checks the durable receipt without sending. An explicit resend uses the same saved key/revision; a historical accepted receipt never claims a later retry is currently stopped.

Remove defaults to keeping both NAS files and the known provider copy. Cleanup options are independent, with a final review before sending. Shared/untracked files or unknown remote submissions may remain even when cleanup is requested.204 means the record was removed; it is not a promise that every provider copy or byte is gone. The native implementation persists original server/ID/revision/both options before DELETE and freezes them after any uncertain result.

Reconciliation is GET-only. A typed successful review with exists:false means the transfer is no longer listed; after an uncertain request the sheet must say cleanup cannot be confirmed, not that the original DELETE succeeded. A missing route/HTTP404 is an error, never absence proof. A still-present matching incarnation permits only an explicit resend of the original conditional DELETE/options. A changed incarnation blocks that saved action. Never automatically resend, replace options, mint a fresh action or delete a later retry.

Both implemented flows check capabilities and captured connection identity before requests and after awaits, retain unknown actions in protected Pending Items on the original server, renew bounded claims, and stop local tasks on dismissal/background/connection changes. Closing a sheet does not cancel a server operation or discard its uncertainty. Double taps and competing launches share the same immutable intent/claim.

## Conditional removal API foundation

`native_removal_precondition_v1` advertises GET `/api/v1/torrents/{id}/removal-review` and DELETE `/api/v1/torrents/{id}/removal` with mandatory strong If-Match. The original DELETE route also enforces a supplied precondition while retaining legacy omission behavior. Review uses exact IDs and returns HTTP200 with torrent_id, exists, optional revision and provider_copy_known. Missing records explicitly return exists:false with no revision; a route404 remains distinguishable.

The native removal route requires exactly one strong quoted64hex revision; missing, present-empty, weak, wildcard, list and duplicate raw header values are refused before any worker/provider/file/record action. Native transport always uses that route, so a missing/stripped header cannot fall back to unconditional deletion. Only the original legacy DELETE route permits a truly absent header. Reviewed deletion requires an exact ID, validates the incarnation before stopping workers and again afterward under per-transfer admission, and returns412 on mismatch before cleanup. The incarnation fence deliberately tolerates normal drain metadata changes; cancellation/retry receipts invalidate older reviews. Responses use no-store.

The existing provider/filesystem/SQLite side effects are not atomic. A provider deletion followed by a record-delete failure leaves a listed record and potentially partial cleanup. Explicit repetition of unchanged options/incarnation can finish the operation; existing provider-not-found and missing-file handling are idempotent. No new backend receipt is claimed or needed for the conservative native contract above.

## Review and validation

Architecture review approved conditional DELETE without a new receipt provided the UI distinguishes record removal, historical cancellation acceptance and cleanup uncertainty. Source review caught empty/duplicate header fallback; raw cardinality validation and no-side-effect regressions address that boundary. A dedicated mandatory-precondition native route also rejects a missing header instead of falling back to legacy behavior. Final source/actual simulator capture approval and checks are recorded in the delivery log.

Focused tests cover stale incarnation before/during drain, cancellation invalidating prior removal review, default preservation, exact IDs, partial cleanup and explicit identical retry, typed absence, legacy omission, cookie/Origin boundaries and malformed/empty/duplicate headers and mandatory native header absence. Native transport checks proxy prefix, scoped cookie, exact ID/revision/options, expected204 and route404 vs typed absence. Later native tests must cover ordinary/largest text/light/dark confirmations, cancellation response loss, removal response loss/partial cleanup, server changes, interruption and Pending Items restore. Fixture-only data; no production transfer/account mutations.


## Protected native implementation

Inbox6 adds bounded management rows and atomically migrates formats1–5, preserving prior drafts, file payloads, submissions, file selections and retries. Version6 requires its management array; corruption/future versions fail closed before cleanup. One unresolved action per original server/transfer prevents changing action kind underneath uncertainty. Up to32 unknown actions are retained without automatic eviction. Claims/nonces are cleared on release/reclaim; restored requests cannot retire an earlier unknown intent as a fresh rejection.

Cancellation keys go to the durable backend receipt API; removal UUIDs are local correlation only. Immutable removal choices/revision go to the mandatory conditional route. Typed absence and a direct204 remain different outcomes. Known results survive local tracking failure and view shutdown; original-server/connection checks fence late responses and401 handling. Renewal tasks cancel on stop and deinit. Pending Items restores only on the original connected server. Done closes local UI; backgrounding/dismissal never claims to undo server work.

| Before | After | Why |
| --- | --- | --- |
| Result followed long review context | Result/recovery appears first after submission | Makes feedback visible at largest text sizes |
| Recovery/detail actions inherited body ink | Explicit brand and destructive text colors | Keeps actions identifiable in both appearances |
| Long cleanup dialog overflowed at largest text | Short selected-effect summary with explicit Keep reviewing | Makes the full choice readable and safely dismissible |
| Renewal could outlive its model | Deinit cancels renewal | Prevents stranded claims |

204 core tests passed, covering migration/quotas/claims, original options and key persistence, nonce boundaries, receipt binding, route404, changed incarnations, connection/capability changes, duplicate confirmation and late unauthorized responses. Five native UI flows passed together; a final largest-text dialog refinement also passed, including Keep reviewing with no mutation, then explicit confirmation. Actual captures cover light/dark, unknown/absence, and largest options/primary/dialog; see [native design captures](../design/ios/management/README.md). Extended two-process probes passed management claim exclusion, original-key reclaim and preservation by an unrelated writer. Unsigned iPhone Release build passed. Go full race/lint/generated and web checks are recorded in the delivery log. No production transfer/account mutation was used; simulator results do not certify real-device signing, protection or VoiceOver.
