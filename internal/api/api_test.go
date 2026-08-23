package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/providertest"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

const key = "secret"

type tc struct {
	t   *testing.T
	srv *httptest.Server
	svc *service.Service
}

func setup(t *testing.T) *tc {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fake := providertest.New(domain.ProviderTorBox)
	factory := func(_ domain.ProviderKind, c domain.Credentials, _ provider.Options) (provider.Provider, error) {
		if c.APIKey == "" {
			return nil, provider.Errorf(provider.ErrAuth, "", "missing key")
		}
		return fake, nil
	}
	svc := service.New(st, service.NewProviders(st, factory, provider.Options{}), nil, events.New(), service.Config{DownloadDir: filepath.Join(dir, "dl")}, nil)
	h := New(svc, Options{APIKey: key})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &tc{t: t, srv: srv, svc: svc}
}

func (c *tc) do(method, path string, body any, auth bool) (*http.Response, []byte) {
	c.t.Helper()
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
		ct = "application/json"
	case *bytes.Buffer:
		rd = b
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
		ct = "application/json"
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, b
}

func (c *tc) mustJSON(method, path string, body any, want int, out any) {
	c.t.Helper()
	resp, b := c.do(method, path, body, true)
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: status %d want %d: %s", method, path, resp.StatusCode, want, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			c.t.Fatalf("decode %s: %v (%s)", path, err, b)
		}
	}
}

func TestAuth(t *testing.T) {
	c := setup(t)
	if resp, _ := c.do("GET", "/api/v1/health", nil, false); resp.StatusCode != 200 {
		t.Fatalf("health should be public: %d", resp.StatusCode)
	}
	if resp, _ := c.do("GET", "/openapi.json", nil, false); resp.StatusCode != 200 {
		t.Fatalf("openapi should be public: %d", resp.StatusCode)
	}
	resp, _ := c.do("GET", "/api/v1/torrents", nil, false)
	if resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("expected 401 with challenge, got %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", c.srv.URL+"/api/v1/torrents", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 401 {
		t.Fatal("wrong key should be 401")
	}
	if resp, _ := c.do("GET", "/api/v1/torrents?api_key="+key, nil, false); resp.StatusCode != 401 {
		t.Fatalf("query api_key must only work for the events stream: %d", resp.StatusCode)
	}
	if resp, _ := c.do("GET", "/api/v1/torrents", nil, true); resp.StatusCode != 200 {
		t.Fatalf("bearer should work: %d", resp.StatusCode)
	}
	// Path-shape exemptions must not exist: ids/names that look like public
	// routes are still protected.
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "name": "docs", "credentials": map[string]string{"api_key": "k"}}, 201, nil)
	for _, p := range []string{"/api/v1/accounts/docs", "/api/v1/torrents/health", "/api/v1/accounts/health", "/api/v1/torrents/foo%2Fhealth", "/api/v1/accounts/openapi"} {
		if resp, _ := c.do("GET", p, nil, false); resp.StatusCode != 401 {
			t.Fatalf("%s should be 401 without key, got %d", p, resp.StatusCode)
		}
		if resp, _ := c.do("DELETE", p, nil, false); resp.StatusCode != 401 {
			t.Fatalf("DELETE %s should be 401 without key, got %d", p, resp.StatusCode)
		}
	}
	// Query key works for SSE.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ereq, _ := http.NewRequestWithContext(ctx, "GET", c.srv.URL+"/api/v1/events?api_key="+key, nil)
	eresp, err := http.DefaultClient.Do(ereq)
	if err != nil || eresp.StatusCode != 200 {
		t.Fatalf("events with query key: %v %v", err, eresp)
	}
	_ = eresp.Body.Close()
}

func TestAccountsAndTorrentsFlow(t *testing.T) {
	c := setup(t)

	// Validation errors map to 422; provider auth failure too.
	resp, b := c.do("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{}}, true)
	if resp.StatusCode != 422 {
		t.Fatalf("missing key: %d %s", resp.StatusCode, b)
	}
	var acc apimodel.Account
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "name": "main", "credentials": map[string]string{"api_key": "k"}}, 201, &acc)
	if !acc.IsDefault || acc.User == nil || acc.Kind != "torbox" {
		t.Fatalf("account %+v", acc)
	}
	resp, _ = c.do("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "name": "main", "credentials": map[string]string{"api_key": "k"}}, true)
	if resp.StatusCode != 409 {
		t.Fatalf("duplicate name should be 409, got %d", resp.StatusCode)
	}
	var accs []apimodel.Account
	c.mustJSON("GET", "/api/v1/accounts", nil, 200, &accs)
	if len(accs) != 1 {
		t.Fatal("list accounts")
	}
	var user apimodel.User
	c.mustJSON("POST", "/api/v1/accounts/main/test", nil, 200, &user)
	if user.Username != "fake" {
		t.Fatalf("test account %+v", user)
	}
	c.mustJSON("PATCH", "/api/v1/accounts/"+acc.ID, map[string]any{"name": "primary"}, 200, &acc)
	if acc.Name != "primary" {
		t.Fatal("rename")
	}

	// Torrents
	resp, b = c.do("POST", "/api/v1/torrents", map[string]any{"magnet": "nope"}, true)
	if resp.StatusCode != 422 {
		t.Fatalf("bad magnet: %d %s", resp.StatusCode, b)
	}
	var tor apimodel.Torrent
	c.mustJSON("POST", "/api/v1/torrents", map[string]any{
		"magnet": "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=Alpha", "category": "tv",
		"settings": map[string]any{"min_file_size": 100, "finished_action": "remove_from_provider", "finished_delay": "5m", "download_retries": 2, "unpack": true},
	}, 201, &tor)
	if tor.Status != "queued" || tor.Category != "tv" || tor.Settings.FinishedDelay != "5m0s" || tor.Settings.MinFileSize != 100 || tor.Files == nil || tor.Downloads == nil {
		t.Fatalf("torrent %+v", tor)
	}
	resp, _ = c.do("POST", "/api/v1/torrents", map[string]any{"magnet": "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, true)
	if resp.StatusCode != 409 {
		t.Fatalf("duplicate torrent should be 409, got %d", resp.StatusCode)
	}
	resp, b = c.do("POST", "/api/v1/torrents", map[string]any{"magnet": "magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "settings": map[string]any{"finished_delay": "soon"}}, true)
	if resp.StatusCode != 422 {
		t.Fatalf("bad duration should be 422: %d %s", resp.StatusCode, b)
	}

	var list []apimodel.Torrent
	c.mustJSON("GET", "/api/v1/torrents?category=tv", nil, 200, &list)
	if len(list) != 1 || list[0].ID != tor.ID {
		t.Fatalf("list %+v", list)
	}
	c.mustJSON("GET", "/api/v1/torrents?status=completed", nil, 200, &list)
	if len(list) != 0 {
		t.Fatal("status filter")
	}
	resp, b = c.do("GET", "/api/v1/torrents?status=bogus", nil, true)
	if resp.StatusCode != 422 {
		t.Fatalf("enum validation: %d %s", resp.StatusCode, b)
	}
	var got apimodel.Torrent
	c.mustJSON("GET", "/api/v1/torrents/"+tor.Hash, nil, 200, &got)
	if got.ID != tor.ID {
		t.Fatal("get by hash")
	}
	c.mustJSON("PATCH", "/api/v1/torrents/"+tor.ID, map[string]any{"category": "movies"}, 200, &got)
	if got.Category != "movies" {
		t.Fatal("patch category")
	}
	resp, _ = c.do("POST", "/api/v1/torrents/"+tor.ID+"/retry", nil, true)
	if resp.StatusCode != 409 {
		t.Fatalf("retry queued should be 409, got %d", resp.StatusCode)
	}
	resp, _ = c.do("PUT", "/api/v1/torrents/"+tor.ID+"/files", map[string]any{"file_ids": []string{}}, true)
	if resp.StatusCode != 422 {
		t.Fatalf("empty file ids should be 422 (minItems), got %d", resp.StatusCode)
	}
	resp, _ = c.do("GET", "/api/v1/torrents/missing", nil, true)
	if resp.StatusCode != 404 {
		t.Fatal("missing torrent 404")
	}
	resp, _ = c.do("DELETE", "/api/v1/torrents/"+tor.ID+"?files=true", nil, true)
	if resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	resp, _ = c.do("DELETE", "/api/v1/accounts/"+acc.ID, nil, true)
	if resp.StatusCode != 204 {
		t.Fatalf("delete account: %d", resp.StatusCode)
	}
}

func TestAddTorrentFileMultipart(t *testing.T) {
	c := setup(t)
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{"api_key": "k"}}, 201, nil)
	info := metainfo.Info{Name: "movie.mkv", Length: 10, PieceLength: 16384, Pieces: make([]byte, 20)}
	ib, _ := bencode.Marshal(info)
	var tb bytes.Buffer
	_ = (&metainfo.MetaInfo{InfoBytes: ib}).Write(&tb)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "movie.torrent")
	_, _ = fw.Write(tb.Bytes())
	_ = mw.WriteField("category", "movies")
	_ = mw.Close()
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/v1/torrents/file", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var tor apimodel.Torrent
	_ = json.Unmarshal(b, &tor)
	if tor.Name != "movie.mkv" || tor.Category != "movies" || len(tor.Files) != 1 {
		t.Fatalf("torrent %+v", tor)
	}
}

func TestSettingsAndStatus(t *testing.T) {
	c := setup(t)
	var s apimodel.Settings
	c.mustJSON("GET", "/api/v1/settings", nil, 200, &s)
	s.Categories = []string{"tv"}
	s.TorrentDefaults.Lifetime = "72h"
	c.mustJSON("PUT", "/api/v1/settings", s, 200, &s)
	if len(s.Categories) != 1 || s.TorrentDefaults.Lifetime != "72h0m0s" {
		t.Fatalf("settings %+v", s)
	}
	s.UnpackMaxDepth = 9
	resp, _ := c.do("PUT", "/api/v1/settings", s, true)
	if resp.StatusCode != 422 {
		t.Fatalf("schema validation (maximum): %d", resp.StatusCode)
	}
	var st apimodel.Status
	c.mustJSON("GET", "/api/v1/system/status", nil, 200, &st)
	if st.Version == "" || st.Torrents == nil {
		t.Fatalf("status %+v", st)
	}
}

func TestEventsSSE(t *testing.T) {
	c := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", c.srv.URL+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	// Trigger an event after the subscription is established.
	go func() {
		time.Sleep(100 * time.Millisecond)
		c.svc.Events().Publish(events.Event{Type: events.TorrentAdded, TorrentID: "t1"})
	}()
	buf := make([]byte, 4096)
	var got strings.Builder
	for {
		n, err := resp.Body.Read(buf)
		got.Write(buf[:n])
		if strings.Contains(got.String(), "event: torrent.added\n") && strings.Contains(got.String(), `"t1"`) {
			break
		}
		if err != nil {
			t.Fatalf("stream ended without event: %v\n%s", err, got.String())
		}
	}
	// A second, differently-typed event must carry its own name (huma picks
	// the SSE event name by Go type).
	c.svc.Events().Publish(events.Event{Type: events.AccountChanged, AccountID: "a1"})
	for !strings.Contains(got.String(), "event: account.changed\n") {
		n, err := resp.Body.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			t.Fatalf("no account.changed event: %v\n%s", err, got.String())
		}
	}
}

func TestOpenAPIDocument(t *testing.T) {
	c := setup(t)
	_, b := c.do("GET", "/openapi.json", nil, false)
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	for _, p := range []string{"/api/v1/torrents", "/api/v1/torrents/{id}", "/api/v1/accounts", "/api/v1/settings", "/api/v1/events"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("missing path %s", p)
		}
	}
}

func TestBasePath(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(context.Background(), filepath.Join(dir, "db"))
	defer func() { _ = st.Close() }()
	svc := service.New(st, service.NewProviders(st, nil, provider.Options{}), nil, nil, service.Config{}, nil)
	h := New(svc, Options{BasePath: "/debrid"})
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/debrid/api/v1/health")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("base path health: %v %v", err, resp)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), srv.URL+"/debrid/schemas/") || strings.Contains(string(b), "/debrid/debrid/") {
		t.Fatalf("$schema must carry the prefix exactly once: %s", b)
	}
	if resp, _ := http.Get(srv.URL + "/api/v1/health"); resp.StatusCode == 200 {
		t.Fatal("unprefixed path should not be served")
	}
	specResp, _ := http.Get(srv.URL + "/debrid/openapi.json")
	spec, _ := io.ReadAll(specResp.Body)
	var doc map[string]any
	_ = json.Unmarshal(spec, &doc)
	if _, hasServers := doc["servers"]; hasServers {
		t.Fatal("spec must not declare servers (paths already include the prefix)")
	}
	if _, ok := doc["paths"].(map[string]any)["/debrid/api/v1/health"]; !ok {
		t.Fatal("spec paths should be prefixed")
	}
}

func TestUploadTooLargeIs413(t *testing.T) {
	c := setup(t)
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{"api_key": "k"}}, 201, nil)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "big.torrent")
	_, _ = fw.Write(bytes.Repeat([]byte("x"), 17<<20))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/v1/torrents/file", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err == nil && resp.StatusCode != 413 {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
	// err != nil is acceptable: the server may close the connection once the cap is hit.
}

func TestInternalErrorsAreOpaque(t *testing.T) {
	c := setup(t)
	// Break the DB underneath the service: close the store via a fresh handler wired to a closed store.
	dir := t.TempDir()
	st, _ := store.Open(context.Background(), filepath.Join(dir, "db"))
	_ = st.Close()
	svc := service.New(st, service.NewProviders(st, nil, provider.Options{}), nil, events.New(), service.Config{}, nil)
	srv := httptest.NewServer(New(svc, Options{}))
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/api/v1/torrents")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 500 || strings.Contains(strings.ToLower(string(b)), "sql") || strings.Contains(string(b), "closed") {
		t.Fatalf("500 must not leak internals: %d %s", resp.StatusCode, b)
	}
	_ = c
}

func TestRequireAPIKeyAndInProcessTransport(t *testing.T) {
	h := RequireAPIKey("k", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo", r.Header.Get("X-In"))
		w.WriteHeader(202)
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write(append([]byte("got:"), b...))
	}))
	cl := &http.Client{Transport: InProcessTransport{Handler: h}}
	// No key → 401 even with ?api_key (not accepted outside SSE).
	resp, err := cl.Get("http://x/mcp?api_key=k")
	if err != nil || resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("expected 401: %v %v", err, resp)
	}
	req, _ := http.NewRequest("POST", "http://x/mcp", strings.NewReader("body"))
	req.Header.Set("Authorization", "Bearer k")
	req.Header.Set("X-In", "hello")
	resp, err = cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 202 || resp.Header.Get("X-Echo") != "hello" || string(b) != "got:body" || resp.Request == nil {
		t.Fatalf("in-process transport: %d %q %q", resp.StatusCode, resp.Header.Get("X-Echo"), b)
	}
	// Context propagates.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h2 := InProcessTransport{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() == nil {
			t.Error("context should be cancelled")
		}
		w.WriteHeader(200)
	})}
	req2, _ := http.NewRequestWithContext(ctx, "GET", "http://x/", nil)
	if _, err := h2.RoundTrip(req2); err != nil {
		t.Fatal(err)
	}
}
