# Web identity × iOS 27 · research and implementation review

Reviewed 2026-11-03–04 following Nathan's correction: preserve Debrid's web identity and feel like an iOS 27 app. This supersedes the initial teal direction. Pocket ID SSO and the three native tabs remain.

## Evidence

- Repository `web/src/styles.css`: warm chassis/face surfaces (#d8d5cd/#e9e7e1/#f2f1ec), orange #ff4f00, dark #1d211b LCD with #c9f5b1 readout, semantic blue/amber/green/red lamps and 12-segment progress. `web/src/routes/__root.tsx` defines the orange-dot DEBRID. wordmark; `components/rack.tsx` maps provider/local phases to lamps. This is the visual source of truth, not a newly invented palette. Production browser opened read-only; two snapshot calls returned `Preview automation snapshot timed out after 15000ms`. No login or server mutation was attempted. The source was sufficient for inspecting actual styles/components.
- Apple's [WWDC26 platform/design update](https://developer.apple.com/videos/play/meet-with-apple/277/): iOS 27 refines Liquid Glass readability/depth and allows users to tune appearance. Recompile with Xcode 27 and use system components rather than freezing their rendering in custom chrome.
- [Materials HIG](https://developer.apple.com/design/human-interface-guidelines/materials): Liquid Glass is the floating functional layer; keep ordinary content surfaces distinct. No glass on every transfer card.
- [Tab bars](https://developer.apple.com/design/human-interface-guidelines/tab-bars), [toolbars](https://developer.apple.com/design/human-interface-guidelines/toolbars): tabs navigate, toolbar buttons act; use native SF Symbols and stable placement.
- [Typography](https://developer.apple.com/design/human-interface-guidelines/typography), [color](https://developer.apple.com/design/human-interface-guidelines/color): semantic SF styles, readable text, adaptive colors with appearance and increased contrast variants. The desktop's tiny mono labels don't become tiny iPhone body copy.

## Applied Emil skills

Installed from [emilkowalski/skills](https://github.com/emilkowalski/skills), read directly and applied to both board and app:

- [emil-design-eng](https://github.com/emilkowalski/skills/tree/main/skills/emil-design-eng): cohesion, response on press, motion purpose/frequency, no decorative animation on repeated actions, reduced motion and a concrete Before/After review.
- [apple-design](https://github.com/emilkowalski/skills/tree/main/skills/apple-design): familiar, interruptible, direct manipulation; system typography, materials, restraint and adaptive text. This skill translates Apple principles for the web; the shipped app uses SwiftUI equivalents, not its CSS or custom gesture physics.
- [write-swift](https://github.com/emilkowalski/skills/tree/main/skills/write-swift): concrete value views/models, simple synchronous presentation, Swift 6 checking, main actor default only for app code and approachable concurrency. No new task/actor for styling work; domain framework remains independent.

Local skill files live in `~/.codex/skills/{emil-design-eng,apple-design,write-swift}/SKILL.md`; they are installed for later turns too. No third-party runtime/library was added to the app.

## Review (Emil format)

| Before | After | Why |
|---|---|---|
| Unrelated teal identity | Orange dot/DEBRID., warm matte surfaces, LCD summary and phase lamps | Preserve the product Nathan already uses |
| Large generic promotional sentence | Compact factual transfer readout | Put queue status and tasks first |
| One teal color for every stage | Blue NAS download, amber selection/provider work, green completion, text/symbol equivalents | Carry existing phase semantics without depending on color |
| Generic continuous progress stroke | Twelve segments, one accessibility value | Keep a recognizable web motif while exposing useful native semantics |
| Risk of copying desktop enclosure | No screws, faux physical keys, outer device frame, scanline animation or desktop fonts in app UI | iPhone content should feel at home on the platform |
| Native controls with unrelated content styling | SwiftUI TabView/NavigationStack/toolbar/search/sheets/forms/pickers around Debrid content | System supplies iOS 27 materials, motion, safe areas and interaction |
| No touch feedback in board controls | 140ms transform-only press feedback, focus ring, 44px targets; static feedback with Reduce Motion | Apply Emil's interaction discipline to the review artifact |
| Color defaults only | Light/dark/increased-contrast content colors; semantic SF text and intrinsic height | Adapt to the person and system settings |

## Motion decisions

No new custom animation is justified for this fixture shell. Tab navigation, back swipe, scrolling, toolbar press and sheet presentation/dismissal remain system-owned, including interruption and Reduce Motion/Transparency preferences. No pulsing brand LED, blinking progress, staggered queue reveal, artificial delay or CSS physics ported to SwiftUI. The board's theme/size controls don't add entrance animation; press feedback is immediate and cancellable.

The HTML board is an approximation for review, never proof of native material or gesture behavior. Simulator capture and interaction checks establish the current implementation; signed-device and VoiceOver checks remain explicit gates. The foundation still has no live requests or submission path.
