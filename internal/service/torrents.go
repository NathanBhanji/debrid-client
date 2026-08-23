package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
	"github.com/NathanBhanji/debrid-client/internal/torrentmeta"
)

// AddTorrentInput describes a torrent to add. Exactly one of Magnet or
// TorrentFile must be set.
type AddTorrentInput struct {
	Magnet      string
	TorrentFile []byte
	// Account is an account id or name; empty uses the default account.
	Account  string
	Category string
	// Settings overrides the configured defaults when non-nil.
	Settings *domain.TorrentSettings
}

// TorrentDetail is a torrent with its downloads.
type TorrentDetail struct {
	Torrent   domain.Torrent
	Downloads []domain.Download
}

// LocalProgress is 0..1 across all downloads (by bytes).
func (d TorrentDetail) LocalProgress() float64 {
	var total, done int64
	for _, dl := range d.Downloads {
		total += dl.Size
		if dl.State == domain.DownloadDone {
			done += dl.Size
		} else {
			done += dl.BytesDone
		}
	}
	if total == 0 {
		if d.Torrent.Status == domain.TorrentCompleted {
			return 1
		}
		return 0
	}
	return float64(done) / float64(total)
}

// ListFilter narrows ListTorrents.
type ListFilter struct {
	Status   domain.TorrentStatus
	Account  string
	Category string
}

// AddTorrent parses the input, checks for duplicates on the account and
// queues the torrent for the engine.
func (s *Service) AddTorrent(ctx context.Context, in AddTorrentInput) (TorrentDetail, error) {
	var (
		meta domain.Torrent
		err  error
	)
	switch {
	case in.Magnet != "" && in.TorrentFile != nil:
		return TorrentDetail{}, validationErr("provide either a magnet or a torrent file, not both")
	case in.Magnet != "":
		m, perr := torrentmeta.ParseMagnet(in.Magnet)
		if perr != nil {
			return TorrentDetail{}, validationErr("%v", perr)
		}
		meta = domain.Torrent{Hash: m.Hash, Name: m.Name, PayloadKind: domain.PayloadMagnet, Payload: []byte(strings.TrimSpace(in.Magnet))}
	case in.TorrentFile != nil:
		m, perr := torrentmeta.ParseTorrent(in.TorrentFile)
		if perr != nil {
			return TorrentDetail{}, validationErr("%v", perr)
		}
		meta = domain.Torrent{Hash: m.Hash, Name: m.Name, Size: m.Size, Files: m.Files, PayloadKind: domain.PayloadFile, Payload: in.TorrentFile}
	default:
		return TorrentDetail{}, validationErr("a magnet link or torrent file is required")
	}

	var acc domain.ProviderAccount
	if in.Account == "" {
		acc, err = s.DefaultAccount(ctx)
	} else {
		acc, err = s.account(ctx, in.Account)
	}
	if err != nil {
		return TorrentDetail{}, err
	}
	if !acc.Enabled {
		return TorrentDetail{}, validationErr("account %q is disabled", acc.Name)
	}

	settings, err := s.GetSettings(ctx)
	if err != nil {
		return TorrentDetail{}, err
	}
	ts := settings.TorrentDefaults
	if in.Settings != nil {
		ts = *in.Settings
		if ts.FinishedAction == "" {
			ts.FinishedAction = domain.FinishedKeep
		}
	}
	if err := ValidateTorrentSettings(ts); err != nil {
		return TorrentDetail{}, err
	}
	if in.Category != "" {
		if err := validateCategory(in.Category); err != nil {
			return TorrentDetail{}, err
		}
	}

	// Duplicate guard: any non-terminal torrent with this hash on the account
	// (not just the newest row — a retried older row counts too).
	rows, err := s.store.ListTorrentsByAccount(ctx, acc.ID)
	if err != nil {
		return TorrentDetail{}, err
	}
	for _, r := range rows {
		if r.Hash == meta.Hash && !domain.TorrentStatus(r.Status).IsTerminal() {
			return TorrentDetail{}, fmt.Errorf("%w: torrent %s already exists on account %s (id %s, %s)", ErrConflict, meta.Hash, acc.Name, r.ID, r.Status)
		}
	}

	now := s.now()
	t := meta
	t.ID = s.newID()
	t.AccountID = acc.ID
	t.Category = in.Category
	t.Status = domain.TorrentQueued
	t.StatusReason = "queued locally"
	t.Settings = ts
	t.AddedAt = now
	t.UpdatedAt = now
	params, err := store.TorrentInsertParams(t)
	if err != nil {
		return TorrentDetail{}, err
	}
	if err := s.store.InsertTorrent(ctx, params); err != nil {
		return TorrentDetail{}, err
	}
	s.events.Publish(events.Event{Type: events.TorrentAdded, TorrentID: t.ID})
	s.engine.Wake()
	return TorrentDetail{Torrent: t, Downloads: []domain.Download{}}, nil
}

// ListTorrents returns torrents (newest first) with their downloads.
func (s *Service) ListTorrents(ctx context.Context, f ListFilter) ([]TorrentDetail, error) {
	var accountID string
	if f.Account != "" {
		a, err := s.GetAccount(ctx, f.Account)
		if err != nil {
			return nil, err
		}
		accountID = a.ID
	}
	rows, err := s.store.ListTorrents(ctx)
	if err != nil {
		return nil, err
	}
	dlRows, err := s.store.ListDownloads(ctx)
	if err != nil {
		return nil, err
	}
	byTorrent := map[string][]domain.Download{}
	for _, r := range dlRows {
		d, err := store.DownloadFromRow(r)
		if err != nil {
			return nil, err
		}
		byTorrent[d.TorrentID] = append(byTorrent[d.TorrentID], d)
	}
	out := make([]TorrentDetail, 0, len(rows))
	for _, r := range rows {
		t, err := store.TorrentFromRow(r)
		if err != nil {
			return nil, err
		}
		if f.Status != "" && t.Status != f.Status {
			continue
		}
		if accountID != "" && t.AccountID != accountID {
			continue
		}
		if f.Category != "" && t.Category != f.Category {
			continue
		}
		dls := byTorrent[t.ID]
		if dls == nil {
			dls = []domain.Download{}
		}
		sort.Slice(dls, func(i, j int) bool { return dls[i].RelPath < dls[j].RelPath })
		out = append(out, TorrentDetail{Torrent: t, Downloads: dls})
	}
	return out, nil
}

// GetTorrent returns one torrent with downloads. Accepts an id or an info hash.
func (s *Service) GetTorrent(ctx context.Context, idOrHash string) (TorrentDetail, error) {
	t, err := s.loadTorrent(ctx, idOrHash)
	if err != nil {
		return TorrentDetail{}, err
	}
	dlRows, err := s.store.ListDownloadsForTorrent(ctx, t.ID)
	if err != nil {
		return TorrentDetail{}, err
	}
	dls := make([]domain.Download, 0, len(dlRows))
	for _, r := range dlRows {
		d, err := store.DownloadFromRow(r)
		if err != nil {
			return TorrentDetail{}, err
		}
		dls = append(dls, d)
	}
	return TorrentDetail{Torrent: t, Downloads: dls}, nil
}

func (s *Service) loadTorrent(ctx context.Context, idOrHash string) (domain.Torrent, error) {
	row, err := s.store.GetTorrent(ctx, idOrHash)
	if store.IsNotFound(err) && len(idOrHash) == 40 {
		// Fall back to hash lookup across accounts.
		rows, lerr := s.store.ListTorrents(ctx)
		if lerr != nil {
			return domain.Torrent{}, lerr
		}
		h := strings.ToLower(idOrHash)
		for _, r := range rows {
			if r.Hash == h {
				row, err = r, nil
				break
			}
		}
	}
	if err != nil {
		if store.IsNotFound(err) {
			return domain.Torrent{}, fmt.Errorf("%w: torrent %q", ErrNotFound, idOrHash)
		}
		return domain.Torrent{}, err
	}
	return store.TorrentFromRow(row)
}

// DeleteOptions control what DeleteTorrent removes besides the local record.
type DeleteOptions struct {
	// DeleteFiles removes downloaded files from disk.
	DeleteFiles bool
	// DeleteFromProvider removes the torrent at the debrid provider too.
	DeleteFromProvider bool
}

// DeleteTorrent cancels in-flight work and removes the torrent.
func (s *Service) DeleteTorrent(ctx context.Context, idOrHash string, opts DeleteOptions) error {
	t, err := s.loadTorrent(ctx, idOrHash)
	if err != nil {
		return err
	}
	if err := s.engine.CancelTorrent(ctx, t.ID); err != nil {
		s.log.Warn("cancel torrent", "id", t.ID, "err", err)
	}
	if opts.DeleteFromProvider && t.ProviderID != "" {
		prov, _, err := s.providers.For(ctx, t.AccountID)
		if err != nil {
			return err
		}
		if err := prov.Delete(ctx, t.ProviderID); err != nil && provider.KindOf(err) != provider.ErrNotFound {
			return fmt.Errorf("delete at provider: %w", err)
		}
	}
	if opts.DeleteFiles {
		dir := TorrentDir(s.cfg.DownloadDir, t)
		shared, err := s.dirSharedByOther(ctx, t.ID, dir)
		if err != nil {
			return err
		}
		switch {
		case shared:
			s.log.Warn("not deleting files: directory shared with another torrent", "id", t.ID, "dir", dir)
		case dir != s.cfg.DownloadDir && strings.HasPrefix(dir, s.cfg.DownloadDir+string(os.PathSeparator)):
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("delete files: %w", err)
			}
		}
	}
	if err := s.store.DeleteTorrent(ctx, t.ID); err != nil {
		return err
	}
	s.events.Publish(events.Event{Type: events.TorrentDeleted, TorrentID: t.ID})
	return nil
}

// dirSharedByOther reports whether another torrent resolves to the same directory.
func (s *Service) dirSharedByOther(ctx context.Context, id, dir string) (bool, error) {
	rows, err := s.store.ListTorrents(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if r.ID == id {
			continue
		}
		o, err := store.TorrentFromRow(r)
		if err != nil {
			return false, err
		}
		if TorrentDir(s.cfg.DownloadDir, o) == dir {
			return true, nil
		}
	}
	return false, nil
}

// RetryTorrent re-queues an errored or completed torrent from scratch: provider
// state is cleared (the engine re-adds, deduping by hash against the provider
// listing), download rows are dropped, local files are left in place (fetch
// resumes .part files and replaces completed ones).
func (s *Service) RetryTorrent(ctx context.Context, idOrHash string) (TorrentDetail, error) {
	cur, err := s.loadTorrent(ctx, idOrHash)
	if err != nil {
		return TorrentDetail{}, err
	}
	_ = s.engine.CancelTorrent(ctx, cur.ID)
	var t domain.Torrent
	err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetTorrent(ctx, cur.ID)
		if err != nil {
			return err
		}
		t, err = store.TorrentFromRow(row)
		if err != nil {
			return err
		}
		if !t.Status.IsTerminal() {
			return fmt.Errorf("%w: torrent is %s; only errored or completed torrents can be retried", ErrConflict, t.Status)
		}
		if err := t.Transition(domain.TorrentQueued, "retry requested"); err != nil {
			return err
		}
		t.Error = ""
		t.RetryCount++
		t.RetryAt = nil
		t.CompletedAt = nil
		t.FilesSelectedAt = nil
		t.ProviderAddedAt = nil
		t.ProviderEndedAt = nil
		t.ProviderID = ""
		t.ProviderStatus = ""
		t.Progress = 0
		t.UpdatedAt = s.now()
		params, err := store.TorrentUpdateParams(t)
		if err != nil {
			return err
		}
		if err := q.DeleteDownloadsForTorrent(ctx, t.ID); err != nil {
			return err
		}
		return q.UpdateTorrent(ctx, params)
	})
	if err != nil {
		return TorrentDetail{}, err
	}
	s.events.Publish(events.Event{Type: events.TorrentUpdated, TorrentID: t.ID})
	s.engine.Wake()
	return TorrentDetail{Torrent: t, Downloads: []domain.Download{}}, nil
}

// RetryDownload re-queues a single errored download.
func (s *Service) RetryDownload(ctx context.Context, downloadID string) (domain.Download, error) {
	d, err := s.store.MutateDownload(ctx, downloadID, func(d *domain.Download) error {
		if d.State != domain.DownloadError {
			return fmt.Errorf("%w: download is %s, not in error", ErrConflict, d.State)
		}
		if err := d.Transition(domain.DownloadPending); err != nil {
			return err
		}
		d.Error = ""
		d.RetryCount = 0
		d.DirectURL = ""
		d.StartedAt, d.FinishedAt, d.CompletedAt = nil, nil, nil
		d.UpdatedAt = s.now()
		return nil
	})
	if err != nil {
		if store.IsNotFound(err) {
			return domain.Download{}, fmt.Errorf("%w: download %q", ErrNotFound, downloadID)
		}
		return domain.Download{}, err
	}
	// The torrent may have been marked errored because of this download.
	_, _ = s.store.MutateTorrent(ctx, d.TorrentID, func(t *domain.Torrent) error {
		if t.Status != domain.TorrentError {
			return store.ErrSkip
		}
		if err := t.Transition(domain.TorrentFinished, "download retry requested"); err != nil {
			return store.ErrSkip
		}
		t.Error = ""
		t.CompletedAt = nil
		t.UpdatedAt = s.now()
		return nil
	})
	s.events.Publish(events.Event{Type: events.DownloadUpdated, TorrentID: d.TorrentID, DownloadID: d.ID})
	s.engine.Wake()
	return d, nil
}

// UpdateTorrentInput holds optional torrent changes.
type UpdateTorrentInput struct {
	Category *string
	Settings *domain.TorrentSettings
}

// UpdateTorrent changes category and/or settings. Category changes are only
// allowed before local downloads start (the path would move otherwise).
// ManualFiles in Settings is ignored — use SelectFiles for that.
func (s *Service) UpdateTorrent(ctx context.Context, idOrHash string, in UpdateTorrentInput) (TorrentDetail, error) {
	cur, err := s.loadTorrent(ctx, idOrHash)
	if err != nil {
		return TorrentDetail{}, err
	}
	if in.Category != nil && *in.Category != "" {
		if err := validateCategory(*in.Category); err != nil {
			return TorrentDetail{}, err
		}
	}
	if in.Settings != nil {
		if in.Settings.FinishedAction == "" {
			in.Settings.FinishedAction = domain.FinishedKeep
		}
		if err := ValidateTorrentSettings(*in.Settings); err != nil {
			return TorrentDetail{}, err
		}
	}
	t, err := s.store.MutateTorrent(ctx, cur.ID, func(t *domain.Torrent) error {
		if in.Category != nil && *in.Category != t.Category {
			if t.DirName != "" {
				return fmt.Errorf("%w: cannot change category after downloads have started", ErrConflict)
			}
			t.Category = *in.Category
		}
		if in.Settings != nil {
			manual := t.Settings.ManualFiles
			t.Settings = *in.Settings
			t.Settings.ManualFiles = manual
		}
		t.UpdatedAt = s.now()
		return nil
	})
	if err != nil {
		return TorrentDetail{}, err
	}
	s.events.Publish(events.Event{Type: events.TorrentUpdated, TorrentID: t.ID})
	return s.GetTorrent(ctx, t.ID)
}

// SelectFiles restricts a torrent to the given provider file ids. It requires
// the provider's file list to be known (ids from a parsed .torrent are not
// provider ids). Downloads that have not started yet and are no longer
// selected are removed; the engine re-runs selection on its next pass.
func (s *Service) SelectFiles(ctx context.Context, idOrHash string, fileIDs []string) (TorrentDetail, error) {
	cur, err := s.loadTorrent(ctx, idOrHash)
	if err != nil {
		return TorrentDetail{}, err
	}
	if len(fileIDs) == 0 {
		return TorrentDetail{}, validationErr("at least one file id is required")
	}
	want := map[string]bool{}
	for _, id := range fileIDs {
		want[id] = true
	}
	var t domain.Torrent
	err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetTorrent(ctx, cur.ID)
		if err != nil {
			return err
		}
		t, err = store.TorrentFromRow(row)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, f := range t.Files {
			if f.ID != "" {
				known[f.ID] = true
			}
		}
		if len(known) == 0 {
			return fmt.Errorf("%w: the provider's file list is not available yet; try again once the torrent is at the provider", ErrConflict)
		}
		for _, id := range fileIDs {
			if !known[id] {
				return validationErr("unknown file id %q", id)
			}
		}
		t.Settings.ManualFiles = append([]string(nil), fileIDs...)
		t.FilesSelectedAt = nil
		t.UpdatedAt = s.now()
		params, err := store.TorrentUpdateParams(t)
		if err != nil {
			return err
		}
		rows, err := q.ListDownloadsForTorrent(ctx, t.ID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if !want[r.FileID] && r.State == string(domain.DownloadPending) {
				if err := q.DeleteDownload(ctx, r.ID); err != nil {
					return err
				}
			}
		}
		return q.UpdateTorrent(ctx, params)
	})
	if err != nil {
		return TorrentDetail{}, err
	}
	s.events.Publish(events.Event{Type: events.TorrentUpdated, TorrentID: t.ID})
	s.engine.Wake()
	return s.GetTorrent(ctx, t.ID)
}

// IsNotFound reports whether err is a service not-found error.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
