# web

The debrid-client web UI: a TanStack Start (React) app built in SPA mode and
embedded into the Go binary via `internal/webui`.

## Known limitation: base path

The built UI uses root-absolute URLs (`/assets/…`, `/api/v1/…`), so it only
works when `server.base_path` is empty (the default). With a non-root base
path the API, docs and MCP endpoints work as usual, but the embedded UI will
not load. Use a reverse proxy on a dedicated (sub)domain instead of a path
prefix if you need the UI behind one.

## Development

Run the API server (`debrid serve`, default 127.0.0.1:8080), then:

```bash
npm install
npm run dev
```

The dev server runs on port 3000 and proxies `/api` and `/openapi.json` to the
Go server.

## Building for the binary

```bash
make web   # from the repo root: builds the SPA and stages it into internal/webui/dist
make build # embeds it
```

## Regenerating API types

`src/lib/api.gen.d.ts` is generated from the server's OpenAPI spec:

```bash
npm run gen:api
```

Run this after `make generate` whenever the API surface changes, and commit the
result.
