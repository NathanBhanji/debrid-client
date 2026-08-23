// Package webui serves the embedded single-page web UI.
//
// The UI is a TanStack Start SPA built into web/dist/client and copied here
// (make web). The Go build works without it: when dist holds no shell, the
// handler serves a small placeholder page instead.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// shellName is the prerendered SPA shell emitted by the web build.
const shellName = "_shell.html"

const placeholder = `<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>debrid</title></head>
<body style="font-family: monospace; background: #1d211b; color: #c9f5b1; padding: 3rem">
<h1>debrid</h1>
<p>The web UI was not included in this build.</p>
<p>Build it with <code>make web</code> and rebuild, or use the API at <code>/api/v1</code> (docs at <code>/docs</code>).</p>
</body>
</html>`

// Handler serves the embedded UI: static assets when the path matches a built
// file, the SPA shell for every other path (client-side routing), and a
// placeholder page when the UI has not been built into the binary.
func Handler() http.Handler {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // embed guarantees the directory exists
	}
	shell, shellErr := fs.ReadFile(dist, shellName)
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" && name != shellName {
			if f, err := dist.Open(name); err == nil {
				_ = f.Close()
				// Asset names carry content hashes, so they can be cached forever.
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if shellErr != nil {
			_, _ = w.Write([]byte(placeholder))
			return
		}
		_, _ = w.Write(shell)
	})
}

// Present reports whether a built UI is embedded in this binary.
func Present() bool {
	_, err := fs.Stat(distFS, "dist/"+shellName)
	return err == nil
}
