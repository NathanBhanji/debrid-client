package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/auth"
	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/service"
)

func TestCancellationHTTPAndNativeCookieBoundaries(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{"api_key": "fixture"}}, 201, nil)
	var torrent apimodel.Torrent
	c.mustJSON("POST", "/api/v1/torrents", map[string]any{"magnet": submissionMagnet}, 201, &torrent)
	if _, err := c.store.MutateTorrent(ctx, torrent.ID, func(tor *domain.Torrent) error {
		tor.Status = domain.TorrentProcessing
		tor.Error = "private-fixture-token"
		at := time.Now().UTC()
		tor.CompletedAt = &at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	root := "/api/v1/torrents/" + torrent.ID
	if response, _ := c.do("GET", root+"/cancellation-review", nil, false); response.StatusCode != 401 {
		t.Fatal("unauthenticated retry review")
	}
	response, data := c.do("GET", root+"/cancellation-review", nil, true)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(data), "private-fixture-token") || strings.Contains(string(data), "settings") {
		t.Fatal("retry review response boundary")
	}
	var view service.CancellationReview
	if err := json.Unmarshal(data, &view); err != nil || !view.Allowed {
		t.Fatal(view, err)
	}
	requestKey := uuid.NewString()
	request := map[string]any{"key": requestKey, "revision": view.Revision}
	if response, _ := c.do("POST", root+"/cancellation-requests", request, false); response.StatusCode != 401 {
		t.Fatal("unauthenticated retry")
	}
	c.mustJSON("POST", root+"/cancellation-requests", map[string]any{"key": "not-a-uuid", "revision": view.Revision}, 422, nil)
	server := httptest.NewServer(New(c.svc, Options{APIKey: key, Auth: auth.New(c.store)}))
	defer server.Close()
	status, _, headers := submissionHTTP(t, server.URL, "POST", "/api/v1/auth/setup/password", []byte(`{"username":"fixture","password":"fixture-password-123"}`), map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"})
	if status != 200 {
		t.Fatal("fixture login", status)
	}
	cookies := (&http.Response{Header: headers}).Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing fixture cookie")
	}
	requestHeaders := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value, "Content-Type": "application/json"}
	body := submissionJSON(t, request)
	for _, origin := range []string{"https://foreign.example.test", "null"} {
		requestHeaders["Origin"] = origin
		status, _, _ = submissionHTTP(t, server.URL, "POST", root+"/cancellation-requests", body, requestHeaders)
		if status != 401 {
			t.Fatal("foreign retry origin accepted", status)
		}
	}
	delete(requestHeaders, "Origin")
	status, data, headers = submissionHTTP(t, server.URL, "POST", root+"/cancellation-requests", body, requestHeaders)
	if status != 200 || headers.Get("Cache-Control") != "no-store" {
		t.Fatal("native retry", status, string(data))
	}
	var receipt service.CancellationAcceptance
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.TorrentID != torrent.ID {
		t.Fatal(receipt, err)
	}
	c.mustJSON("POST", root+"/cancellation-requests", request, 200, nil)
	c.mustJSON("POST", root+"/cancellation-requests", map[string]any{"key": uuid.NewString(), "revision": view.Revision}, 409, nil)
	receiptPath := root + "/cancellation-requests/" + requestKey + "?revision=" + view.Revision
	if response, _ := c.do("GET", receiptPath, nil, false); response.StatusCode != 401 {
		t.Fatal("unauthenticated retry receipt")
	}
	response, _ = c.do("GET", receiptPath, nil, true)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("retry receipt cache boundary")
	}
	c.mustJSON("GET", root+"/cancellation-requests/"+requestKey+"?revision="+strings.Repeat("b", 64), nil, 422, nil)
	c.mustJSON("GET", "/api/v1/torrents/other/cancellation-requests/"+requestKey+"?revision="+view.Revision, nil, 422, nil)
	missing, _ := c.do("GET", root+"/cancellation-requests/"+uuid.NewString()+"?revision="+view.Revision, nil, true)
	if missing.StatusCode != 404 || missing.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing receipt cached or wrong status")
	}
	c.mustJSON("DELETE", root, nil, 204, nil)
	c.mustJSON("POST", root+"/cancellation-requests", request, 200, nil)
	c.mustJSON("GET", receiptPath, nil, 200, &receipt)
	if receipt.TorrentExists {
		t.Fatal("removed parent reported present")
	}
}
