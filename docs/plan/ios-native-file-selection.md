# Native file selection — milestone19

Nathan chose folder navigation like Files. Opening a folder never selects its contents. Files start unselected; bulk selection names its scope. Search spans all folders, preserves hidden choices and keeps the total selection count visible. A second native sheet reviews count/known bytes before explicit confirmation. Provider preparation and NAS downloads remain separate.

The add review offers reviewed automatic rules, all files or choose after adding. Manual review sends `wait_for_selection=true` only after checking `manual_file_selection_v1`. Existing automatic requests preserve their original fingerprint. The provider may prepare all content remotely; this hold controls NAS downloads. Authoritative provider file IDs come exclusively from the backend selection endpoint, never parsed torrent indices.

Provider IDs remain exact UTF-8 bytes through selection, canonical ordering, persistence and PUT. Display labels remove control/format scalars; labels and folder paths never reconstruct IDs. The complete review rejects oversized or ambiguous metadata rather than silently selecting a partial list. Folder depth and metadata/intent storage have explicit bounds. Sizes use checked arithmetic.

Protected inbox version4 stores up to32 server/transfer-bound reviews, with a512KiB limit per encoded ID set and1MiB across saved sets. New submission attempts use version2 to preserve manual policy. Older inbox readers refuse version4. Version1–3 snapshots migrate on the first successful transaction; malformed version4 selection records stop reading before cleanup.

A live120-second cross-process lease prevents concurrent edits. The immutable revision/ID set is saved before PUT. Reopening performs health and GET only; ready/pending is not proof an earlier PUT failed. Explicit retry keeps the same revision and bytes. Only a fresh dispatch's definite409/422 can retire a prepared intent. Reclaimed/restored attempts receive no fresh rejection authority. Accepted remote state resolves tracking; a different accepted choice is clearly reported without replacement. Unknown choices remain in Pending Items on their original server.

The sheet owns its navigation and lease. Folder action tasks cancel when inactive/disappearing; model generation checks reject late results. Protected cleanup survives view-task cancellation. No credentials are written to the shared inbox.

## Design review corrections

| Before | After | Why |
| --- | --- | --- |
| Sheet canceled an unused task | Each folder cancels its actual action task when inactive | Cancels transport alongside invalidating model state |
| Form commands inherited text ink | Explicit action tint and prominent confirmation | Distinguishes commands from content |
| Display names retained format scalars | Sanitize display only; preserve opaque IDs | Prevents visually misleading names without changing identity |
| Empty-links state appeared above saved choices | Hide it when saved selections exist | Keeps recovery discoverable |

Native UI uses standard navigation, lists, search and sheets. Glass stays in system controls; file content uses the existing matte web-derived palette. See Apple's [Liquid Glass overview](https://developer.apple.com/documentation/technologyoverviews/liquid-glass) and [toolbar guidance](https://developer.apple.com/design/human-interface-guidelines/toolbars).

Simulator and real-device validation are distinct. Physical-device/App Group signing and TestFlight remain milestone30 gates; simulator success does not satisfy them.
