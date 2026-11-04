package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func retryFixture(t *testing.T) (*fixture, domain.Torrent, domain.Download) {
	t.Helper()
	f := setup(t)
	ctx := context.Background()
	f.addAccount(t, "main")
	detail, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA})
	if err != nil {
		t.Fatal(err)
	}
	tor := detail.Torrent
	tor.Status = domain.TorrentError
	tor.Error = "fixture failure"
	at := tor.UpdatedAt
	tor.CompletedAt = &at
	params, err := store.TorrentUpdateParams(tor)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateTorrent(ctx, params); err != nil {
		t.Fatal(err)
	}
	dl := domain.Download{ID: "failed-file", TorrentID: tor.ID, ProviderLink: "fixture-private-url", RelPath: "one.mkv", Filename: "one.mkv", State: domain.DownloadError, Error: "fixture failure", QueuedAt: at, UpdatedAt: at, CompletedAt: &at}
	if _, err := f.store.InsertDownload(ctx, store.DownloadInsertParams(dl)); err != nil {
		t.Fatal(err)
	}
	return f, tor, dl
}
func TestReviewedRetryReplaysBeforeStateChecksAndSurvivesRemoval(t *testing.T) {
	f, tor, _ := retryFixture(t)
	ctx := context.Background()
	key := uuid.NewString()
	review, err := f.svc.GetRetryReview(ctx, RetryWholeTorrent, tor.ID)
	if err != nil || !review.Allowed {
		t.Fatal(review, err)
	}
	receipt, err := f.svc.RetryReviewed(ctx, RetryWholeTorrent, tor.ID, key, review.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		replay, err := f.svc.RetryReviewed(ctx, RetryWholeTorrent, tor.ID, key, review.Revision)
		if err != nil || replay.TorrentID != tor.ID || !replay.AcceptedAt.Equal(receipt.AcceptedAt) {
			t.Fatal(replay, err)
		}
	}
	current, err := f.svc.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.RetryCount != tor.RetryCount+1 || len(f.eng.cancels) != 1 {
		t.Fatal("retry repeated", current, err)
	}
	if _, err := f.svc.GetTorrentSubmission(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("retry key leaked into add namespace", err)
	}
	if _, err := f.svc.RetryReviewed(ctx, RetryWholeTorrent, tor.ID, key, strings.Repeat("b", 64)); !errors.Is(err, ErrValidation) {
		t.Fatal("key mismatch accepted", err)
	}
	if err := f.svc.DeleteTorrent(ctx, tor.ID, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	replay, err := f.svc.GetRetryAcceptance(ctx, RetryWholeTorrent, tor.ID, key, review.Revision)
	if err != nil || replay.TorrentExists {
		t.Fatal("receipt removed with parent", replay, err)
	}
	if _, err := f.svc.RetryReviewed(ctx, RetryWholeTorrent, tor.ID, key, review.Revision); err != nil {
		t.Fatal("removed receipt was not replayed", err)
	}
}
func TestReviewedRetryRejectsChangedRulesBeforeCanceling(t *testing.T) {
	f, tor, _ := retryFixture(t)
	ctx := context.Background()
	review, err := f.svc.GetRetryReview(ctx, RetryWholeTorrent, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	tor.Settings.DownloadRetries++
	params, err := store.TorrentUpdateParams(tor)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateTorrent(ctx, params); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RetryReviewed(ctx, RetryWholeTorrent, tor.ID, uuid.NewString(), review.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale rules accepted", err)
	}
	if len(f.eng.cancels) != 0 {
		t.Fatal("stale request canceled work")
	}
}
func TestFileRetryFencePreventsIdenticalTimestampAndCounterABA(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "reviewed", true: "legacy"}[legacy], func(t *testing.T) {
			f, tor, dl := retryFixture(t)
			ctx := context.Background()
			f.svc.now = func() time.Time { return tor.UpdatedAt }
			review, err := f.svc.GetRetryReview(ctx, RetryOneDownload, dl.ID)
			if err != nil || !review.Allowed {
				t.Fatal(review, err)
			}
			if legacy {
				_, err = f.svc.RetryDownload(ctx, dl.ID)
			} else {
				_, err = f.svc.RetryReviewed(ctx, RetryOneDownload, dl.ID, uuid.NewString(), review.Revision)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Reproduce byte-identical failure fields; the committed receipt still
			// distinguishes this later error episode from the earlier review.
			tp, err := store.TorrentUpdateParams(tor)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.UpdateTorrent(ctx, tp); err != nil {
				t.Fatal(err)
			}
			if err := f.store.UpdateDownload(ctx, store.DownloadUpdateParams(dl)); err != nil {
				t.Fatal(err)
			}
			next, err := f.svc.GetRetryReview(ctx, RetryOneDownload, dl.ID)
			if err != nil || next.Revision == review.Revision {
				t.Fatal("retry fence failed", next, err)
			}
			if _, err := f.svc.RetryReviewed(ctx, RetryOneDownload, dl.ID, uuid.NewString(), review.Revision); !errors.Is(err, ErrConflict) {
				t.Fatal("old review applied to later failure", err)
			}
		})
	}
}
func TestRetryMutationAndReceiptRollbackTogether(t *testing.T) {
	f, tor, _ := retryFixture(t)
	ctx := context.Background()
	key := uuid.NewString()
	review, err := f.svc.GetRetryReview(ctx, RetryWholeTorrent, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().ExecContext(ctx, "CREATE TRIGGER reject_retry_receipt BEFORE INSERT ON torrent_submissions BEGIN SELECT RAISE(ABORT,'fixture'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RetryReviewed(ctx, RetryWholeTorrent, tor.ID, key, review.Revision); err == nil {
		t.Fatal("receipt failure accepted")
	}
	current, err := f.svc.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.Status != domain.TorrentError || current.Torrent.RetryCount != tor.RetryCount || len(current.Downloads) != 1 {
		t.Fatal("mutation escaped receipt rollback", current, err)
	}
	if _, err := f.svc.GetRetryAcceptance(ctx, RetryWholeTorrent, tor.ID, key, review.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatal("rollback left receipt", err)
	}
	if _, err := f.store.DB().ExecContext(ctx, "DROP TRIGGER reject_retry_receipt"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RetryReviewed(ctx, RetryWholeTorrent, tor.ID, key, review.Revision); err != nil {
		t.Fatal("safe retry after rollback failed", err)
	}
}
func TestConcurrentSameKeyRetryCommitsOnce(t *testing.T) {
	f, tor, _ := retryFixture(t)
	f.svc.SetEngine(NoopEngine{})
	ctx := context.Background()
	key := uuid.NewString()
	review, err := f.svc.GetRetryReview(ctx, RetryWholeTorrent, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := f.svc.RetryReviewed(ctx, RetryWholeTorrent, tor.ID, key, review.Revision)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := f.svc.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.RetryCount != tor.RetryCount+1 {
		t.Fatal("concurrent retry repeated", current, err)
	}
}

func TestRetryReceiptConcurrencyAcrossIndependentStoreHandles(t *testing.T) {
	f, tor, _ := retryFixture(t)
	f.svc.SetEngine(NoopEngine{})
	ctx := context.Background()
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
	view, err := f.svc.GetRetryReview(ctx, RetryWholeTorrent, tor.ID)
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
		wg.Go(func() {
			_, err := svc.RetryReviewed(ctx, RetryWholeTorrent, tor.ID, key, view.Revision)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := f.svc.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.RetryCount != tor.RetryCount+1 {
		t.Fatal("independent handles repeated retry", current, err)
	}
}
