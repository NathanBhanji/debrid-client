# Native delivery evidence

## Foundations and Pocket ID · 2026-11-04

Roadmap 01–06 were independently reviewed and merged as [36](https://github.com/NathanBhanji/debrid-client/pull/36), [37](https://github.com/NathanBhanji/debrid-client/pull/37), [38](https://github.com/NathanBhanji/debrid-client/pull/38), [39](https://github.com/NathanBhanji/debrid-client/pull/39), [40](https://github.com/NathanBhanji/debrid-client/pull/40), [41](https://github.com/NathanBhanji/debrid-client/pull/41). These deliver native design/gallery, isolated transport, Keychain/profile storage, HTTPS discovery, backend native exchange and system-browser/session orchestration. They do not represent a complete daily-use app yet.

Latest auth validation: 51 core Swift Testing functions, five XCTest UI flows and unsigned arm64 app/framework compilation/linking passed. Independent source/security review resolved configuration races, stale input/cancellation and failed secure-removal recovery. Actual native Connect form reviewed in light/dark with fixture-only discovery. Settled light body/primary contrast measured5.39:1/7.56:1; dark8.98:1/7.35:1. A two-second launch frame had appearance fade; ten/twelve-second settled frames were used for final judgement. Physical iPhone/passkey success is still unverified.

## Backend deployment · 2026-11-04

[Release v0.2.2](https://github.com/NathanBhanji/debrid-client/releases/tag/v0.2.2), commit `c3f386a922215856cdc8549e18c6534115ea2e85`, published by the existing tagged release workflow. NAS service `debrid` now pins:

```
ghcr.io/nathanbhanji/debrid-client:0.2.2@sha256:4f197ee219050935773a21bebcccd214daeea7093ceee47338846b341295f7f8
```

Applied only with `nas-agent debrid-client compose-put` then `up debrid`. Parsed compose documents were compared before applying: the image reference was the only field changed. Existing data/download mounts, ports, environment and access boundary were identical. No source checkout, new privileges/mounts, account changes or example transfers. There are no database migrations between v0.2.1 and this release.

Status confirmed the pinned image running; existing tailnet HTTPS health returned HTTP200, ok=true, version0.2.2; public authentication status reported configured OIDC and native_sso=true. Bounded post-recreate logs showed no error/panic lines. Raw compose/log contents were withheld. NAS source-deploy tracking still says unknown because this is an image-based apply; report release commit plus running digest/health instead. The later logout failure-reporting hardening in PR41 is on main and will be included in the next backend release bundle.

## Apple release access

Heimdall verified/created distinct app and share identifiers, enabled APP_GROUPS and staged dedicated distribution profiles, with matching team certificates. Required `group.dev.bhanji.debrid` is not assigned; profiles are not installed or release-ready. Authenticated Apple website access must register/assign that group to both identifiers and create the new Debrid ASC app record. Regenerate and inspect only Debrid profiles afterward. Physical development testing also needs device registration. Existing credentials/profiles for other apps are not substitutes. This blocks final distribution/device acceptance, not ongoing simulator/app development.
