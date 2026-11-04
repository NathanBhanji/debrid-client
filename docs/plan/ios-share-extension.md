# Share Extension — milestone20

The extension saves inputs locally for later review in Debrid. It never submits transfers, reads credentials or forces the containing app to launch. It uses the existing protected App Group inbox, duplicate handling, bounded storage and transaction lock. The existing app remains the owner of server selection and final submission.

Add a dedicated `dev.bhanji.debrid.share` XcodeGen app-extension target, embed it in Debrid and link the extension-safe DebridKit. Generate its plist/entitlements from project.yml, sharing only `group.dev.bhanji.debrid`; no Keychain access entitlement. Keep extension APIs restricted. Distribution profiles must contain the actual shared group before device/release validation.

Implement bounded NSItemProvider intake for URL, plain text and torrent file representations. Preserve original magnet text and torrent bytes. Multiple supported representations of one attachment must not create duplicate drafts; reject unsupported inputs explicitly. File-provider temporary URLs must be copied/parsed while their validity is guaranteed. Cancellation or termination must leave either the committed draft or no draft, never an automatic submission. Save completion must follow the durable commit.

SwiftUI review shows safe input summaries, Save to Debrid, Cancel and clear saved/duplicate/error results. Use the established native controls, warm matte content and orange actions. Avoid arbitrary web-title fetching and unauthenticated network requests.

Validation uses isolated item providers and inbox namespaces: URL/text/torrent, representation precedence, malformed/oversized data, multiple inputs, provider failure/cancellation, duplicate saves, offline save, lock contention, durable reopening and extension termination. Reuse the existing shared simulator. Inspect generated embedding, extension-safe linking and entitlements in simulator and unsigned arm64 builds. Real host-app share sheets, App Group signing and phone tests remain explicit separate gates; a simulator preview is not proof of them.

## Implemented behavior and evidence

The loader chooses a validated URL magnet before a plain-text caption, falling back to plain magnet text when the URL is not usable. Attachment-free extension text is supported; attributed content is not added again when attachments exist. No webpage contents are fetched. Text preserves the supplied URI; URL representations preserve the URL string supplied by the host. Exact URI bytes distinguish tracker/parameter variants even when their info hashes match.

Loading allows16 drafts,1MiB of URI text and32MiB of retained torrent bytes, with each file at most8MiB. File copying receives the remaining batch budget before allocating/copying its contents. A continuous-clock30-second batch deadline bounds sequential requests; individual waits are at most20seconds. Cancellation/deadline resumes the caller once, cancels provider Progress where the API supplies one, and ignores late callbacks before parsing/copying. Legacy loadItem does not supply Progress; its late host callback is ignored, rather than claiming the host's operation was canceled.

Three initial model tests plus exact-URI regression cover explicit save, no default server, no submission, duplicate accounting, partial retry and pre-cancellation. Provider tests cover temporary file copying, URL plus caption precedence, ordinary URL rejection, unsupported input, held cancellation and late/duplicate completion. Full core suite passed172 tests before the final attachment-free-text regression. Actual simulator ShareLink → More → Debrid opens the embedded extension and Cancel returns to the host. This test deliberately does not press Save in the real shared inbox; isolated tests cover persistence. Actual signed-device extension Save/reopen, host-app matrix and device file protection remain milestone30 integration gates.

Unsigned iPhone Release build passed. Inspection confirmed the embedded `.appex`, correct principal class and bundle ID, only the App Group entitlement, extension-safe framework linking, and the shared framework in the containing app rather than nested inside the extension. No signing profile is claimed ready. Independent source and actual dark-extension visual review approved after resolving the findings below.

| Before | After | Why |
| --- | --- | --- |
| Same-hash magnet variants collapsed | Byte-exact original URI identity | Preserves trackers and parameters |
| Payload quota checked after all files loaded | Remaining quota applied during each copy | Bounds retained file data |
| One wait per provider could accumulate | One batch deadline with per-provider cap | Prevents a prolonged share session |
| Plain caption took precedence over magnet URL | Valid URL first, explicit text fallback | Supports host representation differences |

Apple references: [extension activation rules](https://developer.apple.com/library/archive/documentation/General/Reference/InfoPlistKeyReference/Articles/AppExtensionKeys.html), [shared-container scenarios](https://developer.apple.com/library/archive/documentation/General/Conceptual/ExtensibilityPG/ExtensionScenarios.html), [NSItemProvider](https://developer.apple.com/documentation/foundation/nsitemprovider).
