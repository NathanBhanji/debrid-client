# Signed Debrid delivery · milestone30

Milestones01–29 are merged. Release tooling30 is ready for review; a signed local preview archive and exported IPA have passed verification. No build has been uploaded at this checkpoint. This document distinguishes packaging checks from an actual phone acceptance run.

## Identity and signing

The home screen name is **Debrid**. The distinct App Store Connect record is **Debrid Bhanji**, ID6830894823; main bundle `dev.bhanji.debrid`, embedded extension `dev.bhanji.debrid.share`, shared framework `dev.bhanji.debrid.kit`, teamZTN995BU9H. Both executable targets have exactly `group.dev.bhanji.debrid`. Only the main app has its explicit private Keychain group. The extension has no credential access or network session.

Use the dedicated **Debrid AppStore AppGroup** and **Debrid Share AppStore AppGroup** profiles from the private Heimdall handoff. The pipeline verifies their team, exact app/group entitlement, expiration, distribution status and certificate against the local signing identity before installing the two UUID-named profiles. Old staged profiles without the group remain unsuitable. No other project's identities, records or provisioning profiles are reused.

`project.yml` owns signing settings: Debug device automatic signing; Debug simulator ad-hoc signing; Release device manual distribution signing. The actual archive/export checks cover all three arm64 device Mach-O components, minimum iOS27.0, embedded framework placement, strict signatures, exact entitlements, provisioning certificates, matching versions and source revision. This proves device-platform linking and distribution packaging. It does not prove installation, file protection while locked, or runtime Keychain access on Nathan's phone.

## Reproducible commands

Run from the repository root. Shared API credentials stay in the existing private factory configuration; the code reads only the shared ASC key/issuer identifiers and standard private-key file. It never uses another app's ASC_APP_ID.

```sh
python3 -m unittest discover -s ios/scripts/tests -v
python3 ios/scripts/check-release-metadata.py
python3 ios/scripts/check-contrast.py
python3 ios/scripts/testflight.py --build-number 1 --preflight
# Archive/export only; explicit upload is absent:
python3 ios/scripts/testflight.py --build-number 1
# Following review/merge, check out the exact clean origin/main commit:
python3 ios/scripts/testflight.py --build-number 1 --upload
python3 ios/scripts/distribute-testflight.py --build-number 1 --assign --wait-seconds 900
```

Build numbers are examples: inspect ASC first and choose a new number if1 already exists. A lost upload response is uncertain: inspect ASC processing/build history before attempting another upload. Do not repeatedly submit the same build. `--allow-dirty` permits a local preview only and is incompatible with upload. The actual upload requires clean source at the locally fetched origin/main, validates the package with Apple, then uploads. Never mistake preview artifacts for a reviewed upload.

Distribution checks processing for at most the requested1800 seconds (default900), twenty seconds between reads. Without `--assign` it only observes. Assignment requires a successful clean-upload manifest matching the app/version/build and a private handoff identifying Nathan's exact app-scoped tester. It verifies the exact internal group, no automatic distribution/public link, exactly that member and no other build groups before any metadata/assignment/notification write, then checks again before subsequent writes and reads the result back. Changed membership or unexpected build groups stop distribution. Regression tests exercise both failures while a build changes from processing to valid, requiring zero writes. API writes cannot be made atomic across Apple's separate endpoints.

The dedicated **Debrid Internal** group has automatic distribution disabled. Processing success alone does not assign the build. Assignment and notification request do not establish that an email was received. Report the actual app/version/build and confirmed group availability after readback; Nathan can open Apple's TestFlight app using his verified Apple account.

## Privacy and icon

The original opaque1024px icon carries the web identity: a graphite D and orange dot on warm paper, without baked system corner masking. The vector source is `ios/scripts/render-icon.swift`; it renders with CoreGraphics/ImageIO and uses no external media or packages. Native navigation, materials and motion remain owned by SwiftUI. The independent reviewer approved the actual icon.

| Before | After | Why |
|---|---|---|
| No distribution icon | Simple original D/orange dot using existing colors | Recognizable at phone icon sizes without copying desktop chrome |
| Simulator-only packaging assumptions | Verified signed archive and exported app/extension/framework | Separate distribution evidence from simulator success |
| Provisioning readiness mistaken for TestFlight availability | Explicit upload, processing, audience and assignment checkpoints | Report what Nathan can actually install |
| No machine-checked privacy inventory | Shared audited privacy manifest plus Linux metadata check | Keep declarations aligned with concrete local API use |

The app has no analytics/tracking SDK, advertising identifier or third-party telemetry endpoint. It sends the server address, login exchange, transfer/account/settings requests and user-selected magnet/torrent content to the self-hosted server chosen by the user; that server and its configured providers process the requested operations. Protected drafts remain on the device/App Group until explicit submission/discard. Credentials remain in the main app's non-synchronizing Keychain. Sharing diagnostics is explicit and uses the redacted allowlist; the extension only saves local drafts. TestFlight's Apple-managed feedback/crash collection is separate from an app analytics integration. See [privacy details](../privacy/ios.md).

Required-reason declarations apply to app, extension and shared framework: UserDefaults CA92.1 for app-owned non-secret profile metadata; FileTimestamp C617.1 for own app/group files and 3B52.1 for user-selected imported documents. No undeclared disk-space, uptime or keyboard API use was found in the source audit. The manifest does not substitute for accurate ASC privacy answers or future review when collection behavior changes.

## Resource and acceptance policy

One owned `.artifacts/distribution` directory holds one archive/export pair, an ownership marker, validation/assignment JSON and bounded logs. Archive/export/upload subprocesses have process-group deadlines; only recognized owned artifacts are replaced. The workflow shares the checkout lock with tests/capture, creates no simulator or worktree and refuses a symlinked artifact root. Keep only the latest delivery artifacts; retain small safe provenance JSON separately before replacing them. Never commit archives, IPA, profiles, credentials, API tokens or tester email.

Before calling physical-device acceptance complete, verify on Nathan's iOS27+ iPhone with Tailscale:

- Install the assigned TestFlight build; cold/warm launch, server discovery, Pocket ID browser/passkey sign-in, restore and explicit sign-out.
- Confirm app-private Keychain and app/extension inbox across termination, lock/unlock and restart. Check the Share Extension in actual Safari/Notes hosts, URL/text and torrent intake, explicit save/cancel, then review in the app.
- Test magnet cold/warm delivery and competing-handler behavior; iOS cannot promise universal default magnet routing. Share/paste remain reliable alternatives.
- Exercise offline/tailnet-off recovery without automatic submission, duplicate protection and explicit cancellation. Use fixture/demo content for screenshots and avoid mutating existing production transfers/accounts.
- Inspect largest Dynamic Type, VoiceOver traversal/actions, light/dark, Reduce Motion and landscape on the physical device. Simulator evidence29 remains documented with its specific audit limits.

A signed build or TestFlight invitation does not complete these phone checks. Record actual results and unresolved limitations rather than marking all acceptance items passed by inference.
