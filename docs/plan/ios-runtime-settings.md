# Native runtime settings · milestone27

Connected Settings now provides download policy, categories, organization templates/preview, archive depth and connection details using native SwiftUI navigation, forms and review sheets. The existing warm web identity and system iOS materials continue; these screens use actual app views, not HTML reproductions. [Simulator captures](../design/ios/settings/README.md).

## Save and conflict contract

The app requires native_settings_revision_v1. It reads a complete settings baseline, retains untouched values/manual file IDs and sends a full replacement only after explicit review and Save with the original revision. Category identity and conflict comparisons preserve exact UTF-8; Unicode-equivalent names are not silently collapsed.

A412 fetches current state and proposes a section-level three-way merge across download defaults, categories, organization and archive depth. Conflicting sections show both complete values and require an explicit choice. Combining a draft never saves it: another review and Save are required. Lost responses enter an uncertain state and permit observation only. Matching observed values do not establish request acceptance. Failed refresh clears stale comparison data; background/dismissal/server changes invalidate late requests and previews. Unauthorized responses reach the session coordinator independently of the mutation task owner.

Organization preview is read-only and bounded. The backend now distinguishes an omitted template (use saved template) from an explicit empty template (preview built-in default), without changing saved settings or schema. Current downloads retain their existing paths; global defaults affect future transfers.

The add flow offers a separate native options sheet before submission. Returning to review retains the pending claim, Keep Copies, file-review policy and edited options. It neither submits nor prepares an accepted attempt. Existing stored transfers are not edited by this form.

## Design review using Emil’s skill

| Before | After | Why |
| --- | --- | --- |
| Settings offered connection information only | Grouped native settings and focused forms | Make server policy manageable without an overloaded screen |
| Pushing transfer options released the saved-item claim | Nested native sheet retains review ownership | Returning must preserve the user’s work and never submit |
| Disabled conflict action retained strong accent emphasis | Disabled action uses muted system styling | Visual affordance matches availability |
| Largest-text review could hide lifecycle consequences | Scrollable review exposes cleanup warnings and explicit actions | Destructive defaults need readable consequences before Save |
| Preserved manual IDs were implicit | Expandable exact-ID summary appears when present | Conflict decisions expose all retained policy values |

Independent source and final screenshot review approved the implementation, including the final preserved-ID disclosure. Screenshots were recaptured after navigation settled. Standard controls own motion, focus, pressed and disabled feedback; no custom spring or decorative glass layer was added.

## Validation and limits

236 core tests passed in the final full run. Eight distinct native UI flows passed across focused runs: category review/save, light policy/organization preview, explicit conflict choice/re-review, uncertain-result observation, largest-text review controls, unsupported/offline server, lifecycle deletion warnings and per-transfer options remaining local. Captures record their originating tests; a passing individual case from a mixed failed run is identified rather than labeling that whole run successful.

Full Go race, focused preview regression, pinned lint, generated freshness, tidy, web formatting/lint/types/build and final unsigned arm64 iPhone Release compile/link passed. Preview tests verify that reading defaults/custom/empty templates does not rotate the settings revision. Runtime tests cover held requests, cancellation, duplicate save, stale refresh, validation and exact-byte categories. Transport tests preserve explicit empty templates and server-prefix credentials.

One existing shared simulator, checkout and build directory were reused. No production settings, provider or transfer writes were used. Physical-device signing, complete VoiceOver and host-app share/link acceptance remain milestone30 gates. Simulator success and unsigned Release linking do not establish those results. Production remains v0.2.7 until the reviewed next release.
