# Recovery · native SwiftUI captures

Nine actual app screenshots from the existing shared iPhone simulator on iOS27.0. Every capture comes from the passing combined run Test-Debrid-2026.11.04_20-49-16-+0000.xcresult:243 core tests and four native UI flows. captures.json records each case and source. Fixtures are anonymous in-memory responses and never contact production.

- connection-help-light / connection-reachable / connection-report: explicit check and reviewed, redacted sharing.
- connection-offline / connection-dns / connection-tls: distinct actionable failure guidance without Tailscale detection claims.
- connection-server-largest / connection-report-largest: server-error guidance and the share action reached at Accessibility XXXL.
- transfers-stale-offline: retained transfer progress with last-confirmed date/time.

See [behavior, privacy boundaries and Emil review](../../../plan/ios-recovery-diagnostics.md). Native system controls own motion, feedback and navigation. These images are SwiftUI simulator evidence, not HTML recreations or signed-device/full VoiceOver acceptance.
