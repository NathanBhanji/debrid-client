# debrid-client

A self-hosted debrid download manager written in Go. Add magnets/torrents, let your debrid provider fetch them, and have the files downloaded to your own disk — with an HTTP API, a CLI, and an MCP server for AI agents. Single static binary, SQLite, no cgo.

**Providers:** TorBox, Real-Debrid (private API token; OAuth refresh not yet supported), AllDebrid, Premiumize and Debrid-Link (whitelist your server's IP in your DL account if it's a datacenter/VPN address).

## Install

Download a release from the [Releases page](https://github.com/NathanBhanji/debrid-client/releases), or:

```sh
go install github.com/NathanBhanji/debrid-client/cmd/debrid@latest
```

Docker (`ghcr.io/nathanbhanji/debrid-client`): see [`docker-compose.yml`](docker-compose.yml). The image runs as an unprivileged user (uid 65532), stores its database in `/data` and writes files to `/downloads`. With a named volume it works out of the box; if you bind-mount host directories or set `user:`, create those directories first, owned by that uid.

## Quick start

```sh
debrid config init                       # optional: writes a commented config file
debrid serve                             # API on 127.0.0.1:8080; prints the generated API key on first run

debrid accounts add --kind torbox --key $TORBOX_API_KEY
debrid torrents add "magnet:?xt=urn:btih:..." -c tv
debrid torrents ls -w                    # watch progress
debrid torrents get <id|hash>            # files, per-file download progress
```

The CLI talks to the running server. On the same machine it finds the server address and API key automatically (from config, env or the database); elsewhere pass `--server https://host:8080 --api-key …` or set `DEBRID_SERVER` / `DEBRID_API_KEY`.

Files land in `<download_dir>/[<category>/]<torrent name>/…`. Archives are extracted in place by default.

## Configuration

Config file (`$XDG_CONFIG_HOME/debrid/config.yaml` or `--config`), environment (`DEBRID_SECTION__KEY`), and flags — later wins. `debrid config show` prints the effective config.

| Key | Env | Default | Meaning |
|---|---|---|---|
| `data_dir` | `DEBRID_DATA_DIR` | `~/.local/share/debrid` | database + state |
| `download_dir` | `DEBRID_DOWNLOAD_DIR` | `<data_dir>/downloads` | where files go |
| `server.listen` | `DEBRID_SERVER__LISTEN` | `127.0.0.1:8080` | API address |
| `server.api_key` | `DEBRID_SERVER__API_KEY` | generated | Bearer key for API/MCP |
| `server.base_path` | `DEBRID_SERVER__BASE_PATH` | | URL prefix behind a proxy |
| `engine.download_limit` | `DEBRID_ENGINE__DOWNLOAD_LIMIT` | `2` | concurrent file downloads |
| `engine.unpack_limit` | `DEBRID_ENGINE__UNPACK_LIMIT` | `1` | concurrent extractions (0 = off) |
| `engine.connections_per_download` | `DEBRID_ENGINE__CONNECTIONS_PER_DOWNLOAD` | `8` | parallel connections per file |
| `engine.max_speed` | `DEBRID_ENGINE__MAX_SPEED` | `0` | total bytes/sec cap |
| `engine.poll_interval` | `DEBRID_ENGINE__POLL_INTERVAL` | `10s` | provider polling while active |
| `log.level` / `log.format` | `DEBRID_LOG__LEVEL` / `__FORMAT` | `info` / `text` | |

Runtime settings (default per-torrent filters/retries, categories, unpack depth) live in the database and are editable via `debrid settings get|set`, the API (`/api/v1/settings`) or MCP.

## API

OpenAPI 3.1 at `/openapi.json`, interactive docs at `/docs`. All routes under `/api/v1` take `Authorization: Bearer <api key>` (or `?api_key=`); `/api/v1/health` is public.

- `GET/POST /torrents`, `POST /torrents/file` (multipart), `GET/PATCH/DELETE /torrents/{id|hash}`, `POST /torrents/{id}/retry`, `PUT /torrents/{id}/files`, `POST /downloads/{id}/retry`
- `GET/POST /accounts`, `GET/PATCH/DELETE /accounts/{id|name}`, `POST /accounts/{id}/test`
- `GET/PUT /settings`, `GET /system/status`, `GET /events` (Server-Sent Events)

A typed Go client is generated into `internal/apiclient`; `debrid openapi` prints the spec for other generators.

## MCP

Expose the same operations to AI agents:

- **stdio**: `debrid mcp` (forwards to a running server). Example client config:
  ```json
  {"mcpServers": {"debrid": {"command": "debrid", "args": ["mcp"], "env": {"DEBRID_API_KEY": "…"}}}}
  ```
- **Streamable HTTP**: the server also serves MCP at `<server>/mcp` (Bearer API key).

Tools: `system_status`, `list_torrents`, `get_torrent`, `add_torrent`, `delete_torrent`, `retry_torrent`, `select_files`, `update_torrent`, `retry_download`, `list_accounts`, `add_account`, `test_account`, `get_settings`, `update_settings`.

## Development

```sh
make build      # → bin/debrid
make test       # race tests
make lint       # golangci-lint via go run
make generate   # sqlc + OpenAPI spec + Go client (CI fails if stale)
```

Design notes: [docs/DESIGN.md](docs/DESIGN.md). Provider API references: [docs/research/](docs/research/). Live provider tests run when `TORBOX_API_KEY`, `REALDEBRID_API_KEY`, `ALLDEBRID_API_KEY`, `PREMIUMIZE_API_KEY` or `DEBRIDLINK_API_KEY` are set.

## License

MIT
