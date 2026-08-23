# Go stack research (verified 2026-08-23)

Local toolchain: Go 1.26.2 (Go 1.27.0 released 2026-08-19). Target `go 1.25`+ (required by MCP SDK).

## MCP server
- **Pick: `github.com/modelcontextprotocol/go-sdk` v1.7.0** (2026-07-27; official, Apache-2; Go ≥1.25). Full support for spec `2026-07-28` with negotiated back-compat. Generic `mcp.AddTool[In,Out](server, &mcp.Tool{...}, handler)` infers input *and* output JSON schema from Go structs (`jsonschema:"desc"` tags) → structured output for free. Transports: `&mcp.StdioTransport{}`, `mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true})` (an `http.Handler`). Elicitation supported; roots/sampling/logging deprecated protocol-wide — avoid.
- Alternative `github.com/mark3labs/mcp-go` v1.0.0-beta.1 (2026-08-12) — still pre-1.0, builder DSL; more batteries but churn. Not chosen.
- No official huma→MCP adapter exists. Plan: hand-curate MCP tools that call the same service layer used by the HTTP API (share request/response structs).

```go
type Input struct { Name string `json:"name" jsonschema:"the name"` }
type Output struct { Greeting string `json:"greeting"` }
func SayHi(ctx context.Context, req *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, Output, error) { ... }
server := mcp.NewServer(&mcp.Implementation{Name: "debrid", Version: "v0.1.0"}, nil)
mcp.AddTool(server, &mcp.Tool{Name: "greet", Description: "say hi"}, SayHi)
server.Run(ctx, &mcp.StdioTransport{})
// HTTP: http.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
```

## HTTP API
- Routing: stdlib `net/http.ServeMux` (1.22+ method+pattern routing) is sufficient; `chi/v5` v5.3.2 only if we want middleware groups (stdlib-shaped so trivial to switch). Echo not needed.
- **OpenAPI: `github.com/danielgtaylor/huma/v2` v2.39.1** (2026-07-29). Code-first: Go structs → OpenAPI 3.1 at runtime, validation, RFC 9457 errors, `/openapi.json` + docs UI, adapters for stdlib mux (`humago`) and chi. Spec exportable via `api.OpenAPI().YAML()` / `DowngradeYAML()`.
- Typed Go client: `oapi-codegen/v2` v2.8.0 (`-generate types,client`) from the exported spec → used by the CLI. (ogen v1.24.0 is stricter but opinionated; skip.)
- Alternative considered: share one `Service` interface and call it in-process for MCP, over HTTP for CLI via generated client.

```go
api := humago.New(mux, huma.DefaultConfig("Debrid Client", "0.1.0"))
huma.Register(api, huma.Operation{OperationID: "add-torrent", Method: http.MethodPost, Path: "/api/v1/torrents", DefaultStatus: 201},
  func(ctx context.Context, in *AddTorrentInput) (*AddTorrentOutput, error) { ... })
```

## CLI
- **cobra v1.10.2** (2025-12-04). huma ships `humacli` (cobra-based) for `serve` with options→flags/env. Alternatives: urfave/cli v3.11.0, kong v1.16.1. Cobra chosen for ecosystem + humacli fit.
- Structure: `cmd/debrid` → subcommands `serve`, `mcp`, `torrents add|list|rm`, `openapi`, `config`.

## SQLite / data layer
- **Driver: `modernc.org/sqlite` v1.57.0** (cgo-free, SQLite 3.53.3, `CGO_ENABLED=0` cross-compile). DSN: `file:x.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate`. One writer pool (`SetMaxOpenConns(1)`) + reader pool.
- **Queries: `sqlc` v1.31.1** (SQLite support beta but fine for small schema) — explicit SQL, no ORM. Fallback hand-written `database/sql`.
- **Migrations: `pressly/goose/v3` v3.27.3** with `embed.FS` + Provider API, run at startup.
- GORM (cgo driver / stale pure-Go shim) and ent (overkill) rejected.

## Background jobs
- Hand-rolled supervisor: `downloads` table is the state machine; scheduler goroutine claims work (`UPDATE … RETURNING` under `_txlock=immediate`); per-download workers bounded via `errgroup.SetLimit`; context cancellation for pause/cancel; backoff columns; periodic progress writes.
- River v0.44.1 now has experimental `riversqlite` — not needed for long-running progress-reporting downloads.

## Config
- **`knadh/koanf/v2` v2.3.6**: YAML file → env (`DEBRID_` prefix, `__` → `.`) → flags, unmarshal into one typed struct with defaults. (viper heavy/case-insensitive; caarlos0/env v11 + goccy/go-yaml is the even-lighter option.) Note `gopkg.in/yaml.v3` is archived; use `go.yaml.in/yaml/v3` or `goccy/go-yaml`.

## Download engine
- No slim, maintained multi-connection Go downloader exists. `cavaliergopher/grab/v3` (single-stream, resumable, checksums; revived Aug 2026 but no tag since v3.0.1) ; `got`/`pget` abandoned chunkers; `gopeed/pkg/download` heavy.
- **Plan: write our own chunked downloader** (~200–300 lines): HEAD/Range probe, N goroutines writing to preallocated file at offsets (`WriteAt`), per-chunk retry with `hashicorp/go-retryablehttp` v0.7.8 or plain net/http + backoff, resume via persisted chunk map, bandwidth limiter (`golang.org/x/time/rate`), progress callback. Honour RD `chunks` as max connections.
- aria2 JSON-RPC optional: `siku2/arigo` v0.3.0 (WS-only, events, multicall; alive) — or a tiny in-house client. aria2 upstream last release 1.37.0 (2023), maintenance mode. Defer.

## Archive extraction
- **`mholt/archives` v0.1.5** (wraps `nwaples/rardecode/v2` v2.4.1 — RAR 1.5–5, multi-volume via `Rar{Name, FS}`, passwords, solid — and `bodgit/sevenzip` v1.6.5 — AES-256, `.001` volumes only via direct `sevenzip.OpenReader`). `Identify` + `Extract`/`FileSystem`. 7z needs `io.ReaderAt`. stdlib zip: no encryption.
- Optional CLI fallback `7zz`/`unrar` (unrar licence non-free; 7zz prompts on encrypted archives without `-p`).

## Torrent metadata
- **`github.com/anacrolix/torrent/metainfo` + `/bencode`** v1.61.0 (active). Importing only these pulls ~12 small modules, no network stack. `metainfo.Load`, `HashInfoBytes()`, `UnmarshalInfo().UpvertedFiles()`, `ParseMagnetUri`/`ParseMagnetV2Uri` (v2/hybrid). Alternative zero-dep: `zeebo/bencode` + ~60 lines.

## Packaging
- `CGO_ENABLED=0` static binary; `//go:embed all:web/dist` + `http.FileServerFS` with SPA fallback (later). GoReleaser v2.17.1 (archives, checksums, GitHub release) + `kos:`/`dockers_v2` → `gcr.io/distroless/static-debian12:nonroot`, linux/amd64+arm64. `import _ "time/tzdata"`. Run as provided uid; no chown.
