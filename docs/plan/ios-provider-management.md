# Native provider management · milestone 25

Providers now offers Add provider and Edit on an account's detail. The native form supports all five existing provider kinds, a display name, default selection, enabled status and opt-in credential replacement. Existing keys are never fetched or shown. New keys are masked, bounded, excluded from reflection and cleared from the form before submission and on background/dismissal. They are sent only to the selected authenticated HTTPS server; no provider secret is persisted in Keychain, defaults, the shared inbox or logs. System password-saving semantics are not attached to the API-key field.

Saving uses the existing server-side credential verification. Edits read the exact account before submission and refuse a configuration timestamp change since opening. This is best-effort review, not a transactional conditional-update guarantee: another client may still edit after the read. The patch contains the form's explicit name/enabled/default intent and only includes credentials when replacement is selected. An existing default remains default until another account is chosen; disabling affects new transfers and does not promise to stop existing work.

Each sheet owns one synchronously admitted, cancellable command. Rapid taps cannot replace its cancellation handle. Model in-flight ownership and connection/generation/active guards prevent duplicate commands and stale responses. A lost or canceled response is unknown, never automatically replayed. Check server state only reads accounts; exact-ID matching is used for edits/removal and name/kind matching for creation. An observed account is not proof credentials were saved; observed absence is not cleanup acceptance. Credentials are not retained for replay. Closing an unknown operation discards this sheet's local metadata; reopen/refresh server state before making a new change.

## Account removal and backend boundary

Ordinary removal sends force=false and refuses accounts with transfers. Including associated transfers is a separate off-by-default choice with a final native confirmation. The operation applies to all transfers linked to that account when the server processes it, not a frozen count. It requests no filesystem or remote-provider deletion. A failed stop can leave earlier transfer records removed and later records/account present; no atomic multi-transfer cleanup is claimed.

Audit found that legacy forced account removal ignored CancelTorrent errors before deleting rows. It now reuses the existing per-transfer guarded DeleteTorrent path: operation admission, successful worker drain, reload and record-only deletion. A failed drain retains that transfer/account. A final transaction re-reads current default status before deleting the account and promoting a replacement, so another client's default change during drain is respected. The existing foreign key refuses deleting an account if a concurrently added transfer remains.

The new exact-ID DELETE `/accounts/{id}/removal` route and `native_account_removal_drain_v1` capability protect the native force path from older backends. Names/aliases are rejected there. Ordinary force=false removal retains the existing route; native force uses the new path. Capability or route absence never falls back to legacy force. No database migration or changed mounts/privileges are needed. The new backend behavior must ship in the next release before production force removal is enabled in the app.

## Emil review

| Before | After | Why |
| --- | --- | --- |
| Provider information is read-only | Native add/edit forms using the existing account contract | Completes account management without duplicating backend behavior |
| Existing credentials might look editable by default | Separate opt-in replacement with an empty masked field | Preserve server credentials unless explicitly replaced |
| Lost responses could encourage repeated POST/PATCH/DELETE | Unknown state with observation-only check | Avoid automatic duplicate or destructive replay |
| Repeated taps can overwrite the active task handle | One synchronous command owner with UUID-gated cleanup | Dismissal/background cancels the actual request |
| Forced removal ignored worker-stop failures | Existing guarded transfer deletion, with errors propagated | Keep records while worker safety is uncertain |
| Default selection was captured before a long drain | Re-read current default inside the deletion transaction | Respect concurrent changes from other clients |
| Narrow native popover clips content at largest Dynamic Type | Scrollable native confirmation sheet at accessibility sizes | Make all effects and safe/destructive actions reachable without font caps |
| Success could overclaim that files were kept despite concurrent cleanup | Say file/provider deletion was not requested | State exactly what this removal did |

## Checks and limits

Core tests cover write-only nested encoding, reflection redaction, required entry/clearing, single submission, changed-account refusal, capability gates, stopped/double-tapped preflight and observation-only uncertain results. Transport tests assert POST/PATCH bodies, omitted credentials on unchanged edits, scoped session/proxy path, explicit force=false and the dedicated force=true route. Backend tests exercise failed drain retention, later explicit success, exact-ID/auth/default conflict boundaries and both directions of a concurrent default change during drain.

Native fixture UI tests cover masked add/save, edit without exposing/replacing keys, default-safe removal with Keep account before confirmation, lost-save observation, and largest-text force confirmation. No real provider credentials, accounts or transfers are mutated by automation. Signed-device, full VoiceOver and real interrupted-network checks remain final gates. One shared simulator and checkout are reused; captures and result bundles remain bounded.

Final evidence: full native core222 passed before the last added held-preflight test; final focused8 editor tests passed afterward. All five native editing UI flows passed, with the final AX sheet rerun proving Keep account sends nothing before a later explicit Remove. Nine native PNGs received independent source/visual approval. Full Go race suite passed before the final default-transaction refinement; focused service/API race suites and pinned lint passed after it. Generated contract check, tidy, web formatting/lint/typecheck/build and final unsigned iPhone Release build passed. No production account/transfer request or deployment occurred.
