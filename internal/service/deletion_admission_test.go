package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
)

func TestDeleteReloadsProviderAndPathsAfterDrain(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.addAccount(t, "main")
	detail, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA})
	if err != nil {
		t.Fatal(err)
	}
	late := detail.Torrent
	late.DirName = "late-final-path"
	path := TorrentDir(f.svc.cfg.DownloadDir, late)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "completed.mkv"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.eng.cancelFn = func(id string) error {
		added, err := f.fake.AddMagnet(ctx, magnetA)
		if err != nil {
			return err
		}
		_, err = f.store.MutateTorrent(ctx, id, func(t *domain.Torrent) error { t.ProviderID = added.ID; t.DirName = late.DirName; return nil })
		return err
	}
	if err := f.svc.DeleteTorrent(ctx, late.ID, DeleteOptions{DeleteFiles: true, DeleteFromProvider: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("late final path retained", err)
	}
	if len(f.fake.IDs()) != 0 {
		t.Fatal("late provider identity retained")
	}
}

func TestScheduledDeleteRechecksEligibilityAfterDrain(t *testing.T) {
	f, tor, _ := retryFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Hour)
	tor.CompletedAt = &at
	tor.Settings.DeleteOnError = time.Second
	_, err := f.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error { *t = tor; return nil })
	if err != nil {
		t.Fatal(err)
	}
	f.eng.cancelFn = func(id string) error {
		_, err := f.store.MutateTorrent(ctx, id, func(t *domain.Torrent) error { t.Status = domain.TorrentCompleted; return nil })
		return err
	}
	if err := f.svc.DeleteErroredTorrent(ctx, tor, time.Now()); err != nil {
		t.Fatal(err)
	}
	current, err := f.svc.GetTorrent(ctx, tor.ID)
	if err != nil || current.Torrent.Status != domain.TorrentCompleted || len(current.Downloads) != 1 {
		t.Fatal("stale cleanup removed completed data", current, err)
	}
}
