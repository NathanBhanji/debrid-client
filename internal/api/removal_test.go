package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/auth"
	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/service"
)

func TestRemovalHTTPPreconditionsTypedAbsenceAndNativeCookie(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{"api_key": "fixture"}}, 201, nil)
	var torrent apimodel.Torrent
	c.mustJSON("POST", "/api/v1/torrents", map[string]any{"magnet": submissionMagnet}, 201, &torrent)
	root := "/api/v1/torrents/" + torrent.ID
	if response, _ := c.do("GET", root+"/removal-review", nil, false); response.StatusCode != 401 {
		t.Fatal("unauthenticated review")
	}
	var view service.RemovalReview
	c.mustJSON("GET", root+"/removal-review", nil, 200, &view)
	headers := map[string]string{"Authorization": "Bearer " + key}
	for _, bad := range []string{"*", "W/\"" + view.Revision + "\"", view.Revision, "\"bad\"", "\"" + view.Revision + "\", \"" + view.Revision + "\""} {
		headers["If-Match"] = bad
		status, _, _ := submissionHTTP(t, c.srv.URL, "DELETE", root, nil, headers)
		if status != 422 {
			t.Fatal("malformed/unsupported precondition accepted", status)
		}
	}
	if _, err := c.store.MutateTorrent(ctx, torrent.ID, func(t *domain.Torrent) error { t.RetryCount++; return nil }); err != nil {
		t.Fatal(err)
	}
	headers["If-Match"] = "\"" + view.Revision + "\""
	status, _, responseHeaders := submissionHTTP(t, c.srv.URL, "DELETE", root, nil, headers)
	if status != 412 || responseHeaders.Get("Cache-Control") != "no-store" {
		t.Fatal("stale precondition not refused", status)
	}
	c.mustJSON("GET", root+"/removal-review", nil, 200, &view)
	if !view.Exists {
		t.Fatal("rejected removal changed record")
	}
	server := httptest.NewServer(New(c.svc, Options{APIKey: key, Auth: auth.New(c.store)}))
	defer server.Close()
	status, _, responseHeaders = submissionHTTP(t, server.URL, "POST", "/api/v1/auth/setup/password", []byte(`{"username":"fixture","password":"fixture-password-123"}`), map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"})
	if status != 200 {
		t.Fatal("fixture login", status)
	}
	cookies := (&http.Response{Header: responseHeaders}).Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing fixture cookie")
	}
	native := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value, "If-Match": "\"" + view.Revision + "\""}
	for _, origin := range []string{"https://foreign.example.test", "null"} {
		native["Origin"] = origin
		status, _, _ = submissionHTTP(t, server.URL, "DELETE", root+"/removal", nil, native)
		if status != 401 {
			t.Fatal("foreign removal origin accepted", status)
		}
	}
	delete(native, "Origin")
	status, _, responseHeaders = submissionHTTP(t, server.URL, "DELETE", root+"/removal", nil, native)
	if status != 204 || responseHeaders.Get("Cache-Control") != "no-store" {
		t.Fatal("native removal", status)
	}
	status, _, _ = submissionHTTP(t, server.URL, "DELETE", root+"/removal", nil, native)
	if status != 404 {
		t.Fatal("duplicate removal", status)
	}
	response, data := c.do("GET", root+"/removal-review", nil, true)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(data), "revision") {
		t.Fatal("absence not typed", response.StatusCode)
	}
	if err := json.Unmarshal(data, &view); err != nil || view.Exists || view.TorrentID != torrent.ID {
		t.Fatal(view, err)
	}
	// Legacy clients omit If-Match and retain existing removal semantics.
	c.mustJSON("POST", "/api/v1/torrents", map[string]any{"magnet": submissionMagnet}, 201, &torrent)
	c.mustJSON("DELETE", "/api/v1/torrents/"+torrent.ID, nil, 204, nil)
}

type removalSpyEngine struct {
	service.NoopEngine
	calls atomic.Int32
}

func (e *removalSpyEngine) CancelTorrent(context.Context, string) error { e.calls.Add(1); return nil }
func TestRemovalRejectsEmptyAndDuplicateRawIfMatchWithoutMutation(t *testing.T) {
	c := setup(t)
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{"api_key": "fixture"}}, 201, nil)
	var torrent apimodel.Torrent
	c.mustJSON("POST", "/api/v1/torrents", map[string]any{"magnet": submissionMagnet}, 201, &torrent)

	spy := &removalSpyEngine{}
	c.svc.SetEngine(spy)
	ctx := context.Background()
	remote, err := c.fake.AddMagnet(ctx, submissionMagnet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.MutateTorrent(ctx, torrent.ID, func(t *domain.Torrent) error { t.ProviderID = remote.ID; return nil }); err != nil {
		t.Fatal(err)
	}
	detail, err := c.svc.GetTorrent(ctx, torrent.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := c.svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir := service.TorrentDir(status.DownloadDir, detail.Torrent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "retained.mkv")
	if err := os.WriteFile(file, []byte("fixture bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := "/api/v1/torrents/" + torrent.ID
	var view service.RemovalReview
	c.mustJSON("GET", root+"/removal-review", nil, 200, &view)
	quoted := "\"" + view.Revision + "\""
	for _, suffix := range []string{"", "/removal"} {
		cases := [][]string{{""}, {quoted, quoted}, {quoted, ""}}
		if suffix != "" {
			cases = append(cases, nil)
		}
		for _, values := range cases {
			request, err := http.NewRequest("DELETE", c.srv.URL+root+suffix+"?files=true&provider=true", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+key)
			for _, value := range values {
				request.Header.Add("If-Match", value)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != 422 || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("invalid raw header bypassed precondition", response.StatusCode)
			}
			var current apimodel.Torrent
			c.mustJSON("GET", root, nil, 200, &current)

			data, err := os.ReadFile(file)
			if err != nil || string(data) != "fixture bytes" || len(c.fake.IDs()) != 1 || spy.calls.Load() != 0 {
				t.Fatal("rejected header had deletion side effects", err)
			}
			if current.Status != torrent.Status || current.RetryCount != torrent.RetryCount || c.fake.Calls("Delete") != 0 {
				t.Fatal("rejected header mutated transfer")
			}
		}
	}
}
