package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func cancellationFixture(t *testing.T) (*fixture, domain.Torrent, domain.Download) {
	t.Helper()
	f, tor, dl := retryFixture(t)
	ctx := context.Background()
	tor.Status = domain.TorrentFinished
	tor.CompletedAt = nil
	tor.Error = ""
	tor.ProviderID = "fixture-provider"
	tor.Speed = 4096
	tor.Settings.WaitForSelection = true
	_, err := f.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error { *t = tor; return nil })
	if err != nil {
		t.Fatal(err)
	}
	dl.State = domain.DownloadDownloading
	dl.CompletedAt = nil
	dl.Error = ""
	dl.BytesDone = 7
	dl.Size = 20
	_, err = f.store.MutateDownload(ctx, dl.ID, func(d *domain.Download) error { *d = dl; return nil })
	if err != nil {
		t.Fatal(err)
	}
	return f, tor, dl
}
func TestCancellationPreservesFilesAndReplaysAcrossRetryRemoval(t *testing.T) {
	f, tor, dl := cancellationFixture(t)
	ctx := context.Background()
	done := dl
	done.ID = "completed-file"
	done.ProviderLink = "completed-link"
	done.RelPath = "completed.mkv"
	done.State = domain.DownloadDone
	at := time.Now().UTC()
	done.CompletedAt = &at
	if _, err := f.store.InsertDownload(ctx, store.DownloadInsertParams(done)); err != nil {
		t.Fatal(err)
	}
	dir := TorrentDir(f.svc.cfg.DownloadDir, tor)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "completed.mkv")
	if err := os.WriteFile(path, []byte("retained bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	view, err := f.svc.GetCancellationReview(ctx, tor.ID)
	if err != nil || !view.Allowed || !view.ProviderMayContinue {
		t.Fatal(view, err)
	}
	key := uuid.NewString()
	if _, err := f.svc.GetCancellationAcceptance(ctx, tor.ID, key, view.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	receipt, err := f.svc.CancelReviewed(ctx, tor.ID, key, view.Revision)
	if err != nil || !receipt.TorrentExists {
		t.Fatal(receipt, err)
	}
	current, err := f.svc.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.Status != domain.TorrentCancelled || current.Torrent.Speed != 0 || current.Torrent.ProviderID != tor.ProviderID || len(current.Downloads) != 2 || f.fake.Calls("Delete") != 0 {
		t.Fatal(current, err)
	}
	for _, d := range current.Downloads {
		if d.ID == dl.ID && (d.State != domain.DownloadCancelled || d.BytesDone != dl.BytesDone || d.RelPath != dl.RelPath) {
			t.Fatal("unfinished state/bytes lost", d)
		}
		if d.ID == done.ID && (d.State != domain.DownloadDone || !d.UpdatedAt.Equal(done.UpdatedAt) || !d.CompletedAt.Equal(*done.CompletedAt)) {
			t.Fatal("completed row changed", d)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "retained bytes" {
		t.Fatal("files changed", err)
	}
	if _, err := f.svc.RetryDownload(ctx, dl.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("single file released cancelled parent", err)
	}
	for range 3 {
		if _, err := f.svc.CancelReviewed(ctx, tor.ID, key, view.Revision); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.eng.cancels) != 1 {
		t.Fatal("replay drained again")
	}
	if _, err := f.svc.GetTorrentSubmission(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("crossed add namespace", err)
	}
	if _, err := f.svc.GetRetryAcceptance(ctx, RetryWholeTorrent, tor.ID, key, view.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatal("crossed retry namespace", err)
	}
	if _, err := f.svc.GetCancellationAcceptance(ctx, "other", key, view.Revision); !errors.Is(err, ErrValidation) {
		t.Fatal("resource mismatch", err)
	}
	restarted := New(f.store, f.svc.providers, f.eng, nil, f.svc.cfg, nil)
	next, err := restarted.RetryTorrent(ctx, tor.ID)
	if err != nil || next.Torrent.Status != domain.TorrentQueued || !next.Torrent.Settings.WaitForSelection || next.Torrent.Settings.Selection != nil {
		t.Fatal(next, err)
	}
	if _, err := restarted.CancelReviewed(ctx, tor.ID, key, view.Revision); err != nil {
		t.Fatal(err)
	}
	current, err = restarted.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.Status != domain.TorrentQueued {
		t.Fatal("old receipt cancelled new retry", current, err)
	}
	if err := restarted.DeleteTorrent(ctx, tor.ID, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	receipt, err = restarted.CancelReviewed(ctx, tor.ID, key, view.Revision)
	if err != nil || receipt.TorrentExists {
		t.Fatal("removed receipt lost", receipt, err)
	}
}
func TestCancellationAllowsDrainChangesButPreservesTerminalRace(t *testing.T) {
	for _, terminal := range []domain.TorrentStatus{"", domain.TorrentCompleted, domain.TorrentError} {
		t.Run(string(terminal), func(t *testing.T) {
			f, tor, dl := cancellationFixture(t)
			ctx := context.Background()
			view, err := f.svc.GetCancellationReview(ctx, tor.ID)
			if err != nil {
				t.Fatal(err)
			}
			key := uuid.NewString()
			f.eng.cancelFn = func(id string) error {
				_, err := f.store.MutateDownload(ctx, dl.ID, func(d *domain.Download) error {
					d.State = domain.DownloadDownloaded
					d.UpdatedAt = time.Now().UTC()
					return nil
				})
				if err != nil {
					return err
				}
				_, err = f.store.MutateTorrent(ctx, id, func(t *domain.Torrent) error {
					t.ProviderID = "late-provider-identity"
					t.UpdatedAt = time.Now().UTC()
					if terminal != "" {
						t.Status = terminal
					}
					return nil
				})
				return err
			}
			_, err = f.svc.CancelReviewed(ctx, tor.ID, key, view.Revision)
			current, loadErr := f.svc.GetTorrent(ctx, tor.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if terminal == "" {
				if err != nil || current.Torrent.Status != domain.TorrentCancelled || current.Downloads[0].State != domain.DownloadCancelled {
					t.Fatal(current, err)
				}
			} else {
				if !errors.Is(err, ErrConflict) || current.Torrent.Status != terminal {
					t.Fatal("terminal race overwritten", current, err)
				}
				if _, err := f.svc.GetCancellationAcceptance(ctx, tor.ID, key, view.Revision); !errors.Is(err, ErrNotFound) {
					t.Fatal("rejected race has receipt", err)
				}
			}
		})
	}
}
func TestCancellationFailureDoesNotRecordOrMutate(t *testing.T) {
	for _, mode := range []string{"drain", "receipt", "stale"} {
		t.Run(mode, func(t *testing.T) {
			f, tor, dl := cancellationFixture(t)
			ctx := context.Background()
			view, err := f.svc.GetCancellationReview(ctx, tor.ID)
			if err != nil {
				t.Fatal(err)
			}
			key := uuid.NewString()
			switch mode {
			case "drain":
				f.eng.cancelFn = func(string) error { return context.DeadlineExceeded }
			case "receipt":
				if _, err := f.store.DB().ExecContext(ctx, "CREATE TRIGGER fail_cancel_receipt BEFORE INSERT ON torrent_submissions BEGIN SELECT RAISE(ABORT, 'fixture failure'); END"); err != nil {
					t.Fatal(err)
				}
			case "stale":
				if _, err := f.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error { t.RetryCount++; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.svc.CancelReviewed(ctx, tor.ID, key, view.Revision); err == nil {
				t.Fatal("failure accepted")
			}
			current, err := f.svc.GetTorrent(ctx, tor.ID)
			if err != nil || current.Torrent.Status != tor.Status || current.Downloads[0].State != dl.State {
				t.Fatal("failed cancel changed rows", current, err)
			}
			if mode == "stale" && len(f.eng.cancels) != 0 {
				t.Fatal("stale request drained active work")
			}
			if _, err := f.svc.GetCancellationAcceptance(ctx, tor.ID, key, view.Revision); !errors.Is(err, ErrNotFound) {
				t.Fatal("failed cancel receipt", err)
			}
		})
	}
}
func TestCancellationIndependentHandlesCommitOneReceipt(t *testing.T) {
	f, tor, _ := cancellationFixture(t)
	ctx := context.Background()
	f.svc.SetEngine(NoopEngine{})
	var file string
	if err := f.store.DB().QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&file); err != nil {
		t.Fatal(err)
	}
	secondStore, err := store.Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = secondStore.Close() }()
	second := New(secondStore, f.svc.providers, NoopEngine{}, nil, f.svc.cfg, nil)
	view, err := f.svc.GetCancellationReview(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		svc := f.svc
		if i%2 == 1 {
			svc = second
		}
		wg.Go(func() { _, err := svc.CancelReviewed(ctx, tor.ID, key, view.Revision); results <- err })
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	count, err := f.store.CountTorrentSubmissions(ctx, tor.ID)
	if err != nil || count != 1 {
		t.Fatal("multiple cancellation receipts", count, err)
	}
	if _, err := f.svc.CancelReviewed(ctx, tor.ID, key, strings.Repeat("f", 64)); !errors.Is(err, ErrValidation) {
		t.Fatal("key mismatch", err)
	}
}

func TestCancellationReviewCannotReturnAfterRetryCounterABA(t *testing.T) {
	f, tor, dl := cancellationFixture(t)
	ctx := context.Background()
	review, err := f.svc.GetCancellationReview(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CancelReviewed(ctx, tor.ID, uuid.NewString(), review.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RetryTorrent(ctx, tor.ID); err != nil {
		t.Fatal(err)
	}
	// Restore every public incarnation/progress/time field to the old values.
	// The durable parent receipt fence must still invalidate the old review.
	if _, err := f.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error { *t = tor; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.InsertDownload(ctx, store.DownloadInsertParams(dl)); err != nil {
		t.Fatal(err)
	}
	drains := len(f.eng.cancels)
	if _, err := f.svc.CancelReviewed(ctx, tor.ID, uuid.NewString(), review.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale review survived ABA", err)
	}
	if len(f.eng.cancels) != drains {
		t.Fatal("ABA rejection drained new work")
	}
}
