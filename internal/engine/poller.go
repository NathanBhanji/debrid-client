package engine

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

// pollLoop polls every enabled account. Each account is polled at
// PollInterval while it has non-terminal torrents at the provider, otherwise
// at IdlePollInterval (or skipped entirely when it has no torrents at all).
func (e *Engine) pollLoop(ctx context.Context) {
	next := map[string]time.Time{}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := e.store.ListProviderAccounts(ctx)
		if err != nil {
			e.log.Error("poll: list accounts", "err", err)
			continue
		}
		now := e.now()
		for _, r := range rows {
			if r.Enabled == 0 {
				continue
			}
			if when, ok := next[r.ID]; ok && now.Before(when) {
				continue
			}
			active, total, err := e.accountActivity(ctx, r.ID)
			if err != nil {
				e.log.Error("poll: account activity", "err", err)
				continue
			}
			interval := e.cfg.IdlePollInterval
			if active {
				interval = e.cfg.PollInterval
			}
			next[r.ID] = now.Add(interval)
			if total == 0 {
				continue // nothing to reconcile
			}
			if err := e.pollAccount(ctx, r.ID); err != nil && !errors.Is(err, context.Canceled) {
				e.log.Warn("poll account", "account", r.Name, "err", err)
				if provider.KindOf(err) == provider.ErrRateLimited {
					wait := provider.RetryAfter(err)
					if wait <= 0 {
						wait = 2 * time.Minute
					}
					next[r.ID] = now.Add(wait)
				}
			}
		}
	}
}

// accountActivity reports whether the account has torrents needing provider
// updates, and whether it has any torrents at all.
func (e *Engine) accountActivity(ctx context.Context, accountID string) (active bool, total int, err error) {
	rows, err := e.store.ListTorrentsByAccount(ctx, accountID)
	if err != nil {
		return false, 0, err
	}
	for _, r := range rows {
		total++
		st := domain.TorrentStatus(r.Status)
		if st.AtProvider() && !st.IsTerminal() {
			active = true
		}
	}
	return active, total, nil
}

// pollAccount makes one ListTorrents call and reconciles every local torrent
// of the account against it.
func (e *Engine) pollAccount(ctx context.Context, accountID string) error {
	prov, _, err := e.svc.Providers().For(ctx, accountID)
	if err != nil {
		return err
	}
	// Capture local incarnations before network I/O. An old listing must not
	// reconcile a transfer retried or removed while the request was in flight.
	rows, err := e.store.ListTorrentsByAccount(ctx, accountID)
	if err != nil {
		return err
	}
	e.mu.Lock()
	epoch := e.listEpoch[accountID]
	e.mu.Unlock()
	list, err := prov.ListTorrents(ctx)
	if err != nil {
		return err
	}
	snap := listSnapshot{at: e.now(), byHash: map[string]provider.Torrent{}, byID: map[string]provider.Torrent{}}
	for _, pt := range list {
		snap.byID[pt.ID] = pt
		if pt.Hash != "" {
			snap.byHash[pt.Hash] = pt
		}
	}
	e.mu.Lock()
	if e.listEpoch[accountID] != epoch {
		e.mu.Unlock()
		return nil // invalidation won; never republish or reconcile this old response
	}
	e.lastList[accountID] = snap
	e.mu.Unlock()

	for _, row := range rows {
		expected, err := store.TorrentFromRow(row)
		if err != nil {
			return err
		}
		if expected.ProviderID == "" || expected.Status == domain.TorrentAdding || expected.Status.IsTerminal() {
			continue
		}
		// Nonblocking admission skips a busy retry/delete/cancel; the next poll
		// obtains fresh provider data. Registered jobs never acquire this slot.
		err = e.torrentOperation(ctx, expected.ID, func() error {
			row, err := e.store.GetTorrent(ctx, expected.ID)
			if store.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			current, err := store.TorrentFromRow(row)
			if err != nil {
				return err
			}
			if current.Status.IsTerminal() || current.Status == domain.TorrentQueued || current.Status == domain.TorrentAdding || current.AccountID != expected.AccountID || current.ProviderID != expected.ProviderID || current.RetryCount != expected.RetryCount {
				return nil
			}
			if err := e.reconcileProviderSnapshot(ctx, &current, snap, prov.Caps()); err != nil {
				return err
			}
			// Polling and the scheduler both use nonblocking admission. A successful
			// poll must provide a scheduling opportunity itself, so repeated poller
			// wins cannot leave a ready transfer perpetually skipped by tick.
			return e.tickTorrent(ctx, &current, e.now())
		}, false)
		if err != nil {
			return err
		}
	}

	e.Wake()
	return nil
}

// Caller owns this torrent's operation slot and has reread its incarnation.
func (e *Engine) reconcileProviderSnapshot(ctx context.Context, t *domain.Torrent, snap listSnapshot, caps provider.Caps) error {
	pt, ok := snap.byID[t.ProviderID]
	if !ok {
		if other, found := snap.byHash[t.Hash]; found && t.Hash != "" {
			nt, err := e.mutate(ctx, t.ID, func(tt *domain.Torrent) error { tt.ProviderID = other.ID; return nil })
			if err != nil {
				return err
			}
			*t = nt
			pt, ok = other, true
		}
	}
	if !ok {
		if t.ProviderAddedAt != nil && e.now().Sub(*t.ProviderAddedAt) < 2*e.cfg.PollInterval+30*time.Second {
			return nil
		}
		if t.Status == domain.TorrentFinished {
			return nil
		}
		return e.fail(ctx, t, "torrent no longer exists at provider")
	}
	return e.applyProviderState(ctx, t, pt, caps)
}

// applyProviderState merges a provider torrent into the local record and
// handles provider-side errors (with torrent-level retries). t is refreshed
// with the committed row.
func (e *Engine) applyProviderState(ctx context.Context, t *domain.Torrent, pt provider.Torrent, caps provider.Caps) error {
	row, err := e.store.GetTorrent(ctx, t.ID)
	if err != nil {
		return err
	}
	current, err := store.TorrentFromRow(row)
	if err != nil {
		return err
	}
	if current.Status.IsTerminal() || current.Status == domain.TorrentQueued || current.AccountID != t.AccountID || current.ProviderID != t.ProviderID || current.RetryCount != t.RetryCount {
		*t = current
		return nil
	}
	*t = current
	if pt.Status == domain.TorrentError {
		// Provider gave up. Retry the whole torrent if allowed, else fail.
		msg := "provider error: " + firstNonEmpty(pt.Message, pt.RawStatus)
		if t.RetryCount < t.Settings.TorrentRetries {
			return e.requeueForRetry(ctx, t, msg)
		}
		return e.fail(ctx, t, msg)
	}
	nt, err := e.mutate(ctx, t.ID, func(t *domain.Torrent) error {
		if t.Status.IsTerminal() || t.Status == domain.TorrentQueued {
			return store.ErrSkip // a concurrent retry/delete changed the picture; let the next poll decide
		}
		changed := false
		set := func(cond bool, f func()) {
			if cond {
				f()
				changed = true
			}
		}
		set(pt.Name != "" && pt.Name != t.Name, func() { t.Name = pt.Name })
		set(pt.Size > 0 && pt.Size != t.Size, func() { t.Size = pt.Size })
		set(pt.Progress != t.Progress, func() { t.Progress = pt.Progress })
		set(pt.Speed != t.Speed, func() { t.Speed = pt.Speed })
		set(pt.Seeders != t.Seeders, func() { t.Seeders = pt.Seeders })
		set(pt.RawStatus != t.ProviderStatus, func() { t.ProviderStatus = pt.RawStatus })
		set(pt.EndedAt != nil && t.ProviderEndedAt == nil, func() { t.ProviderEndedAt = pt.EndedAt })
		if len(pt.Files) > 0 && !sameFiles(t.Files, pt.Files) {
			t.Files = mergeFiles(t.Files, pt.Files, caps.SelectFiles)
			changed = true
		}
		switch pt.Status {
		case domain.TorrentFinished:
			if t.Status != domain.TorrentFinished {
				if err := t.Transition(domain.TorrentFinished, "available at provider"); err == nil {
					changed = true
					if t.ProviderEndedAt == nil {
						now := e.now()
						t.ProviderEndedAt = &now
					}
				}
			}
		case domain.TorrentProcessing, domain.TorrentWaitingSelection, domain.TorrentDownloading, domain.TorrentUploading:
			if t.Status != pt.Status && t.Status.CanTransition(pt.Status) {
				_ = t.Transition(pt.Status, firstNonEmpty(pt.Message, "provider: "+pt.RawStatus))
				changed = true
			} else if pt.Message != "" && pt.Message != t.StatusReason {
				t.StatusReason = pt.Message
				changed = true
			}
		}
		if !changed {
			return store.ErrSkip
		}
		return nil
	})
	if err != nil {
		return err
	}
	*t = nt
	return nil
}

// ForgetProviderTorrent drops a provider torrent from the cached listing so
// the dedupe path doesn't adopt something we just deleted. Snapshots are
// immutable once stored (copy-on-write here), so readers holding an older
// snapshot never race with this.
func (e *Engine) ForgetProviderTorrent(accountID, providerID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.listEpoch == nil {
		e.listEpoch = make(map[string]uint64)
	}
	e.listEpoch[accountID]++ // even if no cached snapshot existed when invalidated

	snap, ok := e.lastList[accountID]
	if !ok {
		return
	}
	pt, ok := snap.byID[providerID]
	if !ok {
		return
	}
	next := listSnapshot{at: snap.at, byHash: make(map[string]provider.Torrent, len(snap.byHash)), byID: make(map[string]provider.Torrent, len(snap.byID))}
	for k, v := range snap.byID {
		if k != providerID {
			next.byID[k] = v
		}
	}
	for k, v := range snap.byHash {
		if k != pt.Hash {
			next.byHash[k] = v
		}
	}
	e.lastList[accountID] = next
}

// lookupHash finds a provider torrent by hash in the last listing for the account.
func (e *Engine) lookupHash(accountID, hash string) (provider.Torrent, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	snap, ok := e.lastList[accountID]
	if !ok {
		return provider.Torrent{}, false
	}
	pt, ok := snap.byHash[hash]
	return pt, ok
}

// requeueForRetry deletes the torrent at the provider and queues it again.
func (e *Engine) requeueForRetry(ctx context.Context, t *domain.Torrent, why string) error {
	if prov, _, err := e.svc.Providers().For(ctx, t.AccountID); err == nil && t.ProviderID != "" {
		if err := prov.Delete(ctx, t.ProviderID); err != nil && provider.KindOf(err) != provider.ErrNotFound {
			e.log.Warn("delete before retry", "id", t.ID, "err", err)
		}
		e.ForgetProviderTorrent(t.AccountID, t.ProviderID)
	}
	if err := e.store.DeleteDownloadsForTorrent(ctx, t.ID); err != nil {
		return err
	}
	nt, err := e.mutate(ctx, t.ID, func(t *domain.Torrent) error {
		t.RetryCount++
		if t.Settings.WaitForSelection {
			t.Settings.ManualFiles = nil
			t.Settings.Selection = nil
		}
		t.ProviderID = ""
		t.ProviderStatus = ""
		t.Progress = 0
		t.FilesSelectedAt = nil
		t.ProviderAddedAt = nil
		t.ProviderEndedAt = nil
		at := e.now().Add(e.backoff(t.RetryCount - 1))
		t.RetryAt = &at
		t.Status = domain.TorrentQueued // forced: retry path is always allowed
		t.StatusReason = why + "; retrying (" + strconv.Itoa(t.RetryCount) + "/" + strconv.Itoa(t.Settings.TorrentRetries) + ")"
		return nil
	})
	if err != nil {
		return err
	}
	*t = nt
	return nil
}

func sameFiles(a, b []domain.File) bool {
	if len(a) != len(b) {
		return false
	}
	idx := map[string]domain.File{}
	for _, f := range a {
		idx[f.ID] = f
	}
	for _, f := range b {
		g, ok := idx[f.ID]
		if !ok || g.Path != f.Path || g.Size != f.Size || (f.Link != "" && g.Link != f.Link) {
			return false
		}
	}
	return true
}

// mergeFiles takes the provider's file list but keeps local Selected flags
// when the provider doesn't track selection.
func mergeFiles(local, remote []domain.File, providerTracksSelection bool) []domain.File {
	sel := map[string]bool{}
	for _, f := range local {
		sel[f.ID] = f.Selected
	}
	out := make([]domain.File, len(remote))
	for i, f := range remote {
		out[i] = f
		if !providerTracksSelection {
			if s, ok := sel[f.ID]; ok {
				out[i].Selected = s
			}
		}
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
