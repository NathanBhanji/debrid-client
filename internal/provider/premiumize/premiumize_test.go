package premiumize

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
)

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := New(domain.Credentials{APIKey: "KEY"}, provider.Options{BaseURL: srv.URL + "/api/"})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Client)
}

func js(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

const transfers = `{"status":"success","transfers":[
 {"id":"t1","name":"Show.S01","message":"Loading...","status":"running","progress":0.42,"src":"magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=x","folder_id":null,"file_id":null},
 {"id":"t2","name":"Movie","message":"","status":"finished","progress":1,"src":"magnet:?xt=urn:btih:1111111111111111111111111111111111111111","folder_id":"F1","file_id":null},
 {"id":"t3","name":"Single","message":"","status":"seeding","progress":1,"src":"","folder_id":null,"file_id":"FILE9"},
 {"id":"t4","name":"Bad","message":"Torrent is dead","status":"error","progress":0,"src":"","folder_id":null,"file_id":null},
 {"id":"t5","name":"W","message":"","status":"waiting","progress":0,"src":"","folder_id":null,"file_id":null}]}`

func TestListAndStatus(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/transfer/list" || r.Header.Get("Authorization") != "Bearer KEY" {
			t.Errorf("bad request %s", r.URL)
		}
		js(w, transfers)
	})
	ts, err := c.ListTorrents(context.Background())
	if err != nil || len(ts) != 5 {
		t.Fatalf("list: %v %d", err, len(ts))
	}
	want := map[string]domain.TorrentStatus{"t1": domain.TorrentDownloading, "t2": domain.TorrentFinished, "t3": domain.TorrentFinished, "t4": domain.TorrentError, "t5": domain.TorrentProcessing}
	for _, x := range ts {
		if x.Status != want[x.ID] {
			t.Errorf("%s: %s want %s", x.ID, x.Status, want[x.ID])
		}
	}
	if ts[0].Hash != "abcdef0123456789abcdef0123456789abcdef01" || ts[0].Progress != 0.42 || ts[0].Message != "Loading..." {
		t.Fatalf("t1 %+v", ts[0])
	}
	if ts[3].Message != "Torrent is dead" {
		t.Fatalf("t4 %+v", ts[3])
	}
}

func TestGetTorrentWalksFolderAndLinks(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/transfer/list":
			js(w, transfers)
		case "/api/folder/list":
			switch r.URL.Query().Get("id") {
			case "F1":
				js(w, `{"status":"success","name":"Movie","folder_id":"F1","content":[{"id":"i1","name":"movie.mkv","type":"file","size":500,"link":"https://dl.pm/movie.mkv"},{"id":"F2","name":"Subs","type":"folder"}]}`)
			case "F2":
				js(w, `{"status":"success","name":"Subs","folder_id":"F2","content":[{"id":"i2","name":"en.srt","type":"file","size":10,"link":"https://dl.pm/en.srt"}]}`)
			default:
				t.Errorf("unexpected folder %s", r.URL.Query().Get("id"))
			}
		case "/api/item/details":
			if r.URL.Query().Get("id") != "FILE9" {
				t.Errorf("item id %s", r.URL.Query().Get("id"))
			}
			js(w, `{"status":"success","id":"FILE9","name":"single.mkv","size":77,"link":"https://dl.pm/single.mkv"}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	tor, err := c.GetTorrent(context.Background(), "t2")
	if err != nil || len(tor.Files) != 2 || tor.Size != 510 || tor.Files[1].Path != "Movie/Subs/en.srt" || tor.Files[0].Link != "https://dl.pm/movie.mkv" {
		t.Fatalf("folder torrent: %v %+v", err, tor)
	}
	single, err := c.GetTorrent(context.Background(), "t3")
	if err != nil || len(single.Files) != 1 || single.Files[0].Path != "single.mkv" || single.Size != 77 {
		t.Fatalf("single torrent: %v %+v", err, single)
	}
	links, err := c.Links(context.Background(), "t2")
	if err != nil || len(links) != 2 || links[0].URL != "https://dl.pm/movie.mkv" {
		t.Fatalf("links: %v %+v", err, links)
	}
	if _, err := c.GetTorrent(context.Background(), "nope"); provider.KindOf(err) != provider.ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	d, _ := c.Unrestrict(context.Background(), "https://dl.pm/movie.mkv")
	if d.URL != "https://dl.pm/movie.mkv" || d.Filename != "movie.mkv" {
		t.Fatalf("unrestrict identity: %+v", d)
	}
}

func TestAddDeleteCacheErrors(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		switch r.URL.Path {
		case "/api/transfer/create":
			if r.FormValue("src") == "magnet:?xt=urn:btih:full" {
				js(w, `{"status":"error","message":"You have reached the limit of active transfers","code":"account_limit_reached"}`)
				return
			}
			if f, _, err := r.FormFile("file"); err == nil {
				_ = f.Close()
				js(w, `{"status":"success","id":"tf","name":"n","type":"torrent"}`)
				return
			}
			js(w, `{"status":"success","id":"tn","name":"n","type":"torrent"}`)
		case "/api/transfer/delete":
			if r.FormValue("id") == "missing" {
				js(w, `{"status":"error","message":"Transfer not found","code":"not_found"}`)
				return
			}
			js(w, `{"status":"success"}`)
		case "/api/cache/check":
			if len(r.Form["items[]"]) != 2 {
				t.Errorf("items %v", r.Form)
			}
			js(w, `{"status":"success","response":[true,false],"filename":["a",null],"filesize":["1",0],"transcoded":[false,null]}`)
		case "/api/account/info":
			if r.Header.Get("Authorization") != "Bearer KEY" {
				js(w, `{"status":"error","message":"Not logged in.","code":"authentication_failed"}`)
				return
			}
			js(w, `{"status":"success","customer_id":"123","premium_until":4102444800,"limit_used":0.1}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	ctx := context.Background()
	r, err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01")
	if err != nil || r.ID != "tn" || r.Hash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("add: %v %+v", err, r)
	}
	if _, err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:full"); provider.KindOf(err) != provider.ErrLimit {
		t.Fatalf("limit: %v", err)
	}
	if r, err := c.AddTorrentFile(ctx, []byte("d8:announce0:e")); err != nil || r.ID != "tf" {
		t.Fatalf("add file: %v %+v", err, r)
	}
	if err := c.Delete(ctx, "tn"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "missing"); err != nil {
		t.Fatalf("missing delete ok: %v", err)
	}
	m, err := c.CheckCached(ctx, []string{"AAA", "BBB"})
	if err != nil || !m["aaa"] || m["bbb"] {
		t.Fatalf("cache: %v %v", err, m)
	}
	u, err := c.User(ctx)
	if err != nil || !u.Premium || u.Username != "123" {
		t.Fatalf("user: %v %+v", err, u)
	}
	bad := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		js(w, `{"status":"error","message":"Not logged in.","code":"authentication_failed"}`)
	})
	if _, err := bad.User(ctx); provider.KindOf(err) != provider.ErrAuth {
		t.Fatalf("auth: %v", err)
	}
}

func TestLive(t *testing.T) {
	key := os.Getenv("PREMIUMIZE_API_KEY")
	if key == "" {
		t.Skip("PREMIUMIZE_API_KEY not set")
	}
	p, _ := New(domain.Credentials{APIKey: key}, provider.Options{})
	u, err := p.User(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", u)
}
