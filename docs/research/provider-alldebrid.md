# AllDebrid API v4 / v4.1 — Reference

Source: https://docs.alldebrid.com/ (researched 2026-08-23). Third-party refs: go-debrid/alldebrid, stremthru/store/alldebrid.

## Basics
- **Base URL:** `https://api.alldebrid.com/v4/` — some endpoints have `v4.1` variants (`pin/get`, `user/hosts`, `magnet/status`). Old v4 versions still work but return `"deprecated": true`.
- **Auth:** `Authorization: Bearer <apikey>` header (preferred). Legacy `?apikey=` also accepted.
- **agent param:** no longer required (changelog 15/01/2025). POST (form-encoded/multipart) is now the default; GET with query params still works for most.
- **Rate limits:** 12 req/s and 600 req/min; excess → HTTP 429 or 503.
- **Envelope:** success `{"status":"success","data":{...}}`; error `{"status":"error","error":{"code","message"}}`; optional top-level `deprecated`, `demo`.
- **Demo keys:** `staticDemoApikeyPrem`, `staticDemoApikeyTria`, `staticDemoApikeyFree`.

## PIN auth flow
1. `GET /v4.1/pin/get` (no auth) → `{ pin, check, expires_in (600), user_url, base_url }`.
2. Poll `POST /v4/pin/check` with `pin`, `check` → pending `{ activated:false, expires_in }`; approved `{ activated:true, apikey }`. Errors `PIN_EXPIRED`, `PIN_INVALID`.

## Error codes
- Auth: `AUTH_MISSING_APIKEY`, `AUTH_BAD_APIKEY`, `AUTH_BLOCKED` (carries `token` for `/user/verif`), `AUTH_USER_BANNED`
- Link: `LINK_IS_MISSING`, `LINK_HOST_NOT_SUPPORTED`, `LINK_DOWN`, `LINK_PASS_PROTECTED`, `LINK_TEMPORARY_UNAVAILABLE`, `LINK_HOST_UNAVAILABLE`, `LINK_TOO_MANY_DOWNLOADS`, `LINK_HOST_FULL`, `LINK_HOST_LIMIT_REACHED`, `LINK_ERROR`, `LINK_NOT_SUPPORTED`, `MUST_BE_PREMIUM`, `FREE_TRIAL_LIMIT_REACHED`, `NO_SERVER`
- Magnet: `MAGNET_NO_URI`, `MAGNET_INVALID_URI`, `MAGNET_INVALID_FILE`, `MAGNET_INVALID_ID`, `MAGNET_MUST_BE_PREMIUM`, `MAGNET_NO_SERVER`, `MAGNET_TOO_MANY_ACTIVE` (max 30 active), `MAGNET_FILE_UPLOAD_FAILED`, `MAGNET_PROCESSING`
- Delayed: `DELAYED_INVALID_ID`

## Endpoints

### User
- `GET /v4/user` → `data.user: { username, email, isPremium, isSubscribed, isTrial, premiumUntil (unix), lang, preferedDomain, fidelityPoints, limitedHostersQuotas, notifications[], remainingTrialQuota }`
- `GET /v4.1/user/hosts` → `data.hosts: { <host>: { name, type, domains[], regexp, status, quota, quotaMax, quotaType, limitSimuDl } }`
- `GET /v4/user/links`, `POST /v4/user/links/save` (`links[]`), `POST /v4/user/links/delete`
- `GET /v4/user/history`, `POST /v4/user/history/delete`
- `POST /v4/user/verif` (`token`) → `{ verif: waiting|allowed|denied, resendable, apikey }`

### Link
- `POST /v4/link/unlock` params `link`, `password?` → `{ link (direct URL), host, hostDomain, filename, filesize, id, paws, streams[], delayed? }`
- `POST /v4/link/infos` params `link[]` → `data.infos[]: { link, filename, size, host, hostDomain } | { link, error }`
- `POST /v4/link/redirector` (`link`) → `data.links[]`
- `POST /v4/link/streaming` (`id`, `stream`)
- `POST /v4/link/delayed` (`id`) → `{ status 1=processing|2=ready|3=error, time_left, link }`

### Magnet
- `POST /v4/magnet/upload` params `magnets[]` (URIs or infohashes) → `data.magnets[]: { magnet, hash, name, size, ready, id } | { magnet, error }`
- `POST /v4/magnet/upload/file` multipart `files[]` → `data.files[]: { file, name, hash, id, size, ready } | { file, error }`
- `POST /v4.1/magnet/status` params `id?`, `status?` (`active|ready|expired|error`) → `data.magnets[]: { id, filename, size, status, statusCode, downloaded, uploaded, seeders, downloadSpeed, uploadSpeed, uploadDate, completionDate }` (no links — use /magnet/files).
  - Live mode: `session` + `counter` → deltas with `fullsync` flag.
  - **statusCode:** 0 In Queue; 1 Downloading; 2 Compressing/Moving; 3 Uploading; 4 Ready; 5 Upload fail; 6 Unpack error; 7 Not downloaded in 20 min; 8 File too big; 9 Internal error; 10 >72h; 11 Deleted on hoster; 12/13 Processing failed; 14 Tracker contact error; 15 No peer. (0–3 active, 4 ready, ≥5 error.)
- `POST /v4/magnet/files` params `id[]` → `data.magnets[]: { id, files[] }` tree: folder `{ n, e[] }`, file `{ n, s, l }` (`l` → pass to /link/unlock)
- `POST /v4/magnet/delete` (`id`), `POST /v4/magnet/restart` (`id` | `ids[]`)
- `/magnet/instant` — removed from current docs (treat as gone).

### Hosts (public)
- `GET /v4/hosts`, `GET /v4/hosts/domains`, `GET /v4/hosts/priority`, `GET /v4/ping`

## Notes for our client
- No per-file selection on AllDebrid: the whole magnet is cached; we choose which files to download client-side from `/magnet/files`.
- Each file link requires an extra `/link/unlock` call to get a direct URL (links can expire; re-unlock on retry).
