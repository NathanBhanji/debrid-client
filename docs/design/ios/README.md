# Debrid · iPhone design proposal

**Direction: Debrid’s web identity in a native iOS 27 app.** Warm matte content, orange-dot DEBRID. wordmark, a dark status readout, semantic phase lamps and segmented progress. System TabView, NavigationStack, toolbar buttons, search, sheets and forms supply the navigation layer and gestures. No desktop device frame, physical-key imitation or blanket glass on content. Nathan requested this correction after the unrelated initial teal design; the revised detailed screens are ready for review. Three tabs remain Transfers, Providers, Settings. Pocket ID SSO remains the primary login.

Research and the applied Emil skill review: [research.md](research.md). This is the design contract for both the board and the real SwiftUI code, not just a mood board.

Open [board.html](board.html) for screen compositions, and [native-review.html](native-review.html) for actual simulator captures. It is a review artifact, not shipped UI. The native app intentionally implements only the first fixture shell, search, navigation and review layout. Full board flows arrive through the roadmap.

## Tokens and native behavior

| Token | Light | Dark | Use |
|---|---|---|---|
| Canvas | #F3F1EB | #181B17 | softer derivative of web chassis |
| Surface | #FAF9F5 | #282C25 | matte native list content |
| Primary ink | #23241F | #F2F1EC | titles/content |
| Secondary ink | #626359 | #B8BCAF | readable explanatory text |
| Brand orange | #FF4F00 | #FF4F00 | wordmark dot; graphical identity |
| Action tint | #B63700 | #FF9A6A | legible orange-family native controls |
| Readout | #1D211B / #C9F5B1 | same | status content, never navigation |
| Stage | blue / amber / green / red | adaptive inks | NAS download / choose/provider / complete / error |

Use semantic SF text styles. SF monospaced labels and monospaced digits echo the web instrumentation only where useful; do not copy its desktop fonts or tiny type across the app. All custom colors supply light/dark/increased-contrast variants. Label every phase in words and symbols; color and decorative lamps are supplementary.

Retain system Liquid Glass for functional controls/navigation, including the floating native tab bar, scroll edges, toolbar controls and sheet transitions. Do not hand-draw a replacement or force a fixed glass appearance over the user's settings. Content stays matte; no glass nesting, screws, pulsing lights, artificial beveled controls or slow decorative entrances. Emil's motion decision framework calls for no added custom animation here: native scrolling/back gestures/sheet dragging remain interruptible and system-owned. The HTML board uses press feedback with reduced-motion/transparency/contrast adaptations; it only approximates native chrome.

Minimum 44pt interactive targets; intrinsic row heights, wrapped names, native picker/forms, and no fixed text scaling. At accessibility sizes hide the supplementary lamp to give text room. Segments expose a single progress label/value to assistive technology. Byte counts use tabular digits to avoid jitter. VoiceOver combines meaningful row content and skips decorative lamps/wordmark dot. Do not conflate provider and NAS progress. App Group/signing and full VoiceOver/material testing on iPhone remain device gates.

## Screens and states

| Screen | Primary content/action | Required variants |
|---|---|---|
| Connect | HTTPS server root; Check server; auth method after discovery | invalid URL, checking/cancel, cert/DNS/offline, unconfigured server, unsupported version |
| Sign in | Pocket ID browser SSO; native exchange capability required | rate limited, cancelled SSO, expired session, server update required; no token in diagnostics |
| Transfers | stage rows, search and filter; Add | first transfer, loading, cached/stale with timestamp, empty filtered, error/retry, live |
| Detail/files | provider versus NAS stages, files, category/destination | fetching metadata, waiting selection, unknown size, partial file errors, unpacking, completed |
| Add review | source, provider default/choice, category, file policy, Add | invalid input, unknown name, duplicate/open existing, submitting, uncertain result, safe cancellation |
| Share | compact native review; Add when configured/online, Save draft otherwise | plain text/URL/torrent, unsupported webpage, interrupted, locked inbox, saved but not submitted |
| Providers | account list/default and explicit Check connection | untested, checking, healthy with check time, expired/quota/failed/disabled; all five provider kinds |
| Settings | server/sign-out, categories, download policy, organization, about | full-document conflict review, invalid settings, old server capabilities |
| Remove | explicit local record/provider/files choices and named consequence | default keeps provider/files; in-flight cancellation is a distinct action only when API supports it |
| Connectivity | last updated + precise failure + Retry | offline, DNS/Tailscale suggestion, server unreachable, unauthorized, TLS, 429/5xx; never infer VPN status from DNS alone |

## Magnet journey

Tap magnet / Share / explicit Paste / Files → bounded validation preserving raw payload → durable pending item → connect if needed → review destination/provider and manual file policy → explicit Add → confirmed server result → detail. A missing display name says “Magnet transfer”; do not pretend metadata exists. File names may only arrive after submission. Double taps and repeated delivery converge on one pending identity. After timeout: “Checking whether this was added”, then reconcile by info hash; no blind resend. An extension can save a draft without opening the app; saved is never labeled added.

Offline: cached transfers are read-only with age shown; draft intake remains available; mutations require deliberate recovery. Reconnection does not auto-submit drafts. No clipboard read at launch: only explicit native paste control. No push/widgets in v1.

## Review and acceptance

The initial teal direction is superseded. Review the revised web-family styling, native material/gesture behavior, terminology and add/share flow here before wider UI implementation. Use Pocket ID SSO for primary onboarding; defer advanced API-key fallback. Confirm physical iPhone OS before locking iOS 27 minimum. Test light/dark, largest Dynamic Type, VoiceOver, Reduce Motion, landscape, long names, empty/failed states and an interrupted add. Board typography is illustrative; only simulator/device screenshots establish native HIG behavior. Platform routing limitations and evidence: [audit](../../plan/ios-audit.md).
