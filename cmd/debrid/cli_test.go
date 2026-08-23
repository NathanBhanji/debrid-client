package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/NathanBhanji/debrid-client/internal/api"
	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/providertest"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

// run executes the CLI against srv (API key "k") and returns stdout.
func run(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	return runWith(t, srv, "k", args...)
}

func runWith(t *testing.T, srv *httptest.Server, key string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append(args, "--server", srv.URL, "--api-key", key))
	err := root.Execute()
	return out.String(), err
}

func TestCLIEndToEnd(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	fake := providertest.New(domain.ProviderTorBox)
	factory := func(domain.ProviderKind, domain.Credentials, provider.Options) (provider.Provider, error) {
		return fake, nil
	}
	svc := service.New(st, service.NewProviders(st, factory, provider.Options{}), nil, events.New(), service.Config{DownloadDir: filepath.Join(dir, "dl")}, nil)
	srv := httptest.NewServer(api.New(svc, api.Options{APIKey: "k"}))
	defer srv.Close()

	out, err := run(t, srv, "status")
	if err != nil || !strings.Contains(out, "accounts:     0") {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if _, err := run(t, srv, "accounts", "add", "--kind", "torbox"); err == nil || !strings.Contains(err.Error(), "--key") {
		t.Fatalf("missing api key should error: %v", err)
	}
	out, err = run(t, srv, "accounts", "add", "--kind", "torbox", "--name", "main", "--key", "x")
	if err != nil || !strings.Contains(out, "main") || !strings.Contains(out, "fake (premium)") {
		t.Fatalf("accounts add: %v\n%s", err, out)
	}
	out, err = run(t, srv, "accounts", "ls", "--json")
	if err != nil || !strings.Contains(out, `"name": "main"`) {
		t.Fatalf("accounts ls --json: %v\n%s", err, out)
	}
	out, err = run(t, srv, "accounts", "test", "main")
	if err != nil || !strings.HasPrefix(out, "ok: fake") {
		t.Fatalf("accounts test: %v\n%s", err, out)
	}

	out, err = run(t, srv, "torrents", "add", "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=Alpha", "-c", "tv")
	if err != nil || !strings.Contains(out, "added") || !strings.Contains(out, "Alpha") {
		t.Fatalf("torrents add: %v\n%s", err, out)
	}
	// Duplicate → API 409 rendered as a readable error.
	if _, err := run(t, srv, "torrents", "add", "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate should surface 409 detail: %v", err)
	}
	out, err = run(t, srv, "torrents", "ls")
	if err != nil || !strings.Contains(out, "queued") || !strings.Contains(out, "Alpha") || !strings.Contains(out, "tv") {
		t.Fatalf("torrents ls: %v\n%s", err, out)
	}
	out, err = run(t, srv, "torrents", "get", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil || !strings.Contains(out, "hash:      aaaaaaaa") {
		t.Fatalf("torrents get: %v\n%s", err, out)
	}
	out, err = run(t, srv, "torrents", "set", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "-c", "movies")
	if err != nil || !strings.Contains(out, "category:  movies") {
		t.Fatalf("torrents set: %v\n%s", err, out)
	}
	if _, err := run(t, srv, "torrents", "retry", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("retry of queued torrent should fail (409)")
	}
	out, err = run(t, srv, "settings", "get")
	if err != nil || !strings.Contains(out, `"torrent_defaults"`) {
		t.Fatalf("settings get: %v\n%s", err, out)
	}
	root := newRootCmd()
	var sout bytes.Buffer
	root.SetOut(&sout)
	root.SetIn(strings.NewReader(`{"torrent_defaults":{"download_retries":5,"torrent_retries":1,"unpack":true},"categories":["tv"],"unpack_max_depth":2}`))
	root.SetArgs([]string{"--server", srv.URL, "--api-key", "k", "settings", "set", "-"})
	if err := root.Execute(); err != nil || !strings.Contains(sout.String(), `"download_retries": 5`) {
		t.Fatalf("settings set: %v\n%s", err, sout.String())
	}
	out, err = run(t, srv, "torrents", "rm", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--files")
	if err != nil || !strings.Contains(out, "deleted") {
		t.Fatalf("torrents rm: %v\n%s", err, out)
	}
	out, err = run(t, srv, "accounts", "rm", "main")
	if err != nil || !strings.Contains(out, "deleted") {
		t.Fatalf("accounts rm: %v\n%s", err, out)
	}
	// Wrong key → 401 surfaced.
	var bad bytes.Buffer
	r := newRootCmd()
	r.SetOut(&bad)
	r.SetArgs([]string{"--server", srv.URL, "--api-key", "nope", "status"})
	if err := r.Execute(); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("bad key: %v", err)
	}
}

func TestCLIDiscoveryAndRobustness(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "debrid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	fake := providertest.New(domain.ProviderTorBox)
	factory := func(domain.ProviderKind, domain.Credentials, provider.Options) (provider.Provider, error) {
		return fake, nil
	}
	svc := service.New(st, service.NewProviders(st, factory, provider.Options{}), nil, events.New(), service.Config{DownloadDir: filepath.Join(dir, "dl")}, nil)
	_ = svc.SetRaw(context.Background(), "server.api_key", "dbkey")
	srv := httptest.NewServer(api.New(svc, api.Options{APIKey: "dbkey"}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	_ = host

	// Config file points at the server (as 0.0.0.0 → rewritten to loopback) and the data dir whose DB holds the key.
	cfgPath := filepath.Join(dir, "config.yaml")
	_ = os.WriteFile(cfgPath, []byte("data_dir: "+dir+"\nserver:\n  listen: 0.0.0.0:"+port+"\n"), 0o600)
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"--config", cfgPath, "status"})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "version:") {
		t.Fatalf("discovery via config + db key: %v\n%s", err, out.String())
	}

	// Env overrides.
	t.Setenv("DEBRID_SERVER", srv.URL)
	t.Setenv("DEBRID_API_KEY", "dbkey")
	out.Reset()
	root = newRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"--config", filepath.Join(dir, "none.yaml"), "status"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "config file") {
		t.Fatalf("explicit missing config must error, got %v", err)
	}
	t.Setenv("DEBRID_CONFIG", "")
	out.Reset()
	root = newRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"status"})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "version:") {
		t.Fatalf("discovery via env: %v\n%s", err, out.String())
	}

	// 2xx non-JSON from a wrong server must not panic.
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>captive portal</html>"))
	}))
	defer html.Close()
	_, err = run(t, html, "status")
	if err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Fatalf("expected a clean error for non-JSON 200, got %v", err)
	}
	// Non-JSON error body.
	_, err = run(t, html, "torrents", "rm", "x")
	if err == nil {
		t.Fatal("expected error")
	}

	// Multipart add + select + downloads retry + accounts set via the real API.
	_, _ = runWith(t, srv, "dbkey", "accounts", "add", "--kind", "torbox", "--key", "x")
	info := metainfo.Info{Name: "movie.mkv", Length: 10, PieceLength: 16384, Pieces: make([]byte, 20)}
	ib, _ := bencode.Marshal(info)
	var tb bytes.Buffer
	_ = (&metainfo.MetaInfo{InfoBytes: ib}).Write(&tb)
	tf := filepath.Join(dir, "movie.torrent")
	_ = os.WriteFile(tf, tb.Bytes(), 0o644)
	o, err := runWith(t, srv, "dbkey", "torrents", "add", tf, "-c", "movies")
	if err != nil || !strings.Contains(o, "movie.mkv") {
		t.Fatalf("add file: %v\n%s", err, o)
	}
	o, err = runWith(t, srv, "dbkey", "torrents", "ls")
	if err != nil || !strings.Contains(o, "HASH") {
		t.Fatalf("ls: %v\n%s", err, o)
	}
	// Short hash from ls resolves.
	hash := strings.Fields(strings.Split(o, "\n")[1])[0]
	if o, err = runWith(t, srv, "dbkey", "torrents", "get", hash); err != nil || !strings.Contains(o, "movie.mkv") {
		t.Fatalf("get by short hash %q: %v\n%s", hash, err, o)
	}
	if _, err = runWith(t, srv, "dbkey", "torrents", "select", hash, "1"); err == nil {
		t.Fatal("select before provider files should fail with a readable error")
	}
	if o, err = runWith(t, srv, "dbkey", "accounts", "set", "torbox", "--name", "renamed", "--disable"); err != nil || !strings.Contains(o, "renamed") || !strings.Contains(o, "no") {
		t.Fatalf("accounts set: %v\n%s", err, o)
	}
	if _, err = runWith(t, srv, "dbkey", "accounts", "set", "renamed", "--enable", "--disable"); err == nil {
		t.Fatal("enable+disable should be rejected")
	}
	// settings set merges by default.
	root = newRootCmd()
	out.Reset()
	root.SetOut(&out)
	root.SetIn(strings.NewReader(`{"categories":["tv"]}`))
	root.SetArgs([]string{"--server", srv.URL, "--api-key", "dbkey", "settings", "set", "-"})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), `"download_retries": 3`) || !strings.Contains(out.String(), `"tv"`) {
		t.Fatalf("settings set should merge over current settings: %v\n%s", err, out.String())
	}
}
