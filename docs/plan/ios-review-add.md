# Native review and recoverable add

Milestone17 replaces the intake-only saved-item screen with a real native form. It loads enabled accounts, category suggestions and full settings from the selected server, with Pocket ID credentials held only in the app’s private session context. The App Group contains no credential. The destination, provider, category, file rules, retry limits, unpacking/organization and automatic deletion policies are visible before Add. Keep-for-later and local discard remain distinct from server actions.

## Immutable submission boundary

Protected inbox format3 explicitly migrates formats1/2, preserving exact magnet URI/torrent bytes, draft IDs, targets and leases. Each attempt contains a versioned random UUIDv4 key, canonical server URL, full settings/account/category snapshot and phase. Validation happens before orphan cleanup; unknown or corrupt snapshots remain intact. Existing storage quotas, protection, atomic replacement and cross-process locking apply.

Prepare saves that attempt under the live review lease before any network mutation. Exactly one in-flight model operation is allowed; later form changes cannot alter an accepted or uncertain request. Account/category/settings and target confirmation are captured before suspension. Each POST rechecks the server capability advertisement. The server must support durable receipts; torrent-file intake additionally requires multipart settings parity. The original full URI or original verified file bytes form the payload. Normal reads preserve existing transport behavior; only submission responses interpret validated conflict headers. Error bodies and credentials are not logged.

A fresh invocation receives a one-use rejection token that is never restored or attached to a reused request. Only that original invocation’s definite validation/active-transfer response can retire the intent for explicit editing. A retry/restored request receiving a conflict stays unresolved; it cannot mint a different key and accidentally duplicate a previously accepted operation. A mismatch becomes a durable blocked record. Connection generations scope late401 handling and navigation, including signing back into the same server.

## Outcomes and cancellation

| Outcome | Native behavior |
| --- | --- |
| Confirmed201 or receipt | Persist accepted ID/existence, show confirmation, then View transfer or Done |
| Lost response, offline, cancellation after prepare | Preserve frozen attempt; show Check result and Retry saved request |
| Receipt404 | State that no committed receipt was found; never infer the original POST cannot still finish |
| Explicit retry | Same server, UUID, original payload and options; never a fresh Add |
| Receipt confirms later deletion | Show previously added/removed, without recreating it |
| Local accepted-record write fails | Show known success, retain durable prepared state for later reconciliation |
| Different selected server | Require original server for an unresolved attempt; explicitly confirm retargeting an editable draft |
| Key mismatch | Preserve blocked record without resubmitting or adopting an unrelated result |
| Missing capabilities | Keep saved item, explain server update, permit explicit recheck |

Dismissing/backgrounding cancels the local task and releases the editing lease. It does not promise cancellation of a server mutation already sent. Prepared/blocked drafts cannot be discarded through the ordinary inbox operation; accepted/rejected records can be explicitly archived/discarded. No automatic POST occurs on reopen. A server operator must retain the advertised contract during requests; capabilities cannot make an arbitrary rollback midway through an in-flight POST safe.

## File policy

Reviewed rules preserve full defaults; choosing all files clears manual IDs, size and include/exclude filters. A keep-copies override clears automatic provider cleanup and deletion after an error. The provider completion deadline is preserved and shown separately; it can fail a transfer without deleting its copies. Destructive effects are explicitly shown before Add. Actual provider-file selection before download uses backend18 and native19, with explicit waiting semantics; this form does not claim it.

## Validation

156 hosted core tests passed, covering protected-before-POST ordering, double taps, exact JSON/multipart bodies, nonce consumption, timeout-after-commit,404/reopen, explicit same-key retry, cancellation, wrong server, local write failure, malformed intent preservation and a stale401 from a prior connection. Native UI tests exercise explicit Add, confirmed detail navigation, uncertain receipt recovery and the largest accessibility text size, using a UUID-isolated protected inbox and fictional in-memory API. Release ignores all fixture flags. No production magnet was submitted.

Simulator proof is separate from signed physical-device URL delivery, App Group protection and release readiness. Apple website group assignment/new ASC record remain required. One shared simulator and bounded artifacts are reused.

## Independent design/source review

Emil’s review format was used for the real form and coordinator, with system-owned sheets/navigation/control feedback rather than decorative animation.

| Before | After | Why |
| --- | --- | --- |
| Intake ended at a local saved-item review | Explicit native Add with server/provider/category and full rules | Make the operation and destination reviewable |
| Unknown response could invite a fresh Add | Durable frozen intent, receipt check and same-request retry | Avoid accidental duplicate submission |
| Automatic cleanup remained implicit in inherited settings | Visible lifecycle section before Add and keep-copies override | Explain potentially destructive consequences |
| Ordinary actions inherited content ink | Explicit action tint with native button styles | Distinguish actions from labels in both appearances |
| Old rejected settings could appear during resubmission | Frozen in-flight options take precedence | Display exactly what the current request sends |
| Late401 from the same profile’s older connection | Per-connection generation check | Preserve a newer sign-in |
| Bright red explanatory warning on white (3.57:1) | Adaptive darker DangerInk text, with high-contrast variants | Preserve readable deletion warnings without changing native destructive actions |
| Footer implied permanent downloaded-file retention | Files download to your server using the reviewed settings | Agree with configured cleanup policies |

Independent final source and actual light/dark capture review approved. Light warning ink measured6.93:1 on white; primary action and native controls were reviewed in both appearances. Three fixture UI flows passed (explicit Add/detail navigation, uncertain receipt recovery, largest Dynamic Type); final light/dark confirmation flows additionally asserted accurate cleanup copy. Two-process storage recovery probes passed. Physical-device signing/auth/protection remain unverified.
