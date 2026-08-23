# Real-Debrid REST API — Reference

Source: https://api.real-debrid.com/ (researched 2026-08-23).

## Basics
- **Base URL:** `https://api.real-debrid.com/rest/1.0/`
- **Auth:** `Authorization: Bearer <token>` or `?auth_token=`. Token = private API token (control panel, non-expiring) or OAuth2 access token (`expires_in` ~3600s observed; always read from response).
- **Rate limit:** 250 req/min → HTTP 429.
- **Dates:** JS `toJSON()` ISO 8601. Responses carry `ETag`; `If-None-Match` supported.
- **Errors:** HTTP 4xx/5xx with `{"error": "<msg>", "error_code": <int>}`.

### Error codes
-1 Internal · 1 Missing parameter · 2 Bad parameter value · 3 Unknown method · 4 Method not allowed · 5 Slow down · 6 Resource unreachable · 7 Resource not found · 8 Bad token · 9 Permission denied · 10 2FA needed · 11 2FA pending · 12 Invalid login · 13 Invalid password · 14 Account locked · 15 Account not activated · 16 Unsupported hoster · 17 Hoster in maintenance · 18 Hoster limit reached · 19 Hoster temporarily unavailable · 20 Hoster not available for free users · 21 Too many active downloads · 22 IP not allowed · 23 Traffic exhausted · 24 File unavailable · 25 Service unavailable · 26 Upload too big · 27 Upload error · 28 File not allowed · 29 Torrent too big · 30 Torrent file invalid · 31 Action already done · 32 Image resolution error · 33 Torrent already active · 34 Too many requests · 35 Infringing file · 36 Fair Usage Limit · 37 Disabled endpoint

## OAuth2 device flow (base `https://api.real-debrid.com/oauth/v2/`)
Open-source client id `X245A4XAIBGVM`, scopes `unrestrict, torrents, downloads, user`.
1. `GET /device/code?client_id=X245A4XAIBGVM&new_credentials=yes` → `{device_code, user_code, interval, expires_in, verification_url, direct_verification_url}`
2. Poll `GET /device/credentials?client_id=X245A4XAIBGVM&code=<device_code>` every `interval` s → 400/403 until authorised, then `{client_id, client_secret}` (persist both).
3. `POST /token` form `client_id, client_secret, code=<device_code>, grant_type=http://oauth.net/grant_type/device/1.0` → `{access_token, expires_in, token_type, refresh_token}`
4. Refresh: same POST with `code=<refresh_token>`.

## Endpoints
Pagination: `offset|page`, `limit` (0–5000, default 100); header `X-Total-Count`.
- `GET /user` → `{id, username, email, points, locale, avatar, type:"premium"|"free", premium (secs left), expiration}`
- `GET /torrents` params `offset|page, limit, filter=active` → `[{id, filename, hash, bytes, host, split, progress 0-100, status, added, links[], ended?, speed?, seeders?}]`
- `GET /torrents/info/{id}` → `{id, filename, original_filename, hash, bytes, original_bytes, host, split, progress, status, added, files:[{id, path ("/..."), bytes, selected 0|1}], links[], ended?, speed?, seeders?}`
  - **status enum:** `magnet_error, magnet_conversion, waiting_files_selection, queued, downloading, downloaded, error, virus, compressing, uploading, dead`
- `GET /torrents/activeCount` → `{nb, limit}`
- `GET /torrents/availableHosts` → `[{host, max_file_size}]`
- `PUT /torrents/addTorrent?host=` — body raw .torrent bytes → 201 `{id, uri}`
- `POST /torrents/addMagnet` form `magnet`, `host?` → 201 `{id, uri}`
- `POST /torrents/selectFiles/{id}` form `files="all"|"1,2,3"` → 204 (202 already done; 404 bad id)
- `DELETE /torrents/delete/{id}` → 204
- ~~`GET /torrents/instantAvailability/{hash}`~~ — removed Nov 2024 (error_code 37)
- `POST /unrestrict/link` form `link, password?, remote?` → `{id, filename, mimeType, filesize, link, host, chunks, crc, download, streamable}`
- `POST /unrestrict/check` (no auth) form `link` → same shape; 503 if unavailable
- `POST /unrestrict/folder` form `link` → `[...]`
- `GET /downloads` → `[{id, filename, mimeType, filesize, link, host, chunks, crc?, download, generated, type?}]`
- `DELETE /downloads/delete/{id}` → 204
- `GET /hosts/status`, `GET /time`, `GET /time/iso`, `GET /disable_access_token`

## Notes for our client
- RD requires explicit `selectFiles` after `addMagnet` (status `waiting_files_selection`); `links[]` only appear at `downloaded` and map 1:1 (in order) to selected files — but if RD packs files into a rar (`split`), count may differ.
- `links[]` are `real-debrid.com/d/...` links; call `/unrestrict/link` on each to get the direct `download` URL. `chunks` tells max parallel connections allowed per download.
