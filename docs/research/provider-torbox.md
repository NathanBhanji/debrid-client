# TorBox API — Reference

Sources: Postman collection behind https://api-docs.torbox.app/, live OpenAPI at https://api.torbox.app/openapi.json, official torbox-sdk-py (researched 2026-08-23).

## Basics
- **Base URL:** `https://api.torbox.app/v1/api/`
- **Auth:** `Authorization: Bearer <API_KEY>`. Exception: `*/requestdl` takes `?token=` query param. Some endpoints unauthenticated (`torrentinfo`, `magnettofile`, `stats`).
- **Device flow:** `GET /user/auth/device/start?app=<Name>` → `{device_code, interval, expires_at, verification_url, friendly_verification_url, code}`; poll `POST /user/auth/device/token {device_code}` → `{access_token, token_type}`; `DEVICE_CODE_NOT_USED` keep polling, `ITEM_NOT_FOUND` expired. Valid 10 min.
- **HTTP semantics:** 200 ok; 400 client; 403 auth; 500 server. `success` bool authoritative. Quirk: empty `mylist` → HTTP 404 with `{success:true, error:"ITEM_NOT_FOUND", data:null}`.
- **Dates:** UTC `%Y-%m-%dT%H:%M:%SZ`. Request cap 100 MB.
- **Rate limits (per token):** 300 req/min all endpoints; `createtorrent` additionally 60 *uncached* torrents/hour; `createusenetdownload`, `createwebdownload` 60/hour.
- **Envelope:** `{ success: bool, error: string|null, detail: string, data: any }` (`detail` user-facing).
- **Error codes:** `DATABASE_ERROR, UNKNOWN_ERROR, NO_AUTH, BAD_TOKEN, AUTH_ERROR, INVALID_OPTION, REDIRECT_ERROR, OAUTH_VERIFICATION_ERROR, ENDPOINT_NOT_FOUND, ITEM_NOT_FOUND, PLAN_RESTRICTED_FEATURE, DUPLICATE_ITEM, BOZO_RSS_FEED, TOO_MUCH_DATA, DOWNLOAD_TOO_LARGE, MISSING_REQUIRED_OPTION, TOO_MANY_OPTIONS, BOZO_TORRENT, NO_SERVERS_AVAILABLE_ERROR, MONTHLY_LIMIT, COOLDOWN_LIMIT, ACTIVE_LIMIT, DOWNLOAD_SERVER_ERROR, BOZO_NZB, SEARCH_ERROR, INVALID_DEVICE, DIFF_ISSUE, LINK_OFFLINE, VENDOR_DISABLED, BOZO_REGEX, BAD_CONFIRMATION, CONFIRMATION_EXPIRED, BOZO_FILE` + `NOT_OWNER (409), INVALID_HASH, NO_CHANGES, NAME_TOO_LONG, NAME_TOO_SHORT, UNSUPPORTED_SITE, TEMPORARILY_DISABLED, DEVICE_CODE_NOT_USED`. Codes ending `ERROR` are server-side.

## Torrents
- `POST /torrents/createtorrent` (multipart): `file` | `magnet` (exactly one), `seed` (1 auto/2 seed/3 no), `allow_zip` (default true), `name?`, `as_queued?`, `add_only_if_cached?` → `data: {hash, torrent_id, auth_id}`. Errors `BOZO_TORRENT`, `ACTIVE_LIMIT`, `MONTHLY_LIMIT`, `COOLDOWN_LIMIT`, `DOWNLOAD_TOO_LARGE`. Async variant `/torrents/asynccreatetorrent`.
- `POST /torrents/controltorrent` (JSON) `{torrent_id, operation, all?}` — ops `reannounce|delete|resume|stop_seeding` (**no pause**). Errors `NOT_OWNER`, `INVALID_OPTION`.
- `GET /torrents/requestdl?token=&torrent_id=&file_id=&zip_link=&user_ip=&redirect=&append_name=` → `data: "<CDN URL>"`. Link valid 3h to start; `redirect=true` gives 307 permalink.
- `GET /torrents/mylist?bypass_cache=&id=&offset=&limit=` → `data: [{id, auth_id, server, hash, name, magnet, size, active, created_at, updated_at, download_state, seeds, peers, ratio, progress (0-1), download_speed, upload_speed, eta, torrent_file, expires_at, download_present, files: [{id, md5, hash, name, size, zipped, s3_path, infected, mimetype, short_name, absolute_path}], download_path, availability, download_finished, tracker, total_uploaded, total_downloaded, cached, owner, seed_torrent, allow_zipped, long_term_seeding, tracker_message, cached_at, private, alternative_hashes[], tags[], airlocked}]`. List cached ~600s unless `bypass_cache=true`.
  - `download_state`: `downloading, uploading, stalled (no seeds), paused, completed, cached, metaDL, checkingResumeData` + qBittorrent-style others. **Use `download_finished`/`download_present` as completion flags**, not `download_state`.
- `GET /torrents/checkcached?hash=...&format=object|list&list_files=` (≤100 per GET; `POST` with `{hashes:[]}` unlimited) → object keyed by hash `{name, size, hash, files?}`; uncached absent. (Still works — unlike RD/AD.)
- `GET /torrents/exportdata?torrent_id=&type=magnet|file`
- `GET /torrents/torrentinfo?hash=&timeout=&use_cache_lookup=` (no auth) → `{name, hash, size, trackers[], seeds, peers, files:[{name,size}]}`; POST variant accepts `magnet`/`file`.
- `PUT /torrents/edittorrent` `{torrent_id, name?, tags?, alternative_hashes?, airlocked?}`
- `POST /torrents/magnettofile` (no auth) `{magnet}` → .torrent bytes

## Queued / Web downloads / Usenet
- `GET /queued/getqueued?type=torrent|usenet|webdl`, `POST /queued/controlqueued {queued_id?, operation: delete|start, all?}`
- `POST /webdl/createwebdownload` (form-urlencoded) `link, password?, name?, as_queued?, add_only_if_cached?` → `{hash, webdownload_id, auth_id, ...}`; `POST /webdl/controlwebdownload {webdl_id, operation:"delete"}`; `GET /webdl/requestdl?token&web_id&file_id&zip_link&redirect`; `GET /webdl/mylist`; `GET /webdl/hosters`
- `POST /usenet/createusenetdownload` (multipart) `file|link, name?, password?, post_processing (-1..3), as_queued?`; `POST /usenet/controlusenetdownload {usenet_id, operation: delete|pause|resume}`; `GET /usenet/requestdl`; `GET /usenet/mylist`

## User / misc
- `GET /user/me?settings=false` → `{id, auth_id, plan (0 Free,1 Essential,2 Pro,3 Standard), total_downloaded, customer, is_subscribed, premium_expires_at, cooldown_until, email, ...}`
- `POST /user/refreshtoken`, `GET /user/stats`, `GET /notifications/mynotifications`, `GET /stats` (no auth)

## RD → TorBox translation
`GET /torrents`→`mylist`; `info/{id}`→`mylist?id=`; `addTorrent|addMagnet`→`createtorrent`; `selectFiles`→not needed (all files); `delete`→`controltorrent delete`; `unrestrict/link`→`requestdl`.

## Notes for our client
- No file selection; every file is downloaded server-side; we pick files client-side and request per-file `requestdl` URLs (or `zip_link`).
- `requestdl` returns a bare URL string in `data`.
