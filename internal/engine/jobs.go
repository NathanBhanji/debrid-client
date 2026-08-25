package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/fetch"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
	"github.com/NathanBhanji/debrid-client/internal/unpack"
)

func newID() string { return uuid.Must(uuid.NewV7()).String() }

func downloadInsert(d domain.Download) sqlcgen.InsertDownloadParams {
	return store.DownloadInsertParams(d)
}

func isArchive(name string) bool { return unpack.IsArchive(name) }

// startDownload freezes the torrent's directory name (first local download),
// claims the download (pending → unrestricting) and launches its job.
func (e *Engine) startDownload(ctx context.Context, t *domain.Torrent, d *domain.Download) error {
	prov, _, err := e.svc.Providers().For(ctx, t.AccountID)
	if err != nil {
		return err
	}
	if t.DirName == "" {
		// Settings are loaded before the mutate: queries inside a mutate
		// callback deadlock the single-connection store.
		settings, err := e.svc.GetSettings(ctx)
		if err != nil {
			return err
		}
		dir, organized := service.DirPathFor(*t, settings.Organize)
		nt, err := e.mutate(ctx, t.ID, func(t *domain.Torrent) error {
			if t.DirName != "" {
				return store.ErrSkip
			}
			t.DirName, t.Organized = dir, organized
			return nil
		})
		if err != nil {
			return err
		}
		*t = nt
	}
	nd, err := e.mutateDL(ctx, d.ID, func(d *domain.Download) error {
		if d.State != domain.DownloadPending {
			return store.ErrSkip // someone else moved it
		}
		if err := d.Transition(domain.DownloadUnrestricting); err != nil {
			return err
		}
		d.Error = ""
		return nil
	})
	if err != nil || nd.State != domain.DownloadUnrestricting {
		return err
	}
	tt := *t
	e.startJob(ctx, d.ID, t.ID, jobDownload, func(jctx context.Context) { e.runDownload(jctx, tt, nd, prov) })
	return nil
}

// runDownload unrestricts the link (if needed), fetches the file and records the outcome.
func (e *Engine) runDownload(ctx context.Context, t domain.Torrent, d domain.Download, prov provider.Provider) {
	sctx := context.WithoutCancel(ctx)

	// 1. Resolve a direct URL.
	if d.DirectURL == "" {
		if prov.Caps().DirectLinks {
			d.DirectURL = d.ProviderLink
		} else {
			direct, err := prov.Unrestrict(ctx, d.ProviderLink)
			if err != nil {
				e.downloadFailed(sctx, d.ID, fmt.Errorf("unrestrict: %w", err), provider.IsRetryable(err), false)
				return
			}
			d.DirectURL = direct.URL
			sizeUnknown := d.Size == 0
			if direct.Size > 0 {
				d.Size = direct.Size
			}
			// A provider that couldn't map links to files (repacked/split
			// archives) reports Size 0 and a placeholder path; the unrestricted
			// filename is authoritative then.
			if direct.Filename != "" && (d.Filename == "" || sizeUnknown && d.Filename != direct.Filename) {
				d.Filename = service.SanitizeName(direct.Filename)
				d.RelPath = path.Join(path.Dir(d.RelPath), d.Filename)
			}
		}
	}
	directURL, size, filename, relPath := d.DirectURL, d.Size, d.Filename, d.RelPath
	nd, err := e.mutateDL(sctx, d.ID, func(d *domain.Download) error {
		if d.State != domain.DownloadUnrestricting {
			return store.ErrSkip
		}
		if err := d.Transition(domain.DownloadDownloading); err != nil {
			return err
		}
		d.DirectURL, d.Size, d.Filename, d.RelPath = directURL, size, filename, relPath
		now := e.now()
		d.StartedAt = &now
		return nil
	})
	if err != nil || nd.State != domain.DownloadDownloading {
		return
	}
	d = nd

	// 2. Fetch.
	dest := filepath.Join(service.TorrentDir(e.cfg.DownloadDir, t), filepath.FromSlash(d.RelPath))
	conns := e.cfg.ConnectionsPerDownload
	if c := prov.Caps().MaxConnections; c > 0 && c < conns {
		conns = c
	}
	lastSave := time.Now()
	opts := fetch.Options{
		Connections: conns, Retries: 3, Limiter: e.limiter, ExpectedSize: d.Size,
		ProgressInterval: 500 * time.Millisecond, RequestTimeout: 2 * time.Minute,
		Progress: func(done, _ int64) {
			// Persist and announce progress ~1×/s so the UI's progress bar
			// advances smoothly; without the event the browser only learns of
			// progress on a state change or the next provider poll.
			if time.Since(lastSave) < time.Second {
				return
			}
			lastSave = time.Now()
			if err := e.store.UpdateDownloadProgress(sctx, sqlcgen.UpdateDownloadProgressParams{BytesDone: done, UpdatedAt: store.FormatTime(e.now()), ID: d.ID}); err == nil {
				e.events.Publish(events.Event{Type: events.DownloadUpdated, TorrentID: d.TorrentID, DownloadID: d.ID})
			}
		},
	}
	res, err := e.fetcher(ctx, d.DirectURL, dest, opts)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// Cancelled (delete/shutdown): back to pending; .part stays for resume.
			_, _ = e.mutateDL(sctx, d.ID, func(d *domain.Download) error {
				if d.State != domain.DownloadDownloading {
					return store.ErrSkip
				}
				d.State = domain.DownloadPending
				d.StartedAt = nil
				return nil
			})
			return
		}
		var he *fetch.HTTPError
		if errors.As(err, &he) && he.LinkExpired() {
			// Direct URL went stale: drop it so the next attempt re-unrestricts.
			e.downloadFailed(sctx, d.ID, fmt.Errorf("link expired (%w)", err), true, true)
			return
		}
		e.downloadFailed(sctx, d.ID, err, !errors.Is(err, fetch.ErrSizeMismatch) || d.RetryCount == 0, false)
		return
	}
	_, _ = e.mutateDL(sctx, d.ID, func(d *domain.Download) error {
		if d.State != domain.DownloadDownloading {
			return store.ErrSkip
		}
		d.BytesDone = res.Size
		if d.Size == 0 {
			d.Size = res.Size
		}
		now := e.now()
		d.FinishedAt = &now
		return d.Transition(domain.DownloadDownloaded)
	})
}

// downloadFailed records an error, re-queuing with backoff while retries remain.
func (e *Engine) downloadFailed(ctx context.Context, id string, cause error, retryable, clearURL bool) {
	// Look up the torrent's retry budget BEFORE opening the transaction: the
	// store has a single connection, so querying inside MutateDownload deadlocks.
	maxRetries := domain.DefaultTorrentSettings().DownloadRetries
	if row, err := e.store.GetDownload(ctx, id); err == nil {
		if t, terr := e.svc.GetTorrent(ctx, row.TorrentID); terr == nil {
			maxRetries = t.Torrent.Settings.DownloadRetries
		}
	}
	d, err := e.mutateDL(ctx, id, func(d *domain.Download) error {
		if d.State != domain.DownloadUnrestricting && d.State != domain.DownloadDownloading {
			return store.ErrSkip
		}
		d.Error = cause.Error()
		if clearURL {
			d.DirectURL = ""
		}
		if retryable && d.RetryCount < maxRetries {
			d.RetryCount++
			d.State = domain.DownloadPending
			d.StartedAt = nil
		} else {
			d.State = domain.DownloadError
		}
		return nil
	})
	if err != nil {
		e.log.Error("save download failure", "id", id, "err", err)
		return
	}
	if d.State == domain.DownloadPending {
		e.log.Warn("download failed, will retry", "id", id, "attempt", d.RetryCount, "err", cause)
	} else {
		e.log.Warn("download failed", "id", id, "err", cause)
	}
}

// startUnpack claims the download (downloaded → unpacking) and launches extraction.
func (e *Engine) startUnpack(ctx context.Context, t *domain.Torrent, d *domain.Download) error {
	settings, err := e.svc.GetSettings(ctx)
	if err != nil {
		return err
	}
	nd, err := e.mutateDL(ctx, d.ID, func(d *domain.Download) error {
		if d.State != domain.DownloadDownloaded {
			return store.ErrSkip
		}
		if err := d.Transition(domain.DownloadUnpacking); err != nil {
			return err
		}
		now := e.now()
		d.UnpackStartedAt = &now
		return nil
	})
	if err != nil || nd.State != domain.DownloadUnpacking {
		return err
	}
	tt := *t
	e.startJob(ctx, d.ID, t.ID, jobUnpack, func(jctx context.Context) { e.runUnpack(jctx, tt, nd, settings.UnpackMaxDepth) })
	return nil
}

func (e *Engine) runUnpack(ctx context.Context, t domain.Torrent, d domain.Download, maxDepth int) {
	sctx := context.WithoutCancel(ctx)
	dir := service.TorrentDir(e.cfg.DownloadDir, t)
	archive := filepath.Join(dir, filepath.FromSlash(d.RelPath))
	dest := filepath.Dir(archive)
	res, err := e.unpacker(ctx, archive, dest, unpack.Options{MaxDepth: maxDepth, DeleteArchives: true, Overwrite: true})
	now := e.now()
	_, _ = e.mutateDL(sctx, d.ID, func(d *domain.Download) error {
		if d.State != domain.DownloadUnpacking {
			return store.ErrSkip
		}
		switch {
		case err == nil:
			e.log.Info("unpacked", "id", d.ID, "files", len(res.Files), "bytes", res.Bytes)
			// Result paths are relative to the archive's directory; store them
			// relative to the torrent dir for per-file deletion.
			base := path.Dir(d.RelPath)
			d.ExtractedPaths = make([]string, 0, len(res.Files))
			for _, f := range res.Files {
				d.ExtractedPaths = append(d.ExtractedPaths, path.Join(base, filepath.ToSlash(f)))
			}
			d.UnpackFinishedAt, d.CompletedAt, d.State = &now, &now, domain.DownloadDone
		case errors.Is(err, context.Canceled):
			d.State, d.UnpackStartedAt = domain.DownloadDownloaded, nil
		case errors.Is(err, unpack.ErrNotArchive):
			// Looked like an archive by name but isn't: keep the file as-is.
			d.UnpackFinishedAt, d.CompletedAt, d.State = &now, &now, domain.DownloadDone
		default:
			e.log.Warn("unpack failed", "id", d.ID, "err", err)
			d.Error, d.State = "unpack: "+err.Error(), domain.DownloadError
		}
		return nil
	})
	if err == nil {
		_ = os.Remove(archive + ".part") // belt and braces
	}
}
