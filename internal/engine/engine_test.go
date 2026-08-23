package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/fetch"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/providertest"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

const magnet = "magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb&dn=Show.S01"

// fakeFetch writes content keyed by URL; URLs not in the map yield an error.
type fakeFetch struct {
	mu      sync.Mutex
	content map[string][]byte
	fails   map[string]error // URL → error returned once (then removed)
	calls   int
	delay   time.Duration
}

func (f *fakeFetch) fn(ctx context.Context, url, dest string, o fetch.Options) (fetch.Result, error) {
	f.mu.Lock()
	f.calls++
	if err, ok := f.fails[url]; ok {
		delete(f.fails, url)
		f.mu.Unlock()
		return fetch.Result{}, err
	}
	b, ok := f.content[url]
	delay := f.delay
	f.mu.Unlock()
	if !ok {
		return fetch.Result{}, &fetch.HTTPError{StatusCode: 404, URL: url}
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			return fetch.Result{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	if o.ExpectedSize > 0 && int64(len(b)) != o.ExpectedSize {
		return fetch.Result{}, fetch.ErrSizeMismatch
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fetch.Result{}, err
	}
	if o.Progress != nil {
		o.Progress(int64(len(b))/2, int64(len(b)))
	}
	return fetch.Result{Size: int64(len(b))}, os.WriteFile(dest, b, 0o644)
}

type harness struct {
	t      *testing.T
	svc    *service.Service
	eng    *Engine
	fake   *providertest.Fake
	fetch  *fakeFetch
	dir    string
	cancel context.CancelFunc
	done   chan struct{}
}

func newHarness(t *testing.T, mut func(*Config)) *harness {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := providertest.New(domain.ProviderTorBox)
	factory := func(domain.ProviderKind, domain.Credentials, provider.Options) (provider.Provider, error) {
		return fake, nil
	}
	bus := events.New()
	svc := service.New(st, service.NewProviders(st, factory, provider.Options{}), nil, bus, service.Config{DownloadDir: filepath.Join(dir, "dl")}, nil)
	cfg := Config{
		DownloadDir: filepath.Join(dir, "dl"), DownloadLimit: 2, UnpackLimit: 1,
		PollInterval: 20 * time.Millisecond, IdlePollInterval: 40 * time.Millisecond, TickInterval: 10 * time.Millisecond,
		ConnectionsPerDownload: 2, AddTimeout: 500 * time.Millisecond, LinksTimeout: 300 * time.Millisecond,
	}
	if mut != nil {
		mut(&cfg)
	}
	eng := New(cfg, st, svc, bus, nil)
	eng.backoff = func(int) time.Duration { return 0 }
	ff := &fakeFetch{content: map[string][]byte{}, fails: map[string]error{}}
	eng.SetFetcher(ff.fn)
	svc.SetEngine(eng)
	h := &harness{t: t, svc: svc, eng: eng, fake: fake, fetch: ff, dir: dir, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { defer close(h.done); _ = eng.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-h.done })
	if _, err := svc.AddAccount(context.Background(), service.AddAccountInput{Kind: domain.ProviderTorBox, Name: "acc", Credentials: domain.Credentials{APIKey: "k"}, SkipVerify: true}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) add(settings *domain.TorrentSettings) domain.Torrent {
	h.t.Helper()
	d, err := h.svc.AddTorrent(context.Background(), service.AddTorrentInput{Magnet: magnet, Settings: settings})
	if err != nil {
		h.t.Fatal(err)
	}
	return d.Torrent
}

func (h *harness) waitStatus(id string, want domain.TorrentStatus) domain.Torrent {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, err := h.svc.GetTorrent(context.Background(), id)
		if err == nil && d.Torrent.Status == want {
			return d.Torrent
		}
		time.Sleep(10 * time.Millisecond)
	}
	d, _ := h.svc.GetTorrent(context.Background(), id)
	h.t.Fatalf("torrent %s never reached %s (now %s: %s / %s)", id, want, d.Torrent.Status, d.Torrent.StatusReason, d.Torrent.Error)
	return domain.Torrent{}
}

func (h *harness) waitProviderID(id string) string {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, _ := h.svc.GetTorrent(context.Background(), id)
		if d.Torrent.ProviderID != "" {
			return d.Torrent.ProviderID
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatal("torrent never got a provider id")
	return ""
}

func (h *harness) finishAtProvider(pid string, files map[string][]byte) {
	var fs []domain.File
	var links []provider.Link
	i := 0
	for p, b := range files {
		i++
		id := string(rune('0' + i))
		url := "http://cdn/" + p
		fs = append(fs, domain.File{ID: id, Path: "Show.S01/" + p, Size: int64(len(b))})
		links = append(links, provider.Link{FileID: id, Path: "Show.S01/" + p, Size: int64(len(b)), URL: url})
		h.fetch.mu.Lock()
		h.fetch.content[url] = b
		h.fetch.mu.Unlock()
	}
	h.fake.SetFiles(pid, fs)
	h.fake.Finish(pid, links)
}

func TestHappyPath(t *testing.T) {
	h := newHarness(t, nil)
	tor := h.add(nil)
	pid := h.waitProviderID(tor.ID)
	h.waitStatus(tor.ID, domain.TorrentProcessing)

	h.fake.SetStatus(pid, domain.TorrentDownloading, 0.5)
	got := h.waitStatus(tor.ID, domain.TorrentDownloading)
	if got.Progress != 0.5 {
		t.Fatalf("progress not synced: %+v", got)
	}

	h.finishAtProvider(pid, map[string][]byte{"e01.mkv": []byte("episode one"), "Subs/e01.srt": []byte("sub")})
	got = h.waitStatus(tor.ID, domain.TorrentCompleted)
	d, _ := h.svc.GetTorrent(context.Background(), tor.ID)
	if len(d.Downloads) != 2 {
		t.Fatalf("downloads %+v", d.Downloads)
	}
	for _, dl := range d.Downloads {
		if dl.State != domain.DownloadDone || dl.BytesDone != dl.Size {
			t.Fatalf("download not done: %+v", dl)
		}
	}
	dir := service.TorrentDir(h.eng.cfg.DownloadDir, got)
	if b, err := os.ReadFile(filepath.Join(dir, "e01.mkv")); err != nil || string(b) != "episode one" {
		t.Fatalf("file content: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "Subs", "e01.srt")); err != nil {
		t.Fatal("nested path not written (torrent-name prefix should be stripped)")
	}
	if d.LocalProgress() != 1 {
		t.Fatalf("local progress %v", d.LocalProgress())
	}
	// Provider torrent kept (FinishedKeep default).
	if len(h.fake.IDs()) != 1 {
		t.Fatal("torrent should remain at provider")
	}
}

func TestFiltersApplyAndAllExcludedFails(t *testing.T) {
	h := newHarness(t, nil)
	tor := h.add(&domain.TorrentSettings{ExcludeRegex: `\.srt$`, DownloadRetries: 1, FinishedAction: domain.FinishedKeep})
	pid := h.waitProviderID(tor.ID)
	h.finishAtProvider(pid, map[string][]byte{"e01.mkv": []byte("v"), "e01.srt": []byte("s")})
	h.waitStatus(tor.ID, domain.TorrentCompleted)
	d, _ := h.svc.GetTorrent(context.Background(), tor.ID)
	if len(d.Downloads) != 1 || d.Downloads[0].Filename != "e01.mkv" {
		t.Fatalf("filter not applied: %+v", d.Downloads)
	}

	tor2, err := h.svc.AddTorrent(context.Background(), service.AddTorrentInput{
		Magnet: strings.Replace(magnet, "bbbb", "cccc", 1), Settings: &domain.TorrentSettings{MinFileSize: 1 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	pid2 := h.waitProviderID(tor2.Torrent.ID)
	h.finishAtProvider(pid2, map[string][]byte{"tiny.mkv": []byte("x")})
	got := h.waitStatus(tor2.Torrent.ID, domain.TorrentError)
	if !strings.Contains(got.Error, "no files") {
		t.Fatalf("error %q", got.Error)
	}
}

func TestAddTransientThenPermanentErrors(t *testing.T) {
	h := newHarness(t, nil)
	var n int
	var mu sync.Mutex
	h.fake.SetHooks(providertest.Hooks{OnAdd: func(string) error {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n == 1 {
			return provider.Errorf(provider.ErrTransient, "", "blip")
		}
		return nil
	}})
	tor := h.add(nil)
	h.waitProviderID(tor.ID)
	mu.Lock()
	calls := n
	mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected 2 add calls, got %d", calls)
	}

	h.fake.SetHooks(providertest.Hooks{OnAdd: func(string) error { return provider.Errorf(provider.ErrPermanent, "BOZO_TORRENT", "bad torrent") }})
	tor2, _ := h.svc.AddTorrent(context.Background(), service.AddTorrentInput{Magnet: strings.Replace(magnet, "bbbb", "dddd", 1)})
	got := h.waitStatus(tor2.Torrent.ID, domain.TorrentError)
	if !strings.Contains(got.Error, "rejected") {
		t.Fatalf("error %q", got.Error)
	}
}

func TestAddTimeoutIsReconciledByHash(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.AddTimeout = 50 * time.Millisecond })
	// The add "succeeds" server-side but the response never arrives in time.
	h.fake.SetHooks(providertest.Hooks{OnAdd: func(string) error {
		time.Sleep(120 * time.Millisecond)
		return nil
	}})
	tor := h.add(nil)
	// Eventually the poller lists the provider torrent and the queued torrent adopts it.
	pid := h.waitProviderID(tor.ID)
	if h.fake.Calls("Add") != 1 {
		t.Fatalf("add should not be retried after timeout (dedupe by hash), calls=%d", h.fake.Calls("Add"))
	}
	if len(h.fake.IDs()) != 1 || h.fake.IDs()[0] != pid {
		t.Fatalf("expected exactly one provider torrent, got %v", h.fake.IDs())
	}
}

func TestProviderErrorRetriesThenFails(t *testing.T) {
	h := newHarness(t, nil)
	tor := h.add(&domain.TorrentSettings{TorrentRetries: 1, DownloadRetries: 1, FinishedAction: domain.FinishedKeep})
	pid := h.waitProviderID(tor.ID)
	h.fake.Fail(pid, "dead torrent")
	// First failure → requeued and re-added with a fresh provider id.
	deadline := time.Now().Add(5 * time.Second)
	var pid2 string
	for time.Now().Before(deadline) {
		d, _ := h.svc.GetTorrent(context.Background(), tor.ID)
		if d.Torrent.ProviderID != "" && d.Torrent.ProviderID != pid && d.Torrent.RetryCount == 1 {
			pid2 = d.Torrent.ProviderID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid2 == "" {
		t.Fatal("torrent was not re-added after provider error")
	}
	h.fake.Fail(pid2, "dead again")
	got := h.waitStatus(tor.ID, domain.TorrentError)
	if !strings.Contains(got.Error, "provider error") {
		t.Fatalf("error %q", got.Error)
	}
}

func TestExpiredLinkIsReUnrestricted(t *testing.T) {
	h := newHarness(t, nil)
	h.fake.SetCaps(provider.Caps{DirectLinks: false, MaxConnections: 4})
	tor := h.add(nil)
	pid := h.waitProviderID(tor.ID)
	h.finishAtProvider(pid, map[string][]byte{"a.mkv": []byte("aaa")})
	h.fetch.mu.Lock()
	h.fetch.fails["http://cdn/a.mkv"] = &fetch.HTTPError{StatusCode: 403, URL: "http://cdn/a.mkv"}
	h.fetch.mu.Unlock()
	h.waitStatus(tor.ID, domain.TorrentCompleted)
	if h.fake.Calls("Unrestrict") < 2 {
		t.Fatalf("expected re-unrestrict after 403, calls=%d", h.fake.Calls("Unrestrict"))
	}
	d, _ := h.svc.GetTorrent(context.Background(), tor.ID)
	if d.Downloads[0].RetryCount != 1 {
		t.Fatalf("retry count %d", d.Downloads[0].RetryCount)
	}
}

func TestDownloadRetriesExhaustedThenManualRetry(t *testing.T) {
	h := newHarness(t, nil)
	tor := h.add(&domain.TorrentSettings{DownloadRetries: 1, TorrentRetries: 0, FinishedAction: domain.FinishedKeep})
	pid := h.waitProviderID(tor.ID)
	h.finishAtProvider(pid, map[string][]byte{"a.mkv": []byte("aaa")})
	// Remove content so fetch 404s (not an "expired" code? 404 is LinkExpired → retryable).
	// Use a non-retryable error instead: size mismatch on attempt 2.
	h.fetch.mu.Lock()
	h.fetch.fails["http://cdn/a.mkv"] = errors.New("disk full")
	h.fetch.mu.Unlock()
	// First attempt fails (retryable, generic) → retry → succeeds.
	h.waitStatus(tor.ID, domain.TorrentCompleted)

	// Now a torrent whose single download fails twice with DownloadRetries=1.
	tor2, _ := h.svc.AddTorrent(context.Background(), service.AddTorrentInput{Magnet: strings.Replace(magnet, "bbbb", "eeee", 1),
		Settings: &domain.TorrentSettings{DownloadRetries: 1, FinishedAction: domain.FinishedKeep}})
	pid2 := h.waitProviderID(tor2.Torrent.ID)
	h.fake.SetFiles(pid2, []domain.File{{ID: "1", Path: "b.mkv", Size: 3}})
	h.fake.Finish(pid2, []provider.Link{{FileID: "1", Path: "b.mkv", Size: 3, URL: "http://cdn/missing"}})
	got := h.waitStatus(tor2.Torrent.ID, domain.TorrentError)
	if !strings.Contains(got.Error, "downloads failed") {
		t.Fatalf("error %q", got.Error)
	}
	d, _ := h.svc.GetTorrent(context.Background(), tor2.Torrent.ID)
	if d.Downloads[0].State != domain.DownloadError || d.Downloads[0].RetryCount != 1 {
		t.Fatalf("download %+v", d.Downloads[0])
	}
	// Fix the content and retry the download manually.
	h.fetch.mu.Lock()
	h.fetch.content["http://cdn/missing"] = []byte("bbb")
	h.fetch.mu.Unlock()
	if _, err := h.svc.RetryDownload(context.Background(), d.Downloads[0].ID); err != nil {
		t.Fatal(err)
	}
	h.waitStatus(tor2.Torrent.ID, domain.TorrentCompleted)
}

func TestUnpackAfterDownload(t *testing.T) {
	h := newHarness(t, nil)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("inner/movie.mkv")
	_, _ = w.Write([]byte("movie bytes"))
	_ = zw.Close()
	tor := h.add(nil)
	pid := h.waitProviderID(tor.ID)
	h.finishAtProvider(pid, map[string][]byte{"pack.zip": buf.Bytes()})
	got := h.waitStatus(tor.ID, domain.TorrentCompleted)
	dir := service.TorrentDir(h.eng.cfg.DownloadDir, got)
	if b, err := os.ReadFile(filepath.Join(dir, "inner", "movie.mkv")); err != nil || string(b) != "movie bytes" {
		t.Fatalf("unpacked content: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pack.zip")); !os.IsNotExist(err) {
		t.Fatal("archive should be deleted after unpack")
	}
	d, _ := h.svc.GetTorrent(context.Background(), tor.ID)
	if d.Downloads[0].UnpackFinishedAt == nil {
		t.Fatal("unpack timestamps not set")
	}
}

func TestFinishedActionRemovesFromProvider(t *testing.T) {
	h := newHarness(t, nil)
	tor := h.add(&domain.TorrentSettings{FinishedAction: domain.FinishedRemoveFromProvider, FinishedDelay: 50 * time.Millisecond, DownloadRetries: 1})
	pid := h.waitProviderID(tor.ID)
	h.finishAtProvider(pid, map[string][]byte{"a.mkv": []byte("a")})
	h.waitStatus(tor.ID, domain.TorrentCompleted)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.fake.IDs()) == 0 {
			d, _ := h.svc.GetTorrent(context.Background(), tor.ID)
			if d.Torrent.ProviderID == "" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("torrent was not removed from provider after finished delay")
}

func TestRemovedAtProviderFails(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.PollInterval = 10 * time.Millisecond })
	tor := h.add(nil)
	pid := h.waitProviderID(tor.ID)
	h.waitStatus(tor.ID, domain.TorrentProcessing)
	// Skip the grace period by moving the clock.
	h.eng.setClock(func() time.Time { return time.Now().UTC().Add(time.Hour) })
	h.fake.Remove(pid)
	got := h.waitStatus(tor.ID, domain.TorrentError)
	if !strings.Contains(got.Error, "no longer exists") {
		t.Fatalf("error %q", got.Error)
	}
}

func TestCancelViaDeleteStopsDownload(t *testing.T) {
	h := newHarness(t, nil)
	h.fetch.delay = 2 * time.Second
	tor := h.add(nil)
	pid := h.waitProviderID(tor.ID)
	h.finishAtProvider(pid, map[string][]byte{"a.mkv": []byte("a")})
	// Wait for the download to be in flight.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d, _ := h.svc.GetTorrent(context.Background(), tor.ID)
		if len(d.Downloads) > 0 && d.Downloads[0].State == domain.DownloadDownloading {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	if err := h.svc.DeleteTorrent(context.Background(), tor.ID, service.DeleteOptions{DeleteFiles: true}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("delete should cancel the in-flight download promptly")
	}
	if h.eng.countJobs(jobDownload) != 0 {
		t.Fatal("job still registered")
	}
}

func TestRecoverResetsInFlightRows(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(context.Background(), filepath.Join(dir, "db"))
	defer func() { _ = st.Close() }()
	now := time.Now().UTC()
	_ = st.InsertProviderAccount(context.Background(), mustAcc(now))
	tor := domain.Torrent{ID: "t", AccountID: "a", Hash: "h", Status: domain.TorrentAdding, PayloadKind: domain.PayloadMagnet, Payload: []byte("m"), AddedAt: now, UpdatedAt: now}
	p, _ := store.TorrentInsertParams(tor)
	_ = st.InsertTorrent(context.Background(), p)
	for i, s := range []domain.DownloadState{domain.DownloadDownloading, domain.DownloadUnpacking, domain.DownloadDone} {
		d := domain.Download{ID: string(rune('a' + i)), TorrentID: "t", ProviderLink: string(rune('a' + i)), RelPath: "f", Filename: "f", State: s, QueuedAt: now, UpdatedAt: now}
		_, _ = st.InsertDownload(context.Background(), store.DownloadInsertParams(d))
	}
	svc := service.New(st, service.NewProviders(st, nil, provider.Options{}), nil, nil, service.Config{}, nil)
	e := New(Config{}, st, svc, events.New(), nil)
	if err := e.recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, _ := svc.GetTorrent(context.Background(), "t")
	if d.Torrent.Status != domain.TorrentQueued {
		t.Fatalf("adding should reset to queued, got %s", d.Torrent.Status)
	}
	states := map[string]domain.DownloadState{}
	for _, dl := range d.Downloads {
		states[dl.ID] = dl.State
	}
	if states["a"] != domain.DownloadPending || states["b"] != domain.DownloadDownloaded || states["c"] != domain.DownloadDone {
		t.Fatalf("states %v", states)
	}
}

func TestRelPath(t *testing.T) {
	cases := map[[2]string]string{
		{"Show", "Show/e01.mkv"}:           "e01.mkv",
		{"Show", "Show/Sub/e01.srt"}:       "Sub/e01.srt",
		{"Show", "Other/e01.mkv"}:          "Other/e01.mkv",
		{"Show", "/Show/../../etc/passwd"}: "etc/passwd",
		{"", "a\\b\\c.mkv"}:                "a/b/c.mkv",
		{"Show", "Show"}:                   "Show",
		{"", ""}:                           "file",
	}
	for in, want := range cases {
		if got := relPath(in[0], in[1]); got != want {
			t.Errorf("relPath(%q,%q)=%q want %q", in[0], in[1], got, want)
		}
	}
}

func mustAcc(now time.Time) (p sqlcgen.InsertProviderAccountParams) {
	acc := domain.ProviderAccount{ID: "a", Kind: domain.ProviderTorBox, Name: "a", Enabled: true, IsDefault: true, CreatedAt: now, UpdatedAt: now}
	p, _ = store.AccountInsertParams(acc)
	return p
}


func TestRelinkWhenProviderIDChanges(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.PollInterval = 10 * time.Millisecond })
	tor := h.add(nil)
	pid := h.waitProviderID(tor.ID)
	h.waitStatus(tor.ID, domain.TorrentProcessing)
	// Provider re-homes the torrent under a new id (e.g. TorBox queue → mylist).
	h.fake.Remove(pid)
	res, _ := h.fake.AddMagnet(context.Background(), magnet)
	if res.ID == pid {
		t.Skip("fake reused id")
	}
	h.eng.setClock(func() time.Time { return time.Now().UTC().Add(time.Hour) }) // past the grace period
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d, _ := h.svc.GetTorrent(context.Background(), tor.ID)
		if d.Torrent.ProviderID == res.ID && d.Torrent.Status != domain.TorrentError {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	d, _ := h.svc.GetTorrent(context.Background(), tor.ID)
	t.Fatalf("torrent should relink to the new provider id, got %+v", d.Torrent)
}

func TestDirNameFrozenAndServiceEditsSurvive(t *testing.T) {
	h := newHarness(t, nil)
	h.fetch.delay = 300 * time.Millisecond
	tor := h.add(nil)
	pid := h.waitProviderID(tor.ID)
	h.finishAtProvider(pid, map[string][]byte{"a.mkv": []byte("a")})
	// Wait until the download is running, then change settings via the service.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d, _ := h.svc.GetTorrent(context.Background(), tor.ID)
		if len(d.Downloads) > 0 && d.Downloads[0].State == domain.DownloadDownloading {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := h.svc.UpdateTorrent(context.Background(), tor.ID, service.UpdateTorrentInput{Settings: &domain.TorrentSettings{MinFileSize: 42, DownloadRetries: 1, FinishedAction: domain.FinishedKeep, Unpack: true}}); err != nil {
		t.Fatal(err)
	}
	got := h.waitStatus(tor.ID, domain.TorrentCompleted)
	if got.Settings.MinFileSize != 42 {
		t.Fatalf("engine write clobbered a concurrent service edit: %+v", got.Settings)
	}
	if got.DirName == "" {
		t.Fatal("dir name should be frozen once downloads start")
	}
	// Category can no longer change.
	if _, err := h.svc.UpdateTorrent(context.Background(), tor.ID, service.UpdateTorrentInput{Category: ptr("x")}); err == nil {
		t.Fatal("category should be frozen")
	}
}

func ptr[T any](v T) *T { return &v }
