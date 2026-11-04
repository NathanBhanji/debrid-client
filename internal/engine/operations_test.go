package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func TestTorrentOperationAdmissionIsScopedAndCancellationReleasesReferences(t *testing.T) {
	e := &Engine{}
	ctx := context.Background()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- e.WithTorrentOperation(ctx, "one", func() error { close(entered); <-release; return nil })
	}()
	<-entered
	other := make(chan error, 1)
	go func() { other <- e.WithTorrentOperation(ctx, "two", func() error { return nil }) }()
	select {
	case err := <-other:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated torrent blocked")
	}
	canceled, cancel := context.WithCancel(ctx)
	waiting := make(chan error, 1)
	go func() {
		waiting <- e.WithTorrentOperation(canceled, "one", func() error { return errors.New("canceled waiter entered operation") })
	}()
	cancel()
	select {
	case err := <-waiting:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter blocked")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	remaining := len(e.operations)
	e.mu.Unlock()
	if remaining != 0 {
		t.Fatal("operation slots leaked", remaining)
	}
}

func TestSchedulerSkipsBusyTorrentAndAdmitsUnrelatedWork(t *testing.T) {
	h := newHarness(t, nil)
	h.cancel()
	<-h.done
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	other := h.add(nil)
	busy, err := h.svc.AddTorrent(ctx, service.AddTorrentInput{Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("c", 40)})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := h.eng.loadTorrents(ctx)
	if err != nil || len(listed) != 2 || listed[0].ID != busy.Torrent.ID {
		t.Fatal("fixture order", listed, err)
	}
	entered, release, operationDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		operationDone <- h.eng.WithTorrentOperation(ctx, busy.Torrent.ID, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	defer func() { cancel(); close(release); <-operationDone; h.eng.wg.Wait() }()
	tickDone := make(chan error, 1)
	go func() { tickDone <- h.eng.tick(ctx) }()
	select {
	case err := <-tickDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("busy first torrent blocked the scheduler")
	}
	h.waitProviderID(other.ID)
	current, err := h.svc.GetTorrent(ctx, busy.Torrent.ID)
	if err != nil || current.Torrent.ProviderID != "" {
		t.Fatal("busy torrent admitted work", current, err)
	}
}

func TestStaleScheduledCleanupCannotDeleteRetriedTorrent(t *testing.T) {
	h := newHarness(t, nil)
	h.cancel()
	<-h.done
	ctx := context.Background()
	tor := h.add(nil)
	tor.Status = domain.TorrentError
	at := time.Now().Add(-time.Hour)
	tor.CompletedAt = &at
	tor.Settings.DeleteOnError = time.Second
	params, err := store.TorrentUpdateParams(tor)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.eng.store.UpdateTorrent(ctx, params); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RetryTorrent(ctx, tor.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.eng.deleteErrorTorrent(ctx, tor); err != nil {
		t.Fatal(err)
	}
	current, err := h.svc.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.Status != domain.TorrentQueued || current.Torrent.RetryCount != tor.RetryCount+1 {
		t.Fatal("stale cleanup deleted retry", current, err)
	}
}
