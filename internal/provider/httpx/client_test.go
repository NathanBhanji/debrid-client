package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/NathanBhanji/debrid-client/internal/provider"
)

func newClient(t *testing.T, srv *httptest.Server, mut func(*Config)) *Client {
	t.Helper()
	cfg := Config{BaseURL: srv.URL + "/api/", MaxAttempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond,
		Auth: BearerAuth("tok")}
	if mut != nil {
		mut(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDoSuccessJSONAndAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/api/v1/user" || r.URL.Query().Get("x") != "1" {
			t.Errorf("bad request: %s %s %v", r.URL.Path, r.Header.Get("Authorization"), r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"name":"n"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv, nil)
	resp, err := c.Do(context.Background(), Request{Path: "v1/user", Query: url.Values{"x": {"1"}}, ExpectJSON: true})
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Name string }
	if err := resp.JSON(&out); err != nil || out.Name != "n" {
		t.Fatalf("json: %v %+v", err, out)
	}
}

func TestRetriesOn5xxThenSucceeds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()
	c := newClient(t, srv, nil)
	resp, err := c.Do(context.Background(), Request{Path: "x"})
	if err != nil || string(resp.Body) != "ok" || atomic.LoadInt32(&n) != 3 {
		t.Fatalf("err=%v resp=%+v n=%d", err, resp, n)
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(502)
	}))
	defer srv.Close()
	c := newClient(t, srv, nil)
	_, err := c.Do(context.Background(), Request{Path: "x"})
	if provider.KindOf(err) != provider.ErrTransient || n != 3 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestRateLimited429HonoursRetryAfter(t *testing.T) {
	var n int32
	var gap atomic.Int64
	var last atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		now := time.Now().UnixNano()
		if prev := last.Swap(now); prev != 0 {
			gap.Store(now - prev)
		}
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()
	c := newClient(t, srv, func(c *Config) { c.MaxBackoff = 5 * time.Second }) // Retry-After within the cap → honoured
	if _, err := c.Do(context.Background(), Request{Path: "x"}); err != nil {
		t.Fatal(err)
	}
	if time.Duration(gap.Load()) < 900*time.Millisecond {
		t.Fatalf("Retry-After not honoured, gap=%s", time.Duration(gap.Load()))
	}
}

func TestNoRetryAndClassification(t *testing.T) {
	codes := map[int]provider.ErrorKind{401: provider.ErrAuth, 403: provider.ErrAuth, 404: provider.ErrNotFound, 500: provider.ErrTransient}
	for code, kind := range codes {
		var n int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&n, 1)
			w.WriteHeader(code)
		}))
		c := newClient(t, srv, nil)
		_, err := c.Do(context.Background(), Request{Path: "x", NoRetry: true})
		srv.Close()
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Kind != kind || pe.HTTPStatus != code || n != 1 {
			t.Fatalf("code %d: err=%v n=%d", code, err, n)
		}
	}
	// errors.Is on kind
	if !errors.Is(&provider.Error{Kind: provider.ErrAuth, Code: "x"}, &provider.Error{Kind: provider.ErrAuth}) {
		t.Fatal("errors.Is by kind should match")
	}
}

func TestNon2xxNonSpecialIsReturnedForCallerToClassify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv, nil)
	resp, err := c.Do(context.Background(), Request{Path: "x"})
	if err != nil || resp.StatusCode != 400 {
		t.Fatalf("expected raw 400, got %v %v", resp, err)
	}
}

func TestExpectJSONGuardsAgainstHTMLPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html>maintenance</html>`))
	}))
	defer srv.Close()
	c := newClient(t, srv, func(c *Config) { c.MaxAttempts = 1 })
	_, err := c.Do(context.Background(), Request{Path: "x", ExpectJSON: true})
	if provider.KindOf(err) != provider.ErrTransient {
		t.Fatalf("expected transient, got %v", err)
	}
}

func TestBodiesFormJSONMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		switch r.URL.Path {
		case "/api/form":
			_ = r.ParseForm()
			if ct != "application/x-www-form-urlencoded" || r.PostForm.Get("a") != "1" {
				t.Errorf("form: %s %v", ct, r.PostForm)
			}
		case "/api/json":
			if ct != "application/json" {
				t.Errorf("json ct: %s", ct)
			}
		case "/api/mp":
			if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("k") != "v" {
				t.Errorf("multipart: %v", err)
			}
			f, hdr, err := r.FormFile("file")
			if err != nil || hdr.Filename != "x.torrent" {
				t.Errorf("file: %v", err)
			} else {
				_ = f.Close()
			}
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := newClient(t, srv, nil)
	ctx := context.Background()
	if _, err := c.Do(ctx, Request{Method: "POST", Path: "form", Form: url.Values{"a": {"1"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(ctx, Request{Method: "POST", Path: "json", JSON: map[string]int{"a": 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(ctx, Request{Method: "POST", Path: "mp", Multipart: &Multipart{
		Fields: map[string]string{"k": "v"}, Files: []MultipartFile{{Field: "file", Filename: "x.torrent", Data: []byte("d8:announce")}},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestLimiterThrottles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	// 10 req/s, burst 1 → 3 requests take >= ~200ms.
	c := newClient(t, srv, func(c *Config) { c.Limiter = rate.NewLimiter(10, 1) })
	start := time.Now()
	for range 3 {
		if _, err := c.Do(context.Background(), Request{Path: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("limiter not applied: %s", d)
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	c := newClient(t, srv, func(c *Config) { c.BaseBackoff = time.Second; c.MaxBackoff = time.Second })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Do(ctx, Request{Path: "x"})
	if err == nil || time.Since(start) > 800*time.Millisecond {
		t.Fatalf("should stop early on ctx: err=%v", err)
	}
}

func TestPerMinuteAndRetryAfterParse(t *testing.T) {
	if PerMinute(0) != nil || PerMinute(300) == nil {
		t.Fatal("PerMinute")
	}
	if parseRetryAfter("2") != 2*time.Second || parseRetryAfter("") != 0 || parseRetryAfter("garbage") != 0 {
		t.Fatal("parseRetryAfter")
	}
}

func TestTransportErrorsRedactQuery(t *testing.T) {
	c, err := New(Config{BaseURL: "http://127.0.0.1:1/api/", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(context.Background(), Request{Path: "x", Query: url.Values{"token": {"SECRET123"}}})
	if err == nil || strings.Contains(err.Error(), "SECRET123") {
		t.Fatalf("query string leaked: %v", err)
	}
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Kind != provider.ErrTransient {
		t.Fatalf("expected transient, got %v", err)
	}
	if pe.Err != nil && strings.Contains(pe.Err.Error(), "SECRET123") {
		t.Fatalf("wrapped cause leaked: %v", pe.Err)
	}
}

func TestHugeRetryAfterIsSurfacedNotSlept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	c := newClient(t, srv, func(c *Config) { c.MaxBackoff = 50 * time.Millisecond })
	start := time.Now()
	_, err := c.Do(context.Background(), Request{Path: "x"})
	if time.Since(start) > time.Second {
		t.Fatal("should not sleep for the server's Retry-After when it exceeds MaxBackoff")
	}
	if provider.KindOf(err) != provider.ErrRateLimited || provider.RetryAfter(err) != time.Hour {
		t.Fatalf("expected rate-limited with RetryAfter hint, got %v", err)
	}
}

func TestCtxCancelDuringBackoffKeepsClassification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	c := newClient(t, srv, func(c *Config) { c.MaxBackoff = 2 * time.Second })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.Do(ctx, Request{Path: "x"})
	if provider.KindOf(err) != provider.ErrRateLimited || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected rate-limited error wrapping ctx cause, got %v", err)
	}
}

func TestBackoffNeverOverflows(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://x/", BaseBackoff: 500 * time.Millisecond, MaxBackoff: 5 * time.Second})
	for attempt := 1; attempt < 80; attempt++ {
		if d := c.backoff(attempt, 0); d <= 0 || d > 5*time.Second {
			t.Fatalf("attempt %d: backoff %s out of range", attempt, d)
		}
	}
}

func TestClassifiedErrorsCarryBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"infringing_file","error_code":35}`))
	}))
	defer srv.Close()
	c := newClient(t, srv, nil)
	_, err := c.Do(context.Background(), Request{Path: "x"})
	var pe *provider.Error
	if !errors.As(err, &pe) || !strings.Contains(string(pe.Body), "infringing_file") {
		t.Fatalf("body not attached: %v", err)
	}
}

func TestExpectJSONAllowsEmpty204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	c := newClient(t, srv, nil)
	if _, err := c.Do(context.Background(), Request{Path: "x", ExpectJSON: true}); err != nil {
		t.Fatalf("204 with ExpectJSON should pass: %v", err)
	}
}
