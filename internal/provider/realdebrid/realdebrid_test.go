package realdebrid

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
)

const infoJSON = `{"id":"ABC123","filename":"Show.S01","original_filename":"Show.S01","hash":"ABCDEF0123456789ABCDEF0123456789ABCDEF01","bytes":3000,"original_bytes":4000,
"host":"real-debrid.com","split":2000,"progress":100,"status":"downloaded","added":"2026-08-23T10:00:00.000Z","ended":"2026-08-23T10:20:00.000Z",
"files":[{"id":1,"path":"/Show.S01/e01.mkv","bytes":2000,"selected":1},{"id":2,"path":"/Show.S01/e02.mkv","bytes":1000,"selected":1},{"id":3,"path":"/Show.S01/sample.mkv","bytes":50,"selected":0}],
"links":["https://real-debrid.com/d/AAA","https://real-debrid.com/d/BBB"]}`

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := New(domain.Credentials{APIKey: "TOKEN"}, provider.Options{BaseURL: srv.URL + "/rest/1.0/"})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Client)
}

func jsonw(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

func TestNewAndCaps(t *testing.T) {
	if _, err := New(domain.Credentials{}, provider.Options{}); provider.KindOf(err) != provider.ErrAuth {
		t.Fatal("token required")
	}
	p, err := provider.New(domain.ProviderRealDebrid, domain.Credentials{AccessToken: "t"}, provider.Options{})
	if err != nil || !p.Caps().SelectFiles || p.Caps().DirectLinks {
		t.Fatalf("registry/caps: %v", err)
	}
}

func TestListAndStatusMapping(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/1.0/torrents" || r.Header.Get("Authorization") != "Bearer TOKEN" {
			t.Errorf("bad request %s %s", r.URL, r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("page") != "1" {
			jsonw(w, 200, `[]`)
			return
		}
		jsonw(w, 200, `[
		 {"id":"A","filename":"a","hash":"AA","bytes":1,"progress":50,"status":"downloading","seeders":3,"speed":100,"added":"2026-08-23T10:00:00.000Z","links":[]},
		 {"id":"B","filename":"b","hash":"BB","bytes":1,"progress":0,"status":"waiting_files_selection","links":[]},
		 {"id":"C","filename":"c","hash":"CC","bytes":1,"progress":100,"status":"downloaded","links":["x"]},
		 {"id":"D","filename":"d","hash":"DD","bytes":1,"progress":0,"status":"magnet_error","links":[]},
		 {"id":"E","filename":"e","hash":"EE","bytes":1,"progress":100,"status":"compressing","links":[]},
		 {"id":"F","filename":"f","hash":"FF","bytes":1,"progress":0,"status":"magnet_conversion","links":[]}]`)
	})
	ts, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]domain.TorrentStatus{"A": domain.TorrentDownloading, "B": domain.TorrentWaitingSelection, "C": domain.TorrentFinished, "D": domain.TorrentError, "E": domain.TorrentUploading, "F": domain.TorrentProcessing}
	if len(ts) != len(want) {
		t.Fatalf("got %d torrents", len(ts))
	}
	for _, x := range ts {
		if x.Status != want[x.ID] {
			t.Errorf("%s: %s want %s", x.ID, x.Status, want[x.ID])
		}
		if x.ID == "A" && (x.Progress != 0.5 || x.Seeders != 3 || x.Hash != "aa") {
			t.Errorf("A mapping %+v", x)
		}
	}
}

func TestGetTorrentFilesAndLinks(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/1.0/torrents/info/ABC123" {
			t.Errorf("path %s", r.URL.Path)
		}
		jsonw(w, 200, infoJSON)
	})
	tor, err := c.GetTorrent(context.Background(), "ABC123")
	if err != nil || len(tor.Files) != 3 || tor.Files[0].Path != "Show.S01/e01.mkv" || !tor.Files[0].Selected || tor.Files[2].Selected || tor.Progress != 1 || tor.EndedAt == nil {
		t.Fatalf("get: %v %+v", err, tor)
	}
	links, err := c.Links(context.Background(), "ABC123")
	if err != nil || len(links) != 2 {
		t.Fatalf("links: %v %+v", err, links)
	}
	if links[0].FileID != "1" || links[0].Path != "Show.S01/e01.mkv" || links[0].Size != 2000 || links[0].URL != "https://real-debrid.com/d/AAA" || links[1].FileID != "2" {
		t.Fatalf("link mapping %+v", links)
	}
}

func TestLinksFewerThanSelectedFiles(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		jsonw(w, 200, strings.Replace(infoJSON, `"links":["https://real-debrid.com/d/AAA","https://real-debrid.com/d/BBB"]`, `"links":["https://real-debrid.com/d/RAR"]`, 1))
	})
	links, err := c.Links(context.Background(), "ABC123")
	if err != nil || len(links) != 1 || links[0].Size != 0 || links[0].FileID != "1" {
		t.Fatalf("repacked links: %v %+v", err, links)
	}
}

func TestAddSelectUnrestrictDelete(t *testing.T) {
	var adds int32
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/1.0/torrents/addMagnet" && r.Method == "POST":
			atomic.AddInt32(&adds, 1)
			_ = r.ParseForm()
			if !strings.HasPrefix(r.PostForm.Get("magnet"), "magnet:") {
				t.Errorf("magnet form: %v", r.PostForm)
			}
			jsonw(w, 201, `{"id":"NEW1","uri":"https://api.real-debrid.com/rest/1.0/torrents/info/NEW1"}`)
		case r.URL.Path == "/rest/1.0/torrents/addTorrent" && r.Method == "PUT":
			if r.Header.Get("Content-Type") != "application/x-bittorrent" {
				t.Errorf("content type %s", r.Header.Get("Content-Type"))
			}
			jsonw(w, 201, `{"id":"NEW2","uri":"x"}`)
		case r.URL.Path == "/rest/1.0/torrents/selectFiles/NEW1":
			_ = r.ParseForm()
			if r.PostForm.Get("files") != "1,2" {
				t.Errorf("files %q", r.PostForm.Get("files"))
			}
			w.WriteHeader(204)
		case r.URL.Path == "/rest/1.0/torrents/selectFiles/ALL":
			_ = r.ParseForm()
			if r.PostForm.Get("files") != "all" {
				t.Errorf("files %q", r.PostForm.Get("files"))
			}
			w.WriteHeader(202) // already done
		case r.URL.Path == "/rest/1.0/unrestrict/link":
			_ = r.ParseForm()
			if r.PostForm.Get("link") != "https://real-debrid.com/d/AAA" {
				t.Errorf("link %q", r.PostForm.Get("link"))
			}
			jsonw(w, 200, `{"id":"U","filename":"e01.mkv","mimeType":"video/x-matroska","filesize":2000,"link":"https://real-debrid.com/d/AAA","host":"real-debrid.com","chunks":16,"crc":1,"download":"https://cdn.rd/dl/e01.mkv","streamable":1}`)
		case r.URL.Path == "/rest/1.0/torrents/delete/NEW1" && r.Method == "DELETE":
			w.WriteHeader(204)
		case r.URL.Path == "/rest/1.0/torrents/delete/GONE":
			jsonw(w, 404, `{"error":"unknown_ressource","error_code":7}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	ctx := context.Background()
	r, err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:abc")
	if err != nil || r.ID != "NEW1" {
		t.Fatalf("add: %v %+v", err, r)
	}
	if r, err := c.AddTorrentFile(ctx, []byte("d8:announce0:e")); err != nil || r.ID != "NEW2" {
		t.Fatalf("add file: %v %+v", err, r)
	}
	if err := c.SelectFiles(ctx, "NEW1", []string{"1", "2"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SelectFiles(ctx, "ALL", nil); err != nil {
		t.Fatalf("202 should be ok: %v", err)
	}
	d, err := c.Unrestrict(ctx, "https://real-debrid.com/d/AAA")
	if err != nil || d.URL != "https://cdn.rd/dl/e01.mkv" || d.Size != 2000 || d.MaxConnections != 16 || d.Filename != "e01.mkv" {
		t.Fatalf("unrestrict: %v %+v", err, d)
	}
	if err := c.Delete(ctx, "NEW1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "GONE"); err != nil {
		t.Fatalf("deleting a missing torrent should be ok: %v", err)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   provider.ErrorKind
		code   string
	}{
		{401, `{"error":"bad_token","error_code":8}`, provider.ErrAuth, "8"},
		{403, `{"error":"permission_denied","error_code":9}`, provider.ErrAuth, "9"},
		{429, `{"error":"too_many_requests","error_code":34}`, provider.ErrRateLimited, "34"},
		{503, `{"error":"service_unavailable","error_code":25}`, provider.ErrTransient, "25"},
		{400, `{"error":"torrent_too_big","error_code":29}`, provider.ErrLimit, "29"},
		{400, `{"error":"infringing_file","error_code":35}`, provider.ErrPermanent, "35"},
		{400, `{"error":"fair_usage_limit","error_code":36}`, provider.ErrLimit, "36"},
		{400, `{"error":"torrent_already_active","error_code":33}`, provider.ErrPermanent, "33"},
		{400, `not json`, provider.ErrPermanent, ""},
	}
	for _, tc := range cases {
		var n int32
		c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&n, 1)
			jsonw(w, tc.status, tc.body)
		})
		_, err := c.AddMagnet(context.Background(), "magnet:?xt=urn:btih:abc")
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Kind != tc.kind || pe.Code != tc.code {
			t.Errorf("%d %s: got %v", tc.status, tc.body, err)
		}
		if n != 1 {
			t.Errorf("%d %s: add must not be retried (calls=%d)", tc.status, tc.body, n)
		}
	}
}

func TestUser(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		jsonw(w, 200, `{"id":1,"username":"nate","email":"n@x","points":10,"locale":"en","avatar":"","type":"premium","premium":86400,"expiration":"2027-01-01T00:00:00.000Z"}`)
	})
	u, err := c.User(context.Background())
	if err != nil || u.Username != "nate" || !u.Premium || u.ExpiresAt == nil {
		t.Fatalf("user: %v %+v", err, u)
	}
}

func TestLive(t *testing.T) {
	key := os.Getenv("REALDEBRID_API_KEY")
	if key == "" {
		t.Skip("REALDEBRID_API_KEY not set")
	}
	p, err := New(domain.Credentials{APIKey: key}, provider.Options{})
	if err != nil {
		t.Fatal(err)
	}
	u, err := p.User(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("user %+v", u)
	ts, err := p.ListTorrents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d torrents", len(ts))
}
