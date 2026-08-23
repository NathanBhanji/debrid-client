# PR plan

Each PR is independently reviewable, keeps `main` green (`go build ./... && go test ./...`), and builds on the previous one. Branch names in parentheses. Sizes are rough.

## Phase A — foundations

1. **`chore/scaffold`** — Go module (`github.com/NathanBhanji/debrid-client`), `cmd/debrid` with cobra root + `version`, `.gitignore`, `Makefile`/`Taskfile` (build, test, lint), golangci-lint config, GitHub Actions CI (build/test/lint on PR), `LICENSE` (MIT?). *Small.*
2. **`feat/config`** — `internal/config`: koanf loading (yaml → env `DEBRID_*` → flags), typed `Config` with defaults (data dir, download dir, listen addr, API key, limits), `debrid config init|show`. Tests for precedence. *Small.*
3. **`feat/store`** — `internal/store`: modernc sqlite open helper with pragmas, goose embedded migrations (torrents, downloads, provider_accounts, settings), sqlc setup + generated queries, tx helper. Tests against temp DB. *Medium.*
4. **`feat/domain`** — `internal/domain`: `Torrent`, `Download`, `ProviderAccount`, enums, pure state-transition functions + file filter (min size / include / exclude / manual) with table tests. *Medium.*

## Phase B — providers

5. **`feat/provider-core`** — `internal/provider`: `Provider` interface, `Caps`, registry, typed errors (`ErrRateLimited`, `ErrNotFound`, `ErrAuth`, `ErrPermanent`…), `httpx` shared client (retry/backoff, 429/Retry-After, per-account rate limiter, content-type guard, request logging). Contract-test helper for providers. *Medium.*
6. **`feat/provider-torbox`** — TorBox client: createtorrent, mylist (+bypass_cache), controltorrent delete, requestdl, user/me, checkcached; status mapping; fixture-based tests + opt-in live test (`TORBOX_API_KEY`). *Medium.*
7. **`feat/provider-realdebrid`** — RD client incl. selectFiles/unrestrict, link-count semantics. *Medium.*
8. **`feat/provider-alldebrid`**, 9. **`feat/provider-premiumize`**, 10. **`feat/provider-debridlink`** — one PR each. *Small–medium each; can come after the MVP vertical slice.*

## Phase C — engine

11. **`feat/fetch`** — `internal/fetch`: chunked HTTP downloader (HEAD/Range probe, N connections, `WriteAt`, resume map persisted, retries, size verification, `x/time/rate` limiter, progress callback). Tests with httptest server incl. flaky/range-less server. *Medium–large.*
12. **`feat/unpack`** — `internal/unpack`: detect + extract rar/zip/7z via mholt/archives, nested depth limit, temp dir + atomic rename, cleanup. Tests with fixture archives. *Small–medium.*
13. **`feat/service`** — `internal/service`: application layer (AddTorrent [magnet/file], List/Get, Delete {local files?, provider?}, Retry, UpdateSettings, SelectFiles, provider accounts CRUD + Test, SystemStatus). In-memory/store-backed tests. *Medium.*
14. **`feat/engine`** — `internal/engine`: provider poller (one list call per account per tick, diff → DB), scheduler tick (dedupe-by-hash add, timeouts, error mapping), download + unpack workers with bounded concurrency, finished actions, delete-on-error/lifetime sweeps, event bus. Tests with fake provider + fake fetcher. *Large — may split into `engine-poller` and `engine-workers`.*

## Phase D — interfaces

15. **`feat/api`** — `internal/api`: huma on stdlib mux, `/api/v1/{torrents,downloads,providers,settings,system}`, SSE `/api/v1/events`, Bearer API-key middleware, `/openapi.json`; `debrid serve` wires config → store → engine → api. httptest-based tests. *Medium–large.*
16. **`feat/apiclient-cli`** — export spec, `oapi-codegen` client in `internal/apiclient` (generated, checked in, `make generate`), cobra subcommands `torrents add|ls|get|rm|retry|files`, `providers add|ls|test|rm`, `settings`, JSON output flag. *Medium.*
17. **`feat/mcp`** — `internal/mcp`: curated typed tools over `service`, `debrid mcp` (stdio) + `/mcp` StreamableHTTP on the server behind API-key auth; tests via go-sdk in-memory transport. *Medium.*

## Phase E — ship

18. **`chore/release`** — goreleaser (linux/darwin amd64+arm64), Docker distroless via ko, `docker-compose.yml` example, README install/usage docs. *Small.*
19. Later: web UI (embed.FS), watch folder, aria2 backend, NZB via TorBox.

## Suggested MVP vertical slice
PRs 1–6, 11–17 with TorBox only gives a usable product; other providers (7–10) slot in any time after 5.
