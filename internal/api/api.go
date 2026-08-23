// Package api exposes the service over HTTP with an OpenAPI 3.1 description
// (huma). All routes live under /api/v1 and require a Bearer API key except
// /api/v1/health.
package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/NathanBhanji/debrid-client/internal/buildinfo"
	"github.com/NathanBhanji/debrid-client/internal/service"
)

// Options configure the API.
type Options struct {
	// APIKey protects all routes except health. Empty disables auth (tests only).
	APIKey string
	// BasePath is an optional URL prefix (e.g. "/debrid"); routes become <base>/api/v1/...
	BasePath string
	// Logger receives internal errors (never sent to clients). Nil = slog.Default().
	Logger *slog.Logger
	// MaxUploadBytes caps .torrent uploads (default 16 MiB).
	MaxUploadBytes int64
}

// operation metadata keys
const (
	metaPublic   = "public"    // no API key required
	metaQueryKey = "query_key" // ?api_key= accepted (SSE / browsers)
)

// Handler is the HTTP handler plus the huma API (for spec export).
type Handler struct {
	Mux  *http.ServeMux
	Huma huma.API
	svc  *service.Service
	opts Options
}

// New builds the API handler on a fresh ServeMux.
func New(svc *service.Service, opts Options) *Handler {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("debrid-client API", buildinfo.Version)
	cfg.Info.Description = "Manage torrents on debrid providers and download their files locally."
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer": {Type: "http", Scheme: "bearer", Description: "API key"},
	}
	cfg.Security = []map[string][]string{{"bearer": {}}}
	prefix := strings.TrimSuffix(opts.BasePath, "/")
	// Paths are registered with the prefix already, so no `servers` entry
	// (it would double the prefix in generated clients and $schema links).
	cfg.OpenAPIPath = prefix + "/openapi"
	cfg.DocsPath = prefix + "/docs"
	cfg.SchemasPath = prefix + "/schemas"
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.MaxUploadBytes <= 0 {
		opts.MaxUploadBytes = 16 << 20
	}
	api := humago.New(mux, cfg)
	h := &Handler{Mux: mux, Huma: api, svc: svc, opts: opts}
	api.UseMiddleware(h.authMiddleware)
	h.registerRoutes(prefix + "/api/v1")
	return h
}

// ServeHTTP implements http.Handler. Request bodies are capped (uploads are
// the only large bodies; everything else is tiny JSON).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	limit := h.opts.MaxUploadBytes + 64<<10
	if r.ContentLength > limit {
		http.Error(w, `{"title":"Request Entity Too Large","status":413,"detail":"request body too large"}`, http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	h.Mux.ServeHTTP(w, r)
}

// authMiddleware enforces the API key on every operation except those marked
// public in their metadata — matched by operation identity, never by path
// shape (a torrent or account whose id/name is "health" must not be public).
func (h *Handler) authMiddleware(ctx huma.Context, next func(huma.Context)) {
	op := ctx.Operation()
	if h.opts.APIKey == "" || (op != nil && op.Metadata[metaPublic] == true) {
		next(ctx)
		return
	}
	token, ok := strings.CutPrefix(ctx.Header("Authorization"), "Bearer ")
	if !ok && op != nil && op.Metadata[metaQueryKey] == true {
		token = ctx.Query("api_key") // EventSource can't set headers
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.opts.APIKey)) != 1 {
		ctx.SetHeader("WWW-Authenticate", `Bearer realm="debrid"`)
		_ = huma.WriteErr(h.Huma, ctx, http.StatusUnauthorized, "missing or invalid API key")
		return
	}
	next(ctx)
}

// mapErr converts service errors to HTTP problems. Unknown errors are logged
// and returned as an opaque 500 so internals never reach clients.
func (h *Handler) mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, service.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, service.ErrConflict):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, service.ErrValidation):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, context.Canceled):
		return huma.NewError(499, "request cancelled")
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return huma.Error413RequestEntityTooLarge("request body too large")
	}
	h.opts.Logger.Error("api: internal error", "err", err)
	return huma.Error500InternalServerError("internal error")
}
