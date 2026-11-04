# Accessibility and interaction hardening · milestone29

This pass keeps Debrid’s existing web identity and native SwiftUI navigation/materials. It improves touch exploration, form actions, error contrast, file-selection semantics and landscape keyboard behavior. It does not claim complete accessibility certification from simulator screenshots.

## Changes and Emil review

| Before | After | Why |
| --- | --- | --- |
| Some error/status/discard text used system red | Adaptive DangerInk throughout those foreground overrides | The semantic palette is measured against the shipped light/dark surfaces; destructive roles remain intact |
| Form action labels had compact hit regions | Native Buttons use a shared44-point minimum label/content shape with immediate press/disabled feedback | The actual target grows rather than adding exterior whitespace |
| Pocket ID’s prominent control was compact | Its actual label has a44-point minimum and large control size | Keep the primary sign-in action easy to touch and readable when it wraps |
| Accessible wordmark exposed a small exploration region | Its accessible container has a44-point minimum | Apple’s hit-region crop localized this specific finding to the wordmark |
| Read-only URL was the accessibility label | Human “Server address” label with the exact URL as its value | Announce context before the address; preserve copying |
| File choices relied on implicit image/text extraction | Decorative icon hidden; filename/path, size and selection value explicitly exposed | Both selected and deselected states are clear without SF symbol-name noise |
| Search keyboard persisted while opening a transfer | Search submission/navigation clears focus; query remains bound | Landscape navigation can use the filtered results without clearing the search |
| Preview notices could compress vertically | Semantic-font notices can grow vertically | Preserve the full notice at accessibility text sizes |

No custom animation was introduced; repeated actions use immediate opacity feedback, and navigation/sheets retain system behavior. Error and progress states retain text/icons in addition to color. Existing safe alternatives and final confirmations remain in place for provider removal, transfer/file actions and settings saves.

## Audit scope and evidence

Apple’s installed iOS27 SDK supplies XCUIVoiceOverService. The tested transfer-summary navigation enabled VoiceOver, moved through actual elements, captured speech containing the transfer name and NAS stage, and restored the prior service state. This establishes that focused flow, not every VoiceOver interaction or physical-device behavior.

The initial exploratory audit requested contrast, hit regions, sufficient descriptions, Dynamic Type and text clipping. Its failures are retained in [exploratory-audit.json](../design/ios/accessibility/exploratory-audit.json). These results were not relabeled as passing. The requested detail frame in that baseline was not reached after a navigation assertion failed; its report is not evidence about detail.

Default audit attachments localized both provider contrast crops to text blurred beneath the native floating tab bar. The crops are retained. This narrow observation does not clear unrelated findings. App-owned semantic text colors are independently checked against Chassis and PanelFace in normal/high-contrast light/dark appearances (56 pairings); this mathematical check does not measure composited system materials. Follow-up UI coverage checks an unobscured filtered transfer frame separately.

Dynamic Type/text-clipping heuristics also reported semantic-font section headers, native text/search fields and some app labels. These exploratory findings remain on record. Follow-up validation checks actual regular-to-largest row growth, readable label/value data, scrolling, keyboard dismissal and action reachability. The focused automated audit gate explicitly covers hit regions and sufficient descriptions; it does not silently count the other three exploratory audit types as cleared.

## Remaining release acceptance

Full VoiceOver order/actions, real-device lock/unlock/App Group and Keychain behavior, competing magnet handlers and real share hosts remain milestone30/device checks. Simulator UI evidence does not establish physical-device linking or complete assistive-use acceptance. TestFlight provisioning is now ready; the distinct record and validated profiles are documented in ios/README.md. No production provider/account/transfer or settings mutation is used for this pass.

Sources: [Apple accessibility audits](https://developer.apple.com/documentation/accessibility/performing-accessibility-audits-for-your-app), [audit types](https://developer.apple.com/documentation/xcuiautomation/xcuiaccessibilityaudittype), [iOS27 VoiceOver service](https://developer.apple.com/documentation/xcuiautomation/xcuivoiceoverservice), [Dynamic Type](https://developer.apple.com/videos/play/wwdc2024/10074/).

## Completed validation

243 core tests passed again after rebasing onto all three security merges54/56/55 atdbbae6a; native session/transport behavior showed no regression. Fourteen distinct UI flows passed across focused runs: hit-region/description audits on Connect, Transfers/detail, Providers and Settings; actual row growth from regular to Accessibility XXXL; landscape search/keyboard/Back-query preservation; explicit file-selection values; unobscured filtered-transfer contrast; actual iOS27 VoiceOver summary navigation; system Reduce Motion navigation; largest-text file confirmation, native Share Extension, provider-removal safe alternative and settings review actions. The existing all-five-type exploratory run remains explicitly failed and is not counted as a passing audit.

The system Reduce Motion flow enabled the real simulator setting, navigated the app and restored the observed original value. Cleanup was corrected to reopen Settings and resolve fresh switch geometry after app transitions; a temporary recovery case repaired the known prior zero state and was removed from committed tests. VoiceOver and Motion checks allow assertion cleanup to run and retain the prior state. Native share assertions now scroll to lazy content before testing it.

Final source and all11 actual captures were independently reviewed. An initial landscape application-window screenshot had a black region; the replacement uses XCUIScreen.main after settling and shows valid full-width content. Accessibility-size transfer heading/count now stack vertically. Compact captures were exported before another Xcode build could prune old result bundles; metadata distinguishes passing cases from mixed failed runs.

The final unsigned arm64 iPhone Release compile/link, palette56-pair check, generated freshness, tidy and pinned lint passed. One shared simulator, checkout and build directory were reused. Signed archive/upload remain milestone30; no production mutation was used.
