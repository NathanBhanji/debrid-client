// Package engine drives torrents through their lifecycle: it polls providers,
// submits queued torrents, selects files, creates and runs local downloads,
// unpacks archives and applies finished actions.
//
// Design:
//   - The database is the source of truth; the engine holds only in-flight
//     job handles in memory. A crash loses nothing: on start, in-flight rows are
//     reset and resumable downloads pick up their .part files.
//   - One poller goroutine per account makes exactly one ListTorrents call per
//     interval and diffs it into the DB (never per-torrent polling).
//   - A single scheduler goroutine runs a tick (on a timer or when woken) that
//     reads state and decides what to start. Long work (provider adds,
//     downloads, unpacks) runs in separate goroutines that write their own
//     results back; the tick never blocks on I/O to providers or disk.
package engine

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/fetch"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/unpack"
)

// Config tunes the engine.
type Config struct {
	DownloadDir            string
	DownloadLimit          int           // concurrent file downloads
	UnpackLimit            int           // concurrent extractions (0 disables unpacking)
	PollInterval           time.Duration // provider polling while torrents are active
	IdlePollInterval       time.Duration // provider polling otherwise
	TickInterval           time.Duration // scheduler cadence (default 1s)
	ConnectionsPerDownload int
	MaxSpeed               int64         // bytes/sec across all downloads, 0 = unlimited
	AddTimeout             time.Duration // provider add call timeout (default 90s)
	LinksTimeout           time.Duration // how long to wait for links after finish before erroring (default 15m)
}

// Fetcher downloads a URL to a path. fetch.Download satisfies it.
type Fetcher func(ctx context.Context, url, dest string, o fetch.Options) (fetch.Result, error)

// Unpacker extracts an archive. unpack.Extract satisfies it.
type Unpacker func(ctx context.Context, archive, dest string, o unpack.Options) (unpack.Result, error)

// Engine is the download engine. Create with New, run with Run.
type Engine struct {
	cfg      Config
	store    *store.Store
	svc      *service.Service
	events   *events.Bus
	log      *slog.Logger
	fetcher  Fetcher
	unpacker Unpacker
	limiter  *rate.Limiter
	clock    atomic.Pointer[func() time.Time]
	backoff  func(attempt int) time.Duration

	wake chan struct{}

	mu       sync.Mutex
	jobs     map[string]*job // by download id (or "add:"+torrent id)
	lastList map[string]listSnapshot
	// nextAttempt throttles jobs that finished without making progress
	// (links not ready, metadata not ready, transient provider errors): a job
	// key may not be relaunched before its time. Without this the scheduler
	// would relaunch such jobs at provider-latency rate, not once per tick.
	nextAttempt map[string]time.Time
	// waitSince records when a finished torrent first waited for links, so
	// LinksTimeout measures our wait rather than the provider's EndedAt.
	waitSince map[string]time.Time
	// addFails counts consecutive transient add failures per torrent (not
	// persisted; provider-side retries use Torrent.RetryCount instead).
	addFails map[string]int
	wg       sync.WaitGroup
}

type job struct {
	torrentID string
	kind      jobKind
	cancel    context.CancelFunc
	done      chan struct{}
}

type jobKind int

const (
	jobAdd jobKind = iota
	jobDownload
	jobUnpack
)

type listSnapshot struct {
	at     time.Time
	byHash map[string]provider.Torrent
	byID   map[string]provider.Torrent
}

// New constructs an engine.
func New(cfg Config, st *store.Store, svc *service.Service, bus *events.Bus, log *slog.Logger) *Engine {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.IdlePollInterval < cfg.PollInterval {
		cfg.IdlePollInterval = cfg.PollInterval * 3
	}
	if cfg.DownloadLimit < 1 {
		cfg.DownloadLimit = 1
	}
	if cfg.ConnectionsPerDownload < 1 {
		cfg.ConnectionsPerDownload = 1
	}
	if cfg.AddTimeout <= 0 {
		cfg.AddTimeout = 90 * time.Second
	}
	if cfg.LinksTimeout <= 0 {
		cfg.LinksTimeout = 15 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{
		cfg: cfg, store: st, svc: svc, events: bus, log: log,
		fetcher: fetch.Download, unpacker: unpack.Extract,
		backoff:     defaultBackoff,
		wake:        make(chan struct{}, 1),
		jobs:        map[string]*job{},
		lastList:    map[string]listSnapshot{},
		nextAttempt: map[string]time.Time{},
		waitSince:   map[string]time.Time{},
		addFails:    map[string]int{},
	}
	if cfg.MaxSpeed > 0 {
		e.limiter = rate.NewLimiter(rate.Limit(cfg.MaxSpeed), int(min(cfg.MaxSpeed, 4<<20)))
	}
	e.setClock(func() time.Time { return time.Now().UTC() })
	return e
}

// now returns the current time from the (swappable, for tests) clock.
func (e *Engine) now() time.Time { return (*e.clock.Load())() }

func (e *Engine) setClock(fn func() time.Time) { e.clock.Store(&fn) }

// SetFetcher / SetUnpacker swap implementations (tests).
func (e *Engine) SetFetcher(f Fetcher)   { e.fetcher = f }
func (e *Engine) SetUnpacker(u Unpacker) { e.unpacker = u }

// Wake implements service.Engine: request a scheduler pass soon.
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// CancelTorrent implements service.Engine: stop and wait for the torrent's
// jobs, and drop the torrent's provider entry from the cached listing so a
// delete-and-re-add doesn't adopt a torrent that is about to vanish.
func (e *Engine) CancelTorrent(ctx context.Context, torrentID string) error {
	if row, err := e.store.GetTorrent(ctx, torrentID); err == nil && row.ProviderID != "" {
		e.forgetProviderTorrent(row.AccountID, row.ProviderID)
	}
	e.mu.Lock()
	var waiting []*job
	for _, j := range e.jobs {
		if j.torrentID == torrentID {
			j.cancel()
			waiting = append(waiting, j)
		}
	}
	delete(e.waitSince, torrentID)
	delete(e.addFails, torrentID)
	for k := range e.nextAttempt {
		if strings.HasSuffix(k, ":"+torrentID) {
			delete(e.nextAttempt, k)
		}
	}
	e.mu.Unlock()
	for _, j := range waiting {
		select {
		case <-j.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Run starts the pollers and scheduler and blocks until ctx is cancelled and
// all jobs have stopped.
func (e *Engine) Run(ctx context.Context) error {
	if err := e.recover(ctx); err != nil {
		return err
	}
	e.wg.Add(2)
	go func() { defer e.wg.Done(); e.pollLoop(ctx) }()
	go func() { defer e.wg.Done(); e.tickLoop(ctx) }()
	<-ctx.Done()
	e.wg.Wait()
	return nil
}

// recover resets in-flight rows left by a previous process.
func (e *Engine) recover(ctx context.Context) error {
	now := e.now()
	for _, st := range []domain.DownloadState{domain.DownloadUnrestricting, domain.DownloadDownloading, domain.DownloadUnpacking} {
		rows, err := e.store.ListDownloadsByState(ctx, string(st))
		if err != nil {
			return err
		}
		for _, r := range rows {
			if _, err := e.store.MutateDownload(ctx, r.ID, func(d *domain.Download) error {
				if d.State == domain.DownloadUnpacking {
					d.State = domain.DownloadDownloaded // re-run unpack
				} else {
					d.State = domain.DownloadPending // fetch resumes from .part
				}
				d.UpdatedAt = now
				return nil
			}); err != nil {
				return err
			}
		}
	}
	rows, err := e.store.ListTorrentsByStatus(ctx, string(domain.TorrentAdding))
	if err != nil {
		return err
	}
	for _, r := range rows {
		// We don't know whether the add went through; re-queue and let the
		// dedupe-by-hash path adopt it from the next provider listing — so
		// hold the add until a listing has had time to arrive.
		if _, err := e.store.MutateTorrent(ctx, r.ID, func(t *domain.Torrent) error {
			_ = t.Transition(domain.TorrentQueued, "restarted during add; will reconcile with provider")
			at := now.Add(e.cfg.PollInterval + 5*time.Second)
			t.RetryAt = &at
			t.UpdatedAt = now
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) tickLoop(ctx context.Context) {
	t := time.NewTicker(e.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.wake:
		}
		if err := e.tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			e.log.Error("engine tick", "err", err)
		}
	}
}

// startJob registers and runs fn in a goroutine under a cancellable context.
func (e *Engine) startJob(ctx context.Context, key, torrentID string, kind jobKind, fn func(ctx context.Context)) {
	jctx, cancel := context.WithCancel(ctx)
	j := &job{torrentID: torrentID, kind: kind, cancel: cancel, done: make(chan struct{})}
	e.mu.Lock()
	e.jobs[key] = j
	e.mu.Unlock()
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() {
			cancel()
			e.mu.Lock()
			delete(e.jobs, key)
			e.mu.Unlock()
			close(j.done)
			e.Wake()
		}()
		fn(jctx)
	}()
}

// defer records that key must not run again before when.
func (e *Engine) deferKey(key string, when time.Time) {
	e.mu.Lock()
	e.nextAttempt[key] = when
	e.mu.Unlock()
}

// canRun reports whether a job key is neither running nor deferred.
func (e *Engine) canRun(key string, now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, running := e.jobs[key]; running {
		return false
	}
	if at, ok := e.nextAttempt[key]; ok {
		if now.Before(at) {
			return false
		}
		delete(e.nextAttempt, key)
	}
	return true
}

// waitingSince returns when the torrent started waiting for links (first call
// records now).
func (e *Engine) waitingSince(id string, now time.Time) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	if at, ok := e.waitSince[id]; ok {
		return at
	}
	e.waitSince[id] = now
	return now
}

func (e *Engine) hasJob(key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.jobs[key]
	return ok
}

func (e *Engine) countJobs(kind jobKind) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, j := range e.jobs {
		if j.kind == kind {
			n++
		}
	}
	return n
}

// mutate applies fn to the torrent row atomically (read-modify-write in one
// transaction, see store.MutateTorrent) and publishes an update event. The
// engine never writes back a stale in-memory copy: every change is expressed
// as a closure over the freshly loaded row so it can't clobber concurrent
// edits by the service (category, settings, manual file selection…).
// Returning store.ErrSkip from fn commits nothing.
func (e *Engine) mutate(ctx context.Context, id string, fn func(t *domain.Torrent) error) (domain.Torrent, error) {
	t, err := e.store.MutateTorrent(ctx, id, func(t *domain.Torrent) error {
		if err := fn(t); err != nil {
			return err
		}
		t.UpdatedAt = e.now()
		return nil
	})
	if err != nil {
		return t, err
	}
	e.events.Publish(events.Event{Type: events.TorrentUpdated, TorrentID: id})
	return t, nil
}

// mutateDL is mutate for download rows.
func (e *Engine) mutateDL(ctx context.Context, id string, fn func(d *domain.Download) error) (domain.Download, error) {
	d, err := e.store.MutateDownload(ctx, id, func(d *domain.Download) error {
		if err := fn(d); err != nil {
			return err
		}
		d.UpdatedAt = e.now()
		return nil
	})
	if err != nil {
		return d, err
	}
	e.events.Publish(events.Event{Type: events.DownloadUpdated, TorrentID: d.TorrentID, DownloadID: id})
	return d, nil
}

// fail moves a torrent to error with a message.
func (e *Engine) fail(ctx context.Context, t *domain.Torrent, msg string) error {
	e.log.Warn("torrent failed", "id", t.ID, "name", t.Name, "reason", msg)
	nt, err := e.mutate(ctx, t.ID, func(t *domain.Torrent) error {
		if t.Status.IsTerminal() && t.Error == msg {
			return store.ErrSkip
		}
		if err := t.Transition(domain.TorrentError, msg); err != nil {
			t.Status = domain.TorrentError // force: error is always reachable
			t.StatusReason = msg
		}
		t.Error = msg
		now := e.now()
		t.CompletedAt = &now
		return nil
	})
	if err == nil {
		*t = nt
	}
	return err
}

func (e *Engine) loadTorrents(ctx context.Context) ([]domain.Torrent, error) {
	rows, err := e.store.ListTorrents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Torrent, 0, len(rows))
	for _, r := range rows {
		t, err := store.TorrentFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (e *Engine) loadDownloads(ctx context.Context, torrentID string) ([]domain.Download, error) {
	rows, err := e.store.ListDownloadsForTorrent(ctx, torrentID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Download, 0, len(rows))
	for _, r := range rows {
		d, err := store.DownloadFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// defaultBackoff for retries: 15s, 30s, 60s, 120s, capped at 5m.
func defaultBackoff(attempt int) time.Duration {
	d := 15 * time.Second << uint(max(attempt, 0)) //nolint:gosec // small shift
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}
