package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/auth"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

// setupAuth builds a server with session auth enabled.
func setupAuth(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := service.New(st, service.NewProviders(st, nil, provider.Options{}), nil, events.New(), service.Config{}, nil)
	h := New(svc, Options{APIKey: key, Auth: auth.New(st)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func req(t *testing.T, srv *httptest.Server, method, path, body string, hdr map[string]string) *http.Response {
	t.Helper()
	r, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func sessionCookieFrom(t *testing.T, resp *http.Response) string {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookie {
			if !c.HttpOnly {
				t.Fatal("session cookie must be HttpOnly")
			}
			return c.Value
		}
	}
	t.Fatal("no session cookie in response")
	return ""
}

func TestAuthFlow(t *testing.T) {
	srv := setupAuth(t)

	// Status is public and starts unconfigured.
	resp := req(t, srv, "GET", "/api/v1/auth/status", "", nil)
	var status struct {
		Configured bool   `json:"configured"`
		Mode       string `json:"mode"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&status)
	if resp.StatusCode != 200 || status.Configured {
		t.Fatalf("status: %d %+v", resp.StatusCode, status)
	}

	// Setup without the API key is rejected; a session cannot exist yet anyway.
	resp = req(t, srv, "POST", "/api/v1/auth/setup/password", `{"username":"nathan","password":"password12"}`, nil)
	if resp.StatusCode != 401 {
		t.Fatalf("setup without key: %d", resp.StatusCode)
	}

	// Setup with the API key succeeds and returns a session cookie.
	resp = req(t, srv, "POST", "/api/v1/auth/setup/password", `{"username":"nathan","password":"password12"}`,
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 200 {
		t.Fatalf("setup: %d", resp.StatusCode)
	}
	tok := sessionCookieFrom(t, resp)

	// Second setup conflicts.
	resp = req(t, srv, "POST", "/api/v1/auth/setup/password", `{"username":"other","password":"password12"}`,
		map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 409 {
		t.Fatalf("second setup: %d", resp.StatusCode)
	}

	// The session cookie authenticates a protected GET.
	cookie := map[string]string{"Cookie": SessionCookie + "=" + tok}
	resp = req(t, srv, "GET", "/api/v1/torrents", "", cookie)
	if resp.StatusCode != 200 {
		t.Fatalf("cookie GET: %d", resp.StatusCode)
	}

	// /auth/me reports the session user.
	resp = req(t, srv, "GET", "/api/v1/auth/me", "", cookie)
	var me struct {
		Username string `json:"username"`
		APIKey   bool   `json:"api_key"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&me)
	if me.Username != "nathan" || me.APIKey {
		t.Fatalf("me: %+v", me)
	}

	// Cookie-authenticated writes need a same-origin Origin (CSRF).
	host := strings.TrimPrefix(srv.URL, "http://")
	resp = req(t, srv, "POST", "/api/v1/torrents", `{"magnet":"magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		map[string]string{"Cookie": SessionCookie + "=" + tok, "Origin": "https://evil.example"})
	if resp.StatusCode != 401 {
		t.Fatalf("cross-origin cookie write: %d, want 401", resp.StatusCode)
	}
	resp = req(t, srv, "POST", "/api/v1/torrents", `{"magnet":"magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		map[string]string{"Cookie": SessionCookie + "=" + tok, "Origin": "http://" + host})
	if resp.StatusCode == 401 {
		t.Fatalf("same-origin cookie write rejected")
	}

	// Wrong password 401s; right password logs in.
	resp = req(t, srv, "POST", "/api/v1/auth/login", `{"username":"nathan","password":"wrong-password"}`, nil)
	if resp.StatusCode != 401 {
		t.Fatalf("bad login: %d", resp.StatusCode)
	}
	resp = req(t, srv, "POST", "/api/v1/auth/login", `{"username":"nathan","password":"password12"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	tok2 := sessionCookieFrom(t, resp)

	// Logout revokes the session.
	resp = req(t, srv, "POST", "/api/v1/auth/logout", "", map[string]string{"Cookie": SessionCookie + "=" + tok2})
	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	resp = req(t, srv, "GET", "/api/v1/torrents", "", map[string]string{"Cookie": SessionCookie + "=" + tok2})
	if resp.StatusCode != 401 {
		t.Fatalf("revoked session accepted: %d", resp.StatusCode)
	}

	// The API key path is unaffected throughout.
	resp = req(t, srv, "GET", "/api/v1/torrents", "", map[string]string{"Authorization": "Bearer " + key})
	if resp.StatusCode != 200 {
		t.Fatalf("api key: %d", resp.StatusCode)
	}
	// API-key /auth/me reports api_key.
	resp = req(t, srv, "GET", "/api/v1/auth/me", "", map[string]string{"Authorization": "Bearer " + key})
	me = struct {
		Username string `json:"username"`
		APIKey   bool   `json:"api_key"`
	}{}
	_ = json.NewDecoder(resp.Body).Decode(&me)
	if !me.APIKey {
		t.Fatalf("key me: %+v", me)
	}
}

func TestOrganizePreview(t *testing.T) {
	srv := setupAuth(t)
	hdr := map[string]string{"Authorization": "Bearer " + key}

	resp := req(t, srv, "POST", "/api/v1/organize/preview",
		`{"name":"Some.Movie.2019.2160p.WEB-DL.x265-GRP"}`, hdr)
	if resp.StatusCode != 200 {
		t.Fatalf("preview: %d", resp.StatusCode)
	}
	var out struct {
		Path      string `json:"path"`
		Organized bool   `json:"organized"`
		Parsed    *struct {
			Title string `json:"title"`
			Year  int    `json:"year"`
		} `json:"parsed"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if !out.Organized || out.Path != "Some Movie (2019)" || out.Parsed == nil || out.Parsed.Title != "Some Movie" {
		t.Fatalf("preview result: %+v", out)
	}

	// Template override applies without saving.
	resp = req(t, srv, "POST", "/api/v1/organize/preview",
		`{"name":"Some.Movie.2019.2160p.WEB-DL.x265-GRP","movie_template":"Films/{title} [{resolution}]"}`, hdr)
	out.Parsed = nil
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Path != "Films/Some Movie [2160p]" {
		t.Fatalf("override result: %+v", out)
	}

	// Invalid template → 422; unparseable name → organized=false, raw path.
	resp = req(t, srv, "POST", "/api/v1/organize/preview",
		`{"name":"x","movie_template":"{bogus}"}`, hdr)
	if resp.StatusCode != 422 {
		t.Fatalf("bad template: %d", resp.StatusCode)
	}
	resp = req(t, srv, "POST", "/api/v1/organize/preview", `{"name":"random data"}`, hdr)
	out = struct {
		Path      string `json:"path"`
		Organized bool   `json:"organized"`
		Parsed    *struct {
			Title string `json:"title"`
			Year  int    `json:"year"`
		} `json:"parsed"`
	}{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Organized || out.Path != "random data" {
		t.Fatalf("unparseable result: %+v", out)
	}
}
