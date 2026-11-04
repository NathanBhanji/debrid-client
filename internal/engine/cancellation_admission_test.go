package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/providertest"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func TestProviderPollCannotApplyOldListingToNewIncarnation(t *testing.T) {
	for _, result := range []string{"error", "missing", "relink"} {
		t.Run(result, func(t *testing.T) {
			h := newHarness(t, nil)
			h.cancel()
			<-h.done
			ctx := context.Background()
			tor := h.add(nil)
			added, err := h.fake.AddMagnet(ctx, magnet)
			if err != nil {
				t.Fatal(err)
			}
			tor.ProviderID = added.ID
			tor.Status = domain.TorrentProcessing
			tor.Settings.TorrentRetries = 2
			params, err := store.TorrentUpdateParams(tor)
			if err != nil {
				t.Fatal(err)
			}
			if err = h.eng.store.UpdateTorrent(ctx, params); err != nil {
				t.Fatal(err)
			}
			switch result {
			case "error":
				h.fake.Fail(added.ID, "old remote failure")
			case "missing":
				h.fake.Remove(added.ID)
			case "relink":
				h.fake.Remove(added.ID)
				if _, err = h.fake.AddMagnet(ctx, magnet); err != nil {
					t.Fatal(err)
				}
			}
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			h.fake.SetHooks(providertest.Hooks{OnList: func() error { close(entered); <-release; return nil }})
			go func() { done <- h.eng.pollAccount(ctx, tor.AccountID) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("poll did not reach network")
			}
			// A new explicit retry can get the same provider ID after deduplication.
			// The delayed listing still belongs to the earlier retry incarnation.
			_, err = h.eng.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error { t.RetryCount++; return nil })
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("poll stuck")
			}
			current, err := h.svc.GetTorrent(ctx, tor.ID)
			if err != nil || current.Torrent.Status != domain.TorrentProcessing || current.Torrent.ProviderID != added.ID || current.Torrent.RetryCount != tor.RetryCount+1 || h.fake.Calls("Delete") != 0 {
				t.Fatal("old listing mutated new incarnation", current, err)
			}
		})
	}
}

func TestDeleteOwnsAdmissionAndDoesNotRemoveWhileBusy(t *testing.T) {
	h := newHarness(t, nil)
	h.cancel()
	<-h.done
	tor := h.add(nil)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- h.eng.WithTorrentOperation(context.Background(), tor.ID, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := h.svc.DeleteTorrent(ctx, tor.ID, service.DeleteOptions{})
	close(release)
	if opErr := <-done; opErr != nil {
		t.Fatal(opErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("delete bypassed admission", err)
	}
	if _, err := h.svc.GetTorrent(context.Background(), tor.ID); err != nil {
		t.Fatal("busy record removed", err)
	}
}

func TestProviderErrorAndFailureCannotOverwriteCompletedTransfer(t *testing.T) {
	h := newHarness(t, nil)
	h.cancel()
	<-h.done
	ctx := context.Background()
	tor := h.add(nil)
	tor.Status = domain.TorrentProcessing
	tor.ProviderID = "fixture-provider"
	params, _ := store.TorrentUpdateParams(tor)
	if err := h.eng.store.UpdateTorrent(ctx, params); err != nil {
		t.Fatal(err)
	}
	stale := tor
	_, err := h.eng.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error { t.Status = domain.TorrentCompleted; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := h.eng.applyProviderState(ctx, &stale, provider.Torrent{Status: domain.TorrentError}, h.fake.Caps()); err != nil {
		t.Fatal(err)
	}
	if err := h.eng.fail(ctx, &tor, "late worker failure"); err != nil {
		t.Fatal(err)
	}
	current, err := h.svc.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.Status != domain.TorrentCompleted || h.fake.Calls("Delete") != 0 {
		t.Fatal(current, err)
	}
}

// This provider captures a response before holding it, unlike a hook that
// merely delays the provider's snapshot. It models a real delayed HTTP reply.
type heldListingProvider struct {
	provider.Provider
	entered, release chan struct{}
}

func (p *heldListingProvider) ListTorrents(ctx context.Context) ([]provider.Torrent, error) {
	snapshot, err := p.Provider.ListTorrents(ctx)
	close(p.entered)
	select {
	case <-p.release:
		return snapshot, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestDelayedListingCannotRepopulateDeletedProviderCache(t *testing.T) {
	h := newHarness(t, nil)
	h.cancel()
	<-h.done
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tor := h.add(nil)
	remote, err := h.fake.AddMagnet(ctx, magnet)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.eng.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error {
		t.ProviderID = remote.ID
		t.Status = domain.TorrentProcessing
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	held := &heldListingProvider{Provider: h.fake, entered: make(chan struct{}), release: make(chan struct{})}
	resolver := service.NewProviders(h.eng.store, func(domain.ProviderKind, domain.Credentials, provider.Options) (provider.Provider, error) {
		return held, nil
	}, provider.Options{})
	h.svc = service.New(h.eng.store, resolver, h.eng, h.bus, service.Config{DownloadDir: h.eng.cfg.DownloadDir}, nil)
	h.eng.svc = h.svc
	done := make(chan error, 1)
	go func() { done <- h.eng.pollAccount(ctx, tor.AccountID) }()
	select {
	case <-held.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	err = h.svc.DeleteTorrent(ctx, tor.ID, service.DeleteOptions{DeleteFromProvider: true})
	if err != nil {
		close(held.release)
		<-done
		t.Fatal(err)
	}
	next, err := h.svc.AddTorrent(ctx, service.AddTorrentInput{Magnet: magnet})
	close(held.release)
	if pollErr := <-done; pollErr != nil {
		t.Fatal(pollErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, found := h.eng.lookupHash(tor.AccountID, tor.Hash); found {
		t.Fatal("invalidated provider cache was republished")
	}
	if err := h.eng.tick(ctx); err != nil {
		t.Fatal(err)
	}
	h.eng.wg.Wait()
	current, err := h.svc.GetTorrent(ctx, next.Torrent.ID)
	if err != nil || current.Torrent.ProviderID == "" || current.Torrent.ProviderID == remote.ID || h.fake.Calls("Add") != 2 {
		t.Fatal("new transfer adopted deleted provider ID", current, err, h.fake.Calls("Add"))
	}
}
