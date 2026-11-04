package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/auth"
	"github.com/NathanBhanji/debrid-client/internal/service"
)

func TestSettingsReviewHTTPRequiredStrongHeaderAndLegacyConflict(t *testing.T) {
	c := setup(t)
	root := "/api/v1/settings/review"
	if r, _ := c.do("GET", root, nil, false); r.StatusCode != 401 {
		t.Fatal("unauthenticated review accepted")
	}
	r, data := c.do("GET", root, nil, true)
	var view settingsReviewBody
	if err := json.Unmarshal(data, &view); err != nil || r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("ETag") != "\""+view.Revision+"\"" {
		t.Fatal("invalid review envelope", err)
	}
	body, err := json.Marshal(view.Settings)
	if err != nil {
		t.Fatal(err)
	}
	baseline := view.Revision
	variants := [][]string{nil, {""}, {"*"}, {"W/\"" + baseline + "\""}, {baseline}, {"\"bad\""}, {"\"" + baseline + "\", \"" + baseline + "\""}, {"\"" + baseline + "\"", "\"" + baseline + "\""}}
	for _, values := range variants {
		req, err := http.NewRequest("PUT", c.srv.URL+root, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		for _, value := range values {
			req.Header.Add("If-Match", value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 422 || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("invalid precondition accepted", values, resp.StatusCode)
		}
		got, err := c.svc.GetSettingsReview(context.Background())
		if err != nil || got.Revision != baseline {
			t.Fatal("invalid header mutated settings", err)
		}
	}
	headers := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "If-Match": "\"" + baseline + "\""}
	status, data, head := submissionHTTP(t, c.srv.URL, "PUT", root, body, headers)
	if status != 200 || head.Get("Cache-Control") != "no-store" {
		t.Fatal("native update", status)
	}
	if err := json.Unmarshal(data, &view); err != nil || view.Revision == baseline || head.Get("ETag") != "\""+view.Revision+"\"" {
		t.Fatal("new revision missing", err)
	}
	// Legacy replacement remains supported and must invalidate native editors.
	c.mustJSON("PUT", "/api/v1/settings", view.Settings, 200, new(apimodel.Settings))
	headers["If-Match"] = "\"" + view.Revision + "\""
	status, _, head = submissionHTTP(t, c.srv.URL, "PUT", root, body, headers)
	if status != 412 || head.Get("Cache-Control") != "no-store" {
		t.Fatal("legacy overwrite not fenced", status)
	}
	status, _, _ = submissionHTTP(t, c.srv.URL, "PUT", root, []byte(`{"unpack_max_depth":99}`), headers)
	if status != 422 {
		t.Fatal("invalid settings accepted", status)
	}
}
func TestSettingsReviewNativeCookieOriginBoundary(t *testing.T) {
	c := setup(t)
	server := httptest.NewServer(New(c.svc, Options{APIKey: key, Auth: auth.New(c.store)}))
	defer server.Close()
	status, _, head := submissionHTTP(t, server.URL, "POST", "/api/v1/auth/setup/password", []byte(`{"username":"fixture","password":"fixture-password-123"}`), map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"})
	if status != 200 {
		t.Fatal("fixture sign-in", status)
	}
	cookies := (&http.Response{Header: head}).Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing cookie")
	}
	view, err := c.svc.GetSettingsReview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(apimodel.FromSettings(service.DefaultSettings()))
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value, "Content-Type": "application/json", "If-Match": "\"" + view.Revision + "\""}
	for _, origin := range []string{"https://foreign.example.test", "null"} {
		headers["Origin"] = origin
		status, _, _ = submissionHTTP(t, server.URL, "PUT", "/api/v1/settings/review", body, headers)
		if status != 401 {
			t.Fatal("foreign origin accepted", status)
		}
	}
	delete(headers, "Origin")
	status, data, head := submissionHTTP(t, server.URL, "PUT", "/api/v1/settings/review", body, headers)
	if status != 200 || head.Get("Cache-Control") != "no-store" || !strings.Contains(string(data), "revision") {
		t.Fatal("native cookie write", status)
	}
}
