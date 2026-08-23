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
		var v AccountView
		v, err = s.GetAccount(ctx, in.Account)
		acc = v.ProviderAccount
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
	}
	if err := domain.ValidateFilters(ts); err != nil {
		return TorrentDetail{}, validationErr("%v", err)
	}
	if in.Category != "" {
		if err := validateCategory(in.Category); err != nil {
			return TorrentDetail{}, err
		}
	}

	// Duplicate guard: an existing non-terminal torrent with this hash on the account.
	if row, err := s.store.GetTorrentByHash(ctx, sqlcgen.GetTorrentByHashParams{AccountID: acc.ID, Hash: meta.Hash}); err == nil {
		existing, err := store.TorrentFromRow(row)
		if err != nil {
			return TorrentDetail{}, err
		}
		if !existing.Status.IsTerminal() {
			return TorrentDetail{}, fmt.Errorf("%w: torrent %s already exists on account %s (id %s, %s)", ErrConflict, meta.Hash, acc.Name, existing.ID, existing.Status)
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
		if dir != s.cfg.DownloadDir && strings.HasPrefix(dir, s.cfg.DownloadDir) {
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

// RetryTorrent re-queues an errored or completed torrent from scratch.
func (s *Service) RetryTorrent(ctx context.Context, idOrHash string) (TorrentDetail, error) {
	t, err := s.loadTorrent(ctx, idOrHash)
	if err != nil {
		return TorrentDetail{}, err
	}
	if !t.Status.IsTerminal() {
		return TorrentDetail{}, fmt.Errorf("%w: torrent is %s; only errored or completed torrents can be retried", ErrConflict, t.Status)
	}
	_ = s.engine.CancelTorrent(ctx, t.ID)
	if err := t.Transition(domain.TorrentQueued, "retry requested"); err != nil {
		return TorrentDetail{}, err
	}
	t.Error = ""
	t.RetryCount++
	t.RetryAt = nil
	t.CompletedAt = nil
	t.FilesSelectedAt = nil
	t.ProviderEndedAt = nil
	t.UpdatedAt = s.now()
	params, err := store.TorrentUpdateParams(t)
	if err != nil {
		return TorrentDetail{}, err
	}
	err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
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
	row, err := s.store.GetDownload(ctx, downloadID)
	if err != nil {
		if store.IsNotFound(err) {
			return domain.Download{}, fmt.Errorf("%w: download %q", ErrNotFound, downloadID)
		}
		return domain.Download{}, err
	}
	d, err := store.DownloadFromRow(row)
	if err != nil {
		return domain.Download{}, err
	}
	if d.State != domain.DownloadError {
		return domain.Download{}, fmt.Errorf("%w: download is %s, not in error", ErrConflict, d.State)
	}
	if err := d.Transition(domain.DownloadPending); err != nil {
		return domain.Download{}, err
	}
	d.Error = ""
	d.RetryCount = 0
	d.StartedAt, d.FinishedAt, d.CompletedAt = nil, nil, nil
	d.UpdatedAt = s.now()
	if err := s.store.UpdateDownload(ctx, store.DownloadUpdateParams(d)); err != nil {
		return domain.Download{}, err
	}
	// The torrent may have been marked errored because of this download.
	if t, err := s.loadTorrent(ctx, d.TorrentID); err == nil && t.Status == domain.TorrentError {
		if err := t.Transition(domain.TorrentFinished, "download retry requested"); err == nil {
			t.Error = ""
			t.CompletedAt = nil
			t.UpdatedAt = s.now()
			if p, err := store.TorrentUpdateParams(t); err == nil {
				_ = s.store.UpdateTorrent(ctx, p)
			}
		}
	}
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
func (s *Service) UpdateTorrent(ctx context.Context, idOrHash string, in UpdateTorrentInput) (TorrentDetail, error) {
	t, err := s.loadTorrent(ctx, idOrHash)
	if err != nil {
		return TorrentDetail{}, err
	}
	if in.Category != nil && *in.Category != t.Category {
		if *in.Category != "" {
			if err := validateCategory(*in.Category); err != nil {
				return TorrentDetail{}, err
			}
		}
		n, err := s.store.CountStartedDownloadsForTorrent(ctx, t.ID)
		if err != nil {
			return TorrentDetail{}, err
		}
		if n > 0 {
			return TorrentDetail{}, fmt.Errorf("%w: cannot change category after downloads have started", ErrConflict)
		}
		t.Category = *in.Category
	}
	if in.Settings != nil {
		if err := domain.ValidateFilters(*in.Settings); err != nil {
			return TorrentDetail{}, validationErr("%v", err)
		}
		t.Settings = *in.Settings
	}
	t.UpdatedAt = s.now()
	params, err := store.TorrentUpdateParams(t)
	if err != nil {
		return TorrentDetail{}, err
	}
	if err := s.store.UpdateTorrent(ctx, params); err != nil {
		return TorrentDetail{}, err
	}
	s.events.Publish(events.Event{Type: events.TorrentUpdated, TorrentID: t.ID})
	return s.GetTorrent(ctx, t.ID)
}

// SelectFiles restricts a torrent to the given provider file ids. Downloads
// that have not started yet and are no longer selected are removed; the
// engine re-runs selection on its next pass.
func (s *Service) SelectFiles(ctx context.Context, idOrHash string, fileIDs []string) (TorrentDetail, error) {
	t, err := s.loadTorrent(ctx, idOrHash)
	if err != nil {
		return TorrentDetail{}, err
	}
	if len(fileIDs) == 0 {
		return TorrentDetail{}, validationErr("at least one file id is required")
	}
	known := map[string]bool{}
	for _, f := range t.Files {
		known[f.ID] = true
	}
	if len(known) > 0 {
		for _, id := range fileIDs {
			if !known[id] {
				return TorrentDetail{}, validationErr("unknown file id %q", id)
			}
		}
	}
	t.Settings.ManualFiles = append([]string(nil), fileIDs...)
	t.FilesSelectedAt = nil
	t.UpdatedAt = s.now()
	params, err := store.TorrentUpdateParams(t)
	if err != nil {
		return TorrentDetail{}, err
	}
	want := map[string]bool{}
	for _, id := range fileIDs {
		want[id] = true
	}
	err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
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
