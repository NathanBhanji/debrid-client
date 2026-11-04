package engine

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/fetch"
)

func TestCancellationDrainsWorkerRetainsPartialAndSurvivesRecovery(t *testing.T) {
	h := newHarness(t, nil)
	started, stopped := make(chan string, 1), make(chan struct{})
	var calls atomic.Int32
	h.eng.SetFetcher(func(ctx context.Context, _ string, dest string, _ fetch.Options) (fetch.Result, error) {
		calls.Add(1)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fetch.Result{}, err
		}
		partial := dest + ".part"
		if err := os.WriteFile(partial, []byte("partial fixture bytes"), 0o600); err != nil {
			return fetch.Result{}, err
		}
		started <- partial
		<-ctx.Done()
		close(stopped)
		return fetch.Result{}, ctx.Err()
	})
	settings := domain.DefaultTorrentSettings()
	settings.DeleteOnError = time.Nanosecond
	settings.FinishedAction = domain.FinishedRemoveFromProvider
	tor := h.add(&settings)
	pid := h.waitProviderID(tor.ID)
	h.finishAtProvider(pid, map[string][]byte{"one.mkv": []byte("fixture bytes")})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var partial string
	select {
	case partial = <-started:
	case <-ctx.Done():
		t.Fatal("fetch did not start", ctx.Err())
	}
	view, err := h.svc.GetCancellationReview(ctx, tor.ID)
	if err != nil || !view.Allowed {
		t.Fatal(view, err)
	}
	receipt, err := h.svc.CancelReviewed(ctx, tor.ID, uuid.NewString(), view.Revision)
	if err != nil || !receipt.TorrentExists {
		t.Fatal(receipt, err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("receipt committed before worker stopped")
	}
	current, err := h.svc.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.Status != domain.TorrentCancelled || len(current.Downloads) != 1 || current.Downloads[0].State != domain.DownloadCancelled {
		t.Fatal(current, err)
	}
	data, err := os.ReadFile(partial)
	if err != nil || string(data) != "partial fixture bytes" {
		t.Fatal("partial bytes removed", err)
	}
	h.cancel()
	<-h.done
	h.fake.Fail(pid, "remote provider failed after local cancellation")
	fresh := New(h.eng.cfg, h.eng.store, h.svc, h.bus, nil)
	h.svc.SetEngine(fresh)
	if err := fresh.recover(ctx); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := fresh.pollAccount(ctx, tor.AccountID); err != nil {
			t.Fatal(err)
		}
		if err := fresh.tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	current, err = h.svc.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.Status != domain.TorrentCancelled || current.Downloads[0].State != domain.DownloadCancelled || calls.Load() != 1 || h.fake.Calls("Delete") != 0 {
		t.Fatal("cancelled work resumed or cleanup ran", current, err, calls.Load())
	}
}
