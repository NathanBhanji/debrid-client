// Package app wires config, store, providers, service, engine and HTTP server
// together for the `serve` command (and for in-process use by tests/MCP).
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/api"
	"github.com/NathanBhanji/debrid-client/internal/apiclient"
	"github.com/NathanBhanji/debrid-client/internal/config"
	"github.com/NathanBhanji/debrid-client/internal/engine"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/mcpserver"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	_ "github.com/NathanBhanji/debrid-client/internal/provider/all" // register providers
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

const apiKeySetting = "server.api_key"

// App holds the assembled components.
type App struct {
	Cfg     config.Config
	Log     *slog.Logger
	Store   *store.Store
	Service *service.Service
	Engine  *engine.Engine
	API     *api.Handler
	APIKey  string
	Bus     *events.Bus
	// Extra handlers mounted on the mux (e.g. MCP at /mcp).
	Mux *http.ServeMux
}

// New assembles the application. Call Run to start it.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*App, error) {
	if log == nil {
		log = NewLogger(cfg.Log)
	}
	if err := os.MkdirAll(cfg.DownloadDir, 0o755); err != nil {
		return nil, fmt.Errorf("create download dir: %w", err)
	}
	st, err := store.Open(ctx, dbPath(cfg))
	if err != nil {
		return nil, err
	}
	bus := events.New()
	providers := service.NewProviders(st, nil, provider.Options{UserAgent: "debrid-client"})
	svc := service.New(st, providers, nil, bus, service.Config{DownloadDir: cfg.DownloadDir}, log)
	eng := engine.New(engine.Config{
		DownloadDir: cfg.DownloadDir, DownloadLimit: cfg.Engine.DownloadLimit, UnpackLimit: cfg.Engine.UnpackLimit,
		PollInterval: cfg.Engine.PollInterval, IdlePollInterval: cfg.Engine.IdlePollInterval,
		ConnectionsPerDownload: cfg.Engine.ConnectionsPerDownload, MaxSpeed: cfg.Engine.MaxSpeed,
	}, st, svc, bus, log)
	svc.SetEngine(eng)

	key, err := resolveAPIKey(ctx, cfg, svc, log)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	h := api.New(svc, api.Options{APIKey: key, BasePath: cfg.Server.BasePath, Logger: log})

	// MCP over Streamable HTTP at <base>/mcp, driving the API in-process.
	cl, err := apiclient.NewClientWithResponses("http://127.0.0.1"+strings.TrimSuffix(cfg.Server.BasePath, "/"),
		apiclient.WithHTTPClient(&http.Client{Transport: api.InProcessTransport{Handler: h}}),
		apiclient.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+key)
			return nil
		}))
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	mcpPath := strings.TrimSuffix(cfg.Server.BasePath, "/") + "/mcp"
	h.Mux.Handle(mcpPath, api.RequireAPIKey(key, mcpserver.NewHTTPHandler(cl)))

	return &App{Cfg: cfg, Log: log, Store: st, Service: svc, Engine: eng, API: h, APIKey: key, Bus: bus, Mux: h.Mux}, nil
}

func dbPath(cfg config.Config) string { return cfg.DataDir + string(os.PathSeparator) + "debrid.db" }

// resolveAPIKey uses the configured key, else a persisted generated one, else
// generates and persists a new one.
func resolveAPIKey(ctx context.Context, cfg config.Config, svc *service.Service, log *slog.Logger) (string, error) {
	if cfg.Server.APIKey != "" {
		return cfg.Server.APIKey, nil
	}
	if v, ok, err := svc.GetRaw(ctx, apiKeySetting); err != nil {
		return "", err
	} else if ok && v != "" {
		return v, nil
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	key := hex.EncodeToString(b)
	if err := svc.SetRaw(ctx, apiKeySetting, key); err != nil {
		return "", err
	}
	log.Info("generated API key (saved in database; set server.api_key to override)", "api_key", key)
	return key, nil
}

// Run starts the engine and HTTP server and blocks until ctx is cancelled
// (or the server fails), then shuts down gracefully: request contexts are
// cancelled so long-lived SSE handlers exit, HTTP drains, then the engine
// stops and the store closes.
func (a *App) Run(ctx context.Context) (err error) {
	defer func() {
		if cerr := a.Store.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	ln, err := net.Listen("tcp", a.Cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", a.Cfg.Server.Listen, err)
	}
	reqCtx, cancelReqs := context.WithCancel(context.Background())
	defer cancelReqs()
	srv := &http.Server{
		Handler:           a.Mux,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return reqCtx },
	}
	srv.RegisterOnShutdown(cancelReqs) // unblock SSE streams so Shutdown can drain

	engCtx, engCancel := context.WithCancel(ctx)
	defer engCancel()
	engDone := make(chan error, 1)
	go func() { engDone <- a.Engine.Run(engCtx) }()
	srvErr := make(chan error, 1)
	go func() {
		a.Log.Info("API listening", "addr", ln.Addr().String(), "base_path", a.Cfg.Server.BasePath, "mcp", strings.TrimSuffix(a.Cfg.Server.BasePath, "/")+"/mcp")
		if serr := srv.Serve(ln); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			srvErr <- serr
		}
		close(srvErr)
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case serr := <-srvErr:
		runErr = serr
	}
	a.Log.Info("shutting down")
	shCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if serr := srv.Shutdown(shCtx); serr != nil {
		_ = srv.Close()
	}
	engCancel()
	if eerr := <-engDone; eerr != nil && runErr == nil && !errors.Is(eerr, context.Canceled) {
		runErr = eerr
	}
	return runErr
}

// Close releases resources without running.
func (a *App) Close() error { return a.Store.Close() }

// NewLogger builds a slog logger from config.
func NewLogger(cfg config.Log) *slog.Logger {
	var lvl slog.Level
	switch cfg.Level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if cfg.Format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(h)
}
