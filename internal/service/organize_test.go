package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/organize"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func boolp(b bool) *bool { return &b }

func TestDirPathFor(t *testing.T) {
	on := organize.Settings{Enabled: true}
	off := organize.Settings{}
	movie := domain.Torrent{Name: "Some.Movie.2019.2160p.WEB-DL.x265-GRP", Hash: "h"}
	tvPack := domain.Torrent{Name: "Some.Show.S02.1080p.BluRay.x265-GRP", Hash: "h"}
	garbage := domain.Torrent{Name: "backup-2 of photos", Hash: "h"}

	cases := []struct {
		name          string
		t             domain.Torrent
		org           organize.Settings
		want          string
		wantOrganized bool
	}{
		{"movie organized", movie, on, "Some Movie (2019)", true},
		{"tv pack organized", tvPack, on, "Some Show/Season 02", true},
		{"disabled keeps raw", movie, off, "Some.Movie.2019.2160p.WEB-DL.x265-GRP", false},
		{"unparseable falls back", garbage, on, "backup-2 of photos", false},
		{"custom template", movie, organize.Settings{Enabled: true, MovieTemplate: "Films/{title} ({year}) [{resolution}]"}, "Films/Some Movie (2019) [2160p]", true},
	}
	for _, tc := range cases {
		got, organized := DirPathFor(tc.t, tc.org)
		if got != tc.want || organized != tc.wantOrganized {
			t.Errorf("%s: DirPathFor = (%q, %v), want (%q, %v)", tc.name, got, organized, tc.want, tc.wantOrganized)
		}
	}

	// Per-torrent override wins in both directions.
	optOut := movie
	optOut.Settings.Organize = boolp(false)
	if got, org := DirPathFor(optOut, on); org || got != movie.Name {
		t.Errorf("opt-out: (%q, %v)", got, org)
	}
	optIn := movie
	optIn.Settings.Organize = boolp(true)
	if got, org := DirPathFor(optIn, off); !org || got != "Some Movie (2019)" {
		t.Errorf("opt-in: (%q, %v)", got, org)
	}
}

// TestOrganizedDelete verifies per-file deletion in a shared organized dir:
// only the deleted torrent's tracked files (and its extracted output) go,
// sibling files survive, and directories are pruned once empty.
func TestOrganizedDelete(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now()
	acc := f.addAccount(t, "main")

	mk := func(id, hash, dirName string, organized bool) domain.Torrent {
		tor := domain.Torrent{
			ID: id, AccountID: acc.ID, Hash: hash, Name: id,
			DirName: dirName, Organized: organized,
			Status: domain.TorrentCompleted, AddedAt: now, UpdatedAt: now,
			Settings: domain.DefaultTorrentSettings(),
		}
		params, err := store.TorrentInsertParams(tor)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.InsertTorrent(ctx, params); err != nil {
			t.Fatal(err)
		}
		return tor
	}
	addDL := func(id, torrentID, rel string, extracted []string) {
		d := domain.Download{
			ID: id, TorrentID: torrentID, FileID: id, ProviderLink: "link-" + id,
			RelPath: rel, Filename: filepath.Base(rel), State: domain.DownloadDone,
			ExtractedPaths: extracted, QueuedAt: now, UpdatedAt: now,
		}
		if _, err := f.store.InsertDownload(ctx, store.DownloadInsertParams(d)); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel string) string {
		p := filepath.Join(f.svc.cfg.DownloadDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Two season packs share "Show/Season 02"; a movie owns its own dir.
	a := mk("torA", "aaaa", "Show/Season 02", true)
	mk("torB", "bbbb", "Show/Season 02", true)
	addDL("dlA1", "torA", "ep1.mkv", nil)
	addDL("dlA2", "torA", "ep2.rar", []string{"ep2.mkv"})
	addDL("dlB1", "torB", "ep3.mkv", nil)
	a1 := write("Show/Season 02/ep1.mkv")
	a2rar := write("Show/Season 02/ep2.rar")
	a2mkv := write("Show/Season 02/ep2.mkv")
	b1 := write("Show/Season 02/ep3.mkv")

	if err := f.svc.DeleteTorrent(ctx, a.ID, DeleteOptions{DeleteFiles: true}); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{a1, a2rar, a2mkv} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("still exists: %s", gone)
		}
	}
	if _, err := os.Stat(b1); err != nil {
		t.Errorf("sibling torrent's file removed: %v", err)
	}

	// Deleting the second torrent empties the tree: Season 02 and Show prune.
	if err := f.svc.DeleteTorrent(ctx, "torB", DeleteOptions{DeleteFiles: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.svc.cfg.DownloadDir, "Show")); !os.IsNotExist(err) {
		t.Error("empty organized tree not pruned")
	}
	if _, err := os.Stat(f.svc.cfg.DownloadDir); err != nil {
		t.Errorf("download root must survive: %v", err)
	}
}
