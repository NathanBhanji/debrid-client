# Native cancellation and removal

These are actual SwiftUI captures from the shared iOS27.0 iPhone simulator, using the same ManagementSheet as live management with an isolated fictional API/inbox. No production transfer or account was used. This is an image viewer of shipped UI code, not an HTML recreation.

| Screen | Capture |
| --- | --- |
| Cancellation review | [Dark](cancel-dark.png) |
| Removal and optional cleanup | [Light](remove-light.png), [dark](remove-dark.png) |
| Unknown removal result | [Check/retry original action](removal-unknown.png) |
| Absence observed, cleanup unconfirmed | [Result](removal-absence.png) |
| Largest Dynamic Type | [Options](cleanup-options-largest.png), [primary](remove-primary-largest.png), [confirmation](cleanup-dialog-largest.png) |

Default cleanup choices preserve files and provider copies. Requesting cleanup requires a final readable selected-effect confirmation with an explicit Keep reviewing action (including in a native popover). Results/recovery move ahead of original review context after submission. Native glass navigation/controls, matte content and dynamic system typography retain the web orange identity without custom motion. Source and final captures received independent review; device signing/protection and full VoiceOver remain separate acceptance work. Capture/test provenance is in captures.json.
