# Debrid · iPhone design proposal

**Direction: a quiet transfer desk.** Native on your phone, downloads on your server. Three tabs: Transfers, Providers, Settings. Add lives in the Transfers toolbar and opens a native review sheet. No dashboard charts, cache-hit claims or device-download metaphor. Nathan approved the name, palette and tab structure on 2026-11-03; detailed flow review continues in this thread. Pocket ID SSO is the primary login.

Open [board.html](board.html) for screen compositions. It is a review artifact, not shipped UI. The native app intentionally implements only the first fixture shell, search, navigation and review layout. Full board flows arrive through the roadmap.

## Tokens and native behavior

| Token | Light | Dark | Use |
|---|---|---|---|
| Canvas | warm grouped #F4F3EF | graphite #121716 | grouped screen background |
| Surface | #FFFFFF | #202725 | native grouped rows/sheets |
| Primary ink | #182C29 | #F1F5F2 | titles/content |
| Secondary ink | #566661 | #B3C2BC | explanatory text |
| Accent | #087B70 | #70D9C5 | primary actions and progress |
| Attention | #875300 | #F3BF70 | action needed, always with icon/text |
| Destructive | system red | system red | explicit destructive buttons |

Use SF native semantic text styles (largeTitle/title2/headline/body/caption), rounded numerals only for progress, monospaced digits for byte counts. System colors in shipped UI adapt to accessibility; the board illustrates palette intent. Native glass belongs to system navigation/toolbars, not every row. Group sections with 16–24pt rhythm, generous headings, subtle separators and no decorative graphs. Minimum 44pt interactive targets. Use semantic labels, not color-only state.

Progress row: title → explicit stage → stage progress when measurable → bytes/provider. Do not combine provider and server progress into a misleading single percentage. Details show destination/category, provider stage, download stage, files and contextual actions. Safe area insets and intrinsic heights; no fixed card height. Largest accessibility sizes stack labels/values vertically; long release names wrap. Reduce Motion removes decorative transitions. VoiceOver reads title, stage, progress and provider; exposes actions with clear consequences.

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

Nathan approved the tab structure and palette; review terminology and add/share flow here before wider UI implementation. Use Pocket ID SSO for primary onboarding; defer advanced API-key fallback. Confirm physical iPhone OS before locking iOS 27 minimum. Test light/dark, largest Dynamic Type, VoiceOver, Reduce Motion, landscape, long names, empty/failed states and an interrupted add. Board typography is illustrative; only simulator/device screenshots establish native HIG behavior. Platform routing limitations and evidence: [audit](../../plan/ios-audit.md).
