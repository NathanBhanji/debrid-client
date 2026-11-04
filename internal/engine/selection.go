package engine

import (
	"context"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

// Recheck after scheduling and before external work. Selection is immutable
// once accepted; retries/provider reincarnations require a fresh confirmation.
func (e *Engine) selectionSnapshot(ctx context.Context, expected domain.Torrent) (domain.Torrent, bool) {
	row, err := e.store.GetTorrent(ctx, expected.ID)
	if err != nil {
		return domain.Torrent{}, false
	}
	t, err := store.TorrentFromRow(row)
	if err != nil || t.ProviderID != expected.ProviderID || t.RetryCount != expected.RetryCount || t.AccountID != expected.AccountID || !t.Status.AtProvider() || t.Status.IsTerminal() || !t.SelectionReady() {
		return domain.Torrent{}, false
	}
	return t, true
}

func sameSelection(a, b domain.Torrent) bool {
	if a.ProviderID != b.ProviderID || a.RetryCount != b.RetryCount || a.AccountID != b.AccountID || !b.Status.AtProvider() || b.Status.IsTerminal() || !b.SelectionReady() {
		return false
	}
	if !a.Settings.WaitForSelection {
		return !b.Settings.WaitForSelection
	}
	return b.Settings.WaitForSelection && a.Settings.Selection != nil && b.Settings.Selection != nil && a.Settings.Selection.Revision == b.Settings.Selection.Revision
}

// A delayed provider response cannot fail a new retry or another receipt.
func (e *Engine) failSelection(ctx context.Context, t *domain.Torrent, message string) error {
	if !t.Settings.WaitForSelection {
		return e.fail(ctx, t, message)
	}
	expected := *t
	updated, err := e.mutate(ctx, t.ID, func(current *domain.Torrent) error {
		if !sameSelection(expected, *current) {
			return store.ErrSkip
		}
		if err := current.Transition(domain.TorrentError, message); err != nil {
			return err
		}
		current.Error = message
		now := e.now()
		current.CompletedAt = &now
		return nil
	})
	if err == nil {
		*t = updated
	}
	return err
}
