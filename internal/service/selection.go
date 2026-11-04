package service

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

// FileSelection is an explicit review, separate from provider lifecycle status.
type FileSelection struct {
	TorrentID       string
	State           string
	Revision        string
	Files           []domain.File
	SelectedIDs     []string
	ProviderSelects bool
	Message         string
}

func selectionView(t domain.Torrent) FileSelection {
	v := FileSelection{TorrentID: t.ID, State: "pending", Files: []domain.File{}, SelectedIDs: []string{}, Message: "Waiting for provider file metadata. No files will download to the NAS before confirmation."}
	switch {
	case !t.Settings.WaitForSelection:
		v.State, v.Message = "automatic", "This transfer uses automatic file rules. Manual review must be chosen when adding a transfer."
	case t.SelectionReady():
		v.State, v.Message = "accepted", "This selection is confirmed and cannot be changed."
		v.ProviderSelects = t.Settings.Selection.ProviderSelects
		v.Revision = t.Settings.Selection.Revision
		v.SelectedIDs = slices.Clone(t.Settings.Selection.FileIDs)
		v.Files = slices.Clone(t.Files)
	case t.Status.IsTerminal():
		v.State, v.Message = "unavailable", "This transfer is no longer waiting for file selection."
	}
	return v
}

// GetFileSelection explicitly reads authoritative remote metadata. Parsed
// .torrent indices in the local row are never offered as provider IDs.
func (s *Service) GetFileSelection(ctx context.Context, id string) (FileSelection, error) {
	t, err := s.loadTorrent(ctx, id)
	if err != nil {
		return FileSelection{}, err
	}
	v := selectionView(t)
	if v.State != "pending" || t.ProviderID == "" || !t.Status.AtProvider() {
		return v, nil
	}
	prov, _, err := s.providers.For(ctx, t.AccountID)
	if err != nil {
		return FileSelection{}, err
	}
	v.ProviderSelects = prov.Caps().SelectFiles
	readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	remote, err := prov.GetTorrent(readCtx, t.ProviderID)
	if err != nil {
		return FileSelection{}, err
	}
	if (v.ProviderSelects && remote.Status != domain.TorrentWaitingSelection) || (!v.ProviderSelects && remote.Status != domain.TorrentFinished) {
		if v.ProviderSelects && remote.Status == domain.TorrentFinished {
			v.State = "unavailable"
			v.Message = "The provider completed its selection before review. A safe subset cannot be requested for this transfer."
		}
		return v, nil
	}
	if len(remote.Files) == 0 {
		return v, nil
	}
	if !t.SelectionSourceMatches(remote.ID, remote.Hash) {
		v.State, v.Message = "unavailable", "The provider transfer changed. Refresh before reviewing its files."
		return v, nil
	}
	revision, err := domain.FileSelectionRevision(t, remote.Files)
	if err != nil {
		v.State, v.Message = "unavailable", "The provider did not return an unambiguous file list within supported limits. No NAS download has been released."
		return v, nil
	}
	// Re-read after network I/O: retry/delete/another confirmation may have won.
	current, err := s.loadTorrent(ctx, t.ID)
	if err != nil {
		return FileSelection{}, err
	}
	if current.ProviderID != t.ProviderID || current.AccountID != t.AccountID || current.RetryCount != t.RetryCount {
		return FileSelection{}, fmt.Errorf("%w: provider transfer changed; refresh file review", ErrConflict)
	}
	if current.SelectionReady() || current.Status.IsTerminal() {
		return selectionView(current), nil
	}
	v.State, v.Revision, v.Files = "ready", revision, slices.Clone(remote.Files)
	v.Message = "Choose files before downloading to the NAS. The provider may already be processing all files."
	if v.ProviderSelects {
		v.Message = "Choose files to request from the provider before downloading to the NAS. The provider may return repacked or split download files."
	}
	return v, nil
}

// ConfirmFileSelection is accept-once. Identical retries replay success even
// after downloads start; competing or stale choices cannot replace the receipt.
func (s *Service) ConfirmFileSelection(ctx context.Context, id, revision string, fileIDs []string) (FileSelection, error) {
	ids, err := domain.CanonicalFileIDs(fileIDs)
	if err != nil || !domain.ValidSelectionRevision(revision) {
		return FileSelection{}, validationErr("provide a valid review revision and nonempty unique file ids")
	}
	cur, err := s.loadTorrent(ctx, id)
	if err != nil {
		return FileSelection{}, err
	}
	if !cur.Settings.WaitForSelection {
		return FileSelection{}, fmt.Errorf("%w: this transfer uses automatic file rules", ErrConflict)
	}
	if cur.SelectionReady() {
		if cur.Settings.Selection.Revision == revision && slices.Equal(cur.Settings.Selection.FileIDs, ids) {
			return selectionView(cur), nil
		}
		return FileSelection{}, fmt.Errorf("%w: a different selection was already confirmed", ErrConflict)
	}
	view, err := s.GetFileSelection(ctx, cur.ID)
	if err != nil {
		return FileSelection{}, err
	}
	if view.State == "accepted" && view.Revision == revision && slices.Equal(view.SelectedIDs, ids) {
		return view, nil
	}
	if view.State != "ready" || view.Revision != revision {
		return FileSelection{}, fmt.Errorf("%w: file review changed or is not ready; refresh before confirming", ErrConflict)
	}
	if _, err := domain.StrictSelectedFiles(view.Files, ids); err != nil {
		return FileSelection{}, validationErr("selected file ids do not match this review")
	}
	var accepted domain.Torrent
	err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetTorrent(ctx, cur.ID)
		if err != nil {
			return err
		}
		t, err := store.TorrentFromRow(row)
		if err != nil {
			return err
		}
		if t.Settings.WaitForSelection && t.SelectionReady() {
			if t.Settings.Selection.Revision == revision && slices.Equal(t.Settings.Selection.FileIDs, ids) {
				accepted = t
				return nil
			}
			return fmt.Errorf("%w: a different selection was already confirmed", ErrConflict)
		}
		if !t.Settings.WaitForSelection || t.ProviderID != cur.ProviderID || t.AccountID != cur.AccountID || t.RetryCount != cur.RetryCount || !t.Status.AtProvider() || t.Status.IsTerminal() || t.FilesSelectedAt != nil {
			return fmt.Errorf("%w: transfer changed during file review", ErrConflict)
		}
		rows, err := q.ListDownloadsForTorrent(ctx, t.ID)
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			return fmt.Errorf("%w: NAS downloads already exist; this selection cannot be replaced", ErrConflict)
		}
		t.Settings.ManualFiles = slices.Clone(ids)
		t.Settings.Selection = &domain.FileSelectionReceipt{Version: 1, ProviderID: t.ProviderID, RetryCount: t.RetryCount, ProviderSelects: view.ProviderSelects, Revision: revision, FileIDs: slices.Clone(ids)}
		t.Files = slices.Clone(view.Files)
		selected := make(map[string]bool, len(ids))
		for _, id := range ids {
			selected[id] = true
		}
		for i := range t.Files {
			t.Files[i].Selected = selected[t.Files[i].ID]
		}
		t.UpdatedAt = s.now()
		params, err := store.TorrentUpdateParams(t)
		if err != nil {
			return err
		}
		if err := q.UpdateTorrent(ctx, params); err != nil {
			return err
		}
		accepted = t
		return nil
	})
	if err != nil {
		return FileSelection{}, err
	}
	s.events.Publish(events.Event{Type: events.TorrentUpdated, TorrentID: accepted.ID})
	s.engine.Wake()
	return selectionView(accepted), nil
}
