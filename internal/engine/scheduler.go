package engine

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/service"
)

// tick is one scheduling pass. It never blocks on provider or disk I/O:
// anything slow is handed to startJob.
func (e *Engine) tick(ctx context.Context) error {
	torrents, err := e.loadTorrents(ctx)
	if err != nil {
		return err
	}
	now := e.now()
	for i := range torrents {
		t := &torrents[i]
		var err error
		switch t.Status {
		case domain.TorrentQueued:
			err = e.tickQueued(ctx, t, now)
		case domain.TorrentAdding:
			// in flight; nothing to do
		case domain.TorrentProcessing, domain.TorrentWaitingSelection, domain.TorrentDownloading, domain.TorrentUploading:
			err = e.tickAtProvider(ctx, t, now)
		case domain.TorrentFinished:
			err = e.tickFinished(ctx, t, now)
		case domain.TorrentCompleted:
			err = e.tickCompleted(ctx, t, now)
		case domain.TorrentError:
			err = e.tickError(ctx, t, now)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			e.log.Error("tick torrent", "id", t.ID, "status", t.Status, "err", err)
		}
	}
	return nil
}

// tickQueued submits a queued torrent to its provider (or adopts an existing
// provider torrent with the same hash).
func (e *Engine) tickQueued(ctx context.Context, t *domain.Torrent, now time.Time) error {
	if t.RetryAt != nil && now.Before(*t.RetryAt) {
		return nil
	}
	if e.hasJob("add:" + t.ID) {
		return nil
	}
	prov, acc, err := e.svc.Providers().For(ctx, t.AccountID)
	if err != nil {
		return e.fail(ctx, t, "provider unavailable: "+err.Error())
	}
	if !acc.Enabled {
		t.StatusReason = "account disabled"
		return e.saveTorrent(ctx, t)
	}

	// Dedupe against the last provider listing: if the hash is already there
	// (previous timed-out add, or added via the provider UI), adopt it.
	e.mu.Lock()
	snap, ok := e.lastList[t.AccountID]
	e.mu.Unlock()
	if ok {
		if pt, found := snap.byHash[t.Hash]; found {
			if pt.Status != domain.TorrentError {
				return e.adopt(ctx, t, pt, prov.Caps())
			}
			// A dead copy is sitting at the provider; clear it so the add is clean.
			e.forgetProviderTorrent(t.AccountID, pt.ID)
			tt := *t
			e.startJob(ctx, "add:"+t.ID, t.ID, jobAdd, func(jctx context.Context) {
				if err := prov.Delete(jctx, pt.ID); err != nil && provider.KindOf(err) != provider.ErrNotFound {
					e.log.Warn("delete dead provider torrent before add", "id", tt.ID, "err", err)
				}
			})
			return nil
		}
	}

	if err := t.Transition(domain.TorrentAdding, "submitting to provider"); err != nil {
		return err
	}
	t.RetryAt = nil
	if err := e.saveTorrent(ctx, t); err != nil {
		return err
	}
	tt := *t
	e.startJob(ctx, "add:"+t.ID, t.ID, jobAdd, func(jctx context.Context) { e.runAdd(jctx, tt, prov) })
	return nil
}

// adopt links a local torrent to an existing provider torrent.
func (e *Engine) adopt(ctx context.Context, t *domain.Torrent, pt provider.Torrent, caps provider.Caps) error {
	t.ProviderID = pt.ID
	now := e.now()
	t.ProviderAddedAt = &now
	t.RetryAt = nil
	// Move out of queued so applyProviderState can take it from here.
	_ = t.Transition(domain.TorrentAdding, "found existing torrent at provider")
	_ = t.Transition(domain.TorrentProcessing, "found existing torrent at provider")
	if err := e.saveTorrent(ctx, t); err != nil {
		return err
	}
	return e.applyProviderState(ctx, t, pt, caps)
}

// runAdd performs the provider add call (in a job goroutine).
func (e *Engine) runAdd(ctx context.Context, t domain.Torrent, prov provider.Provider) {
	actx, cancel := context.WithTimeout(ctx, e.cfg.AddTimeout)
	defer cancel()
	var res provider.AddResult
	var err error
	switch t.PayloadKind {
	case domain.PayloadMagnet:
		res, err = prov.AddMagnet(actx, string(t.Payload))
	default:
		res, err = prov.AddTorrentFile(actx, t.Payload)
	}
	// Re-load: the torrent may have been deleted meanwhile.
	cur, lerr := e.svc.GetTorrent(context.WithoutCancel(ctx), t.ID)
	if lerr != nil {
		if res.ID != "" && !service.IsNotFound(lerr) {
			e.log.Warn("add: torrent vanished after provider add", "id", t.ID)
		}
		return
	}
	tt := cur.Torrent
	if tt.Status != domain.TorrentAdding {
		return // deleted/retried concurrently
	}
	sctx := context.WithoutCancel(ctx)
	if err != nil {
		kind := provider.KindOf(err)
		switch {
		case errors.Is(err, context.DeadlineExceeded) || (kind == provider.ErrTransient && strings.Contains(err.Error(), "deadline")):
			// Unknown outcome: re-queue; dedupe-by-hash adopts it if it went through.
			at := e.now().Add(e.cfg.PollInterval + 5*time.Second)
			tt.RetryAt = &at
			_ = tt.Transition(domain.TorrentQueued, "add timed out; reconciling with provider")
			e.log.Warn("add timed out", "id", t.ID)
		case kind == provider.ErrPermanent || kind == provider.ErrAuth:
			_ = e.fail(sctx, &tt, "provider rejected torrent: "+err.Error())
			return
		case kind == provider.ErrLimit, kind == provider.ErrRateLimited:
			wait := provider.RetryAfter(err)
			if wait <= 0 {
				wait = 5 * time.Minute
			}
			at := e.now().Add(wait)
			tt.RetryAt = &at
			_ = tt.Transition(domain.TorrentQueued, "provider limit reached; retrying at "+at.Format(time.Kitchen)+": "+err.Error())
		default:
			tt.RetryCount++
			at := e.now().Add(e.backoff(tt.RetryCount - 1))
			tt.RetryAt = &at
			_ = tt.Transition(domain.TorrentQueued, "add failed, retrying: "+err.Error())
		}
		if err := e.saveTorrent(sctx, &tt); err != nil {
			e.log.Error("save after add failure", "err", err)
		}
		return
	}
	now := e.now()
	tt.ProviderID = res.ID
	tt.ProviderAddedAt = &now
	tt.RetryAt = nil
	if res.Hash != "" && res.Hash != tt.Hash {
		e.log.Warn("provider hash differs", "local", tt.Hash, "provider", res.Hash)
	}
	_ = tt.Transition(domain.TorrentProcessing, "accepted by provider")
	if err := e.saveTorrent(sctx, &tt); err != nil {
		e.log.Error("save after add", "err", err)
	}
	// Pull the full record right away so files/status are known without
	// waiting for the next poll.
	if pt, err := prov.GetTorrent(sctx, res.ID); err == nil {
		_ = e.applyProviderState(sctx, &tt, pt, prov.Caps())
	}
}

// tickAtProvider handles torrents the provider is working on: lifetime
// timeouts and file selection.
func (e *Engine) tickAtProvider(ctx context.Context, t *domain.Torrent, now time.Time) error {
	if t.Settings.Lifetime > 0 && now.Sub(t.AddedAt) > t.Settings.Lifetime {
		return e.fail(ctx, t, "lifetime exceeded before provider finished")
	}
	if t.Status != domain.TorrentWaitingSelection || t.FilesSelectedAt != nil {
		return nil
	}
	prov, _, err := e.svc.Providers().For(ctx, t.AccountID)
	if err != nil {
		return err
	}
	if !prov.Caps().SelectFiles {
		return nil
	}
	if e.hasJob("select:" + t.ID) {
		return nil
	}
	tt := *t
	e.startJob(ctx, "select:"+t.ID, t.ID, jobAdd, func(jctx context.Context) { e.runSelect(jctx, tt, prov) })
	return nil
}

// runSelect fetches files if needed, applies filters and selects at the provider.
func (e *Engine) runSelect(ctx context.Context, t domain.Torrent, prov provider.Provider) {
	sctx := context.WithoutCancel(ctx)
	if len(t.Files) == 0 {
		pt, err := prov.GetTorrent(ctx, t.ProviderID)
		if err != nil {
			e.log.Warn("select: get torrent", "id", t.ID, "err", err)
			return
		}
		t.Files = pt.Files
		if len(t.Files) == 0 {
			return // metadata not ready yet; try again next tick
		}
	}
	selected, err := domain.SelectFiles(t.Files, t.Settings)
	if err != nil {
		_ = e.fail(sctx, &t, err.Error())
		return
	}
	ids := make([]string, len(selected))
	sel := map[string]bool{}
	for i, f := range selected {
		ids[i] = f.ID
		sel[f.ID] = true
	}
	if err := prov.SelectFiles(ctx, t.ProviderID, ids); err != nil {
		if !provider.IsRetryable(err) {
			_ = e.fail(sctx, &t, "select files: "+err.Error())
		} else {
			e.log.Warn("select files", "id", t.ID, "err", err)
		}
		return
	}
	for i := range t.Files {
		t.Files[i].Selected = sel[t.Files[i].ID]
	}
	now := e.now()
	t.FilesSelectedAt = &now
	t.StatusReason = "files selected"
	_ = e.saveTorrent(sctx, &t)
}

// tickFinished creates downloads once links are available, starts downloads
// and unpacks within limits, and completes the torrent when everything is done.
func (e *Engine) tickFinished(ctx context.Context, t *domain.Torrent, now time.Time) error {
	downloads, err := e.loadDownloads(ctx, t.ID)
	if err != nil {
		return err
	}
	if len(downloads) == 0 {
		if e.hasJob("links:" + t.ID) {
			return nil
		}
		if t.ProviderEndedAt != nil && now.Sub(*t.ProviderEndedAt) > e.cfg.LinksTimeout && t.StatusReason == "waiting for download links" {
			return e.fail(ctx, t, "no download links from provider after "+e.cfg.LinksTimeout.String())
		}
		prov, _, err := e.svc.Providers().For(ctx, t.AccountID)
		if err != nil {
			return err
		}
		tt := *t
		e.startJob(ctx, "links:"+t.ID, t.ID, jobAdd, func(jctx context.Context) { e.runCreateDownloads(jctx, tt, prov) })
		return nil
	}

	var pending, active, done, failed int
	for i := range downloads {
		d := &downloads[i]
		switch d.State {
		case domain.DownloadPending:
			pending++
			if e.countJobs(jobDownload) >= e.cfg.DownloadLimit {
				continue
			}
			if d.RetryCount > 0 && now.Before(d.UpdatedAt.Add(e.backoff(d.RetryCount-1))) {
				continue
			}
			if e.hasJob(d.ID) {
				continue
			}
			if err := e.startDownload(ctx, t, d); err != nil {
				return err
			}
			active++
		case domain.DownloadDownloaded:
			if !t.Settings.Unpack || e.cfg.UnpackLimit == 0 || !isArchive(d.Filename) {
				if err := d.Transition(domain.DownloadDone); err == nil {
					d.CompletedAt = &now
					if err := e.saveDownload(ctx, d); err != nil {
						return err
					}
					done++
				}
				continue
			}
			if e.hasJob(d.ID) || e.countJobs(jobUnpack) >= e.cfg.UnpackLimit {
				active++
				continue
			}
			if err := e.startUnpack(ctx, t, d); err != nil {
				return err
			}
			active++
		case domain.DownloadUnrestricting, domain.DownloadDownloading, domain.DownloadUnpacking:
			active++
		case domain.DownloadDone:
			done++
		case domain.DownloadError:
			failed++
		}
	}
	if pending > 0 || active > 0 {
		if t.StatusReason != "downloading" {
			t.StatusReason = "downloading"
			return e.saveTorrent(ctx, t)
		}
		return nil
	}
	if failed > 0 {
		return e.fail(ctx, t, "one or more downloads failed")
	}
	if done == len(downloads) {
		if err := t.Transition(domain.TorrentCompleted, "all downloads complete"); err != nil {
			return err
		}
		t.CompletedAt = &now
		t.Error = ""
		e.log.Info("torrent completed", "id", t.ID, "name", t.Name)
		return e.saveTorrent(ctx, t)
	}
	return nil
}

// runCreateDownloads fetches links for a finished torrent and inserts download rows.
func (e *Engine) runCreateDownloads(ctx context.Context, t domain.Torrent, prov provider.Provider) {
	sctx := context.WithoutCancel(ctx)
	if len(t.Files) == 0 {
		pt, err := prov.GetTorrent(ctx, t.ProviderID)
		if err != nil {
			e.log.Warn("create downloads: get torrent", "id", t.ID, "err", err)
			return
		}
		t.Files = pt.Files
	}
	links, err := prov.Links(ctx, t.ProviderID)
	if err != nil {
		if provider.KindOf(err) == provider.ErrNotFound {
			_ = e.fail(sctx, &t, "torrent disappeared at provider before links were fetched")
		} else {
			e.log.Warn("links", "id", t.ID, "err", err)
		}
		return
	}
	if len(links) == 0 {
		if t.StatusReason != "waiting for download links" {
			t.StatusReason = "waiting for download links"
			_ = e.saveTorrent(sctx, &t)
		}
		return
	}
	// Decide which files to download. Providers that track selection already
	// restricted the links; otherwise apply our filters now.
	var wanted map[string]bool
	if prov.Caps().SelectFiles {
		wanted = map[string]bool{}
		for _, f := range t.Files {
			if f.Selected {
				wanted[f.ID] = true
			}
		}
		if len(wanted) == 0 { // selection unknown (adopted torrent): take all linked files
			wanted = nil
		}
	} else {
		selected, err := domain.SelectFiles(linkFiles(links, t.Files), t.Settings)
		if err != nil {
			_ = e.fail(sctx, &t, err.Error())
			return
		}
		wanted = map[string]bool{}
		for _, f := range selected {
			wanted[f.ID] = true
		}
	}
	now := e.now()
	n := 0
	for _, l := range links {
		if wanted != nil && !wanted[l.FileID] {
			continue
		}
		rel := relPath(t.Name, l.Path)
		d := domain.Download{
			ID: newID(), TorrentID: t.ID, FileID: l.FileID, ProviderLink: l.URL, RelPath: rel, Filename: path.Base(rel),
			Size: l.Size, State: domain.DownloadPending, QueuedAt: now, UpdatedAt: now,
		}
		if err := e.store.InsertDownload(sctx, downloadInsert(d)); err != nil {
			e.log.Error("insert download", "err", err)
			return
		}
		n++
	}
	if n == 0 {
		_ = e.fail(sctx, &t, "no files matched the filters")
		return
	}
	if t.FilesSelectedAt == nil {
		t.FilesSelectedAt = &now
	}
	t.StatusReason = "downloading"
	_ = e.saveTorrent(sctx, &t)
	e.events.Publish(events.Event{Type: events.TorrentUpdated, TorrentID: t.ID})
}

// linkFiles builds domain.Files for filtering from provider links, using the
// torrent's file list for sizes when the link lacks them.
func linkFiles(links []provider.Link, files []domain.File) []domain.File {
	size := map[string]int64{}
	for _, f := range files {
		size[f.ID] = f.Size
	}
	out := make([]domain.File, len(links))
	for i, l := range links {
		s := l.Size
		if s == 0 {
			s = size[l.FileID]
		}
		out[i] = domain.File{ID: l.FileID, Path: l.Path, Size: s, Link: l.URL}
	}
	return out
}

// relPath strips a leading "<torrent name>/" from a provider file path since
// TorrentDir already ends with the torrent name.
func relPath(torrentName, p string) string {
	p = strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "/")
	if torrentName != "" {
		if rest, ok := strings.CutPrefix(p, torrentName+"/"); ok && rest != "" {
			p = rest
		}
	}
	// Never allow traversal from provider-supplied paths.
	parts := strings.Split(p, "/")
	clean := parts[:0]
	for _, s := range parts {
		if s == "" || s == "." || s == ".." {
			continue
		}
		clean = append(clean, service.SanitizeName(s))
	}
	if len(clean) == 0 {
		return "file"
	}
	return strings.Join(clean, "/")
}

// tickCompleted applies the finished action after its delay.
func (e *Engine) tickCompleted(ctx context.Context, t *domain.Torrent, now time.Time) error {
	if t.Settings.FinishedAction != domain.FinishedRemoveFromProvider || t.ProviderID == "" || t.CompletedAt == nil {
		return nil
	}
	if now.Before(t.CompletedAt.Add(t.Settings.FinishedDelay)) {
		return nil
	}
	if e.hasJob("finish:" + t.ID) {
		return nil
	}
	prov, _, err := e.svc.Providers().For(ctx, t.AccountID)
	if err != nil {
		return err
	}
	tt := *t
	e.startJob(ctx, "finish:"+t.ID, t.ID, jobAdd, func(jctx context.Context) {
		err := prov.Delete(jctx, tt.ProviderID)
		if err != nil && provider.KindOf(err) != provider.ErrNotFound {
			e.log.Warn("finished action: delete at provider", "id", tt.ID, "err", err)
			return
		}
		sctx := context.WithoutCancel(jctx)
		cur, err := e.svc.GetTorrent(sctx, tt.ID)
		if err != nil {
			return
		}
		c := cur.Torrent
		c.ProviderID = ""
		c.StatusReason = "removed from provider"
		_ = e.saveTorrent(sctx, &c)
	})
	return nil
}

// tickError applies delete-on-error.
func (e *Engine) tickError(ctx context.Context, t *domain.Torrent, now time.Time) error {
	if t.Settings.DeleteOnError <= 0 || t.CompletedAt == nil || now.Before(t.CompletedAt.Add(t.Settings.DeleteOnError)) {
		return nil
	}
	e.log.Info("delete-on-error", "id", t.ID, "name", t.Name)
	return e.svc.DeleteTorrent(ctx, t.ID, service.DeleteOptions{DeleteFiles: true, DeleteFromProvider: true})
}
