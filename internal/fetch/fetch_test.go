package fetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// blobServer serves a deterministic blob with optional Range support and
// fault injection.
type blobServer struct {
	data      []byte
	ranges    bool
	etag      string
	mu        sync.Mutex
	requests  []string // Range headers seen (or "" for full GETs)
	failFirst int32    // number of range requests to fail with 500 before succeeding
	truncAt   int64    // if >0, first request for a range crossing this offset gets cut short once
	truncDone bool
	status    int // override status for data requests (0 = normal)
}

func newBlob(n int, ranges bool) *blobServer {
	r := rand.New(rand.NewPCG(1, 2))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.IntN(256))
	}
	return &blobServer{data: b, ranges: ranges, etag: `"v1"`}
}

func (s *blobServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		s.mu.Lock()
		s.requests = append(s.requests, rng)
		st := s.status
		s.mu.Unlock()
		if st != 0 && rng != "bytes=0-0" {
			w.WriteHeader(st)
			return
		}
		w.Header().Set("ETag", s.etag)
		if !s.ranges || rng == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(s.data)))
			w.WriteHeader(200)
			_, _ = w.Write(s.data)
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err != nil {
			w.WriteHeader(416)
			return
		}
		if end >= int64(len(s.data)) {
			end = int64(len(s.data)) - 1
		}
		if rng != "bytes=0-0" && atomic.LoadInt32(&s.failFirst) > 0 {
			atomic.AddInt32(&s.failFirst, -1)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(s.data)))
		w.Header().Set("Accept-Ranges", "bytes")
		chunk := s.data[start : end+1]
		s.mu.Lock()
		trunc := s.truncAt > 0 && !s.truncDone && start < s.truncAt && end >= s.truncAt
		if trunc {
			s.truncDone = true
		}
		s.mu.Unlock()
		if trunc {
			w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
			w.WriteHeader(206)
			_, _ = w.Write(chunk[:s.truncAt-start])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Hijack & close to cut the body short.
			if hj, ok := w.(http.Hijacker); ok {
				c, _, _ := hj.Hijack()
				_ = c.Close()
			}
			return
		}
		w.WriteHeader(206)
		_, _ = w.Write(chunk)
	})
}

func (s *blobServer) rangeRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.requests {
		if r != "" && r != "bytes=0-0" {
			n++
		}
	}
	return n
}

func sum(b []byte) string { h := sha256.Sum256(b); return fmt.Sprintf("%x", h) }

func fileSum(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return sum(b)
}

func TestParallelDownloadIsCorrect(t *testing.T) {
	bs := newBlob(3*MinChunkSize+12345, true)
	srv := httptest.NewServer(bs.handler())
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "sub", "file.bin")
	var calls int32
	res, err := Download(context.Background(), srv.URL, dest, Options{
		Connections: 4, ChunkSize: MinChunkSize, Retries: 2,
		Progress: func(_, _ int64) { atomic.AddInt32(&calls, 1) }, ProgressInterval: 10 * time.Millisecond,
		ExpectedSize: int64(len(bs.data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ranged || res.Resumed || res.Size != int64(len(bs.data)) {
		t.Fatalf("result %+v", res)
	}
	if fileSum(t, dest) != sum(bs.data) {
		t.Fatal("content mismatch")
	}
	if bs.rangeRequests() != 4 { // 3 full chunks + 1 tail
		t.Fatalf("expected 4 range requests, got %d", bs.rangeRequests())
	}
	if atomic.LoadInt32(&calls) == 0 {
		t.Fatal("progress never called")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("part file should be gone")
	}
	if _, err := os.Stat(dest + ".part.json"); !os.IsNotExist(err) {
		t.Fatal("state file should be gone")
	}
}

func TestRetriesChunkOn5xx(t *testing.T) {
	bs := newBlob(2*MinChunkSize, true)
	bs.failFirst = 2
	srv := httptest.NewServer(bs.handler())
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f")
	if _, err := Download(context.Background(), srv.URL, dest, Options{Connections: 2, ChunkSize: MinChunkSize, Retries: 3}); err != nil {
		t.Fatal(err)
	}
	if fileSum(t, dest) != sum(bs.data) {
		t.Fatal("content mismatch")
	}
}

func TestResumeAfterFailure(t *testing.T) {
	bs := newBlob(4*MinChunkSize, true)
	srv := httptest.NewServer(bs.handler())
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f")

	// First run: chunk 2 keeps failing → whole download fails, state persists.
	failing := &blobServer{data: bs.data, ranges: true, etag: bs.etag}
	failing.status = 0
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Range"), fmt.Sprintf("bytes=%d-", 2*MinChunkSize)) {
			w.WriteHeader(500)
			return
		}
		failing.handler().ServeHTTP(w, r)
	}))
	defer srv2.Close()
	_, err := Download(context.Background(), srv2.URL, dest, Options{Connections: 1, ChunkSize: MinChunkSize, Retries: 0})
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != 500 {
		t.Fatalf("expected http 500 error, got %v", err)
	}
	if _, err := os.Stat(dest + ".part.json"); err != nil {
		t.Fatal("state file should persist after failure")
	}

	// Second run against a healthy server (different URL, same content): only the missing chunks are fetched.
	res, err := Download(context.Background(), srv.URL, dest, Options{Connections: 2, ChunkSize: MinChunkSize, Retries: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resumed {
		t.Fatal("expected resume")
	}
	if got := bs.rangeRequests(); got != 2 { // chunks 2 and 3 (chunks 0,1 were done; 1 was done because connections=1 sequential)
		t.Fatalf("expected 2 range requests on resume, got %d", got)
	}
	if fileSum(t, dest) != sum(bs.data) {
		t.Fatal("content mismatch after resume")
	}
}

func TestStateIgnoredWhenETagChanges(t *testing.T) {
	bs := newBlob(2*MinChunkSize, true)
	srv := httptest.NewServer(bs.handler())
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f")
	// Fabricate a "complete" state for a different version of the file.
	st := state{URL: "x", Size: int64(len(bs.data)), ETag: `"old"`, ChunkSz: MinChunkSize, Done: []bool{true, true}, Ranged: true}
	if err := writeAtomic(dest+".part.json", &st); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".part", bytes.Repeat([]byte{0}, len(bs.data)), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Download(context.Background(), srv.URL, dest, Options{Connections: 2, ChunkSize: MinChunkSize})
	if err != nil || res.Resumed {
		t.Fatalf("stale state must be discarded: %v %+v", err, res)
	}
	if fileSum(t, dest) != sum(bs.data) {
		t.Fatal("content mismatch")
	}
}

func TestNoRangeSupportStreams(t *testing.T) {
	bs := newBlob(100_000, false)
	srv := httptest.NewServer(bs.handler())
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f")
	res, err := Download(context.Background(), srv.URL, dest, Options{Connections: 4})
	if err != nil || res.Ranged {
		t.Fatalf("%v %+v", err, res)
	}
	if fileSum(t, dest) != sum(bs.data) {
		t.Fatal("content mismatch")
	}
}

func TestSizeMismatchIsRejected(t *testing.T) {
	bs := newBlob(1000, true)
	srv := httptest.NewServer(bs.handler())
	defer srv.Close()
	_, err := Download(context.Background(), srv.URL, filepath.Join(t.TempDir(), "f"), Options{ExpectedSize: 999})
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("expected size mismatch, got %v", err)
	}
}

func TestExpiredLinkSurfacesHTTPError(t *testing.T) {
	bs := newBlob(1000, true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }))
	defer srv.Close()
	_ = bs
	_, err := Download(context.Background(), srv.URL, filepath.Join(t.TempDir(), "f"), Options{})
	var he *HTTPError
	if !errors.As(err, &he) || !he.LinkExpired() || he.Retryable() {
		t.Fatalf("expected expired-link HTTPError, got %v", err)
	}
}

func TestLimiterCapsThroughput(t *testing.T) {
	bs := newBlob(200_000, true)
	srv := httptest.NewServer(bs.handler())
	defer srv.Close()
	lim := rate.NewLimiter(rate.Limit(400_000), 64<<10) // 400 KB/s → ≥ ~0.35s for 200 KB after burst
	start := time.Now()
	if _, err := Download(context.Background(), srv.URL, filepath.Join(t.TempDir(), "f"), Options{Connections: 2, ChunkSize: 50_000, Limiter: lim}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 250*time.Millisecond {
		t.Fatalf("limiter not applied: %s", d)
	}
}

func TestCancelStopsPromptlyAndKeepsState(t *testing.T) {
	bs := newBlob(8*MinChunkSize, true)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-0" {
			time.Sleep(50 * time.Millisecond)
		}
		bs.handler().ServeHTTP(w, r)
	}))
	defer slow.Close()
	dest := filepath.Join(t.TempDir(), "f")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	_, err := Download(ctx, slow.URL, dest, Options{Connections: 2, ChunkSize: MinChunkSize})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancel, got %v", err)
	}
	if _, err := os.Stat(dest + ".part.json"); err != nil {
		t.Fatal("state should be kept on cancel for later resume")
	}
	Cleanup(dest)
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("Cleanup should remove part file")
	}
}

func TestShortChunkIsRetried(t *testing.T) {
	bs := newBlob(2*MinChunkSize, true)
	bs.truncAt = MinChunkSize / 2
	srv := httptest.NewServer(bs.handler())
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f")
	if _, err := Download(context.Background(), srv.URL, dest, Options{Connections: 1, ChunkSize: MinChunkSize, Retries: 2}); err != nil {
		t.Fatal(err)
	}
	if fileSum(t, dest) != sum(bs.data) {
		t.Fatal("content mismatch after truncated chunk retry")
	}
}

func TestAutoChunkSize(t *testing.T) {
	if autoChunkSize(1<<20, 8) != MinChunkSize {
		t.Fatal("small files clamp to min")
	}
	if autoChunkSize(100<<30, 8) != MaxChunkSize {
		t.Fatal("huge files clamp to max")
	}
	if numChunks(0, MinChunkSize) != 0 || numChunks(1, MinChunkSize) != 1 || numChunks(MinChunkSize+1, MinChunkSize) != 2 {
		t.Fatal("numChunks")
	}
	if parseContentRangeTotal("bytes 0-0/123") != 123 || parseContentRangeTotal("bytes */5") != 5 || parseContentRangeTotal("garbage") != -1 {
		t.Fatal("parseContentRangeTotal")
	}
	_ = io.Discard
}
