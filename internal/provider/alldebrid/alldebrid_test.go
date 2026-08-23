package alldebrid

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
)

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := New(domain.Credentials{APIKey: "KEY"}, provider.Options{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Client)
}

func ok(w http.ResponseWriter, data string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"success","data":` + data + `}`))
}

func fail(w http.ResponseWriter, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"error","error":{"code":"` + code + `","message":"` + msg + `"}}`))
}

const statusList = `{"magnets":[
 {"id":1,"filename":"Show.S01","size":3000,"hash":"ABCDEF","status":"Downloading","statusCode":1,"downloaded":1500,"seeders":5,"downloadSpeed":99,"uploadDate":1700000000,"completionDate":0},
 {"id":2,"filename":"Movie","size":500,"hash":"111111","status":"Ready","statusCode":4,"downloaded":500,"uploadDate":1700000000,"completionDate":1700001000},
 {"id":3,"filename":"Dead","size":0,"hash":"222222","status":"Upload fail","statusCode":5},
 {"id":4,"filename":"Packing","size":10,"hash":"333333","status":"Compressing / Moving","statusCode":2},
 {"id":5,"filename":"Q","size":10,"hash":"444444","status":"In Queue","statusCode":0}]}`

const filesTree = `{"magnets":[{"id":2,"files":[{"n":"Movie","e":[{"n":"movie.mkv","s":400,"l":"https://alldebrid.com/f/AAA"},{"n":"Subs","e":[{"n":"en.srt","s":100,"l":"https://alldebrid.com/f/BBB"}]}]}]}]}`

func TestListAndStatusMapping(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4.1/magnet/status" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer KEY" {
			t.Errorf("bad request %s %s", r.Method, r.URL.Path)
		}
		ok(w, statusList)
	})
	ts, err := c.ListTorrents(context.Background())
	if err != nil || len(ts) != 5 {
		t.Fatalf("list: %v %d", err, len(ts))
	}
	want := map[string]domain.TorrentStatus{"1": domain.TorrentDownloading, "2": domain.TorrentFinished, "3": domain.TorrentError, "4": domain.TorrentUploading, "5": domain.TorrentProcessing}
	for _, x := range ts {
		if x.Status != want[x.ID] {
			t.Errorf("%s: %s want %s", x.ID, x.Status, want[x.ID])
		}
	}
	if ts[0].Progress != 0.5 || ts[0].Hash != "abcdef" || ts[0].Seeders != 5 || ts[0].AddedAt == nil {
		t.Fatalf("mapping %+v", ts[0])
	}
	if ts[1].Progress != 1 || ts[1].EndedAt == nil {
		t.Fatalf("finished mapping %+v", ts[1])
	}
	if ts[2].Message != "Upload fail" {
		t.Fatalf("error message %+v", ts[2])
	}
}

func TestGetTorrentFilesAndLinks(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/v4.1/magnet/status":
			if r.PostForm.Get("id") != "2" {
				t.Errorf("id %q", r.PostForm.Get("id"))
			}
			ok(w, `{"magnets":{"id":2,"filename":"Movie","size":500,"hash":"111111","status":"Ready","statusCode":4,"downloaded":500}}`)
		case "/v4/magnet/files":
			if r.PostForm.Get("id[]") != "2" {
				t.Errorf("id[] %v", r.PostForm)
			}
			ok(w, filesTree)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	tor, err := c.GetTorrent(context.Background(), "2")
	if err != nil || tor.Status != domain.TorrentFinished || len(tor.Files) != 2 {
		t.Fatalf("get: %v %+v", err, tor)
	}
	if tor.Files[0].Path != "Movie/movie.mkv" || tor.Files[0].Size != 400 || tor.Files[0].Link != "https://alldebrid.com/f/AAA" || tor.Files[1].Path != "Movie/Subs/en.srt" {
		t.Fatalf("files %+v", tor.Files)
	}
	links, err := c.Links(context.Background(), "2")
	if err != nil || len(links) != 2 || links[1].URL != "https://alldebrid.com/f/BBB" || links[1].FileID != "Movie/Subs/en.srt" {
		t.Fatalf("links %v %+v", err, links)
	}
}

func TestAddUnlockDeleteErrors(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		switch r.URL.Path {
		case "/v4/magnet/upload":
			m := r.FormValue("magnets[]")
			if m == "magnet:?xt=urn:btih:bad" {
				ok(w, `{"magnets":[{"magnet":"magnet:?xt=urn:btih:bad","error":{"code":"MAGNET_INVALID_URI","message":"Magnet is not valid"}}]}`)
				return
			}
			if m == "magnet:?xt=urn:btih:full" {
				fail(w, "MAGNET_TOO_MANY_ACTIVE", "Already have maximum allowed active magnets")
				return
			}
			ok(w, `{"magnets":[{"magnet":"`+m+`","hash":"ABC","name":"n","size":1,"ready":true,"id":77}]}`)
		case "/v4/magnet/upload/file":
			f, _, err := r.FormFile("files[]")
			if err != nil {
				t.Errorf("file part: %v", err)
			} else {
				_ = f.Close()
			}
			ok(w, `{"files":[{"file":"upload.torrent","name":"n","hash":"DEF","id":78,"size":1,"ready":false}]}`)
		case "/v4/link/unlock":
			switch r.FormValue("link") {
			case "https://alldebrid.com/f/AAA":
				ok(w, `{"link":"https://cdn.ad/movie.mkv","host":"alldebrid","filename":"movie.mkv","filesize":400,"id":"x","paws":false}`)
			case "https://alldebrid.com/f/SLOW":
				ok(w, `{"link":"https://alldebrid.com/f/SLOW","filename":"x","filesize":1,"delayed":123}`) // echoed link + delayed id
			case "https://alldebrid.com/f/FAILS":
				ok(w, `{"link":"https://alldebrid.com/f/FAILS","filename":"x","filesize":1,"delayed":124}`)
			default:
				fail(w, "LINK_DOWN", "Link is not available")
			}
		case "/v4/link/delayed":
			switch r.FormValue("id") {
			case "123":
				ok(w, `{"status":2,"time_left":0,"link":"https://cdn.ad/slow.mkv"}`)
			default:
				ok(w, `{"status":3,"time_left":0}`)
			}
		case "/v4/magnet/delete":
			if r.FormValue("id") == "missing" {
				fail(w, "MAGNET_INVALID_ID", "This magnet ID does not exists or is invalid")
				return
			}
			ok(w, `{"message":"Magnet was successfully deleted"}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	ctx := context.Background()
	r, err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:abc")
	if err != nil || r.ID != "77" || r.Hash != "abc" {
		t.Fatalf("add: %v %+v", err, r)
	}
	if _, err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:bad"); provider.KindOf(err) != provider.ErrPermanent {
		t.Fatalf("per-item error should be permanent: %v", err)
	}
	if _, err := c.AddMagnet(ctx, "magnet:?xt=urn:btih:full"); provider.KindOf(err) != provider.ErrLimit {
		t.Fatalf("too many active should be limit: %v", err)
	}
	if r, err := c.AddTorrentFile(ctx, []byte("d8:announce0:e")); err != nil || r.ID != "78" {
		t.Fatalf("add file: %v %+v", err, r)
	}
	d, err := c.Unrestrict(ctx, "https://alldebrid.com/f/AAA")
	if err != nil || d.URL != "https://cdn.ad/movie.mkv" || d.Size != 400 {
		t.Fatalf("unlock: %v %+v", err, d)
	}
	d, err = c.Unrestrict(ctx, "https://alldebrid.com/f/SLOW")
	if err != nil || d.URL != "https://cdn.ad/slow.mkv" {
		t.Fatalf("delayed link should be resolved via /link/delayed, got %v %+v", err, d)
	}
	_, err = c.Unrestrict(ctx, "https://alldebrid.com/f/FAILS")
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Kind != provider.ErrPermanent {
		t.Fatalf("delayed status 3 should be permanent: %v", err)
	}
	if _, err := c.Unrestrict(ctx, "https://alldebrid.com/f/DEAD"); provider.KindOf(err) != provider.ErrNotFound {
		t.Fatalf("link down: %v", err)
	}
	if err := c.Delete(ctx, "77"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "missing"); err != nil {
		t.Fatalf("missing magnet delete should be ok: %v", err)
	}
}

func TestGetTorrentArrayFormAndFilesError(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/v4.1/magnet/status":
			ok(w, `{"magnets":[{"id":9,"filename":"Arr","size":5,"hash":"abc","status":"Ready","statusCode":4,"downloaded":5}]}`)
		case "/v4/magnet/files":
			ok(w, `{"magnets":[{"id":"9","error":{"code":"MAGNET_INVALID_ID","message":"gone"}}]}`)
		}
	})
	_, err := c.GetTorrent(context.Background(), "9")
	if provider.KindOf(err) != provider.ErrNotFound || strings.Contains(err.Error(), "http 200") {
		t.Fatalf("per-magnet files error: %v", err)
	}
	if mapError("MAGNET_PROCESSING", "", 0).Kind != provider.ErrTransient || mapError("LINK_TOO_MANY_DOWNLOADS", "", 0).Kind != provider.ErrLimit {
		t.Fatal("classification")
	}
	// Finished with an empty tree → not ready.
	c2 := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4.1/magnet/status" {
			ok(w, `{"magnets":[{"id":9,"filename":"Arr","status":"Ready","statusCode":4}]}`)
			return
		}
		ok(w, `{"magnets":[{"id":"9","files":[]}]}`)
	})
	if links, err := c2.Links(context.Background(), "9"); err != nil || links != nil {
		t.Fatalf("empty tree should be not-ready: %v %v", err, links)
	}
}

func TestUserAndAuthError(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer KEY" {
			ok(w, `{"user":{"username":"u","email":"e","isPremium":true,"isSubscribed":false,"isTrial":false,"premiumUntil":1900000000}}`)
			return
		}
		fail(w, "AUTH_BAD_APIKEY", "The auth apikey is invalid")
	})
	u, err := c.User(context.Background())
	if err != nil || !u.Premium || u.Username != "u" || u.ExpiresAt == nil {
		t.Fatalf("user: %v %+v", err, u)
	}
	bad := newClient(t, func(w http.ResponseWriter, _ *http.Request) { fail(w, "AUTH_BAD_APIKEY", "The auth apikey is invalid") })
	if _, err := bad.User(context.Background()); provider.KindOf(err) != provider.ErrAuth {
		t.Fatalf("auth error: %v", err)
	}
	if _, err := New(domain.Credentials{}, provider.Options{}); provider.KindOf(err) != provider.ErrAuth {
		t.Fatal("key required")
	}
}

func TestLive(t *testing.T) {
	key := os.Getenv("ALLDEBRID_API_KEY")
	if key == "" {
		t.Skip("ALLDEBRID_API_KEY not set")
	}
	p, err := New(domain.Credentials{APIKey: key}, provider.Options{})
	if err != nil {
		t.Fatal(err)
	}
	u, err := p.User(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v", u)
}
