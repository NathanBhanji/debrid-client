package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/auth"
	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
)

func TestManualFileReviewHTTPAndNativeCookieBoundaries(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{"api_key": "fixture"}}, 201, nil)
	var torrent apimodel.Torrent
	c.mustJSON("POST", "/api/v1/torrents", map[string]any{"magnet": submissionMagnet, "settings": map[string]any{"wait_for_selection": true}}, 201, &torrent)
	if !torrent.Settings.WaitForSelection || torrent.FileSelectionState != "pending" {
		t.Fatal("manual add did not hold")
	}
	path := "/api/v1/torrents/" + torrent.ID + "/file-selection"
	if response, _ := c.do("GET", path, nil, false); response.StatusCode != 401 {
		t.Fatal("unauthenticated file metadata")
	}
	var view apimodel.FileSelection
	c.mustJSON("GET", path, nil, 200, &view)
	if view.State != "pending" || len(view.Files) != 0 {
		t.Fatal("parsed files offered before provider")
	}
	remote, err := c.fake.AddMagnet(ctx, submissionMagnet)
	if err != nil {
		t.Fatal(err)
	}
	c.fake.SetFiles(remote.ID, []domain.File{{ID: "provider/one", Path: "one.mkv", Size: 3, Link: "private-fixture-link"}})
	c.fake.Finish(remote.ID, nil)
	if _, err := c.store.MutateTorrent(ctx, torrent.ID, func(t *domain.Torrent) error { t.ProviderID = remote.ID; t.Status = domain.TorrentFinished; return nil }); err != nil {
		t.Fatal(err)
	}
	response, bytes := c.do("GET", path, nil, true)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(bytes), "private-fixture-link") {
		t.Fatal("review cache/link boundary")
	}
	if err := json.Unmarshal(bytes, &view); err != nil || view.State != "ready" {
		t.Fatal("authoritative view", err)
	}
	c.mustJSON("PUT", path, map[string]any{"revision": strings.Repeat("a", 64), "file_ids": []string{"provider/one"}}, 409, nil)
	c.mustJSON("PUT", path, map[string]any{"revision": view.Revision, "file_ids": []string{"provider/one", "provider/one"}}, 422, nil)
	c.mustJSON("PUT", "/api/v1/torrents/"+torrent.ID+"/files", map[string]any{"file_ids": []string{"provider/one"}}, 409, nil)
	server := httptest.NewServer(New(c.svc, Options{APIKey: key, Auth: auth.New(c.store)}))
	defer server.Close()
	status, _, headers := submissionHTTP(t, server.URL, "POST", "/api/v1/auth/setup/password", []byte(`{"username":"fixture","password":"fixture-password-123"}`), map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"})
	if status != 200 {
		t.Fatal("fixture login")
	}
	cookies := (&http.Response{Header: headers}).Cookies()
	if len(cookies) == 0 {
		t.Fatal("fixture cookie")
	}
	requestHeaders := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value, "Content-Type": "application/json"}
	body := submissionJSON(t, map[string]any{"revision": view.Revision, "file_ids": []string{"provider/one"}})
	for _, origin := range []string{"https://foreign.example.test", "null"} {
		requestHeaders["Origin"] = origin
		status, _, _ = submissionHTTP(t, server.URL, "PUT", path, body, requestHeaders)
		if status != 401 {
			t.Fatal("foreign origin accepted")
		}
	}
	delete(requestHeaders, "Origin")
	status, bytes, headers = submissionHTTP(t, server.URL, "PUT", path, body, requestHeaders)
	if status != 200 || headers.Get("Cache-Control") != "no-store" || strings.Contains(string(bytes), "selection_receipt") {
		t.Fatal("native confirmation", status)
	}
	c.fake.SetErr(provider.Errorf(provider.ErrAuth, "", "offline fixture"))
	status, _, _ = submissionHTTP(t, server.URL, "PUT", path, body, requestHeaders)
	if status != 200 {
		t.Fatal("replay depended on provider")
	}
	_, bytes = c.do("GET", "/api/v1/torrents/"+torrent.ID, nil, true)
	if strings.Contains(string(bytes), "selection_receipt") || !strings.Contains(string(bytes), `"file_selection_state":"accepted"`) {
		t.Fatal("private receipt leaked or acceptance omitted")
	}
}
