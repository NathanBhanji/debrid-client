package debridlink

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
)

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := New(domain.Credentials{APIKey: "KEY"}, provider.Options{BaseURL: srv.URL + "/api/v2/"})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Client)
}

func ok(w http.ResponseWriter, value string, pagination string) {
	w.Header().Set("Content-Type", "application/json")
	body := `{"success":true,"value":` + value
	if pagination != "" {
		body += `,"pagination":` + pagination
	}
	_, _ = w.Write([]byte(body + `}`))
}

func fail(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"success":false,"error":"` + code + `","error_description":"` + desc + `"}`))
}

const list = `[
 {"id":"A","name":"Show.S01","created":1700000000,"hashString":"ABCDEF","wait":false,"peersConnected":4,"status":6,"totalSize":3000,"downloadPercent":40,"downloadSpeed":77,"isZip":false,"files":[{"id":"A-0","name":"Show.S01/e01.mkv","size":2000,"downloadUrl":"https://dl.dl/e01.mkv","downloadPercent":60},{"id":"A-1","name":"Show.S01/e02.mkv","size":1000,"downloadUrl":"https://dl.dl/e02.mkv","downloadPercent":0}]},
 {"id":"B","name":"Movie","created":1700000000,"hashString":"111111","wait":false,"status":100,"totalSize":500,"downloadPercent":100,"files":[{"id":"B-0","name":"Movie/movie.mkv","size":500,"downloadUrl":"https://dl.dl/movie.mkv","downloadPercent":100}]},
 {"id":"C","name":"Wait","wait":true,"status":0,"totalSize":0,"downloadPercent":0,"files":[]},
 {"id":"D","name":"Seed","status":8,"totalSize":10,"downloadPercent":99.5,"files":[]},
 {"id":"E","name":"Queued","status":1,"totalSize":10,"downloadPercent":0,"files":[]},
 {"id":"Z","name":"ManyFiles","status":100,"totalSize":10,"downloadPercent":100,"isZip":true,"files":[{"id":"Z-zip","name":"ManyFiles.zip","size":10,"downloadUrl":"https://dl.dl/ManyFiles.zip","downloadPercent":100}]}]`

func TestListPaginationAndStatus(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/seedbox/list" || r.Header.Get("Authorization") != "Bearer KEY" {
			t.Errorf("bad request %s", r.URL)
		}
		switch r.URL.Query().Get("page") {
		case "0":
			ok(w, list, `{"page":0,"pages":2,"next":1,"previous":-1}`)
		case "1":
			ok(w, `[{"id":"F","name":"Page2","status":100,"totalSize":1,"downloadPercent":100,"files":[]}]`, `{"page":1,"pages":2,"next":-1,"previous":0}`)
		default:
			t.Errorf("unexpected page %s", r.URL.Query().Get("page"))
		}
	})
	ts, err := c.ListTorrents(context.Background())
	if err != nil || len(ts) != 7 {
		t.Fatalf("list: %v %d", err, len(ts))
	}
	want := map[string]domain.TorrentStatus{"A": domain.TorrentDownloading, "B": domain.TorrentFinished, "C": domain.TorrentWaitingSelection, "D": domain.TorrentUploading, "E": domain.TorrentProcessing, "F": domain.TorrentFinished, "Z": domain.TorrentFinished}
	for _, x := range ts {
		if x.ID == "Z" && len(x.Files) != 0 {
			t.Fatalf("zip-only listing must not report files: %+v", x.Files)
		}
	}
	for _, x := range ts {
		if x.Status != want[x.ID] {
			t.Errorf("%s: %s want %s", x.ID, x.Status, want[x.ID])
		}
	}
	if ts[0].Progress != 0.4 || ts[0].Hash != "abcdef" || ts[0].Seeders != 4 || len(ts[0].Files) != 2 || ts[0].Files[0].Link != "https://dl.dl/e01.mkv" || ts[0].AddedAt == nil {
		t.Fatalf("A mapping %+v", ts[0])
	}
}

func TestGetLinksAddDelete(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		switch {
		case r.URL.Path == "/api/v2/seedbox/list" && r.URL.Query().Get("ids") == "B":
			ok(w, `[{"id":"B","name":"Movie","hashString":"111111","status":100,"totalSize":500,"downloadPercent":100,"files":[{"id":"B-0","name":"Movie/movie.mkv","size":500,"downloadUrl":"https://dl.dl/movie.mkv","downloadPercent":100}]}]`, "")
		case r.URL.Path == "/api/v2/seedbox/list" && r.URL.Query().Get("ids") == "nope":
			ok(w, `[]`, "")
		case r.URL.Path == "/api/v2/seedbox/add" && r.Method == "POST":
			if r.FormValue("url") == "magnet:?xt=urn:btih:full" {
				fail(w, 400, "maxTorrent", "Max torrents per day reached")
				return
			}
			if f, _, err := r.FormFile("file"); err == nil {
				_ = f.Close()
				ok(w, `{"id":"NEWF","name":"f","hashString":"FFFF","status":1,"files":[]}`, "")
				return
			}
			ok(w, `{"id":"NEW","name":"n","hashString":"ABC","status":1,"files":[]}`, "")
		case r.URL.Path == "/api/v2/seedbox/NEW/remove" && r.Method == "DELETE":
			ok(w, `["NEW"]`, "")
		case r.URL.Path == "/api/v2/seedbox/missing/remove":
			fail(w, 400, "badId", "Bad id")
		case r.URL.Path == "/api/v2/account/infos":
			ok(w, `{"username":"u","email":"e","accountType":1,"premiumLeft":86400,"pts":0}`, "")
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	})
	ctx := context.Background()
	tor, err := c.GetTorrent(ctx, "B")
	if err != nil || tor.Status != domain.TorrentFinished || len(tor.Files) != 1 || tor.Files[0].Path != "Movie/movie.mkv" {
		t.Fatalf("get: %v %+v", err, tor)
	}
	if _, err := c.GetTorrent(ctx, "nope"); provider.KindOf(err) != provider.ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	links, err := c.Links(ctx, "B")
	if err != nil || len(links) != 1 || links[0].URL != "https://dl.dl/movie.mkv" {
		t.Fatalf("links: %v %+v", err, links)
	}
	d, _ := c.Unrestrict(ctx, links[0].URL)
	if d.URL != links[0].URL || d.Filename != "movie.mkv" {
		t.Fatalf("identity unrestrict %+v", d)
	}
	r, err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:abc")
	if err != nil || r.ID != "NEW" || r.Hash != "abc" {
		t.Fatalf("add: %v %+v", err, r)
	}
	if _, err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:full"); provider.KindOf(err) != provider.ErrLimit {
		t.Fatalf("limit: %v", err)
	}
	if r, err := c.AddTorrentFile(ctx, []byte("d8:announce0:e")); err != nil || r.ID != "NEWF" {
		t.Fatalf("add file: %v %+v", err, r)
	}
	if err := c.Delete(ctx, "NEW"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "missing"); err != nil {
		t.Fatalf("missing delete ok: %v", err)
	}
	u, err := c.User(ctx)
	if err != nil || !u.Premium || u.Username != "u" || u.ExpiresAt == nil {
		t.Fatalf("user: %v %+v", err, u)
	}
}

func TestAuthAndFlood(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) { fail(w, 401, "badToken", "Session expired") })
	_, err := c.User(context.Background())
	if provider.KindOf(err) != provider.ErrAuth {
		t.Fatalf("auth: %v", err)
	}
	// Long descriptions on HTTP-classified statuses must still yield the DL code (parsed from Body, not the snippet).
	long := strings.Repeat("Your server IP is not allowed; please whitelist it in your account settings. ", 5)
	c2 := newClient(t, func(w http.ResponseWriter, _ *http.Request) { fail(w, 403, "serverNotAllowed", long) })
	_, err = c2.User(context.Background())
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Code != "serverNotAllowed" || pe.Kind != provider.ErrAuth || !strings.Contains(pe.Message, "whitelist") {
		t.Fatalf("enrich from body: %v", err)
	}
	c3 := newClient(t, func(w http.ResponseWriter, _ *http.Request) { fail(w, 429, "floodDetected", long) })
	_, err = c3.User(context.Background())
	if provider.KindOf(err) != provider.ErrRateLimited || provider.RetryAfter(err) != time.Hour {
		t.Fatalf("flood via 429 should carry the 1h hint: %v", err)
	}
	fl := newClient(t, func(w http.ResponseWriter, _ *http.Request) { fail(w, 200, "floodDetected", "retry in 1h") })
	_, err = fl.User(context.Background())
	if provider.KindOf(err) != provider.ErrRateLimited || provider.RetryAfter(err) == 0 {
		t.Fatalf("flood: %v", err)
	}
	if _, err := New(domain.Credentials{}, provider.Options{}); provider.KindOf(err) != provider.ErrAuth {
		t.Fatal("key required")
	}
}

func TestPaginationTerminatesOnNullNext(t *testing.T) {
	var n int32
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		ok(w, `[{"id":"X","name":"x","status":100,"downloadPercent":100,"files":[]}]`, `{"page":0,"pages":1,"next":null,"previous":-1}`)
	})
	ts, err := c.ListTorrents(context.Background())
	if err != nil || len(ts) != 1 || atomic.LoadInt32(&n) != 1 {
		t.Fatalf("pagination: %v %d requests=%d", err, len(ts), n)
	}
}

func TestLive(t *testing.T) {
	key := os.Getenv("DEBRIDLINK_API_KEY")
	if key == "" {
		t.Skip("DEBRIDLINK_API_KEY not set")
	}
	p, _ := New(domain.Credentials{APIKey: key}, provider.Options{})
	u, err := p.User(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", u)
}
