# Runtime settings revisions · milestone 26

Native settings editing needs to reject stale full replacements when another client saves. GET `/api/v1/settings/review` returns `{settings, revision}` and the same strong quoted ETag. PUT to that dedicated route requires exactly one strong quoted If-Match and the existing full settings body. Missing/empty/weak/wildcard/list/duplicate headers are 422, never an unconditional fallback. A stale valid revision is 412 before any write or settings event. Both routes and their errors are no-store. Existing authentication and native cookie/Origin boundaries apply.

The service snapshots structured settings and their opaque per-write nonce in one SQLite transaction. A reviewed update compares its revision, saves the normalized value and rotates the nonce inside one BEGIN IMMEDIATE transaction. Publication happens only after commit. The nonce is stored at reserved `_settings_revision_v1` in the existing KV table; no schema migration is added. Reads on an uninitialized database insert nothing and return a stable default revision. Existing settings rows need no eager conversion. Legacy web/CLI/MCP replacements continue using UpdateSettings, which now rotates the nonce atomically even for identical values. Other raw keys/auth settings are unchanged; SetRaw refuses both structured settings and its revision metadata.

The public revision hashes a version prefix, nonce and canonical domain settings. Changes A→B→A, or repeated saves under the same clock, invalidate older reviews. Current settings and metadata are read together; a failed value/nonce transaction rolls back both. Corrupt nonce metadata fails review/conditional updates closed; a deliberate legacy replacement remains available to recover valid settings. Database constraints and the single writer transaction, rather than an in-process lock, serialize separate service handles.

ABA protection covers supported writers through this upgraded service. Older binaries and direct/raw database writers cannot rotate the nonce; running those writers concurrently is outside the guarantee. The domain hash still detects changed semantic values, but direct A→B→A writes bypass that history. Existing downgrade restrictions for cancelled transfers/manual selection also remain.

The Swift transport validates revision shape, emits snake-case full settings with the quoted header on the dedicated route, and requires a new valid revision after a successful update. It never falls back to legacy PUT or automatically resends after errors. A successful response carries normalized current state, not a durable acceptance receipt. After a lost response, read current settings and require user review before another save; matching values do not prove which writer applied them. Future settings schema changes must preserve the full-replacement contract or introduce a new capability/contract before old native editors can edit new semantics safely.

This milestone adds the contract and transport. Native category/default/filter/organization forms and conflict UI follow in 27. `native_settings_revision_v1` is the additive discovery capability; absence must disable native mutation rather than use unconditional replacement.

## Acceptance evidence

- Stable default review with zero inserted settings rows; normalization remains compatible.
- Fixed-clock A→B→A returns exactly original values with a different revision; stale replacement changes neither value/metadata nor events.
- A trigger failing the nonce update rolls back the earlier settings value; malformed nonce fails closed.
- Two independent SQLite service handles race the same revision: one succeeds and one receives ErrPrecondition.
- HTTP raw header cardinality covers absent, present-empty, weak, unquoted, wildcard, list and duplicate values, with unchanged state after every rejection.
- ETag/body binding, no-store errors/success, legacy PUT invalidation, validation errors, authentication and native cookie foreign/null-Origin refusal.
- Go-generated native envelope fixture and URLProtocol tests cover scoped cookies/proxy prefixes, snake-case payload parity, changed/malformed revision, 404 without fallback and a single explicit 412 request.

Independent source review approved the transaction, strict-header and transport boundaries. Full Go race suite and 225 native core tests passed; final focused service repetitions cover the explicit semantic ABA assertion. Pinned lint, generated contracts, tidy, web validation and unsigned iPhone Release checks are recorded in the delivery log. No production settings were written to test this change. Existing shared simulator/build output is reused; no new UI screenshots are required for this contract-only milestone. Physical-device UI acceptance remains later.
