package engine

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
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
			e.tickError(ctx, t, now)
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
	if !e.canRun("add:"+t.ID, now) {
		return nil
	}
	prov, acc, err := e.svc.Providers().For(ctx, t.AccountID)
	if err != nil {
		return e.fail(ctx, t, "provider unavailable: "+err.Error())
	}
	if !acc.Enabled {
		if t.StatusReason != "account disabled" {
			_, err := e.mutate(ctx, t.ID, func(t *domain.Torrent) error { t.StatusReason = "account disabled"; return nil })
			return err
		}
		return nil
	}

	// Dedupe against the last provider listing: if the hash is already there
	// (previous timed-out add, or added via the provider UI), adopt it.
	if pt, found := e.lookupHash(t.AccountID, t.Hash); found {
		if pt.Status != domain.TorrentError {
			return e.adopt(ctx, t, pt, prov.Caps())
		}
		// A dead copy is sitting at the provider; clear it so the add is clean.
		// The listing entry is forgotten only once the delete went through.
		accountID, key := t.AccountID, "add:"+t.ID
		e.startJob(ctx, key, t.ID, jobAdd, func(jctx context.Context) {
			err := prov.Delete(jctx, pt.ID)
			if err != nil && provider.KindOf(err) != provider.ErrNotFound {
				e.log.Warn("delete dead provider torrent before add", "provider_id", pt.ID, "err", err)
				e.deferKey(key, e.now().Add(e.backoff(0)))
				return
			}
			e.forgetProviderTorrent(accountID, pt.ID)
		})
		return nil
	}

	tt, err := e.mutate(ctx, t.ID, func(t *domain.Torrent) error {
		if t.Status != domain.TorrentQueued {
			return store.ErrSkip // changed under us
		}
		if err := t.Transition(domain.TorrentAdding, "submitting to provider"); err != nil {
			return err
		}
		t.RetryAt = nil
		return nil
	})
	if err != nil || tt.Status != domain.TorrentAdding {
		return err
	}
	e.startJob(ctx, "add:"+t.ID, t.ID, jobAdd, func(jctx context.Context) { e.runAdd(jctx, tt, prov) })
	return nil
}

// adopt links a local torrent to an existing provider torrent.
func (e *Engine) adopt(ctx context.Context, t *domain.Torrent, pt provider.Torrent, caps provider.Caps) error {
	nt, err := e.mutate(ctx, t.ID, func(t *domain.Torrent) error {
		if t.Status != domain.TorrentQueued {
			return store.ErrSkip
		}
		t.ProviderID = pt.ID
		now := e.now()
		t.ProviderAddedAt = &now
		t.RetryAt = nil
		// Move out of queued so applyProviderState can take it from here.
		return t.Transition(domain.TorrentProcessing, "found existing torrent at provider")
	})
	if err != nil {
		return err
	}
	*t = nt
	return e.applyProviderState(ctx, t, pt, caps)
}

// maxAddFailures bounds consecutive transient add failures before giving up.
const maxAddFailures = 10

func (e *Engine) bumpAddFails(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.addFails[id]++
	return e.addFails[id]
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
	sctx := context.WithoutCancel(ctx)
	if err != nil {
		kind := provider.KindOf(err)
		var failMsg string
		tt, merr := e.mutate(sctx, t.ID, func(tt *domain.Torrent) error {
			if tt.Status != domain.TorrentAdding {
				return store.ErrSkip // deleted/retried concurrently
			}
			switch {
			case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), ctx.Err() != nil:
				// Unknown outcome (timeout or shutdown): re-queue without penalty;
				// dedupe-by-hash adopts it if it went through.
				at := e.now().Add(e.cfg.PollInterval + 5*time.Second)
				tt.RetryAt = &at
				_ = tt.Transition(domain.TorrentQueued, "add interrupted; reconciling with provider")
				e.log.Warn("add interrupted", "id", t.ID, "err", err)
			case kind == provider.ErrPermanent || kind == provider.ErrAuth:
				failMsg = "provider rejected torrent: " + err.Error()
				return store.ErrSkip
			case kind == provider.ErrLimit, kind == provider.ErrRateLimited:
				wait := provider.RetryAfter(err)
				if wait <= 0 {
					wait = 5 * time.Minute
				}
				at := e.now().Add(wait)
				tt.RetryAt = &at
				_ = tt.Transition(domain.TorrentQueued, "provider limit reached; retrying at "+at.Format(time.Kitchen)+": "+err.Error())
			default:
				// Transient: exponential backoff on an in-memory counter (the
				// persisted RetryCount is the provider-side retry budget).
				n := e.bumpAddFails(t.ID)
				if n > maxAddFailures {
					failMsg = "provider add keeps failing: " + err.Error()
					return store.ErrSkip
				}
				at := e.now().Add(e.backoff(n - 1))
				tt.RetryAt = &at
				_ = tt.Transition(domain.TorrentQueued, "add failed, retrying: "+err.Error())
			}
			return nil
		})
		if merr != nil {
			if !service.IsNotFound(merr) && !store.IsNotFound(merr) {
				e.log.Error("save after add failure", "err", merr)
			}
			return
		}
		if failMsg != "" && tt.Status == domain.TorrentAdding {
			_ = e.fail(sctx, &tt, failMsg)
		}
		return
	}
	if res.Hash != "" && res.Hash != t.Hash {
		e.log.Warn("provider hash differs", "local", t.Hash, "provider", res.Hash)
	}
	e.mu.Lock()
	delete(e.addFails, t.ID)
	e.mu.Unlock()
	tt, merr := e.mutate(sctx, t.ID, func(tt *domain.Torrent) error {
		if tt.Status != domain.TorrentAdding {
			return store.ErrSkip
		}
		now := e.now()
		tt.ProviderID = res.ID
		tt.ProviderAddedAt = &now
		tt.RetryAt = nil
		return tt.Transition(domain.TorrentProcessing, "accepted by provider")
	})
	if merr != nil {
		if !store.IsNotFound(merr) {
			e.log.Error("save after add", "err", merr)
		} else if res.ID != "" {
			e.log.Warn("add: torrent deleted locally while adding; it remains at the provider", "id", t.ID, "provider_id", res.ID)
		}
		return
	}
	if tt.Status != domain.TorrentProcessing {
		return // changed under us
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
	key := "select:" + t.ID
	if !e.canRun(key, now) {
		return nil
	}
	tt := *t
	e.startJob(ctx, key, t.ID, jobAdd, func(jctx context.Context) {
		if !e.runSelect(jctx, tt, prov) {
			e.deferKey(key, e.now().Add(e.cfg.PollInterval)) // not ready / transient: try again next poll
		}
	})
	return nil
}

// runSelect fetches files if needed, applies filters and selects at the
// provider. Returns false when nothing could be decided yet (metadata not
// ready, transient error) so the caller throttles the next attempt.
func (e *Engine) runSelect(ctx context.Context, t domain.Torrent, prov provider.Provider) bool {
	sctx := context.WithoutCancel(ctx)
	if len(t.Files) == 0 {
		pt, err := prov.GetTorrent(ctx, t.ProviderID)
		if err != nil {
			e.log.Warn("select: get torrent", "id", t.ID, "err", err)
			return false
		}
		t.Files = pt.Files
		if len(t.Files) == 0 {
			return false // metadata not ready yet
		}
	}
	selected, err := domain.SelectFiles(t.Files, t.Settings)
	if err != nil {
		_ = e.fail(sctx, &t, err.Error())
		return true
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
			return true
		}
		e.log.Warn("select files", "id", t.ID, "err", err)
		return false
	}
	files := t.Files
	_, _ = e.mutate(sctx, t.ID, func(t *domain.Torrent) error {
		if len(t.Files) == 0 {
			t.Files = files
		}
		for i := range t.Files {
			t.Files[i].Selected = sel[t.Files[i].ID]
		}
		now := e.now()
		t.FilesSelectedAt = &now
		t.StatusReason = "files selected"
		return nil
	})
	return true
}

// tickFinished creates downloads once links are available, starts downloads
// and unpacks within limits, and completes the torrent when everything is done.
func (e *Engine) tickFinished(ctx context.Context, t *domain.Torrent, now time.Time) error {
	downloads, err := e.loadDownloads(ctx, t.ID)
	if err != nil {
		return err
	}
	if len(downloads) == 0 {
		if since := e.waitingSince(t.ID, now); now.Sub(since) > e.cfg.LinksTimeout {
			return e.fail(ctx, t, "no download links from provider after "+e.cfg.LinksTimeout.String())
		}
		key := "links:" + t.ID
		if !e.canRun(key, now) {
			return nil
		}
		prov, _, err := e.svc.Providers().For(ctx, t.AccountID)
		if err != nil {
			return err
		}
		tt := *t
		e.startJob(ctx, key, t.ID, jobAdd, func(jctx context.Context) {
			if !e.runCreateDownloads(jctx, tt, prov) {
				e.deferKey(key, e.now().Add(e.cfg.PollInterval)) // links not ready / transient: next poll
			}
		})
		return nil
	}
	e.mu.Lock()
	delete(e.waitSince, t.ID)
	e.mu.Unlock()

	// Archives may span several downloads (.part1.rar + .part2.rar …); only
	// start unpacking once no download of this torrent is still in flight.
	var inflight int
	for i := range downloads {
		switch downloads[i].State {
		case domain.DownloadPending, domain.DownloadUnrestricting, domain.DownloadDownloading:
			inflight++
		}
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
				if _, err := e.mutateDL(ctx, d.ID, func(d *domain.Download) error {
					if err := d.Transition(domain.DownloadDone); err != nil {
						return store.ErrSkip
					}
					d.CompletedAt = &now
					return nil
				}); err != nil {
					return err
				}
				done++
				continue
			}
			if inflight > 0 || e.hasJob(d.ID) || e.countJobs(jobUnpack) >= e.cfg.UnpackLimit {
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
			_, err := e.mutate(ctx, t.ID, func(t *domain.Torrent) error { t.StatusReason = "downloading"; return nil })
			return err
		}
		return nil
	}
	if failed > 0 {
		return e.fail(ctx, t, "one or more downloads failed")
	}
	if done == len(downloads) {
		e.log.Info("torrent completed", "id", t.ID, "name", t.Name)
		_, err := e.mutate(ctx, t.ID, func(t *domain.Torrent) error {
			if err := t.Transition(domain.TorrentCompleted, "all downloads complete"); err != nil {
				return store.ErrSkip
			}
			t.CompletedAt = &now
			t.Error = ""
			return nil
		})
		return err
	}
	return nil
}

// runCreateDownloads fetches links for a finished torrent and inserts download
// rows (atomically, skipping links that already have a row). Returns false when
// links weren't available yet or a transient error occurred.
func (e *Engine) runCreateDownloads(ctx context.Context, t domain.Torrent, prov provider.Provider) bool {
	sctx := context.WithoutCancel(ctx)
	if len(t.Files) == 0 {
		pt, err := prov.GetTorrent(ctx, t.ProviderID)
		if err != nil {
			e.log.Warn("create downloads: get torrent", "id", t.ID, "err", err)
			return false
		}
		t.Files = pt.Files
	}
	links, err := prov.Links(ctx, t.ProviderID)
	if err != nil {
		if provider.KindOf(err) == provider.ErrNotFound {
			_ = e.fail(sctx, &t, "torrent disappeared at provider before links were fetched")
			return true
		}
		e.log.Warn("links", "id", t.ID, "err", err)
		return false
	}
	if len(links) == 0 {
		if t.StatusReason != "waiting for download links" {
			_, _ = e.mutate(sctx, t.ID, func(t *domain.Torrent) error { t.StatusReason = "waiting for download links"; return nil })
		}
		return false
	}
	// Decide which files to download. Providers that track selection already
	// restricted the links (and may have repacked/split them, so link ids need
	// not match file ids) — download everything they return. Otherwise apply
	// our filters now.
	var wanted map[string]bool
	if !prov.Caps().SelectFiles {
		selected, err := domain.SelectFiles(linkFiles(links, t.Files), t.Settings)
		if err != nil {
			_ = e.fail(sctx, &t, err.Error())
			return true
		}
		wanted = map[string]bool{}
		for _, f := range selected {
			wanted[f.ID] = true
		}
	}
	now := e.now()
	var rows []domain.Download
	for _, l := range links {
		if wanted != nil && !wanted[l.FileID] {
			continue
		}
		rel := relPath(t.Name, l.Path)
		rows = append(rows, domain.Download{
			ID: newID(), TorrentID: t.ID, FileID: l.FileID, ProviderLink: l.URL, RelPath: rel, Filename: path.Base(rel),
			Size: l.Size, State: domain.DownloadPending, QueuedAt: now, UpdatedAt: now,
		})
	}
	if len(rows) == 0 {
		_ = e.fail(sctx, &t, "no files matched the filters")
		return true
	}
	err = e.store.WithTx(sctx, func(q *sqlcgen.Queries) error {
		for _, d := range rows {
			if _, err := q.InsertDownload(sctx, downloadInsert(d)); err != nil { // ON CONFLICT DO NOTHING → idempotent
				return err
			}
		}
		return nil
	})
	if err != nil {
		e.log.Error("insert downloads", "id", t.ID, "err", err)
		return false
	}
	_, _ = e.mutate(sctx, t.ID, func(t *domain.Torrent) error {
		if t.FilesSelectedAt == nil {
			t.FilesSelectedAt = &now
		}
		t.StatusReason = "downloading"
		return nil
	})
	return true
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
	key := "finish:" + t.ID
	if !e.canRun(key, now) {
		return nil
	}
	prov, _, err := e.svc.Providers().For(ctx, t.AccountID)
	if err != nil {
		return err
	}
	tt := *t
	e.startJob(ctx, key, t.ID, jobAdd, func(jctx context.Context) {
		err := prov.Delete(jctx, tt.ProviderID)
		if err != nil && provider.KindOf(err) != provider.ErrNotFound {
			e.log.Warn("finished action: delete at provider", "id", tt.ID, "err", err)
			e.deferKey(key, e.now().Add(e.backoff(1)))
			return
		}
		_, _ = e.mutate(context.WithoutCancel(jctx), tt.ID, func(t *domain.Torrent) error {
			t.ProviderID = ""
			t.StatusReason = "removed from provider"
			return nil
		})
	})
	return nil
}

// tickError applies delete-on-error (in a job: it talks to the provider).
func (e *Engine) tickError(ctx context.Context, t *domain.Torrent, now time.Time) {
	if t.Settings.DeleteOnError <= 0 || t.CompletedAt == nil || now.Before(t.CompletedAt.Add(t.Settings.DeleteOnError)) {
		return
	}
	key := "delete:" + t.ID
	if !e.canRun(key, now) {
		return
	}
	// Not a registered job: DeleteTorrent calls CancelTorrent, which waits for
	// this torrent's jobs and would wait on itself. Guard re-entry with the
	// throttle key instead.
	e.deferKey(key, now.Add(time.Hour))
	id, name := t.ID, t.Name
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.log.Info("delete-on-error", "id", id, "name", name)
		if err := e.svc.DeleteTorrent(context.WithoutCancel(ctx), id, service.DeleteOptions{DeleteFiles: true, DeleteFromProvider: true}); err != nil {
			e.log.Warn("delete-on-error failed", "id", id, "err", err)
			e.deferKey(key, e.now().Add(e.backoff(2)))
			return
		}
		e.mu.Lock()
		delete(e.nextAttempt, key)
		e.mu.Unlock()
	}()
}
