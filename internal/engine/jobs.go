package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/domain"
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

// startDownload claims the download (pending → unrestricting) and launches its job.
func (e *Engine) startDownload(ctx context.Context, t *domain.Torrent, d *domain.Download) error {
	prov, _, err := e.svc.Providers().For(ctx, t.AccountID)
	if err != nil {
		return err
	}
	if err := d.Transition(domain.DownloadUnrestricting); err != nil {
		return err
	}
	d.Error = ""
	if err := e.saveDownload(ctx, d); err != nil {
		return err
	}
	tt, dd := *t, *d
	e.startJob(ctx, d.ID, t.ID, jobDownload, func(jctx context.Context) { e.runDownload(jctx, tt, dd, prov) })
	return nil
}

// runDownload unrestricts the link (if needed), fetches the file and records the outcome.
func (e *Engine) runDownload(ctx context.Context, t domain.Torrent, d domain.Download, prov provider.Provider) {
	sctx := context.WithoutCancel(ctx)
	finish := func(d *domain.Download) {
		if err := e.saveDownload(sctx, d); err != nil {
			e.log.Error("save download", "id", d.ID, "err", err)
		}
	}

	// 1. Resolve a direct URL.
	if d.DirectURL == "" {
		if prov.Caps().DirectLinks {
			d.DirectURL = d.ProviderLink
		} else {
			direct, err := prov.Unrestrict(ctx, d.ProviderLink)
			if err != nil {
				e.downloadFailed(sctx, &d, fmt.Errorf("unrestrict: %w", err), provider.IsRetryable(err))
				return
			}
			d.DirectURL = direct.URL
			if direct.Size > 0 {
				d.Size = direct.Size
			}
			if direct.Filename != "" && d.Filename == "" {
				d.Filename = direct.Filename
			}
		}
	}
	if err := d.Transition(domain.DownloadDownloading); err != nil {
		e.log.Error("download transition", "err", err)
		return
	}
	now := e.now()
	d.StartedAt = &now
	finish(&d)

	// 2. Fetch.
	dest := filepath.Join(service.TorrentDir(e.cfg.DownloadDir, t), filepath.FromSlash(d.RelPath))
	conns := e.cfg.ConnectionsPerDownload
	if c := prov.Caps().MaxConnections; c > 0 && c < conns {
		conns = c
	}
	lastSave := time.Now()
	opts := fetch.Options{
		Connections: conns, Retries: 3, Limiter: e.limiter, ExpectedSize: d.Size,
		ProgressInterval: time.Second,
		Progress: func(done, _ int64) {
			d.BytesDone = done
			if time.Since(lastSave) < 2*time.Second {
				return
			}
			lastSave = time.Now()
			_ = e.store.UpdateDownloadProgress(sctx, sqlcgen.UpdateDownloadProgressParams{BytesDone: done, UpdatedAt: store.FormatTime(e.now()), ID: d.ID})
		},
	}
	res, err := e.fetcher(ctx, d.DirectURL, dest, opts)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// Cancelled (delete/shutdown): leave the row for recovery; .part stays for resume.
			d.State = domain.DownloadPending
			d.StartedAt = nil
			finish(&d)
			return
		}
		var he *fetch.HTTPError
		if errors.As(err, &he) && he.LinkExpired() {
			// Direct URL went stale: drop it so the next attempt re-unrestricts.
			d.DirectURL = ""
			e.downloadFailed(sctx, &d, fmt.Errorf("link expired (%w)", err), true)
			return
		}
		e.downloadFailed(sctx, &d, err, !errors.Is(err, fetch.ErrSizeMismatch) || d.RetryCount == 0)
		return
	}
	d.BytesDone = res.Size
	if d.Size == 0 {
		d.Size = res.Size
	}
	now = e.now()
	d.FinishedAt = &now
	if err := d.Transition(domain.DownloadDownloaded); err != nil {
		e.log.Error("download transition", "err", err)
	}
	finish(&d)
}

// downloadFailed records an error, re-queuing with backoff while retries remain.
func (e *Engine) downloadFailed(ctx context.Context, d *domain.Download, err error, retryable bool) {
	d.Error = err.Error()
	t, terr := e.svc.GetTorrent(ctx, d.TorrentID)
	maxRetries := domain.DefaultTorrentSettings().DownloadRetries
	if terr == nil {
		maxRetries = t.Torrent.Settings.DownloadRetries
	}
	if retryable && d.RetryCount < maxRetries {
		d.RetryCount++
		d.State = domain.DownloadPending
		d.StartedAt = nil
		e.log.Warn("download failed, will retry", "id", d.ID, "attempt", d.RetryCount, "err", err)
	} else {
		d.State = domain.DownloadError
		e.log.Warn("download failed", "id", d.ID, "err", err)
	}
	if serr := e.saveDownload(ctx, d); serr != nil {
		e.log.Error("save download", "err", serr)
	}
}

// startUnpack claims the download (downloaded → unpacking) and launches extraction.
func (e *Engine) startUnpack(ctx context.Context, t *domain.Torrent, d *domain.Download) error {
	if err := d.Transition(domain.DownloadUnpacking); err != nil {
		return err
	}
	now := e.now()
	d.UnpackStartedAt = &now
	if err := e.saveDownload(ctx, d); err != nil {
		return err
	}
	settings, err := e.svc.GetSettings(ctx)
	if err != nil {
		return err
	}
	tt, dd := *t, *d
	e.startJob(ctx, d.ID, t.ID, jobUnpack, func(jctx context.Context) { e.runUnpack(jctx, tt, dd, settings.UnpackMaxDepth) })
	return nil
}

func (e *Engine) runUnpack(ctx context.Context, t domain.Torrent, d domain.Download, maxDepth int) {
	sctx := context.WithoutCancel(ctx)
	dir := service.TorrentDir(e.cfg.DownloadDir, t)
	archive := filepath.Join(dir, filepath.FromSlash(d.RelPath))
	dest := filepath.Dir(archive)
	res, err := e.unpacker(ctx, archive, dest, unpack.Options{MaxDepth: maxDepth, DeleteArchives: true, Overwrite: true})
	now := e.now()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			d.State = domain.DownloadDownloaded
			d.UnpackStartedAt = nil
			_ = e.saveDownload(sctx, &d)
			return
		}
		if errors.Is(err, unpack.ErrNotArchive) {
			// Looked like an archive by name but isn't: keep the file as-is.
			d.UnpackFinishedAt = &now
			d.CompletedAt = &now
			d.State = domain.DownloadDone
			_ = e.saveDownload(sctx, &d)
			return
		}
		d.Error = "unpack: " + err.Error()
		d.State = domain.DownloadError
		e.log.Warn("unpack failed", "id", d.ID, "err", err)
		_ = e.saveDownload(sctx, &d)
		return
	}
	e.log.Info("unpacked", "id", d.ID, "files", len(res.Files), "bytes", res.Bytes)
	d.UnpackFinishedAt = &now
	d.CompletedAt = &now
	d.State = domain.DownloadDone
	_ = e.saveDownload(sctx, &d)
	_ = os.Remove(archive + ".part") // belt and braces
}
