# Debrid-Link API v2 — Reference

Sources: `GET https://debrid-link.com/api/v2/api_doc/infos`, `GET https://debrid-link.com/api/api_doc/errors`, https://debrid-link.com/api_doc/v2 (researched 2026-08-23, live-probed).

## Basics
- **Base URL:** `https://debrid-link.com/api/v2/`; OAuth `https://debrid-link.com/api/oauth/`; auth page `https://debrid-link.com/oauth/auth`
- **Methods:** GET, POST, DELETE. JSON bodies (form also works).
- **Auth:** `Authorization: Bearer <token>` (or `?access_token=`). Token = OAuth2 access_token **or Private API key** (https://debrid-link.com/webapp/apikey) used identically. Scopes: `get.post.delete.downloader`, `get.post.delete.seedbox`, `get.account`, `get.files`, `get.post.stream`.
- **Device flow:** `POST /api/oauth/device/code` form `client_id, scope` → `{device_code, user_code, verification_url, expires_in 900, interval 3}`; poll `POST /api/oauth/token` `client_id, code=<device_code>, grant_type=urn:ietf:params:oauth:grant-type:device_code` (legacy alias `http://oauth.net/grant_type/device/1.0`) while `authorization_pending` → `{access_token, refresh_token, expires_in 86400, type:"bearer"}`. Refresh: `grant_type=refresh_token` (revokes old token — only ONE access token per authorization). Revoke `POST /api/oauth/revoke?token=`.
- **Rate limits:** none published; `floodDetected` = retry after 1 hour. `/seedbox/limits`, `/downloader/limits` expose daily quotas.
- **Envelope:** success `{success:true, value, pagination?: {page (0-based), pages, next, previous}}` (`perPage` 20–100); error `{success:false, error:"<code>", error_description}` with 4xx/5xx.
- **Errors:** session `badToken, badSign, hidedToken`; general `unknowR, internalError, badArguments, badId, floodDetected, serverNotAllowed (datacenter/VPN IPs blocked by default!), freeServerOverload`; downloader `notDebrid, hostNotValid, fileNotFound, fileNotAvailable, badFileUrl, badFilePassword, notFreeHost, maintenanceHost, noServerHost, maxLink, maxLinkHost, maxData, maxDataHost, disabledServerHost`; seedbox `notAddTorrent, torrentTooBig, maxTorrent, maxTransfer`.

## Endpoints
### account
- `GET /account/infos` → `{username, email, emailVerified, accountType, premiumLeft (secs), pts, registerDate, serverDetected, settings{}}`
### seedbox
- `GET /seedbox/list?ids=&structureType=list|tree&page=&perPage=` → `value: [torrent]`. Torrent: `{id, name, created, hashString, uploadRatio, serverId, wait, peersConnected, status, totalSize, downloadPercent, downloadSpeed, uploadSpeed, isZip, srvMaint, files: [{id "<torrentId>-<n>", name, size, downloadUrl, downloadPercent}], trackers[]}`. If many files, `files` contains only a ZIP and `isZip=true`; fetch `?ids=<id>` for all files.
  - **status enum:** `0 paused, 1 queued, 2 verification, 4 downloading, 8 seeding, 100 finished` (bitmask-ish; sample shows 6). File ready when `files[].downloadPercent == 100`.
- `GET /seedbox/activity?ids=` → lightweight poller `{<id>: {status, downloadPercent, downloadSpeed, files:[pct...], ...}}`
- `POST /seedbox/add` body `url` (torrent URL | magnet | infohash-if-cached), `file` (multipart), `wait` (pause for file selection), `structureType` → `value: torrent` (metadata may be incomplete; poll list)
- `DELETE /seedbox/:ids/remove` → `[ids]`
- `POST /seedbox/:id/config` body `files-unwanted: [fileIds]` (for `wait=true` torrents)
- `POST /seedbox/:id/zip` body `ids`
- `GET /seedbox/limits`; `GET /seedbox/cached` (exists, undocumented)
### downloader
- `POST /downloader/add` body `url`, `password?` → link `{created, id, name, url, downloadUrl, expired, chunk, host, size}` (array for folders)
- `GET /downloader/list`, `DELETE /downloader/:ids/remove`, `GET /downloader/hosts` (no auth), `GET /downloader/domains`, `GET /downloader/limits`
### files / stream
- `GET /files/:idParent/list` (`root`), `POST /stream/transcode/add {id}`

## Notes for our client
- Seedbox files carry `downloadUrl` directly — no unrestrict step.
- Debrid-Link blocks datacenter/VPN IPs by default (`serverNotAllowed`); users must whitelist.
