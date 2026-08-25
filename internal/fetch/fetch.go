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
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
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

// ErrRangeNotSupported is returned internally when a server that advertised
// range support answers a chunk request with 200; Download falls back to a
// single stream.
var ErrRangeNotSupported = errors.New("fetch: server ignored range request")

// ErrBadContentRange is returned when a 206 response's Content-Range does not
// match the requested range (would corrupt the file).
var ErrBadContentRange = errors.New("fetch: content-range mismatch")

// HTTPError is returned for non-success status codes. Callers typically treat
// 401/403/404/410 as "link expired, unrestrict again" and 5xx/429 as transient.
type HTTPError struct {
	StatusCode int
	URL        string
	RetryAfter time.Duration // from a 429/503 Retry-After header, if any
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
	if pr.size < 0 || !pr.ranges {
		return o.streamToDest(ctx, url, part, stateFile, dest, pr.size)
	}

	st, resumed := loadState(stateFile, part, url, pr, o)
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
		if errors.Is(err, ErrRangeNotSupported) {
			_ = f.Close()
			return o.streamToDest(ctx, url, part, stateFile, dest, pr.size)
		}
		return Result{}, err
	}
	if err := f.Sync(); err != nil {
		return Result{}, err
	}
	fi, err := f.Stat()
	if err != nil {
		return Result{}, err
	}
	if fi.Size() != st.Size {
		return Result{}, fmt.Errorf("%w: wrote %d bytes, expected %d", ErrSizeMismatch, fi.Size(), st.Size)
	}
	if err := f.Close(); err != nil {
		return Result{}, err
	}
	if err := os.Rename(part, dest); err != nil {
		return Result{}, err // keep state: the complete .part is reusable
	}
	_ = os.Remove(stateFile)
	if o.Progress != nil {
		o.Progress(st.Size, st.Size)
	}
	return Result{Size: st.Size, Resumed: resumed, Ranged: true}, nil
}

// streamToDest downloads with a single connection into part and renames it
// into place. Any chunk map from a previous ranged attempt is discarded first:
// the stream overwrites .part, so the map would describe bytes that no longer
// exist.
func (o Options) streamToDest(ctx context.Context, url, part, stateFile, dest string, size int64) (Result, error) {
	_ = os.Remove(stateFile)
	_ = os.Remove(stateFile + ".tmp")
	n, err := o.stream(ctx, url, part, size)
	if err != nil {
		return Result{}, err
	}
	if err := os.Rename(part, dest); err != nil {
		return Result{}, err
	}
	return Result{Size: n, Ranged: false}, nil
}

func (o Options) withDefaults() Options {
	if o.Connections < 1 {
		o.Connections = 1
	}
	if o.Retries < 0 {
		o.Retries = 0
	}
	if o.Client == nil {
		o.Client = defaultClient(o.Connections)
	}
	if o.UserAgent == "" {
		o.UserAgent = "debrid-client"
	}
	if o.ProgressInterval <= 0 {
		o.ProgressInterval = time.Second
	}
	return o
}

// defaultClient keeps enough idle connections for parallel chunks and forces
// HTTP/1.1: HTTP/2 multiplexes every range request onto one TCP connection,
// which defeats the point of parallelism against CDNs.
func defaultClient(conns int) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = max(conns, 2)
	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	return &http.Client{Transport: tr}
}

// normalizeURL re-escapes illegal raw characters (spaces, brackets, …) in a
// provider download URL's query so the request line is well-formed. Providers
// like TorBox hand back links with an unencoded filename= parameter; sent
// verbatim, a raw space truncates the request target and the CDN answers 400.
// Order and existing %XX escapes are preserved so any signed URL survives.
func normalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.RawQuery != "" {
		u.RawQuery = escapeRawQuery(u.RawQuery)
	}
	return u.String()
}

// escapeRawQuery percent-encodes bytes outside the RFC 3986 query set,
// leaving structural &/= and already-encoded %XX sequences untouched.
func escapeRawQuery(q string) string {
	const upperhex = "0123456789ABCDEF"
	allowed := func(c byte) bool {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			return true
		}
		return strings.IndexByte("-._~!$&'()*+,;=:@/?", c) >= 0
	}
	isHex := func(c byte) bool {
		return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') || (c >= 'a' && c <= 'f')
	}
	var b strings.Builder
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '%' && i+2 < len(q) && isHex(q[i+1]) && isHex(q[i+2]):
			b.WriteByte(c) // keep an existing escape as-is
		case allowed(c):
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&0x0f])
		}
	}
	return b.String()
}

func (o Options) newRequest(ctx context.Context, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizeURL(rawURL), nil)
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
// relying on HEAD (which some CDNs reject). Transient failures are retried.
func (o Options) probe(ctx context.Context, url string) (probe, error) {
	var lastErr error
	for attempt := 0; attempt <= o.Retries; attempt++ {
		pr, err := o.probeOnce(ctx, url)
		if err == nil {
			return pr, nil
		}
		lastErr = err
		if !retryable(err) || ctx.Err() != nil {
			break
		}
		sleep(ctx, retryDelay(attempt, err))
	}
	return probe{}, lastErr
}

func (o Options) probeOnce(ctx context.Context, url string) (probe, error) {
	if o.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.RequestTimeout)
		defer cancel()
	}
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
		sleep(ctx, retryDelay(attempt, err))
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
	n, err := copyBuf(f, o.limited(ctx, &done, o.stallGuard(ctx, resp.Body)))
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
	if size < 0 && o.ExpectedSize > 0 && n != o.ExpectedSize {
		return 0, fmt.Errorf("%w: got %d bytes, expected %d", ErrSizeMismatch, n, o.ExpectedSize)
	}
	if o.Progress != nil {
		o.Progress(n, n)
	}
	return n, nil
}

// loadState returns the resumable chunk map when the state file matches the
// probed content (size and ETag) AND the data file is present with the full
// preallocated size; otherwise a fresh map. The .part size check guards
// against a map that outlived its data (manual cleanup, a stream attempt
// overwriting .part, …), which would otherwise yield zero-filled chunks.
func loadState(stateFile, part, url string, pr probe, o Options) (*state, bool) {
	chunkSz := o.ChunkSize
	if chunkSz <= 0 {
		chunkSz = autoChunkSize(pr.size, o.Connections)
	}
	if b, err := os.ReadFile(stateFile); err == nil {
		var st state
		fi, statErr := os.Stat(part)
		if json.Unmarshal(b, &st) == nil && st.Size == pr.size && st.Ranged && st.ChunkSz > 0 &&
			len(st.Done) == numChunks(st.Size, st.ChunkSz) && (st.ETag == "" || pr.etag == "" || st.ETag == pr.etag) &&
			statErr == nil && fi.Size() == st.Size {
			st.URL = url // URLs change between unrestricts; the content is what matters
			return &st, true
		}
	}
	_ = os.Remove(stateFile)
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
				// Make the chunk durable before the map says it's done, so a power
				// loss can't leave a "done" chunk of zeros.
				if err := f.Sync(); err != nil {
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
		sleep(ctx, retryDelay(attempt, err))
	}
	return fmt.Errorf("chunk %d: %w", i, lastErr)
}

func (o Options) fetchChunk(ctx context.Context, url string, f *os.File, st *state, i int, done *atomic.Int64) error {
	start := int64(i) * st.ChunkSz
	end := start + chunkLen(st, i) - 1
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		return ErrRangeNotSupported
	default:
		he := &HTTPError{StatusCode: resp.StatusCode, URL: url}
		if resp.StatusCode == http.StatusTooManyRequests {
			he.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		}
		return he
	}
	if cs, ce, total, ok := parseContentRange(resp.Header.Get("Content-Range")); !ok || cs != start || ce != end || (total >= 0 && total != st.Size) {
		return fmt.Errorf("%w: requested %d-%d/%d, got %q", ErrBadContentRange, start, end, st.Size, resp.Header.Get("Content-Range"))
	}
	want := end - start + 1
	w := io.NewOffsetWriter(f, start)
	cr := o.limited(ctx, done, o.stallGuard(ctx, io.LimitReader(resp.Body, want)))
	n, err := copyBuf(w, cr)
	if err != nil {
		done.Add(-cr.read()) // undo everything counted for this attempt; the chunk is refetched whole
		return err
	}
	if n != want {
		done.Add(-cr.read())
		return fmt.Errorf("short chunk: got %d of %d bytes", n, want)
	}
	return nil
}

// stallGuard cancels the request if no bytes arrive for RequestTimeout
// (an idle timeout rather than a whole-transfer timeout, so a bandwidth cap
// or a big chunk can't trip it).
func (o Options) stallGuard(ctx context.Context, r io.Reader) io.Reader {
	if o.RequestTimeout <= 0 {
		return r
	}
	return &stallReader{r: r, timeout: o.RequestTimeout, ctx: ctx}
}

type stallReader struct {
	r       io.Reader
	timeout time.Duration
	ctx     context.Context
}

func (s *stallReader) Read(p []byte) (int, error) {
	type res struct {
		n   int
		err error
	}
	ch := make(chan res, 1)
	go func() { n, err := s.r.Read(p); ch <- res{n, err} }()
	t := time.NewTimer(s.timeout)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.n, r.err
	case <-t.C:
		return 0, fmt.Errorf("fetch: no data for %s (stalled)", s.timeout)
	case <-s.ctx.Done():
		return 0, s.ctx.Err()
	}
}

// parseContentRange parses "bytes start-end/total" (total may be "*" → -1).
func parseContentRange(v string) (start, end, total int64, ok bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "bytes ") {
		return 0, 0, 0, false
	}
	rangePart, totalPart, found := strings.Cut(v[len("bytes "):], "/")
	if !found {
		return 0, 0, 0, false
	}
	se := strings.SplitN(rangePart, "-", 2)
	if len(se) != 2 {
		return 0, 0, 0, false
	}
	var err error
	if start, err = strconv.ParseInt(strings.TrimSpace(se[0]), 10, 64); err != nil {
		return 0, 0, 0, false
	}
	if end, err = strconv.ParseInt(strings.TrimSpace(se[1]), 10, 64); err != nil {
		return 0, 0, 0, false
	}
	total = -1
	if tp := strings.TrimSpace(totalPart); tp != "*" {
		if total, err = strconv.ParseInt(tp, 10, 64); err != nil {
			return 0, 0, 0, false
		}
	}
	return start, end, total, true
}

// copyBuf copies with a 256 KiB buffer (io.Copy's 32 KiB means 8× more pwrite syscalls).
func copyBuf(dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, readBuf)
	return io.CopyBuffer(dst, struct{ io.Reader }{src}, buf) // hide WriterTo/ReaderFrom so the buffer is used
}

// limited wraps r with the rate limiter and progress counter.
func (o Options) limited(ctx context.Context, done *atomic.Int64, r io.Reader) *countingReader {
	lim := o.Limiter
	// rate.Inf or a non-positive burst means "unlimited" — a zero burst would
	// otherwise shrink every read to 0 bytes and spin forever.
	if lim != nil && (lim.Limit() == rate.Inf || lim.Burst() <= 0) {
		lim = nil
	}
	return &countingReader{ctx: ctx, r: r, lim: lim, done: done}
}

type countingReader struct {
	ctx  context.Context
	r    io.Reader
	lim  *rate.Limiter
	done *atomic.Int64
	n    int64 // bytes counted by this reader (to undo on failure)
}

func (c *countingReader) read() int64 { return c.n }

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
	c.n += int64(n)
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
	if errors.Is(err, ErrSizeMismatch) || errors.Is(err, ErrBadContentRange) || errors.Is(err, ErrRangeNotSupported) || errors.Is(err, context.Canceled) {
		return false
	}
	// Local I/O failures (disk full, permissions, read-only fs) won't fix themselves.
	var pe *os.PathError
	if errors.As(err, &pe) {
		return false
	}
	return true // network errors, short reads, stalls, timeouts
}

// retryDelay is the wait before retry number attempt (0-based): the server's
// Retry-After when present, else 0.5s·2^attempt capped at 15s with jitter.
func retryDelay(attempt int, err error) time.Duration {
	var he *HTTPError
	if errors.As(err, &he) && he.RetryAfter > 0 {
		return min(he.RetryAfter, 60*time.Second)
	}
	d := 15 * time.Second
	if attempt < 5 {
		d = 500 * time.Millisecond << uint(attempt) //nolint:gosec // attempt < 5
	}
	return d/2 + time.Duration(rand.Int64N(int64(d/2))) //nolint:gosec // jitter
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
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
