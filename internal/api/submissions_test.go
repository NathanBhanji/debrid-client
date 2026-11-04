package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/auth"
	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/providertest"
	"github.com/NathanBhanji/debrid-client/internal/service"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

const submissionMagnet = "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=Fixture%2bOriginal&tr=https%3a%2f%2ffixture.test%2fa%3ftoken%3dprivate&x.fixture=keep"

func submissionHTTP(t *testing.T, base, method, path string, body []byte, headers map[string]string) (int, []byte, http.Header) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data, response.Header
}
func submissionJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func torrentUpload(t *testing.T, settings []string, fields ...map[string]string) ([]byte, string) {
	t.Helper()
	info, err := bencode.Marshal(metainfo.Info{Name: "Fixture.mkv", Length: 10, PieceLength: 16384, Pieces: make([]byte, 20)})
	if err != nil {
		t.Fatal(err)
	}
	var file, body bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: info}).Write(&file); err != nil {
		t.Fatal(err)
	}
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "fixture.torrent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(file.Bytes()); err != nil {
		t.Fatal(err)
	}
	for _, value := range settings {
		if err := writer.WriteField("settings", value); err != nil {
			t.Fatal(err)
		}
	}
	for _, group := range fields {
		for name, value := range group {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}
func TestSubmissionHTTPReplayConflictReceiptAndRemoval(t *testing.T) {
	c := setup(t)
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{"api_key": "fixture"}}, 201, nil)
	id := uuid.NewString()
	headers := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Idempotency-Key": id}
	body := submissionJSON(t, map[string]any{"magnet": submissionMagnet})
	status, data, hdr := submissionHTTP(t, c.srv.URL, "POST", "/api/v1/torrents", body, headers)
	var first apimodel.Torrent
	if err := json.Unmarshal(data, &first); err != nil {
		t.Fatal(err)
	}
	if status != 201 || hdr.Get("X-Debrid-Submission-Replayed") != "false" {
		t.Fatalf("first status=%d headers=%v", status, hdr)
	}
	status, data, hdr = submissionHTTP(t, c.srv.URL, "POST", "/api/v1/torrents", body, headers)
	var replay apimodel.Torrent
	_ = json.Unmarshal(data, &replay)
	if status != 201 || hdr.Get("X-Debrid-Submission-Replayed") != "true" || first.ID != replay.ID {
		t.Fatal("replay did not return same accepted torrent")
	}
	status, data, hdr = submissionHTTP(t, c.srv.URL, "GET", "/api/v1/torrent-submissions/"+id, nil, headers)
	var receipt apimodel.TorrentSubmission
	_ = json.Unmarshal(data, &receipt)
	if status != 200 || receipt.TorrentID != first.ID || !receipt.TorrentExists || hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("receipt %d %+v", status, receipt)
	}
	changed := submissionJSON(t, map[string]any{"magnet": submissionMagnet, "category": "different"})
	status, _, hdr = submissionHTTP(t, c.srv.URL, "POST", "/api/v1/torrents", changed, headers)
	if status != 422 || hdr.Get("X-Debrid-Error-Code") != "idempotency_mismatch" {
		t.Fatalf("mismatch %d %v", status, hdr)
	}
	secondKey := uuid.NewString()
	headers["Idempotency-Key"] = secondKey
	status, _, hdr = submissionHTTP(t, c.srv.URL, "POST", "/api/v1/torrents", body, headers)
	if status != 409 || hdr.Get("X-Debrid-Error-Code") != "active_torrent" || hdr.Get("X-Debrid-Existing-Torrent-ID") != first.ID {
		t.Fatalf("duplicate %d %v", status, hdr)
	}
	status, _, hdr = submissionHTTP(t, c.srv.URL, "GET", "/api/v1/torrent-submissions/"+secondKey, nil, headers)
	if status != 404 || hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("uncommitted receipt %d %v", status, hdr)
	}
	if err := c.svc.DeleteTorrent(context.Background(), first.ID, service.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	headers["Idempotency-Key"] = id
	status, _, hdr = submissionHTTP(t, c.srv.URL, "POST", "/api/v1/torrents", body, headers)
	if status != 409 || hdr.Get("X-Debrid-Error-Code") != "submission_removed" {
		t.Fatalf("removed replay %d %v", status, hdr)
	}
	status, data, _ = submissionHTTP(t, c.srv.URL, "GET", "/api/v1/torrent-submissions/"+id, nil, headers)
	_ = json.Unmarshal(data, &receipt)
	if status != 200 || receipt.TorrentExists {
		t.Fatal("accepted receipt disappeared after deletion")
	}
}
func TestMultipartFullSettingsParityAndBoundaryIndependentReplay(t *testing.T) {
	c := setup(t)
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{"api_key": "fixture"}}, 201, nil)
	settings := apimodel.TorrentSettings{MinFileSize: 7, IncludeRegex: "mkv$", FinishedAction: "keep", FinishedDelay: "1m", DownloadRetries: 0, TorrentRetries: 0, Unpack: false, Organize: ptrAPI(false)}
	raw := string(submissionJSON(t, settings))
	body, contentType := torrentUpload(t, []string{raw})
	id := uuid.NewString()
	headers := map[string]string{"Authorization": "Bearer " + key, "Content-Type": contentType, "Idempotency-Key": id}
	status, data, _ := submissionHTTP(t, c.srv.URL, "POST", "/api/v1/torrents/file", body, headers)
	var first apimodel.Torrent
	_ = json.Unmarshal(data, &first)
	if status != 201 || first.Settings.MinFileSize != 7 || first.Settings.IncludeRegex != "mkv$" || first.Settings.Unpack || first.Settings.DownloadRetries != 0 || first.Settings.Organize == nil || *first.Settings.Organize || first.Settings.FinishedDelay != "1m0s" {
		t.Fatalf("multipart settings %d %+v: %s", status, first.Settings, data)
	}
	body, contentType = torrentUpload(t, []string{raw})
	headers["Content-Type"] = contentType
	status, data, hdr := submissionHTTP(t, c.srv.URL, "POST", "/api/v1/torrents/file", body, headers)
	var second apimodel.Torrent
	_ = json.Unmarshal(data, &second)
	if status != 201 || first.ID != second.ID || hdr.Get("X-Debrid-Submission-Replayed") != "true" {
		t.Fatal("multipart boundary changed intent")
	}
	// Omitting settings preserves the same defaults for both payload kinds.
	other, err := c.svc.AddAccount(context.Background(), service.AddAccountInput{Kind: domain.ProviderTorBox, Name: "other", Credentials: domain.Credentials{APIKey: "fixture"}, SkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	body, contentType = torrentUpload(t, nil, map[string]string{"account": other.ID, "category": "Fixture"})
	headers["Content-Type"] = contentType
	headers["Idempotency-Key"] = uuid.NewString()
	status, data, _ = submissionHTTP(t, c.srv.URL, "POST", "/api/v1/torrents/file", body, headers)
	var omitted apimodel.Torrent
	_ = json.Unmarshal(data, &omitted)
	expected := apimodel.FromTorrentSettings(domain.DefaultTorrentSettings())
	if status != 201 || omitted.AccountID != other.ID || omitted.Category != "Fixture" || !reflect.DeepEqual(omitted.Settings, expected) {
		t.Fatal("multipart omitted settings did not preserve defaults")
	}
	headers["Content-Type"] = "application/json"
	headers["Idempotency-Key"] = uuid.NewString()
	status, data, _ = submissionHTTP(t, c.srv.URL, "POST", "/api/v1/torrents", submissionJSON(t, map[string]any{"magnet": submissionMagnet}), headers)
	var magnet apimodel.Torrent
	_ = json.Unmarshal(data, &magnet)
	if status != 201 || !reflect.DeepEqual(magnet.Settings, expected) {
		t.Fatal("JSON/multipart defaults differ")
	}

}
func ptrAPI(value bool) *bool { return &value }
func TestMultipartSettingsRejectMalformedRepeatedAndSchemaInvalid(t *testing.T) {
	for index, settings := range [][]string{{""}, {"null"}, {"[]"}, {"{} {}"}, {`{"unknown":true}`}, {`{"min_file_size":-1}`}, {`{"download_retries":-1}`}, {`{"finished_action":"delete_everything"}`}, {`{"finished_delay":"never"}`}, {`{"include_regex":"["}`}, {"{}", "{}"}, {strings.Repeat(" ", 16385)}} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			c := setup(t)
			c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{"api_key": "fixture"}}, 201, nil)
			body, contentType := torrentUpload(t, settings)
			id := uuid.NewString()
			headers := map[string]string{"Authorization": "Bearer " + key, "Content-Type": contentType, "Idempotency-Key": id}
			status, _, _ := submissionHTTP(t, c.srv.URL, "POST", "/api/v1/torrents/file", body, headers)
			if status != 422 {
				t.Fatalf("invalid settings accepted: %d", status)
			}
			if all, err := c.svc.ListTorrents(context.Background(), service.ListFilter{}); err != nil || len(all) != 0 {
				t.Fatal("invalid settings queued data")
			}
			if _, err := c.svc.GetTorrentSubmission(context.Background(), id); err == nil {
				t.Fatal("invalid settings stored receipt")
			}
		})
	}
}
func TestNativeCookieSubmissionsNeedNoOriginButForeignOriginsStayBlocked(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	fake := providertest.New(domain.ProviderTorBox)
	factory := func(_ domain.ProviderKind, _ domain.Credentials, _ provider.Options) (provider.Provider, error) {
		return fake, nil
	}
	svc := service.New(st, service.NewProviders(st, factory, provider.Options{}), nil, events.New(), service.Config{}, nil)
	_, err = svc.AddAccount(context.Background(), service.AddAccountInput{Kind: domain.ProviderTorBox, Credentials: domain.Credentials{APIKey: "fixture"}, SkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(svc, Options{APIKey: key, Auth: auth.New(st)}))
	defer server.Close()
	status, _, hdr := submissionHTTP(t, server.URL, "POST", "/api/v1/auth/setup/password", []byte(`{"username":"fixture","password":"fixture-password-123"}`), map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"})
	if status != 200 {
		t.Fatal("fixture sign in")
	}
	cookies := (&http.Response{Header: hdr}).Cookies()
	if len(cookies) == 0 {
		t.Fatal("no fixture cookie")
	}
	id := uuid.NewString()
	headers := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value, "Content-Type": "application/json", "Idempotency-Key": id}
	body := submissionJSON(t, map[string]any{"magnet": submissionMagnet})
	for _, origin := range []string{"https://foreign.example.test", "null"} {
		headers["Origin"] = origin
		status, _, _ = submissionHTTP(t, server.URL, "POST", "/api/v1/torrents", body, headers)
		if status != 401 {
			t.Fatal("foreign cookie write accepted")
		}
	}
	delete(headers, "Origin")
	status, _, _ = submissionHTTP(t, server.URL, "POST", "/api/v1/torrents", body, headers)
	if status != 201 {
		t.Fatalf("native cookie write rejected: %d", status)
	}
	status, _, _ = submissionHTTP(t, server.URL, "GET", "/api/v1/torrent-submissions/"+id, nil, headers)
	if status != 200 {
		t.Fatal("native receipt authentication")
	}
	upload, contentType := torrentUpload(t, nil)
	headers["Content-Type"] = contentType
	headers["Idempotency-Key"] = uuid.NewString()
	status, _, _ = submissionHTTP(t, server.URL, "POST", "/api/v1/torrents/file", upload, headers)
	if status != 201 {
		t.Fatal("native cookie multipart rejected")
	}
	for _, path := range []string{"/api/v1/torrents", "/api/v1/torrents/file", "/api/v1/torrent-submissions/" + id} {
		method := "POST"
		if strings.Contains(path, "submissions") {
			method = "GET"
		}
		status, _, _ = submissionHTTP(t, server.URL, method, path, nil, nil)
		if status != 401 {
			t.Fatalf("unauthenticated route %s returned %d", path, status)
		}
	}
}

func TestLostSuccessfulResponseReconcilesAndReplaysWithoutSecondInsert(t *testing.T) {
	c := setup(t)
	c.mustJSON("POST", "/api/v1/accounts", map[string]any{"kind": "torbox", "credentials": map[string]string{"api_key": "fixture"}}, 201, nil)
	var drop atomic.Bool
	drop.Store(true)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/torrents" && drop.CompareAndSwap(true, false) {
			committed := httptest.NewRecorder()
			c.srv.Config.Handler.ServeHTTP(committed, r)
			if committed.Code != 201 {
				t.Errorf("fixture enqueue %d", committed.Code)
			}
			// A proxy timeout after commit must not become a second accepted enqueue.
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		c.srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	id := uuid.NewString()
	headers := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json", "Idempotency-Key": id}
	body := submissionJSON(t, map[string]any{"magnet": submissionMagnet})
	status, _, _ := submissionHTTP(t, proxy.URL, "POST", "/api/v1/torrents", body, headers)
	if status != 504 {
		t.Fatal("fixture did not lose response")
	}
	status, data, _ := submissionHTTP(t, proxy.URL, "GET", "/api/v1/torrent-submissions/"+id, nil, headers)
	var receipt apimodel.TorrentSubmission
	_ = json.Unmarshal(data, &receipt)
	if status != 200 || !receipt.TorrentExists {
		t.Fatal("committed request was not reconcilable")
	}
	status, data, hdr := submissionHTTP(t, proxy.URL, "POST", "/api/v1/torrents", body, headers)
	var replay apimodel.Torrent
	_ = json.Unmarshal(data, &replay)
	if status != 201 || hdr.Get("X-Debrid-Submission-Replayed") != "true" || replay.ID != receipt.TorrentID {
		t.Fatal("lost response retry was not replayed")
	}
	all, err := c.svc.ListTorrents(context.Background(), service.ListFilter{})
	if err != nil || len(all) != 1 || string(all[0].Torrent.Payload) != submissionMagnet {
		t.Fatal("retry duplicated or changed original magnet bytes")
	}
}
