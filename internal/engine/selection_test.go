package engine

import (
	"context"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
)

type selectionWorkerProvider struct {
	provider.Provider
	get   func(context.Context, string) (provider.Torrent, error)
	links func(context.Context, string) ([]provider.Link, error)
}

func (p selectionWorkerProvider) GetTorrent(ctx context.Context, id string) (provider.Torrent, error) {
	if p.get != nil {
		return p.get(ctx, id)
	}
	return p.Provider.GetTorrent(ctx, id)
}
func (p selectionWorkerProvider) Links(ctx context.Context, id string) ([]provider.Link, error) {
	if p.links != nil {
		return p.links(ctx, id)
	}
	return p.Provider.Links(ctx, id)
}

func TestManualSelectionHoldsCachedTorrentPastLinksTimeout(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	settings := domain.DefaultTorrentSettings()
	settings.WaitForSelection = true
	settings.Unpack = false
	tor := h.add(&settings)
	pid := h.waitProviderID(tor.ID)
	h.finishAtProvider(pid, map[string][]byte{"one.mkv": []byte("one"), "two.mkv": []byte("two")})
	h.waitStatus(tor.ID, domain.TorrentFinished)
	time.Sleep(450 * time.Millisecond) // exceeds the fixture's300ms link timeout
	detail, err := h.svc.GetTorrent(ctx, tor.ID)
	if err != nil || detail.Torrent.Status != domain.TorrentFinished || len(detail.Downloads) != 0 || h.fake.Calls("Links") != 0 {
		t.Fatal("held transfer advanced or timed out", detail, err)
	}
	h.fetch.mu.Lock()
	calls := h.fetch.calls
	h.fetch.mu.Unlock()
	if calls != 0 {
		t.Fatal("NAS fetch before confirmation")
	}
	view, err := h.svc.GetFileSelection(ctx, tor.ID)
	if err != nil || view.State != "ready" {
		t.Fatal("review", view, err)
	}
	chosen := view.Files[0].ID
	if _, err := h.svc.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{chosen}); err != nil {
		t.Fatal(err)
	}
	h.waitStatus(tor.ID, domain.TorrentCompleted)
	detail, err = h.svc.GetTorrent(ctx, tor.ID)
	if err != nil || len(detail.Downloads) != 1 || detail.Downloads[0].FileID != chosen {
		t.Fatal("unselected file downloaded", detail, err)
	}
}

func acceptedSelectionWorker(t *testing.T, selects bool) (*harness, domain.Torrent) {
	t.Helper()
	h := newHarness(t, nil)
	h.cancel()
	<-h.done
	ctx := context.Background()
	settings := domain.DefaultTorrentSettings()
	settings.WaitForSelection = true
	settings.Unpack = false
	tor := h.add(&settings)
	h.fake.SetCaps(provider.Caps{SelectFiles: selects, DirectLinks: true})
	remote, err := h.fake.AddMagnet(ctx, magnet)
	if err != nil {
		t.Fatal(err)
	}
	files := []domain.File{{ID: "1", Path: "one.mkv", Size: 3}, {ID: "2", Path: "two.mkv", Size: 3}}
	h.fake.SetFiles(remote.ID, files)
	state := domain.TorrentFinished
	if selects {
		state = domain.TorrentWaitingSelection
	}
	h.fake.SetStatus(remote.ID, state, 1)
	_, err = h.eng.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error { t.ProviderID = remote.ID; t.Status = state; return nil })
	if err != nil {
		t.Fatal(err)
	}
	view, err := h.svc.GetFileSelection(ctx, tor.ID)
	if err != nil || view.State != "ready" {
		t.Fatal("fixture review", view, err)
	}
	if _, err := h.svc.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	d, err := h.svc.GetTorrent(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	return h, d.Torrent
}

func TestDelayedSelectionWorkersCannotTouchNewProviderIncarnation(t *testing.T) {
	for _, selects := range []bool{false, true} {
		for _, mismatch := range []bool{false, true} {
			name := "links"
			if selects {
				name = "select"
			}
			if mismatch {
				name += "-mismatch"
			}
			t.Run(name, func(t *testing.T) {
				h, old := acceptedSelectionWorker(t, selects)
				ctx := context.Background()
				entered, release := make(chan struct{}), make(chan struct{})
				remote, err := h.fake.GetTorrent(ctx, old.ProviderID)
				if err != nil {
					t.Fatal(err)
				}
				if mismatch {
					remote.Files[0].Path = "changed.mkv"
				}
				wrapped := selectionWorkerProvider{Provider: h.fake, get: func(context.Context, string) (provider.Torrent, error) { close(entered); <-release; return remote, nil }}
				done := make(chan struct{})
				go func() {
					defer close(done)
					if selects {
						h.eng.runSelect(ctx, old, wrapped)
					} else {
						h.eng.runCreateDownloads(ctx, old, wrapped)
					}
				}()
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("worker never reached metadata")
				}
				_, err = h.eng.store.MutateTorrent(ctx, old.ID, func(t *domain.Torrent) error {
					t.RetryCount++
					t.ProviderID = "new-provider"
					t.Settings.Selection = nil
					t.Settings.ManualFiles = nil
					t.Status = domain.TorrentProcessing
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				close(release)
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("worker did not finish")
				}
				current, err := h.svc.GetTorrent(ctx, old.ID)
				if err != nil || current.Torrent.Status != domain.TorrentProcessing || current.Torrent.Error != "" || len(current.Downloads) != 0 || h.fake.Calls("SelectFiles") != 0 || h.fake.Calls("Links") != 0 {
					t.Fatal("old worker affected new incarnation", current, err)
				}
			})
		}
	}
}

func TestReviewedLinkMetadataCannotChangeUnderSameID(t *testing.T) {
	for _, change := range []string{"path", "size", "missing"} {
		t.Run(change, func(t *testing.T) {
			h, tor := acceptedSelectionWorker(t, false)
			ctx := context.Background()
			link := provider.Link{FileID: "1", Path: "one.mkv", Size: 3, URL: "http://fixture.invalid/one"}
			switch change {
			case "path":
				link.Path = "unreviewed.mkv"
			case "size":
				link.Size = 4
			case "missing":
				link.FileID = "different"
			}
			wrapped := selectionWorkerProvider{Provider: h.fake, links: func(context.Context, string) ([]provider.Link, error) { return []provider.Link{link}, nil }}
			h.eng.runCreateDownloads(ctx, tor, wrapped)
			current, err := h.svc.GetTorrent(ctx, tor.ID)
			if err != nil || current.Torrent.Status != domain.TorrentError || len(current.Downloads) != 0 {
				t.Fatal("changed links queued", current, err)
			}
		})
	}
}

func TestRealDebridSelectionWaitAndRepackedResults(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.fake.SetCaps(provider.Caps{SelectFiles: true, DirectLinks: true})
	settings := domain.DefaultTorrentSettings()
	settings.WaitForSelection = true
	settings.Unpack = false
	tor := h.add(&settings)
	pid := h.waitProviderID(tor.ID)
	h.fake.SetFiles(pid, []domain.File{{ID: "1", Path: "one.mkv", Size: 3}, {ID: "2", Path: "two.mkv", Size: 3}})
	h.fake.SetStatus(pid, domain.TorrentWaitingSelection, 0)
	h.waitStatus(tor.ID, domain.TorrentWaitingSelection)
	time.Sleep(80 * time.Millisecond)
	if h.fake.Calls("SelectFiles") != 0 {
		t.Fatal("provider selection before confirmation")
	}
	view, err := h.svc.GetFileSelection(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.fake.Calls("SelectFiles") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if h.fake.Calls("SelectFiles") != 1 {
		t.Fatal("provider choice was not dispatched exactly once")
	}
	h.fetch.mu.Lock()
	h.fetch.content["http://cdn/packed"] = []byte("one")
	h.fetch.mu.Unlock()
	h.fake.Finish(pid, []provider.Link{{FileID: "link-1", Path: "provider-packed.bin", Size: 3, URL: "http://cdn/packed"}})
	h.waitStatus(tor.ID, domain.TorrentCompleted)
	detail, err := h.svc.GetTorrent(ctx, tor.ID)
	if err != nil || len(detail.Downloads) != 1 || detail.Downloads[0].FileID != "link-1" {
		t.Fatal("RD repacked result incorrectly remapped", detail, err)
	}
}

func TestQueuedSelectionDoesNotRepeatRecordedProviderChoice(t *testing.T) {
	h, stale := acceptedSelectionWorker(t, true)
	ctx := context.Background()
	if !h.eng.runSelect(ctx, stale, h.fake) {
		t.Fatal("first selection did not finish")
	}
	before := h.fake.Calls("SelectFiles")
	wrapped := selectionWorkerProvider{Provider: h.fake, get: func(context.Context, string) (provider.Torrent, error) {
		t.Fatal("already selected snapshot queried the provider again")
		return provider.Torrent{}, nil
	}}
	if !h.eng.runSelect(ctx, stale, wrapped) {
		t.Fatal("recorded selection not recognized")
	}
	current, err := h.svc.GetTorrent(ctx, stale.ID)
	if err != nil || current.Torrent.Status == domain.TorrentError || current.Torrent.FilesSelectedAt == nil || h.fake.Calls("SelectFiles") != before {
		t.Fatal("recorded selection repeated or failed", current, err)
	}
}
