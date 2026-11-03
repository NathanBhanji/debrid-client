# Foundation verification · 2026-11-03

PR 01 is a fixture-only native foundation, not a connected client. No network path exists in the app. No production transfers, accounts, database, mounts or settings were changed.

- Xcode 27.1 / SDK 27.1; iOS 27.0 deployment and runtime.
- `python3 ios/scripts/check.py build`: passed; native app and DebridKit compile/link for simulator.
- `python3 ios/scripts/check.py device`: passed; unsigned arm64 app/framework compilation and linking. This does **not** establish device install, provisioning, distribution or App Group/Keychain behavior.
- `python3 ios/scripts/check.py test --simulator …`: passed on the single Debrid Shared iPhone, 3 Swift Testing functions (including 5 unsafe-address cases). Checks HTTPS boundary, forbidden URL credentials/query/fragment, reverse-proxy prefix, tailnet host and IPv6. XCTest's 0-test preamble is followed by Swift Testing's successful suite.
- Go `go mod tidy`, `make lint` (0 issues), `make generate-check`: passed without backend/generated changes.
- Board preview: 728px light and 360px dark/large layout study, no console errors. Ten screen/state studies. These do not substitute for native accessibility checks.
- Simulator app installed/launched. Native transfer shell visually inspected light, dark and largest accessibility text size. Text wraps in the scrolling list; explicit navigation-bar color scheme keeps title legible after theme changes. Captures: [light](../design/ios/screenshots/transfers-light.png), [dark](../design/ios/screenshots/transfers-dark.png). Search/detail/sheet are implemented but no automated interaction test or VoiceOver audit is claimed.
- One reusable simulator created, no cloned devices or new runtimes. Shut down after verification; normal content size restored. Local build artifacts ~284 MB, bounded logs/screenshots ~1 MB; one latest test result retained. No extra worktrees.

Xcode emitted AppIntents metadata-skipped notices (no AppIntents dependency) and simulator build-version parsing warnings (`invalidDigitCount(94232)`); the build/test processes exited successfully. No TestFlight upload, signed iPhone test, SSO session, URL association or Share Extension exists yet. App icon/distribution metadata remain release gates.

Nathan approved Debrid/teal/graphite/three tabs and chose Pocket ID SSO during this work. The roadmap now gates live transfers on the secure native browser exchange, rather than a phone API-key copy flow.
