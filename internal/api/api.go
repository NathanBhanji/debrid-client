// Package api exposes the service over HTTP with an OpenAPI 3.1 description
// (huma). All routes live under /api/v1 and require a Bearer API key except
// /api/v1/health.
package api

import (
	"context"
	"crypto/subtle"
	"errors"
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
}

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
	cfg.OpenAPIPath = prefix + "/openapi"
	cfg.DocsPath = prefix + "/docs"
	cfg.SchemasPath = prefix + "/schemas"
	if prefix != "" {
		cfg.Servers = []*huma.Server{{URL: prefix}}
	}
	api := humago.New(mux, cfg)
	h := &Handler{Mux: mux, Huma: api, svc: svc, opts: opts}
	api.UseMiddleware(h.authMiddleware)
	h.registerRoutes(prefix + "/api/v1")
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.Mux.ServeHTTP(w, r) }

func (h *Handler) authMiddleware(ctx huma.Context, next func(huma.Context)) {
	if h.opts.APIKey == "" || strings.HasSuffix(ctx.URL().Path, "/health") || isDocsPath(ctx.URL().Path) {
		next(ctx)
		return
	}
	auth := ctx.Header("Authorization")
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		// Also accept ?api_key= for SSE/browser convenience.
		token = ctx.Query("api_key")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.opts.APIKey)) != 1 {
		ctx.SetHeader("WWW-Authenticate", `Bearer realm="debrid"`)
		_ = huma.WriteErr(h.Huma, ctx, http.StatusUnauthorized, "missing or invalid API key")
		return
	}
	next(ctx)
}

func isDocsPath(p string) bool {
	return strings.HasSuffix(p, "/openapi") || strings.HasSuffix(p, "/openapi.json") || strings.HasSuffix(p, "/openapi.yaml") ||
		strings.HasSuffix(p, "/docs") || strings.Contains(p, "/schemas/")
}

// mapErr converts service errors to HTTP problems.
func mapErr(err error) error {
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
	return huma.Error500InternalServerError("internal error", err)
}
