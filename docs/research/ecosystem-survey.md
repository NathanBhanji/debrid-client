# Ecosystem survey — existing debrid download tools & Go debrid code (2026-08-23)

## Apps
| Project | Lang / License | Stars | Activity | What |
|---|---|---|---|---|
| **sirrobot01/decypharr** | **Go / MIT** | 869 | v2.5 (2026-08-11) | Most complete Go implementation of this idea. Mock qBittorrent + SABnzbd APIs, WebDAV, multi-provider (RD, AD, PM, TB, DL + Usenet), repair, symlinks. `pkg/debrid/providers/*` importable in principle. |
| MunifTanjim/stremthru | Go / MIT | 532 | 2026-08-22 | Stremio proxy; `store/{realdebrid,alldebrid,torbox,premiumize,debridlink,offcloud,easydebrid,pikpak,debrider}` typed clients — widest coverage, best reference code. |
| 5rahim/seanime | Go / GPL-3 | 4,053 | 2026-07-23 | Anime server; `internal/debrid/*` (RD, AD, TB, PM) not importable. |
| debridmediamanager/DMM | TS / AGPL-3 | 1,412 | daily | Library manager, not an *arr tool. |
| zurg | closed-source | 904 | v1.0.0 (2026-08-09) | WebDAV over RD library; sponsor-gated multi-provider. |
| westsurname/scripts | Python | 167 | v1.5.5 | Blackhole + symlink from rclone mount (RD, TB). |
| rivenmedia/riven | Python / GPL-3 | 817 | 2026-06 | Full pipeline; vendors RD/AD/DL clients. |
| plex_debrid | Python | 1,644 | archived 2024 | — |
| GopeedLab/gopeed | Go | 25.9k | active | Download engine (HTTP multi-conn + BT); `pkg/download` importable but heavy. |

## Go debrid client libraries
**No maintained, standalone, permissively-licensed Go lib covers the five providers.**
- Deflix-tv/go-debrid (RD/AD/PM) — AGPL, dead since 2021.
- TorBox-App/torbox-sdk-go — official, MIT, but stale (2025-04) and `module torbox-sdk-go` (not `go get`-able).
- Assorted tiny RD/AD wrappers — dead or unlicensed.
- Debrid-Link: zero standalone Go clients.

**Decision input:** write our own thin provider clients (`internal/provider/<name>`) using stremthru `store/*` and decypharr `pkg/debrid/providers/*` (both MIT, both active) as reference implementations. Each is ~300–600 lines per provider.
