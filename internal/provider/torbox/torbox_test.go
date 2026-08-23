package torbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
)

const mylistJSON = `{"success":true,"error":null,"detail":"ok","data":[
 {"id":101,"hash":"ABCDEF0123456789ABCDEF0123456789ABCDEF01","name":"Show.S01","size":3000,"active":true,"cached":false,
  "download_state":"downloading","download_finished":false,"download_present":false,"progress":0.42,"download_speed":1234,"seeds":7,
  "created_at":"2026-08-23T10:00:00Z","updated_at":"2026-08-23T10:05:00Z",
  "files":[{"id":1,"name":"Show.S01/e01.mkv","short_name":"e01.mkv","size":2000},{"id":2,"name":"Show.S01/e02.mkv","short_name":"e02.mkv","size":1000}]},
 {"id":102,"hash":"1111111111111111111111111111111111111111","name":"Movie","size":500,"active":false,"cached":true,
  "download_state":"cached","download_finished":true,"download_present":true,"progress":1,"download_speed":0,"seeds":0,
  "created_at":"2026-08-22T10:00:00Z","updated_at":"2026-08-22T10:01:00Z",
  "files":[{"id":9,"name":"Movie/movie.mkv","short_name":"movie.mkv","size":500}]},
 {"id":103,"hash":"2222222222222222222222222222222222222222","name":"Dead","size":0,"active":false,"cached":false,
  "download_state":"error","download_finished":false,"download_present":false,"progress":0,"files":[]},
 {"id":104,"hash":"3333333333333333333333333333333333333333","name":"Seeding","size":10,"active":true,"cached":false,
  "download_state":"uploading","download_finished":true,"download_present":false,"progress":1,"files":[]}
]}`

func newServer(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p, err := New(domain.Credentials{APIKey: "KEY"}, provider.Options{BaseURL: srv.URL + "/v1/api/"})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Client)
}

func TestNewRequiresKey(t *testing.T) {
	if _, err := New(domain.Credentials{}, provider.Options{}); provider.KindOf(err) != provider.ErrAuth {
		t.Fatalf("expected auth error, got %v", err)
	}
	if _, err := provider.New(domain.ProviderTorBox, domain.Credentials{APIKey: "k"}, provider.Options{}); err != nil {
		t.Fatalf("registry: %v", err)
	}
}

func TestListTorrentsMapsStatuses(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api/torrents/mylist" || r.Header.Get("Authorization") != "Bearer KEY" || r.URL.Query().Get("bypass_cache") != "true" {
			t.Errorf("unexpected request %s %v", r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mylistJSON))
	})
	ts, err := c.ListTorrents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]provider.Torrent{}
	for _, x := range ts {
		byID[x.ID] = x
	}
	dl := byID["101"]
	if dl.Status != domain.TorrentDownloading || dl.Hash != "abcdef0123456789abcdef0123456789abcdef01" || dl.Progress != 0.42 || dl.Seeders != 7 || len(dl.Files) != 2 {
		t.Fatalf("101: %+v", dl)
	}
	if dl.Files[0].Path != "Show.S01/e01.mkv" || dl.Files[0].ID != "1" || dl.Files[0].Link != "torbox://torrent/101/file/1" || !dl.Files[0].Selected {
		t.Fatalf("file mapping: %+v", dl.Files[0])
	}
	if dl.AddedAt == nil || dl.AddedAt.Hour() != 10 {
		t.Fatalf("added_at: %v", dl.AddedAt)
	}
	if fin := byID["102"]; fin.Status != domain.TorrentFinished || fin.Progress != 1 || fin.EndedAt == nil {
		t.Fatalf("102: %+v", fin)
	}
	if e := byID["103"]; e.Status != domain.TorrentError {
		t.Fatalf("103: %+v", e)
	}
	if s := byID["104"]; s.Status != domain.TorrentUploading {
		t.Fatalf("104 (finished but not present) should be uploading: %+v", s)
	}
}

func TestListEmptyQuirk404(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"success":true,"error":"ITEM_NOT_FOUND","detail":"no items","data":null}`))
	})
	ts, err := c.ListTorrents(context.Background())
	if err != nil || len(ts) != 0 {
		t.Fatalf("expected empty list, got %v %v", ts, err)
	}
}

func TestGetTorrentAndLinks(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("id") != "102" {
			t.Errorf("id param missing: %s", r.URL.RawQuery)
		}
		var all struct{ Data []json.RawMessage }
		_ = json.Unmarshal([]byte(mylistJSON), &all)
		_, _ = w.Write([]byte(`{"success":true,"data":` + string(all.Data[1]) + `}`))
	})
	links, err := c.Links(context.Background(), "102")
	if err != nil || len(links) != 1 || links[0].URL != "torbox://torrent/102/file/9" || links[0].Path != "Movie/movie.mkv" {
		t.Fatalf("links: %v %+v", err, links)
	}
}

func TestGetTorrentNotFound(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"success":false,"error":"ITEM_NOT_FOUND","detail":"nope","data":null}`))
	})
	if _, err := c.GetTorrent(context.Background(), "9"); provider.KindOf(err) != provider.ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestAddMagnetAndFile(t *testing.T) {
	var calls int32
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/v1/api/torrents/createtorrent" || r.Method != http.MethodPost {
			t.Errorf("bad path/method %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart: %v", err)
		}
		if r.FormValue("allow_zip") != "false" {
			t.Errorf("allow_zip should be false")
		}
		w.Header().Set("Content-Type", "application/json")
		if m := r.FormValue("magnet"); m != "" {
			if !strings.HasPrefix(m, "magnet:") {
				t.Errorf("magnet field: %q", m)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"torrent_id":555,"hash":"ABC","auth_id":"x"}}`))
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil || hdr.Filename == "" {
			t.Errorf("file part missing: %v", err)
		} else {
			_ = f.Close()
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"torrent_id":556,"hash":"DEF"}}`))
	})
	ctx := context.Background()
	r, err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:abc")
	if err != nil || r.ID != "555" || r.Hash != "abc" {
		t.Fatalf("magnet: %v %+v", err, r)
	}
	r, err = c.AddTorrentFile(ctx, []byte("d8:announce0:e"))
	if err != nil || r.ID != "556" {
		t.Fatalf("file: %v %+v", err, r)
	}
}

func TestAddErrorsAreClassifiedAndNotRetried(t *testing.T) {
	cases := map[string]provider.ErrorKind{
		"ACTIVE_LIMIT":   provider.ErrLimit,
		"BOZO_TORRENT":   provider.ErrPermanent,
		"DATABASE_ERROR": provider.ErrTransient,
		"BAD_TOKEN":      provider.ErrAuth,
	}
	for code, kind := range cases {
		var n int32
		c := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&n, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"success":false,"error":"` + code + `","detail":"d","data":null}`))
		})
		_, err := c.AddMagnet(context.Background(), "magnet:?xt=urn:btih:abc")
		if provider.KindOf(err) != kind || n != 1 {
			t.Fatalf("%s: kind=%v err=%v calls=%d", code, provider.KindOf(err), err, n)
		}
	}
}

func TestUnrestrictUsesTokenQueryNotBearer(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/v1/api/torrents/requestdl" || q.Get("token") != "KEY" || q.Get("torrent_id") != "102" || q.Get("file_id") != "9" || r.Header.Get("Authorization") != "" {
			t.Errorf("bad requestdl: %s auth=%q", r.URL, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":"https://cdn.example/dld/abc/movie.mkv?token=t"}`))
	})
	d, err := c.Unrestrict(context.Background(), "torbox://torrent/102/file/9")
	if err != nil || !strings.HasPrefix(d.URL, "https://cdn.example/") || d.Filename != "movie.mkv" {
		t.Fatalf("unrestrict: %v %+v", err, d)
	}
	if _, err := c.Unrestrict(context.Background(), "https://not-torbox/x"); provider.KindOf(err) != provider.ErrPermanent {
		t.Fatalf("foreign link should be permanent error: %v", err)
	}
}

func TestDelete(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/api/torrents/controltorrent" || body["operation"] != "delete" || body["torrent_id"] != float64(102) {
			t.Errorf("bad control: %s %v", r.URL.Path, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":null}`))
	})
	if err := c.Delete(context.Background(), "102"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), "x"); provider.KindOf(err) != provider.ErrPermanent {
		t.Fatal("bad id should be permanent error")
	}
}

func TestUserAndCheckCached(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/api/user/me":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"email":"a@b.c","plan":2,"premium_expires_at":"2027-01-01T00:00:00Z"}}`))
		case "/v1/api/torrents/checkcached":
			var body struct{ Hashes []string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Hashes) != 2 {
				t.Errorf("hashes: %v", body.Hashes)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"AAAA":{"name":"x","size":1,"hash":"AAAA"}}}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	u, err := c.User(context.Background())
	if err != nil || !u.Premium || u.Plan != "pro" || u.ExpiresAt == nil {
		t.Fatalf("user: %v %+v", err, u)
	}
	m, err := c.CheckCached(context.Background(), []string{"aaaa", "bbbb"})
	if err != nil || !m["aaaa"] || m["bbbb"] {
		t.Fatalf("cached: %v %v", err, m)
	}
}

func TestMapStatusTable(t *testing.T) {
	cases := []struct {
		in   torrentData
		want domain.TorrentStatus
	}{
		{torrentData{DownloadState: "metaDL", Active: true}, domain.TorrentProcessing},
		{torrentData{DownloadState: "stalled (no seeds)", Active: true}, domain.TorrentDownloading},
		{torrentData{DownloadState: "paused", Active: true}, domain.TorrentDownloading},
		{torrentData{DownloadState: "completed", Active: true}, domain.TorrentUploading},
		{torrentData{DownloadState: "completed", DownloadFinished: true, DownloadPresent: true}, domain.TorrentFinished},
		{torrentData{DownloadState: "cached", Cached: true, DownloadPresent: true}, domain.TorrentFinished},
		{torrentData{DownloadState: "missingFiles"}, domain.TorrentError},
		{torrentData{DownloadState: "somethingNew", Active: false}, domain.TorrentError},
		{torrentData{DownloadState: "somethingNew", Active: true}, domain.TorrentProcessing},
	}
	for _, c := range cases {
		if got, _ := mapStatus(c.in); got != c.want {
			t.Errorf("%+v → %s, want %s", c.in, got, c.want)
		}
	}
}

// TestLive exercises the real API when TORBOX_API_KEY is set. Read-only.
func TestLive(t *testing.T) {
	key := os.Getenv("TORBOX_API_KEY")
	if key == "" {
		t.Skip("TORBOX_API_KEY not set")
	}
	p, err := New(domain.Credentials{APIKey: key}, provider.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, err := p.User(ctx)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	t.Logf("user: %+v", u)
	ts, err := p.ListTorrents(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, x := range ts {
		t.Logf("torrent %s %s %s %.2f files=%d", x.ID, x.Name, x.Status, x.Progress, len(x.Files))
	}
}
