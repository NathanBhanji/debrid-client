# Debrid for iPhone

Native SwiftUI app, Swift 6 complete concurrency checking. **This first milestone is a fixture-only design preview**: no credentials, network requests, URL registration or live mutations. Search, tabs, detail navigation and the add design sheet work locally. It is not ready for daily use.

XcodeGen `project.yml` is the source of truth. Do not edit generated projects/Info.plists/entitlements. Xcode 27.1 (27A9275), iPhoneOS/Simulator SDK 27.1 and installed iOS 27.0 runtime verified on 2026-11-03. Minimum iOS **27.0**, provisionally matching Nathan's app fleet and the available runtime; confirm the phone's OS before device delivery. No third-party packages. Design and Swift implementation follow Emil’s `emil-design-eng`, `apple-design` and `write-swift` skills; see the [research/review](../docs/design/ios/research.md). Nathan requested the existing web identity within native iOS 27 navigation and controls, and chose Pocket ID SSO as the primary login; this requires a secure native exchange addition to the backend before live use.

```sh
cd ios
xcodegen generate
python3 scripts/check.py build
python3 scripts/check.py device # arm64 device compilation/linking, signing disabled
python3 scripts/check.py test --simulator <existing-Debrid-simulator-UUID>
open Debrid.xcodeproj
```

`dev.bhanji.debrid`, framework `dev.bhanji.debrid.kit`; proposed extension `dev.bhanji.debrid.share`, App Group `group.dev.bhanji.debrid`, team `ZTN995BU9H`. Only app/framework/test targets exist today. No copied profiles, shared Keychain entitlements or APNs capabilities. Device builds in Xcode use automatic signing; command-line checks deliberately disable it. Device installation, App Group/Keychain verification, app icon, distribution profiles and App Store Connect registration are future gates. Never reuse Flux/Heimdall profiles. No TestFlight upload is possible yet.

`DebridKit` is extension-safe and owns domain/transport/storage boundaries as they are added. `TransferPreview` is presentation data, not a speculative API schema. `ServerAddress` validates HTTPS roots without credentials/query fragments and preserves proxy prefixes. API DTOs, URLSession client, Keychain and protected inbox arrive separately with focused tests. Never persist secrets in defaults, design fixtures or logs.

## Resource policy

Never create a simulator per PR or reviewer. Check `xcrun simctl list devices available`, runtimes, `pgrep -fl xcodebuild` and disk space first. Reuse one named `Debrid Shared iPhone` device on iOS 27.0; record its UUID locally, pass it explicitly, and serialize tests. Do not borrow a booted device from another project. Do not install runtimes automatically. If no Debrid device exists, create exactly one after checking free space (at least 20 GB). A busy device is a reason to wait, not clone it.

Checks have a 15 minute process-group timeout, one derived-data directory, no parallel simulator clones, no explicit xcresult archive, and one log per operation. Logs retain the final 2 MB after completion. Xcode activity/results can still grow under `.build`: after each milestone inspect `du -sh .build .artifacts`, retain at most one relevant result and 8 screenshots, and remove only this checkout's generated artifacts once no owned test is running. Never erase unrelated caches/devices or delete a runtime. Shut down the Debrid simulator after use; retain it for reuse. Use this worktree for the train; only create another when independent work actually requires it. Remove completed worktrees only after checking clean state, pushed branch and PR safety. Never delete uncommitted changes.

Design: [spec](../docs/design/ios/README.md), [board](../docs/design/ios/board.html). Delivery: [29-PR plan](../docs/plan/ios-native.md). Audit: [API/platform evidence](../docs/plan/ios-audit.md).

## Native design gallery

Nathan requested native simulator evidence for the complete screen family. Debug builds expose `-DesignGallery` (browse sheets) and `-DesignScreen <slug>` (standalone capture). Screens use real SwiftUI forms/navigation/pickers and shared design components, with fictional data and honest preview-only action alerts. The gallery is compiled out of Release; it does not implement live features ahead of their roadmap milestones.

```sh
python3 scripts/check.py build
python3 scripts/capture-gallery.py --simulator <existing-Debrid-simulator-UUID>
# Optional small recapture batch:
python3 scripts/capture-gallery.py --simulator <UUID> --screens connect sign-in add
```

Check/test/capture share a checkout lock; test and capture additionally acquire a canonical UUID lock under `~/Library/Caches/dev.bhanji.debrid/locks`, spanning worktrees. Capture refuses a booted device, creates no devices, has bounded subprocesses and restores/shuts down only the shared device it booted. Capture and tests must run serially.

Gallery retention is one catalog of **at most 32 screenshots**, one per route, plus the existing two primary appearance references. This explicitly replaces the eight-image local limit for Nathan’s requested all-screen catalog; keep it below 15 MB, overwrite the same route files rather than keeping per-PR sets, and retain at most eight temporary captures. Never copy derived data or make a simulator per screen. Future state permutations are interactive studies, not an assertion that every state has been captured.
