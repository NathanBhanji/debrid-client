// Package fetch downloads a single URL to disk using parallel HTTP range
// requests, with resume across process restarts, per-chunk retries, bandwidth
// limiting, size verification and progress reporting.
//
// Layout on disk while in flight:
//
//	<dest>.part        preallocated data file written at offsets
//	<dest>.part.json   chunk map (which byte ranges are complete)
//
// On success <dest>.part is renamed to <dest> and the state file removed.
package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

// Options configure a download.
type Options struct {
	// Connections is the number of parallel range requests (>=1). Ignored when
	// the server does not support ranges.
	Connections int
	// ChunkSize is the size of each range request. 0 picks a size based on the
	// file size and Connections (between MinChunkSize and MaxChunkSize).
	ChunkSize int64
	// Retries is the number of times a failed chunk is retried before the whole
	// download fails.
	Retries int
	// Limiter caps throughput in bytes/sec across this download (nil = unlimited).
	// Share one limiter between downloads for a global cap.
	Limiter *rate.Limiter
	// ExpectedSize, when > 0, must match the server-reported size.
	ExpectedSize int64
	// Progress is called with bytes done and total (total may be -1 if unknown).
	// It is called at most every ProgressInterval and once at the end.
	Progress         func(done, total int64)
	ProgressInterval time.Duration
	// Client is the HTTP client to use (nil = http.DefaultClient-like with no timeout).
	Client    *http.Client
	UserAgent string
	Header    http.Header
	// RequestTimeout bounds each individual range request (0 = none).
	RequestTimeout time.Duration
}

const (
	// MinChunkSize is the smallest automatic chunk.
	MinChunkSize = 4 << 20
	// MaxChunkSize is the largest automatic chunk.
	MaxChunkSize = 64 << 20
	readBuf      = 256 << 10
)

// Result describes a completed download.
type Result struct {
	Size    int64
	Resumed bool
	// Ranged reports whether parallel range requests were used.
	Ranged bool
}

// ErrSizeMismatch is returned when the server's size differs from ExpectedSize,
// or the final file size differs from what the server announced.
var ErrSizeMismatch = errors.New("fetch: size mismatch")

// HTTPError is returned for non-success status codes. Callers typically treat
// 401/403/404/410 as "link expired, unrestrict again" and 5xx/429 as transient.
type HTTPError struct {
	StatusCode int
	URL        string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("fetch: http %d from %s", e.StatusCode, e.URL) }

// Retryable reports whether the status is worth retrying as-is.
func (e *HTTPError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500 || e.StatusCode == http.StatusRequestTimeout
}

// LinkExpired reports whether the status suggests the URL itself is no longer valid.
func (e *HTTPError) LinkExpired() bool {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return true
	}
	return false
}

type state struct {
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	ETag    string `json:"etag,omitempty"`
	ChunkSz int64  `json:"chunk_size"`
	Done    []bool `json:"done"` // per chunk
	Ranged  bool   `json:"ranged"`
	Created string `json:"created"`
}

type probe struct {
	size   int64 // -1 if unknown
	ranges bool
	etag   string
}

// Download fetches url into dest. It is safe to call again after a failure or
// crash: completed chunks are reused when the state file matches.
func Download(ctx context.Context, url, dest string, o Options) (Result, error) {
	o = o.withDefaults()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return Result{}, err
	}
	part := dest + ".part"
	stateFile := part + ".json"

	pr, err := o.probe(ctx, url)
	if err != nil {
		return Result{}, err
	}
	if o.ExpectedSize > 0 && pr.size >= 0 && pr.size != o.ExpectedSize {
		return Result{}, fmt.Errorf("%w: server reports %d bytes, expected %d", ErrSizeMismatch, pr.size, o.ExpectedSize)
	}

	// Unknown size or no range support → single stream.
	if pr.size < 0 || !pr.ranges || o.Connections == 1 && pr.size < MinChunkSize {
		n, err := o.stream(ctx, url, part, pr.size)
		if err != nil {
			return Result{}, err
		}
		_ = os.Remove(stateFile)
		if err := os.Rename(part, dest); err != nil {
			return Result{}, err
		}
		return Result{Size: n, Ranged: false}, nil
	}

	st, resumed := loadState(stateFile, url, pr, o)
	f, err := os.OpenFile(part, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(st.Size); err != nil {
		return Result{}, err
	}

	var done atomic.Int64
	for i, d := range st.Done {
		if d {
			done.Add(chunkLen(st, i))
		}
	}
	if err := o.downloadChunks(ctx, url, f, st, stateFile, &done); err != nil {
		return Result{}, err
	}
	if err := f.Sync(); err != nil {
		return Result{}, err
	}
	if fi, err := f.Stat(); err != nil || fi.Size() != st.Size {
		return Result{}, fmt.Errorf("%w: wrote %d bytes, expected %d", ErrSizeMismatch, fi.Size(), st.Size)
	}
	if err := f.Close(); err != nil {
		return Result{}, err
	}
	_ = os.Remove(stateFile)
	if err := os.Rename(part, dest); err != nil {
		return Result{}, err
	}
	if o.Progress != nil {
		o.Progress(st.Size, st.Size)
	}
	return Result{Size: st.Size, Resumed: resumed, Ranged: true}, nil
}

func (o Options) withDefaults() Options {
	if o.Connections < 1 {
		o.Connections = 1
	}
	if o.Retries < 0 {
		o.Retries = 0
	}
	if o.Client == nil {
		o.Client = &http.Client{}
	}
	if o.UserAgent == "" {
		o.UserAgent = "debrid-client"
	}
	if o.ProgressInterval <= 0 {
		o.ProgressInterval = time.Second
	}
	return o
}

func (o Options) newRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", o.UserAgent)
	for k, vs := range o.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return req, nil
}

// probe issues a tiny range request to learn size and range support without
// relying on HEAD (which some CDNs reject).
func (o Options) probe(ctx context.Context, url string) (probe, error) {
	req, err := o.newRequest(ctx, url)
	if err != nil {
		return probe{}, err
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := o.Client.Do(req)
	if err != nil {
		return probe{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	switch resp.StatusCode {
	case http.StatusPartialContent:
		total := parseContentRangeTotal(resp.Header.Get("Content-Range"))
		return probe{size: total, ranges: total >= 0, etag: resp.Header.Get("ETag")}, nil
	case http.StatusOK:
		size := int64(-1)
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
				size = n
			}
		}
		return probe{size: size, ranges: false, etag: resp.Header.Get("ETag")}, nil
	case http.StatusRequestedRangeNotSatisfiable:
		// Empty file or weird server; fall back to streaming.
		return probe{size: -1, ranges: false}, nil
	}
	return probe{}, &HTTPError{StatusCode: resp.StatusCode, URL: url}
}

func parseContentRangeTotal(v string) int64 {
	// "bytes 0-0/12345" or "bytes */12345"
	i := strings.LastIndex(v, "/")
	if i < 0 {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v[i+1:]), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// stream downloads with a single connection (no resume).
func (o Options) stream(ctx context.Context, url, part string, size int64) (int64, error) {
	var lastErr error
	for attempt := 0; attempt <= o.Retries; attempt++ {
		n, err := o.streamOnce(ctx, url, part, size)
		if err == nil {
			return n, nil
		}
		lastErr = err
		if !retryable(err) || ctx.Err() != nil {
			break
		}
		sleep(ctx, backoff(attempt))
	}
	return 0, lastErr
}

func (o Options) streamOnce(ctx context.Context, url, part string, size int64) (int64, error) {
	req, err := o.newRequest(ctx, url)
	if err != nil {
		return 0, err
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, &HTTPError{StatusCode: resp.StatusCode, URL: url}
	}
	f, err := os.Create(part)
	if err != nil {
		return 0, err
	}
	var done atomic.Int64
	stop := o.progressLoop(ctx, &done, size)
	n, err := io.Copy(f, o.limited(ctx, &done, resp.Body))
	stop()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	if size >= 0 && n != size {
		return 0, fmt.Errorf("%w: got %d bytes, expected %d", ErrSizeMismatch, n, size)
	}
	if o.Progress != nil {
		o.Progress(n, n)
	}
	return n, nil
}

func loadState(stateFile, url string, pr probe, o Options) (*state, bool) {
	chunkSz := o.ChunkSize
	if chunkSz <= 0 {
		chunkSz = autoChunkSize(pr.size, o.Connections)
	}
	if b, err := os.ReadFile(stateFile); err == nil {
		var st state
		if json.Unmarshal(b, &st) == nil && st.Size == pr.size && st.Ranged && st.ChunkSz > 0 &&
			len(st.Done) == numChunks(st.Size, st.ChunkSz) && (st.ETag == "" || pr.etag == "" || st.ETag == pr.etag) {
			st.URL = url // URLs change between unrestricts; the content is what matters
			return &st, true
		}
	}
	n := numChunks(pr.size, chunkSz)
	return &state{URL: url, Size: pr.size, ETag: pr.etag, ChunkSz: chunkSz, Done: make([]bool, n), Ranged: true,
		Created: time.Now().UTC().Format(time.RFC3339)}, false
}

func autoChunkSize(size int64, conns int) int64 {
	// Aim for ~4 chunks per connection so stragglers balance out.
	c := size / int64(conns*4)
	if c < MinChunkSize {
		c = MinChunkSize
	}
	if c > MaxChunkSize {
		c = MaxChunkSize
	}
	return c
}

func numChunks(size, chunkSz int64) int {
	if size == 0 {
		return 0
	}
	return int((size + chunkSz - 1) / chunkSz)
}

func chunkLen(st *state, i int) int64 {
	start := int64(i) * st.ChunkSz
	end := start + st.ChunkSz
	if end > st.Size {
		end = st.Size
	}
	return end - start
}

func (o Options) downloadChunks(ctx context.Context, url string, f *os.File, st *state, stateFile string, done *atomic.Int64) error {
	var saveMu sync.Mutex
	save := func() error {
		saveMu.Lock()
		defer saveMu.Unlock()
		return writeAtomic(stateFile, st)
	}
	if err := save(); err != nil {
		return err
	}

	pending := make(chan int, len(st.Done))
	for i, d := range st.Done {
		if !d {
			pending <- i
		}
	}
	close(pending)

	stop := o.progressLoop(ctx, done, st.Size)
	defer stop()

	g, gctx := errgroup.WithContext(ctx)
	for range o.Connections {
		g.Go(func() error {
			for i := range pending {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				if err := o.fetchChunkRetry(gctx, url, f, st, i, done); err != nil {
					return err
				}
				saveMu.Lock()
				st.Done[i] = true
				saveMu.Unlock()
				if err := save(); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return g.Wait()
}

func (o Options) fetchChunkRetry(ctx context.Context, url string, f *os.File, st *state, i int, done *atomic.Int64) error {
	var lastErr error
	for attempt := 0; attempt <= o.Retries; attempt++ {
		err := o.fetchChunk(ctx, url, f, st, i, done)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable(err) || ctx.Err() != nil {
			break
		}
		sleep(ctx, backoff(attempt))
	}
	return fmt.Errorf("chunk %d: %w", i, lastErr)
}

func (o Options) fetchChunk(ctx context.Context, url string, f *os.File, st *state, i int, done *atomic.Int64) error {
	start := int64(i) * st.ChunkSz
	end := start + chunkLen(st, i) - 1
	if o.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.RequestTimeout)
		defer cancel()
	}
	req, err := o.newRequest(ctx, url)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := o.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusPartialContent {
		return &HTTPError{StatusCode: resp.StatusCode, URL: url}
	}
	want := end - start + 1
	w := io.NewOffsetWriter(f, start)
	n, err := io.Copy(w, o.limited(ctx, done, io.LimitReader(resp.Body, want)))
	if err != nil {
		done.Add(-n) // undo partial progress; the chunk will be refetched whole
		return err
	}
	if n != want {
		done.Add(-n)
		return fmt.Errorf("short chunk: got %d of %d bytes", n, want)
	}
	return nil
}

// limited wraps r with the rate limiter and progress counter.
func (o Options) limited(ctx context.Context, done *atomic.Int64, r io.Reader) io.Reader {
	return &countingReader{ctx: ctx, r: r, lim: o.Limiter, done: done}
}

type countingReader struct {
	ctx  context.Context
	r    io.Reader
	lim  *rate.Limiter
	done *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if len(p) > readBuf {
		p = p[:readBuf]
	}
	if c.lim != nil {
		// Never request more than the burst in one go or WaitN errors out.
		if b := c.lim.Burst(); len(p) > b {
			p = p[:b]
		}
		if err := c.lim.WaitN(c.ctx, len(p)); err != nil {
			return 0, err
		}
	}
	n, err := c.r.Read(p)
	c.done.Add(int64(n))
	return n, err
}

func (o Options) progressLoop(ctx context.Context, done *atomic.Int64, total int64) (stop func()) {
	if o.Progress == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(o.ProgressInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				o.Progress(done.Load(), total)
			}
		}
	}()
	return func() { cancel(); wg.Wait() }
}

func writeAtomic(path string, st *state) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func retryable(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Retryable()
	}
	if errors.Is(err, ErrSizeMismatch) || errors.Is(err, context.Canceled) {
		return false
	}
	return true // network errors, short reads, timeouts
}

func backoff(attempt int) time.Duration {
	d := 500 * time.Millisecond << attempt
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	return d/2 + time.Duration(rand.Int64N(int64(d/2))) //nolint:gosec // jitter
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// Cleanup removes any partial files for dest.
func Cleanup(dest string) {
	_ = os.Remove(dest + ".part")
	_ = os.Remove(dest + ".part.json")
	_ = os.Remove(dest + ".part.json.tmp")
}
