# Premiumize.me API — Reference

Sources: SwaggerHub `premiumize.me/api/1.7.2`, live docs https://www.premiumize.me/api (researched 2026-08-23, live-probed).

## Basics
- **Base URL:** `https://www.premiumize.me/api/`. GET and POST only; POST is form-urlencoded (multipart for `/transfer/create` with file).
- **Auth:** `Authorization: Bearer <API_KEY or OAuth token>` (recommended). Legacy `?apikey=` / `pin`. API key at https://www.premiumize.me/account.
- **OAuth2:** authorize `https://www.premiumize.me/authorize`, token `https://www.premiumize.me/token`, scope `full`. Device flow: `POST /token response_type=device_code&client_id=` → `{verification_uri, user_code, device_code, expires_in 600, interval 5}`; poll `POST /token grant_type=device_code&code=&client_id=`; pending → 400 `authorization_pending` / `slow_down` / `access_denied` / `invalid_grant`. **No refresh_token grant** (verified live) — re-authorize on expiry. Requires registering a client. For v1 just use API key.
- **Envelope:** always JSON `{status: "success"|"error", ...}`; errors `{status:"error", message, code}` with **HTTP 200** (500 only catastrophic).
- **Error codes:** transient `link_generation_failed, transient_error`; semi-permanent `service_down, service_limit_reached, account_limit_reached, rate_limit_reached, semi_permanent_error`; permanent `service_unsupported, not_found, authentication_failed, permission_denied, invalid_request, permanent_error`; `unknown_error`.
- **Rate limits:** undocumented numerically; `rate_limit_reached` code.

## Endpoints
### Account
- `GET /account/info` → `{status, customer_id, premium_until (unix|null), limit_used (0..1), booster_points}`
### Transfers
- `POST /transfer/create` multipart: `src` (magnet/hoster link/container link) | `file` (torrent/nzb/dlc), `folder_id?`, `password?` → `{status, id, name, type}`
- `GET /transfer/list` → `{transfers: [{id, name, message, status, progress (0.0-1.0), src, folder_id, file_id}]}`
  - status: `waiting, finished, running, deleted, banned, error, timeout, seeding, queued` (expect mostly `queued, running, finished, seeding, error`; `seeding` = done & in cloud)
  - `folder_id` set once finished (multi-file); `file_id` set for single-file results.
- `POST /transfer/clearfinished`, `POST /transfer/delete` (`id`), `POST /transfer/retry` (`id`)
- `POST /transfer/directdl` `src` → `{content: [{path, size, link, stream_link, transcode_status}], location, filename, filesize}` — use `content[]` (path/size/link)
### Folders / items
- `GET /folder/list` `id?|path?`, `includebreadcrumbs` → `{name, parent_id, folder_id, content: [{id, name, type: file|folder, created_at, size, mime_type, link}]}`
- `POST /folder/create|rename|paste|delete`, `GET /folder/search?q=`, `GET /folder/uploadinfo`
- `GET /item/listall` → `{files: [{id, name, path, size, created_at, mime_type}]}`; `GET /item/details?id=` → `{id, name, size, created_at, folder_id, mime_type, link}`; `POST /item/rename|delete`
### Cache / services / zip
- `POST /cache/check` `items[]` → parallel arrays `{response: [bool], filename: [], filesize: [], transcoded: []}` (still works, unlike RD/AD instant availability)
- `GET /services/list` (unauth) → `{cache[], directdl[], queue[], fairusefactor{}, aliases{}, regexpatterns{}}`
- `POST /zip/generate` `files[]`, `folders[]` → `{location}`

## Notes for our client
- Premiumize is cloud-folder based: after transfer finishes, list `folder_id` (recursively) to enumerate files; `link` on items is already a direct download URL (no unrestrict step). Alternative: `/transfer/directdl` with the original magnet gives a flat file list with links.
- No file selection; download whole folder and filter client-side.
