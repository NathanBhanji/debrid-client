# Foundation verification · 2026-11-03

PR 01 is a fixture-only native foundation, not a connected client. No network path exists in the app. No production transfers, accounts, database, mounts or settings were changed.

- Xcode 27.1 / SDK 27.1; iOS 27.0 deployment and runtime.
- `python3 ios/scripts/check.py build`: passed; native app and DebridKit compile/link for simulator.
- `python3 ios/scripts/check.py device`: passed; unsigned arm64 app/framework compilation and linking. This does **not** establish device install, provisioning, distribution or App Group/Keychain behavior.
- `python3 ios/scripts/check.py test --simulator …`: passed on the single Debrid Shared iPhone, 3 Swift Testing functions (including 5 unsafe-address cases). Checks HTTPS boundary, forbidden URL credentials/query/fragment, reverse-proxy prefix, tailnet host and IPv6. XCTest's 0-test preamble is followed by Swift Testing's successful suite.
- Go `go mod tidy`, `make lint` (0 issues), `make generate-check`: passed without backend/generated changes.
- Board preview: 728px light and 360px dark/large layout study, no console errors. Ten screen/state studies. These do not substitute for native accessibility checks.
- Simulator app installed/launched. Native transfer shell visually inspected light, dark and largest accessibility text size. Text wraps in the scrolling list; explicit navigation-bar color scheme keeps title legible after theme changes. Captures: [light](../design/ios/screenshots/transfers-light.png), [dark](../design/ios/screenshots/transfers-dark.png). The original foundation had no interaction tests. The web-identity revision adds 2 passing XCTest UI flows covering native sheet presentation/dismissal, detail navigation, tab switching, and transfer/sheet reachability at the largest accessibility text size. A full VoiceOver audit is still pending.
- One reusable simulator created, no cloned devices or new runtimes. Shut down after verification; normal content size restored. Local build artifacts remain below 500 MB, bounded logs/screenshots below 5 MB; one latest test result retained. No extra worktrees.

Xcode emitted AppIntents metadata-skipped notices (no AppIntents dependency) and simulator build-version parsing warnings (`invalidDigitCount(94232)`); the build/test processes exited successfully. No TestFlight upload, signed iPhone test, SSO session, URL association or Share Extension exists yet. App icon/distribution metadata remain release gates.

Nathan approved Debrid/three tabs and chose Pocket ID SSO during this work. He then superseded the initial teal direction, requiring the existing web identity within native iOS 27 design and Emil skill use in boards and implementation. The roadmap now gates live transfers on the secure native browser exchange, rather than a phone API-key copy flow.

## Revised design evidence · completed 2026-11-04

Nathan required the existing web identity and iOS 27 design language, with Emil skills applied to artifacts and real SwiftUI. [Research and Before/After review](../design/ios/research.md) records Apple primary sources and `emil-design-eng`, `apple-design`, `write-swift` application. Native glass navigation/controls remain system-owned; content now uses warm surfaces, orange identity, a dark readout and semantic segmented progress. Custom assets include light/dark/increased-contrast variants. App main-actor isolation and approachable concurrency enabled without coupling DebridKit to UI isolation.

Revised simulator build and complete test scheme passed: 3 Swift Testing functions plus 2 XCTest interaction flows, no failures. Updated screenshots replace the initial teal captures. Board light/desktop and dark/large/phone previews had no console errors. Signed-device, full VoiceOver, and production integration remain pending; this app still cannot send network requests.
