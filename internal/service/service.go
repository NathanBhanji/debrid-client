// Package service is the application layer: every operation the API, CLI and
// MCP server expose is implemented here once, on top of the store, providers
// and engine.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

// Sentinel errors. API layers map these to status codes.
var (
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
	ErrValidation = errors.New("validation failed")
)

// Engine is the subset of the download engine the service drives. A no-op
// implementation is used until the engine is wired in.
type Engine interface {
	// Wake asks the engine to run a scheduling pass soon (after adds/retries).
	Wake()
	// CancelTorrent stops any in-flight downloads/unpacks for the torrent.
	CancelTorrent(ctx context.Context, torrentID string) error
}

// NoopEngine satisfies Engine when no engine is running (tests, CLI tools).
type NoopEngine struct{}

func (NoopEngine) Wake()                                       {}
func (NoopEngine) CancelTorrent(context.Context, string) error { return nil }

// Config holds the service-level paths.
type Config struct {
	DownloadDir string
}

// Service implements the application operations.
type Service struct {
	store     *store.Store
	providers *Providers
	engine    Engine
	events    *events.Bus
	cfg       Config
	log       *slog.Logger
	now       func() time.Time
	newID     func() string
}

// New constructs a Service.
func New(st *store.Store, providers *Providers, eng Engine, bus *events.Bus, cfg Config, log *slog.Logger) *Service {
	if eng == nil {
		eng = NoopEngine{}
	}
	if bus == nil {
		bus = events.New()
	}
	if log == nil {
		log = slog.Default()
	}
	if abs, err := filepath.Abs(cfg.DownloadDir); err == nil {
		cfg.DownloadDir = abs
	}
	return &Service{
		store: st, providers: providers, engine: eng, events: bus, cfg: cfg, log: log,
		now:   func() time.Time { return time.Now().UTC() },
		newID: func() string { return uuid.Must(uuid.NewV7()).String() },
	}
}

// Events exposes the bus for subscribers (SSE, engine).
func (s *Service) Events() *events.Bus { return s.events }

// Providers exposes the provider resolver (engine uses it too).
func (s *Service) Providers() *Providers { return s.providers }

// SetEngine wires the engine after construction (engine depends on service).
func (s *Service) SetEngine(e Engine) { s.engine = e }

func validationErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}
