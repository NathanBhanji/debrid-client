# debrid-client — design (draft v0, 2026-08-23)

A Go service that manages torrents on a debrid provider and pulls the files to local disk. Phase 1 deliverables: **HTTP API, CLI, MCP server**. Web UI later.

**Out of scope (decided 2026-08-23):** Sonarr/Radarr/Overseerr integration — no qBittorrent or SABnzbd API emulation.

Research backing every decision lives in `docs/research/`:
`provider-{realdebrid,alldebrid,premiumize,torbox,debridlink}.md`, `go-stack.md`, `ecosystem-survey.md`.

## 1. Goals / non-goals

Goals (phase 1)
- Multi-provider from day one (RD, AllDebrid, Premiumize, TorBox, Debrid-Link), multiple configured accounts, provider chosen per torrent with a default.
- Fast, resumable, multi-connection local downloads.
- Robust state machine that never silently hangs (every wait has a timeout and a visible reason), never deletes local data because the provider erred, and is idempotent against provider timeouts (no duplicate adds).
- First-class API (OpenAPI), CLI (talks to the API), MCP server (stdio + streamable HTTP) over the same service layer.
- Single static binary, no cgo, Docker distroless, runs as any uid, no chown on start.

Non-goals (phase 1)
- *arr integration (qBittorrent/SABnzbd emulation) — not planned.
- Symlink / rclone-mount mode — not planned (only makes sense for *arr + Plex setups streaming from a mount; we download to disk).
- Web UI, Usenet/NZB, Synology DownloadStation, aria2, tracker enrichment, WebDAV. Additive later if wanted.

## 2. Stack (decided; see go-stack.md)

| Concern | Choice |
|---|---|
| Go | 1.25+ (local 1.26.2) |
| HTTP/OpenAPI | `huma/v2` on stdlib `net/http.ServeMux`; spec served at `/openapi.json` |
| Typed client (CLI) | `oapi-codegen/v2` from exported spec |
| CLI | `cobra` (humacli for `serve`) |
| MCP | official `modelcontextprotocol/go-sdk` v1.7+, typed `mcp.AddTool[In,Out]`, stdio + StreamableHTTP (stateless) |
| DB | SQLite via `modernc.org/sqlite` (WAL, busy_timeout, single writer), `sqlc` queries, `goose` embedded migrations |
| Config | `koanf/v2`: yaml file → env `DEBRID_*` → flags; settings that must be editable at runtime live in DB `settings` table |
| Torrent parsing | `anacrolix/torrent/metainfo` + `bencode` only |
| Downloads | own chunked downloader (`internal/fetch`), `x/time/rate` limiter |
| Archives | `mholt/archives` (rardecode/v2 + bodgit/sevenzip) |
| Logging | `log/slog` |
| Packaging | goreleaser v2 + ko/distroless static, linux/amd64+arm64, darwin |

## 3. Architecture

```
cmd/debrid/                 single binary: serve | mcp | torrents … | providers … | config | openapi
internal/
  config/                   koanf loading, typed Config, defaults
  store/                    sqlc-generated queries, goose migrations (embed), tx helpers
  domain/                   Torrent, Download, File, enums, state transitions (pure, unit-tested)
  provider/                 Provider interface + registry
    realdebrid/ alldebrid/ premiumize/ torbox/ debridlink/
    httpx/                  shared client: retry/backoff, 429 + Retry-After, per-provider rate limiter, content-type guard
  engine/                   the runner: scheduler tick, provider poller, download + unpack workers, finished actions
  fetch/                    chunked HTTP downloader (Range, N conns, resume map, limiter, size verification)
  unpack/                   archive detection + extraction (nested depth limit, temp dir + atomic rename)
  service/                  application service layer (AddTorrent, ListTorrents, Retry, Delete, Settings…) — used by API, MCP, CLI(in-process option)
  api/                      huma routes /api/v1/* ; auth middleware (API key)
  mcp/                      tool definitions → service
  apiclient/                generated Go client (from OpenAPI) used by CLI
docs/
```

Dependency direction: `cmd → api|mcp → service → engine/store/provider → domain`. Nothing imports upward.

### 3.1 Domain model

- **Torrent** `{id (uuid), hash, name, provider_account_id, provider_id?, status, status_raw, progress, size, speed, seeders, files[] (json), category, settings{download_action, host_action, min_size, include_re, exclude_re, manual_files, finished_action, finished_delay, download_retries, torrent_retries, delete_on_error, lifetime, priority}, payload (magnet | torrent bytes), error?, added, provider_added?, provider_ended?, files_selected_at?, completed_at?, retry_at?, retry_count}`
- **Download** `{id, torrent_id, provider_link (unique per torrent), direct_url?, file_path (relative), filename, size?, bytes_done, state, queued_at, started_at, finished_at, unpack_started_at, unpack_finished_at, completed_at, error?, retry_count}`
- **ProviderAccount** `{id, kind, name, api_key/token blob, enabled, is_default}`
- **Setting** `{key, value}` for runtime-editable settings; **Category** list derived from settings.

**TorrentStatus** (provider-neutral): `queued_local → adding → processing → waiting_selection → downloading → uploading → finished → error`. Plus local phases derived from Download states.
**DownloadState**: `pending → unrestricting → downloading → downloaded → unpacking → done | error`.

### 3.2 Provider interface

```go
type Provider interface {
    Kind() Kind
    User(ctx) (User, error)
    ListTorrents(ctx) ([]Torrent, error)                // single list call per tick
    GetTorrent(ctx, id) (Torrent, error)                 // files + links
    AddMagnet(ctx, magnet string) (id string, err error)
    AddTorrentFile(ctx, b []byte) (id string, err error)
    SelectFiles(ctx, id string, fileIDs []string) error  // no-op for AD/PM/TB/DL
    Links(ctx, id string) ([]Link, error)                // per-file restricted links / direct URLs
    Unrestrict(ctx, link string) (Direct, error)         // RD /unrestrict/link, AD /link/unlock, TB requestdl; identity for PM/DL
    Delete(ctx, id string) error
    Capabilities() Caps                                  // file selection, cache check, nzb, max conns hint
}
```
Provider quirks encoded in Caps rather than `if kind == …` in the engine. Shared `httpx` client handles retries, 429/Retry-After, per-account token-bucket limits (RD 250/min, AD 12/s & 600/min, TB 300/min), and a content-type guard (RD 503 HTML pages).

### 3.3 Engine

Requirements learned from how existing debrid download tools fail in practice:
- Slow downloads: need multi-connection, resumable fetching with tunable parallelism.
- Provider rate limits: poll the list endpoint once per tick, never per-torrent; central per-account limiter; backoff on 429.
- Torrents stuck waiting (file selection / links): every wait has a timeout and a visible reason.
- Duplicate adds when the provider call times out but still succeeds: long add timeout, dedupe by info hash, re-list rather than re-add.
- Unrestrict/link failures must be retried with backoff and surfaced, not hang.
- Per-file failures (e.g. fair-use limits) must not fail the whole torrent or delete already-downloaded data.
- Unpacking must not loop on nested/solid archives; extract to temp then move atomically.
- SQLite locking: WAL + busy_timeout + single writer; no infinite retry on constraint errors.
- Torrents removed locally but not at the provider must not be re-imported forever.
- Dead/infringing torrents must map to `error` quickly instead of waiting.
- Provider API hiccups (HTML 503 pages, CDN blips): content-type guard, retry on 5xx.


- **Poller** (per provider account, interval configurable, default 10s; 30s when idle): one `ListTorrents` call, diff against DB, update provider fields; detect torrents added at provider (auto-import opt-in) and removed (mark, don't loop); AD live-mode delta endpoint later.
- **Scheduler tick** (1s): pure function over DB state → list of actions; each action runs with its own context/timeout. Sequence: reap finished workers → retries/sweeps → dequeue local queue to provider → finished actions → per-torrent progression (select files, create downloads, complete). Adds: dedupe-by-hash before add (check provider list first), long add timeout (60s+) and on timeout re-list to find the torrent instead of re-adding, map provider error statuses to `error` immediately.
- **Download workers**: bounded by `download_limit`; claim rows with `UPDATE … RETURNING`; resumable; per-file retry with backoff; re-unrestrict on resume (links expire); size verification vs provider size. Per-file failure never fails the whole torrent unless all retries exhausted; never auto-delete local files on provider error (opt-in only).
- **Unpack workers**: bounded; extract to `<dest>/.tmp-<id>` then rename; nested archive depth ≤ 2; delete archive only after verified extract.
- **Finished actions** after delay (remove from provider / keep).
- All waits have explicit timeouts and record `status_reason` shown in API.

### 3.4 API (`/api/v1`)
Resources: `torrents` (list/get/add-magnet/add-file/delete/retry/update settings/select files), `downloads` (list/retry), `providers` (accounts CRUD, test, user info), `settings` (get/patch), `categories`, `system` (health, version, disk space, rate-limit status), `events` (SSE stream for live updates — replaces SignalR). Auth: `Authorization: Bearer <api key>` (generated on first run, printed/stored). Optional base path.

### 3.5 CLI
`debrid serve`, `debrid mcp [--http]`, `debrid torrents add <magnet|file> [--category] [--provider]`, `torrents ls [--watch]`, `torrents rm <id|hash> [--provider] [--files]`, `torrents retry`, `torrents files <id>`, `providers add|ls|test|rm`, `settings get|set`, `config init`, `openapi`. Talks to a running server via generated client (`--server`, `--api-key`, config file); JSON output flag for scripting.

### 3.6 MCP
Curated tools (not 1:1 with REST): `list_torrents`, `get_torrent`, `add_torrent` (magnet/url), `delete_torrent`, `retry_torrent`, `select_files`, `list_providers`, `provider_account_info`, `get_settings`, `update_settings`, `system_status`. Typed In/Out structs shared with the API DTOs. Transports: stdio (`debrid mcp`) and StreamableHTTP mounted at `/mcp` on the server (same API key auth). Resources: `debrid://torrents/{id}` optional later.

## 4. Phased plan

Delivered as a sequence of small PRs — see `docs/PR-PLAN.md`.

1. **Skeleton**: go.mod, cmd/debrid, config, store+migrations, domain + state machine unit tests, service interfaces, huma API with `system`, `settings`, `providers` CRUD. CI (test, vet, golangci-lint), goreleaser.
2. **Providers**: TorBox first (Nathan's account; live-testable), then RealDebrid, AllDebrid, Premiumize, Debrid-Link. Contract test suite using recorded fixtures + optional live test with env API key.
3. **Engine + fetch + unpack**: poller, scheduler, downloader, unpacker; end-to-end with TorBox.
4. **CLI** (generated client) and **MCP** server; docs.
5. Then: web UI (embed.FS), watch folder, aria2, WebDAV if wanted.

## 5. Open questions for Nathan
- Should `add` block until the provider accepts (immediate error feedback) or return a queued torrent immediately? Leaning: return immediately with status `adding`, CLI `--wait` flag.
