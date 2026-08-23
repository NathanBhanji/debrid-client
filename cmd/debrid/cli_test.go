package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/api"
	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/providertest"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

// run executes the CLI against srv and returns stdout.
func run(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"--server", srv.URL, "--api-key", "k"}, args...))
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
	if err := r.Execute(); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("bad key: %v", err)
	}
}
