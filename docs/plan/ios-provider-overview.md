# Native provider overview · milestone 24

The connected app now uses the approved Transfers, Providers and Settings tabs. Provider browsing reads saved account metadata without contacting providers. A deliberate **Check connection** action calls the existing POST account-test route through the current authenticated server. It never enables an account or starts a transfer. This milestone needs no backend change or migration; account mutations follow in 25.

The list puts the default account first, preserves disabled accounts, supports native search and pull-to-refresh, and distinguishes loading, an empty server, no search results and an offline response. Detail shows enabled/default status, a dated successful credential check, provider identity, membership, plan and expiry where supplied. All five provider names use the existing kind mapping. Quota is explicitly not reported by this API; missing expiry does not mean unlimited membership. A successful credential check is not proof a provider can download any particular transfer.

A check reads the exact account before and after the provider operation, refusing to publish health when its configuration revision changes during the request. Refresh invalidates successful results for edited or removed accounts. A failed check retains the previous dated success alongside the error. Lists and health remain in memory within this server connection; no credential copies or provider identity are written to defaults, the inbox or logs. There is one actual in-flight check at a time, even across stop/resume; refresh cannot replace that ownership. Queued actions cannot run while the model is inactive. There is no automatic replay, and generation/current-connection/cancellation fences discard responses from stopped views, background transitions and old connections. This is a best-effort check boundary: the existing backend does not offer transactional provider-test revisions.

Settings exposes the connected HTTPS address, Pocket ID identity, app version and confirmed sign-out using the existing session coordinator. Transfer navigation requests select the Transfers tab, so links received while viewing Providers or Settings can still lead to their transfer. The existing live transfer stream stops when the tab disappears and restarts on return. General server policy settings remain milestone 27.

## Emil review

| Before | After | Why |
| --- | --- | --- |
| Connected app opens only Transfers | Three stock native tabs with independent navigation | Frequent destinations stay one tap away; system navigation keeps its native materials and behavior |
| Saved account could be mistaken for working credentials | “Not checked this session” and deliberate Check connection | Metadata is not evidence of current provider availability |
| Health could silently outlive a credential edit | Read configuration around the check and invalidate dated results after edits | Avoid attributing old credentials' success to a changed account |
| A failed check could discard useful context | Error alongside the previous dated successful result | Preserve useful evidence without presenting it as current |
| Fixed status labels compete with long names and large type | Stacked badges and no decorative fixed-width icon at accessibility sizes | Preserve hierarchy and wrapping at accessibility sizes |
| Ordinary form actions inherit muted/body ink | Explicit action tint and destructive sign-out ink | Make controls recognizable within native lists |
| Fixture accounts could appear real in captures | Visible sample disclosures on list/detail/settings | Keep review evidence honest |
| Unavailable quotas could look like zero or unlimited | “Not reported” | Reflect the API's actual information |

## Validation

Core tests exercise read-only browsing, explicit checks, free/unknown membership fields, partial failure with retained success, concurrent account edits, removed accounts, offline/malformed list retention, double taps, stopped/late 401 responses, cancellation and connection changes. URLProtocol transport tests assert GET versus POST, reverse-proxy prefixes, scoped session cookies, exact response-ID binding and rejection of path syntax.

The Debug fixture is an in-memory provider API at an example.test address and uses the production list/detail/settings views; it never connects to a real service. UI tests cover the three-tab shell, unchecked/checked detail, search, light/dark, largest Dynamic Type failure, empty and offline states. Screenshots are exported from those tests, not recreated in HTML. Physical-device VoiceOver, real Pocket ID sessions and background network behavior remain final acceptance gates. One existing shared simulator and the existing build directory are reused.

Simulator review exposed a cramped horizontal status layout at the largest accessibility size; badges now stack and decorative fixed-width icons are omitted there. Native labeled values have explicit accessibility names/values used by UI checks. Search stays visible in the native navigation drawer. The Settings version also exposed XcodeGen’s default Info.plist version; app and extension now explicitly take their versions from the project’s MARKETING_VERSION/CURRENT_PROJECT_VERSION settings.

Final local validation: initial full core run passed 213 tests; after review fixes, all 32 focused provider/transport tests passed (including the new stopped-action regression). All four ProviderFlowTests passed across focused reruns: normal explicit check/native tab navigation, light/search, largest-text failed check, and final empty/offline polish. The unsigned iPhone Release app/framework/extension build passed. Nine native captures received independent source/visual review. No production provider/account/transfer calls were used. These checks do not certify signed-device behavior.
