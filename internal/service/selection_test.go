package service

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func selectionFixture(t *testing.T) (*fixture, domain.Torrent, string) {
	t.Helper()
	f := setup(t)
	f.addAccount(t, "main")
	ctx := context.Background()
	settings := domain.DefaultTorrentSettings()
	settings.WaitForSelection = true
	d, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA, Settings: &settings})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := f.fake.AddMagnet(ctx, magnetA)
	if err != nil {
		t.Fatal(err)
	}
	files := []domain.File{{ID: "real/one", Path: "one.mkv", Size: 10}, {ID: "real/two", Path: "two.mkv", Size: 20}}
	f.fake.SetFiles(remote.ID, files)
	f.fake.Finish(remote.ID, nil)
	tor, err := f.store.MutateTorrent(ctx, d.Torrent.ID, func(t *domain.Torrent) error {
		t.ProviderID = remote.ID
		t.Status = domain.TorrentFinished
		t.Files = []domain.File{{ID: "parsed-index", Path: "untrusted"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, tor, remote.ID
}

func TestFileReviewUsesProviderIDsAndMonotonicAcceptance(t *testing.T) {
	f, tor, pid := selectionFixture(t)
	ctx := context.Background()
	view, err := f.svc.GetFileSelection(ctx, tor.ID)
	if err != nil || view.State != "ready" || view.Files[0].ID == "parsed-index" {
		t.Fatalf("view %+v %v", view, err)
	}
	if _, err := f.svc.SelectFiles(ctx, tor.ID, []string{"parsed-index"}); !errors.Is(err, ErrConflict) {
		t.Fatal("legacy bypass", err)
	}
	defaults := domain.DefaultTorrentSettings()
	updated, err := f.svc.UpdateTorrent(ctx, tor.ID, UpdateTorrentInput{Settings: &defaults})
	if err != nil || !updated.Torrent.Settings.WaitForSelection {
		t.Fatal("generic update released hold", err)
	}
	if _, err := f.svc.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{"parsed-index"}); !errors.Is(err, ErrValidation) {
		t.Fatal("parsed ID accepted", err)
	}
	old := view.Revision
	f.fake.SetFiles(pid, []domain.File{{ID: "real/one", Path: "renamed.mkv", Size: 10}, {ID: "real/two", Path: "two.mkv", Size: 20}})
	if _, err := f.svc.ConfirmFileSelection(ctx, tor.ID, old, []string{"real/one"}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale review accepted", err)
	}
	view, err = f.svc.GetFileSelection(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := f.svc.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{"real/one"})
	if err != nil || accepted.State != "accepted" {
		t.Fatal("accept", err)
	}
	download := domain.Download{ID: "started", TorrentID: tor.ID, FileID: "real/one", State: domain.DownloadDownloading, QueuedAt: tor.AddedAt, UpdatedAt: tor.AddedAt}
	if _, err := f.store.InsertDownload(ctx, store.DownloadInsertParams(download)); err != nil {
		t.Fatal(err)
	}
	f.fake.SetErr(provider.Errorf(provider.ErrAuth, "", "fixture offline"))
	restarted := New(f.store, f.svc.providers, NoopEngine{}, events.New(), Config{}, nil)
	if replay, err := restarted.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{"real/one"}); err != nil || !slices.Equal(replay.SelectedIDs, accepted.SelectedIDs) {
		t.Fatal("lost response replay needs provider", err)
	}
	if _, err := restarted.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{"real/two"}); !errors.Is(err, ErrConflict) {
		t.Fatal("changed accepted choice", err)
	}
	updated, err = restarted.UpdateTorrent(ctx, tor.ID, UpdateTorrentInput{Settings: &defaults})
	if err != nil || !updated.Torrent.SelectionReady() {
		t.Fatal("generic update lost receipt", err)
	}
}

func TestSelectionWaitsForProviderPhaseAndRejectsAmbiguity(t *testing.T) {
	f, tor, pid := selectionFixture(t)
	ctx := context.Background()
	f.fake.SetStatus(pid, domain.TorrentDownloading, .5)
	if v, err := f.svc.GetFileSelection(ctx, tor.ID); err != nil || v.State != "pending" {
		t.Fatal("offered unfinished provider metadata", v, err)
	}
	f.fake.SetCaps(provider.Caps{SelectFiles: true})
	f.fake.SetStatus(pid, domain.TorrentWaitingSelection, 0)
	if v, err := f.svc.GetFileSelection(ctx, tor.ID); err != nil || v.State != "ready" || !v.ProviderSelects {
		t.Fatal("RD review unavailable", v, err)
	}
	f.fake.SetStatus(pid, domain.TorrentFinished, 1)
	if v, err := f.svc.GetFileSelection(ctx, tor.ID); err != nil || v.State != "unavailable" {
		t.Fatal("RD already-selected subset promised", v, err)
	}
	f.fake.SetStatus(pid, domain.TorrentWaitingSelection, 0)
	f.fake.SetFiles(pid, []domain.File{{ID: "dup"}, {ID: "dup"}})
	if v, err := f.svc.GetFileSelection(ctx, tor.ID); err != nil || v.State != "unavailable" {
		t.Fatal("ambiguous metadata", v, err)
	}
}

func TestSelectionConflictsSerializeAcrossIndependentDatabaseHandles(t *testing.T) {
	f, tor, _ := selectionFixture(t)
	ctx := context.Background()
	f.svc.SetEngine(NoopEngine{})
	view, err := f.svc.GetFileSelection(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(ctx, filepath.Join(f.dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	other := New(second, f.svc.providers, NoopEngine{}, events.New(), Config{}, nil)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, svc := range []*Service{f.svc, other} {
		wg.Add(1)
		go func(i int, svc *Service) {
			defer wg.Done()
			<-start
			_, err := svc.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{[]string{"real/one", "real/two"}[i]})
			results <- err
		}(i, svc)
	}
	close(start)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	if _, err := f.svc.ConfirmFileSelection(ctx, tor.ID, strings.Repeat("a", 64), nil); !errors.Is(err, ErrValidation) {
		t.Fatal("empty selection", err)
	}
}

func TestSelectionCannotReleasePreexistingDownloadsAndRetryNeedsNewReview(t *testing.T) {
	f, tor, _ := selectionFixture(t)
	ctx := context.Background()
	view, err := f.svc.GetFileSelection(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	download := domain.Download{ID: "pending", TorrentID: tor.ID, FileID: "real/one", State: domain.DownloadPending, QueuedAt: tor.AddedAt, UpdatedAt: tor.AddedAt}
	if _, err := f.store.InsertDownload(ctx, store.DownloadInsertParams(download)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{"real/one"}); !errors.Is(err, ErrConflict) {
		t.Fatal("existing downloads accepted", err)
	}
	if err := f.store.DeleteDownload(ctx, "pending"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{"real/one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error { t.Status = domain.TorrentError; return nil }); err != nil {
		t.Fatal(err)
	}
	retried, err := f.svc.RetryTorrent(ctx, tor.ID)
	if err != nil || !retried.Torrent.Settings.WaitForSelection || retried.Torrent.Settings.Selection != nil || len(retried.Torrent.Settings.ManualFiles) != 0 {
		t.Fatal("retry reused old selection", retried, err)
	}
	// Even a provider that reuses its old ID cannot reuse the prior revision.
	if _, err := f.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error {
		t.ProviderID = tor.ProviderID
		t.Status = domain.TorrentFinished
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ConfirmFileSelection(ctx, tor.ID, view.Revision, []string{"real/one"}); !errors.Is(err, ErrConflict) {
		t.Fatal("old revision survived retry", err)
	}
}

func TestAutomaticSubmissionFingerprintRemainsV023Compatible(t *testing.T) {
	settings := domain.DefaultTorrentSettings()
	in := AddTorrentInput{SubmissionKey: "11111111-1111-4111-8111-111111111111", Magnet: magnetA, Settings: &settings}
	meta := domain.Torrent{PayloadKind: domain.PayloadMagnet, Payload: []byte(magnetA)}
	_, fingerprint, err := submissionIdentity(in, meta)
	// Golden from the v0.2.3 version1 intent representation, before selection fields.
	const legacy = "d6a457375d2d7eed06576b0c10f894a122ef009e89b5dfe144555234d56ff6f3"
	if err != nil || fingerprint != legacy {
		t.Fatal("automatic intent changed across upgrade", fingerprint, err)
	}
	settings.WaitForSelection = true
	_, manual, err := submissionIdentity(in, meta)
	if err != nil || manual == legacy {
		t.Fatal("manual policy was not fingerprinted", err)
	}
}
