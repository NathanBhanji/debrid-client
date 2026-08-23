package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/providertest"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

type recEngine struct {
	wakes    int
	cancels  []string
	cancelFn func(string) error
}

func (r *recEngine) Wake() { r.wakes++ }
func (r *recEngine) CancelTorrent(_ context.Context, id string) error {
	r.cancels = append(r.cancels, id)
	if r.cancelFn != nil {
		return r.cancelFn(id)
	}
	return nil
}

type fixture struct {
	svc   *Service
	fake  *providertest.Fake
	eng   *recEngine
	dir   string
	store *store.Store
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := providertest.New(domain.ProviderTorBox)
	factory := func(kind domain.ProviderKind, creds domain.Credentials, _ provider.Options) (provider.Provider, error) {
		if creds.APIKey == "" {
			return nil, provider.Errorf(provider.ErrAuth, "", "key required")
		}
		return fake, nil
	}
	eng := &recEngine{}
	svc := New(st, NewProviders(st, factory, provider.Options{}), eng, events.New(), Config{DownloadDir: filepath.Join(dir, "dl")}, nil)
	return &fixture{svc: svc, fake: fake, eng: eng, dir: dir, store: st}
}

const magnetA = "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=Alpha"

func (f *fixture) addAccount(t *testing.T, name string) AccountView {
	t.Helper()
	a, err := f.svc.AddAccount(context.Background(), AddAccountInput{Kind: domain.ProviderTorBox, Name: name, Credentials: domain.Credentials{APIKey: "k"}})
	if err != nil {
		t.Fatalf("add account: %v", err)
	}
	return a
}

func TestAccountsLifecycle(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	if _, err := f.svc.AddAccount(ctx, AddAccountInput{Kind: "nope"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err := f.svc.AddAccount(ctx, AddAccountInput{Kind: domain.ProviderTorBox, Name: "x"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing key should fail validation: %v", err)
	}
	f.fake.Err = provider.Errorf(provider.ErrAuth, "", "bad")
	if _, err := f.svc.AddAccount(ctx, AddAccountInput{Kind: domain.ProviderTorBox, Name: "x", Credentials: domain.Credentials{APIKey: "k"}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("provider rejection should fail validation: %v", err)
	}
	f.fake.Err = nil

	a1 := f.addAccount(t, "main")
	if !a1.IsDefault || a1.User == nil || a1.User.Username != "fake" {
		t.Fatalf("first account should be default with user info: %+v", a1)
	}
	if _, err := f.svc.AddAccount(ctx, AddAccountInput{Kind: domain.ProviderTorBox, Name: "main", Credentials: domain.Credentials{APIKey: "k"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	a2 := f.addAccount(t, "second")
	if a2.IsDefault {
		t.Fatal("second account must not become default")
	}
	list, _ := f.svc.ListAccounts(ctx)
	if len(list) != 2 {
		t.Fatalf("list %d", len(list))
	}
	if got, _ := f.svc.GetAccount(ctx, "second"); got.ID != a2.ID {
		t.Fatal("get by name")
	}
	upd, err := f.svc.UpdateAccount(ctx, a2.ID, UpdateAccountInput{SetDefault: true, Name: ptr("two")})
	if err != nil || !upd.IsDefault || upd.Name != "two" {
		t.Fatalf("update: %v %+v", err, upd)
	}
	def, _ := f.svc.DefaultAccount(ctx)
	if def.ID != a2.ID {
		t.Fatal("default not switched")
	}
	if u, err := f.svc.TestAccount(ctx, "two"); err != nil || u.Username != "fake" {
		t.Fatalf("test account: %v %+v", err, u)
	}
	if r := upd.Redact(); r.Credentials.APIKey != "" {
		t.Fatal("redact")
	}
	// Delete default → other promoted.
	if err := f.svc.DeleteAccount(ctx, "two", false); err != nil {
		t.Fatal(err)
	}
	def, err = f.svc.DefaultAccount(ctx)
	if err != nil || def.ID != a1.ID {
		t.Fatalf("promotion: %v %+v", err, def)
	}
	if _, err := f.svc.GetAccount(ctx, "two"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted account should be not found")
	}
}

func TestAddTorrentValidationAndDuplicates(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no account should be not found: %v", err)
	}
	f.addAccount(t, "main")
	if _, err := f.svc.AddTorrent(ctx, AddTorrentInput{}); !errors.Is(err, ErrValidation) {
		t.Fatal("empty input")
	}
	if _, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: "nope"}); !errors.Is(err, ErrValidation) {
		t.Fatal("bad magnet")
	}
	if _, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA, Category: "../x"}); !errors.Is(err, ErrValidation) {
		t.Fatal("bad category")
	}
	if _, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA, Settings: &domain.TorrentSettings{IncludeRegex: "("}}); !errors.Is(err, ErrValidation) {
		t.Fatal("bad regex")
	}
	d, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA, Category: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	tor := d.Torrent
	if tor.Status != domain.TorrentQueued || tor.Hash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || tor.Name != "Alpha" || tor.Category != "tv" || tor.PayloadKind != domain.PayloadMagnet {
		t.Fatalf("torrent %+v", tor)
	}
	if tor.Settings.DownloadRetries != domain.DefaultTorrentSettings().DownloadRetries {
		t.Fatal("defaults not applied")
	}
	if f.eng.wakes != 1 {
		t.Fatalf("engine should be woken once, got %d", f.eng.wakes)
	}
	if _, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate active torrent should conflict: %v", err)
	}
	// Lookup by hash and by id.
	if got, err := f.svc.GetTorrent(ctx, tor.Hash); err != nil || got.Torrent.ID != tor.ID {
		t.Fatalf("get by hash: %v", err)
	}
	list, _ := f.svc.ListTorrents(ctx, ListFilter{Category: "tv"})
	if len(list) != 1 || len(list[0].Downloads) != 0 {
		t.Fatalf("list %+v", list)
	}
	if list, _ := f.svc.ListTorrents(ctx, ListFilter{Status: domain.TorrentError}); len(list) != 0 {
		t.Fatal("status filter")
	}
}

func TestDeleteTorrentOptions(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.addAccount(t, "main")
	d, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA, Category: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate engine having added it at the provider and written files.
	res, _ := f.fake.AddMagnet(ctx, magnetA)
	tor := d.Torrent
	tor.ProviderID = res.ID
	p, _ := store.TorrentUpdateParams(tor)
	_ = f.store.UpdateTorrent(ctx, p)
	dir := TorrentDir(f.svc.cfg.DownloadDir, tor)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a.mkv"), []byte("x"), 0o644)

	if err := f.svc.DeleteTorrent(ctx, tor.ID, DeleteOptions{DeleteFiles: true, DeleteFromProvider: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.eng.cancels) != 1 || f.eng.cancels[0] != tor.ID {
		t.Fatal("engine cancel not called")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("files should be deleted")
	}
	if len(f.fake.IDs()) != 0 {
		t.Fatal("should be deleted at provider")
	}
	if _, err := f.svc.GetTorrent(ctx, tor.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("record should be gone")
	}
	if err := f.svc.DeleteTorrent(ctx, "missing", DeleteOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing torrent")
	}
}

func TestRetryTorrentAndDownload(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.addAccount(t, "main")
	d, _ := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA})
	tor := d.Torrent
	if _, err := f.svc.RetryTorrent(ctx, tor.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("retrying a queued torrent should conflict")
	}
	// Put it in error with a failed download.
	tor.Status = domain.TorrentError
	tor.Error = "boom"
	p, _ := store.TorrentUpdateParams(tor)
	_ = f.store.UpdateTorrent(ctx, p)
	dl := domain.Download{ID: "d1", TorrentID: tor.ID, ProviderLink: "L", RelPath: "a", Filename: "a", State: domain.DownloadError, Error: "x", QueuedAt: tor.AddedAt, UpdatedAt: tor.AddedAt}
	_ = f.store.InsertDownload(ctx, store.DownloadInsertParams(dl))

	got, err := f.svc.RetryDownload(ctx, "d1")
	if err != nil || got.State != domain.DownloadPending || got.Error != "" {
		t.Fatalf("retry download: %v %+v", err, got)
	}
	tt, _ := f.svc.GetTorrent(ctx, tor.ID)
	if tt.Torrent.Status != domain.TorrentFinished {
		t.Fatalf("torrent should go back to finished, got %s", tt.Torrent.Status)
	}
	if _, err := f.svc.RetryDownload(ctx, "d1"); !errors.Is(err, ErrConflict) {
		t.Fatal("retrying a pending download should conflict")
	}

	tor.Status = domain.TorrentError
	p, _ = store.TorrentUpdateParams(tor)
	_ = f.store.UpdateTorrent(ctx, p)
	r, err := f.svc.RetryTorrent(ctx, tor.ID)
	if err != nil || r.Torrent.Status != domain.TorrentQueued || r.Torrent.RetryCount != 1 || r.Torrent.Error != "" {
		t.Fatalf("retry torrent: %v %+v", err, r.Torrent)
	}
	if dls, _ := f.store.ListDownloadsForTorrent(ctx, tor.ID); len(dls) != 0 {
		t.Fatal("downloads should be cleared on torrent retry")
	}
}

func TestUpdateTorrentAndSelectFiles(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.addAccount(t, "main")
	d, _ := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA})
	tor := d.Torrent
	tor.Files = []domain.File{{ID: "1", Path: "a.mkv", Size: 10}, {ID: "2", Path: "b.mkv", Size: 10}}
	tor.Status = domain.TorrentFinished
	p, _ := store.TorrentUpdateParams(tor)
	_ = f.store.UpdateTorrent(ctx, p)
	for _, id := range []string{"1", "2"} {
		dl := domain.Download{ID: "d" + id, TorrentID: tor.ID, FileID: id, ProviderLink: "L" + id, RelPath: id, Filename: id, State: domain.DownloadPending, QueuedAt: tor.AddedAt, UpdatedAt: tor.AddedAt}
		_ = f.store.InsertDownload(ctx, store.DownloadInsertParams(dl))
	}

	u, err := f.svc.UpdateTorrent(ctx, tor.ID, UpdateTorrentInput{Category: ptr("movies"), Settings: &domain.TorrentSettings{MinFileSize: 5}})
	if err != nil || u.Torrent.Category != "movies" || u.Torrent.Settings.MinFileSize != 5 {
		t.Fatalf("update: %v %+v", err, u.Torrent)
	}
	if _, err := f.svc.SelectFiles(ctx, tor.ID, []string{"9"}); !errors.Is(err, ErrValidation) {
		t.Fatal("unknown file id")
	}
	sel, err := f.svc.SelectFiles(ctx, tor.ID, []string{"2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Downloads) != 1 || sel.Downloads[0].FileID != "2" || sel.Torrent.Settings.ManualFiles[0] != "2" || sel.Torrent.FilesSelectedAt != nil {
		t.Fatalf("select: %+v", sel)
	}
	// Once a download has started, the category is frozen.
	dl := sel.Downloads[0]
	dl.State = domain.DownloadDownloading
	_ = f.store.UpdateDownload(ctx, store.DownloadUpdateParams(dl))
	if _, err := f.svc.UpdateTorrent(ctx, tor.ID, UpdateTorrentInput{Category: ptr("other")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("category change after start should conflict: %v", err)
	}
}

func TestSettingsAndStatus(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	st, err := f.svc.GetSettings(ctx)
	if err != nil || st.UnpackMaxDepth != 1 || st.TorrentDefaults.DownloadRetries != 3 {
		t.Fatalf("defaults: %v %+v", err, st)
	}
	st.Categories = []string{"tv", " movies ", "tv", ""}
	st.TorrentDefaults.MinFileSize = 1024
	got, err := f.svc.UpdateSettings(ctx, st)
	if err != nil || len(got.Categories) != 2 || got.Categories[0] != "movies" {
		t.Fatalf("update: %v %+v", err, got)
	}
	again, _ := f.svc.GetSettings(ctx)
	if again.TorrentDefaults.MinFileSize != 1024 {
		t.Fatal("settings not persisted")
	}
	st.Categories = []string{"a/b"}
	if _, err := f.svc.UpdateSettings(ctx, st); !errors.Is(err, ErrValidation) {
		t.Fatal("bad category")
	}
	st.Categories = nil
	st.TorrentDefaults.ExcludeRegex = "["
	if _, err := f.svc.UpdateSettings(ctx, st); !errors.Is(err, ErrValidation) {
		t.Fatal("bad regex")
	}
	if err := f.svc.SetRaw(ctx, "api_key", "abc"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := f.svc.GetRaw(ctx, "api_key"); !ok || v != "abc" {
		t.Fatal("raw setting")
	}
	if _, ok, _ := f.svc.GetRaw(ctx, "nope"); ok {
		t.Fatal("missing raw")
	}

	f.addAccount(t, "main")
	_, _ = f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA})
	_ = os.MkdirAll(f.svc.cfg.DownloadDir, 0o755)
	sys, err := f.svc.Status(ctx)
	if err != nil || sys.Accounts != 1 || sys.Torrents[domain.TorrentQueued] != 1 || sys.DiskTotal == 0 {
		t.Fatalf("status: %v %+v", err, sys)
	}
}

func TestPathsAndProgress(t *testing.T) {
	tor := domain.Torrent{Name: `Show: S01/E01 "x"?`, Category: "tv", Hash: "h"}
	if got := TorrentDir("/dl", tor); got != filepath.Join("/dl", "tv", `Show_ S01_E01 _x__`) {
		t.Fatalf("dir %q", got)
	}
	if got := TorrentDir("/dl", domain.Torrent{Hash: "abc"}); got != filepath.Join("/dl", "abc") {
		t.Fatalf("empty name falls back to hash: %q", got)
	}
	d := TorrentDetail{Downloads: []domain.Download{{Size: 100, BytesDone: 50}, {Size: 100, State: domain.DownloadDone}}}
	if p := d.LocalProgress(); p != 0.75 {
		t.Fatalf("progress %v", p)
	}
	if (TorrentDetail{Torrent: domain.Torrent{Status: domain.TorrentCompleted}}).LocalProgress() != 1 {
		t.Fatal("completed w/o downloads = 1")
	}
}

func ptr[T any](v T) *T { return &v }
