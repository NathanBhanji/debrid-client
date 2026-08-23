// Package httpx is the shared HTTP client used by provider implementations:
// rate limiting, retries with backoff honouring Retry-After, response guards
// and small JSON/form helpers. It classifies transport-level failures into
// provider.Error; providers map their own API error bodies on top.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/NathanBhanji/debrid-client/internal/provider"
)

// Config configures a Client.
type Config struct {
	BaseURL   string
	UserAgent string
	// Auth mutates each request to add credentials (e.g. Bearer header).
	Auth func(*http.Request)
	// Rate limits requests per account. Nil means unlimited.
	Limiter *rate.Limiter
	// MaxAttempts is the total number of tries for retryable failures (>=1).
	MaxAttempts int
	// BaseBackoff is the initial backoff; doubled per attempt with jitter, capped at MaxBackoff.
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	// Timeout per attempt. Zero means no per-attempt timeout beyond ctx.
	Timeout time.Duration
	// MaxBodyBytes caps how much of a response body we read (guards against huge error pages).
	MaxBodyBytes int64
	// HTTPClient overrides the underlying client (tests).
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// Client is a rate-limited, retrying HTTP client.
type Client struct {
	cfg  Config
	base *url.URL
	http *http.Client
	log  *slog.Logger
}

// New builds a Client, applying defaults.
func New(cfg Config) (*Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("httpx: invalid base url %q", cfg.BaseURL)
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 3
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 500 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 16 << 20
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "debrid-client"
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 0}
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Client{cfg: cfg, base: base, http: hc, log: log}, nil
}

// Response is a fully-read HTTP response.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// JSON decodes the body into v.
func (r *Response) JSON(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return &provider.Error{Kind: provider.ErrTransient, HTTPStatus: r.StatusCode,
			Message: fmt.Sprintf("decode json: %v (body %q)", err, snippet(r.Body)), Err: err}
	}
	return nil
}

// Request describes one API call.
type Request struct {
	Method string
	// Path is relative to BaseURL (no leading slash needed). May include a query string.
	Path  string
	Query url.Values
	// Exactly one of Form, JSON, Multipart, Body may be set.
	Form      url.Values
	JSON      any
	Multipart *Multipart
	Body      []byte
	// ContentType for Body.
	ContentType string
	Header      http.Header
	// ExpectJSON, when true, treats a non-JSON content type on a 2xx as a transient
	// error (e.g. a CDN/maintenance HTML page returned with 200).
	ExpectJSON bool
	// NoRetry disables retries for non-idempotent calls where a timeout may still
	// have succeeded server-side (e.g. "add torrent").
	NoRetry bool
	// NoAuth skips the Auth hook.
	NoAuth bool
}

// Multipart describes a multipart/form-data body.
type Multipart struct {
	Fields map[string]string
	Files  []MultipartFile
}

// MultipartFile is one file part.
type MultipartFile struct {
	Field, Filename string
	Data            []byte
}

// Do performs the request with rate limiting and retries. Non-2xx responses
// are returned as-is (with err == nil) for the caller to classify, EXCEPT
// 401/403 (→ ErrAuth), 404 (→ ErrNotFound), 429 (→ ErrRateLimited, retried) and
// 5xx (→ ErrTransient, retried). Callers that need the raw body of those can
// inspect the returned *provider.Error's fields.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		if c.cfg.Limiter != nil {
			if err := c.cfg.Limiter.Wait(ctx); err != nil {
				return nil, provider.Wrap(provider.ErrTransient, err)
			}
		}
		resp, err := c.once(ctx, r)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if r.NoRetry || !provider.IsRetryable(err) || attempt == c.cfg.MaxAttempts {
			break
		}
		if ra := provider.RetryAfter(err); ra > c.cfg.MaxBackoff {
			// The server asked us to wait longer than we're willing to block a
			// caller; surface the error (with RetryAfter) so the engine reschedules.
			break
		}
		wait := c.backoff(attempt, provider.RetryAfter(err))
		c.log.Debug("httpx: retrying", "method", r.Method, "path", r.Path, "attempt", attempt, "wait", wait, "err", err)
		select {
		case <-ctx.Done():
			// Keep the classified error (kind/RetryAfter) and attach the ctx cause.
			var pe *provider.Error
			if errors.As(lastErr, &pe) {
				cp := *pe
				cp.Err = errors.Join(pe.Err, ctx.Err())
				return nil, &cp
			}
			return nil, provider.Wrap(provider.ErrTransient, errors.Join(lastErr, ctx.Err()))
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

// backoff returns the wait before the next attempt: the server's Retry-After
// when given, else exponential (base·2^(attempt-1)) capped at MaxBackoff with
// full jitter.
func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, c.cfg.MaxBackoff)
	}
	d := c.cfg.MaxBackoff
	if shift := attempt - 1; shift < 30 {
		if v := c.cfg.BaseBackoff << uint(shift); v < d { //nolint:gosec // shift < 30 guarded
			d = v
		}
	}
	return time.Duration(rand.Int64N(int64(d)) + 1) //nolint:gosec // jitter, not crypto
}

func (c *Client) once(ctx context.Context, r Request) (*Response, error) {
	if c.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}
	req, err := c.build(ctx, r)
	if err != nil {
		return nil, provider.Wrap(provider.ErrPermanent, err)
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, classifyTransport(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxBodyBytes))
	if err != nil {
		return nil, classifyTransport(err)
	}
	c.log.Debug("httpx: response", "method", r.Method, "path", r.Path, "status", resp.StatusCode, "dur", time.Since(start))
	out := &Response{StatusCode: resp.StatusCode, Header: resp.Header, Body: body}

	// Pre-classify the statuses the retry loop cares about. The full body is
	// attached (Error.Body) so providers can refine the classification from
	// their own error envelope (e.g. Real-Debrid uses 403 for non-auth errors).
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &provider.Error{Kind: provider.ErrRateLimited, HTTPStatus: 429, Body: body,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")), Message: snippet(body)}
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, &provider.Error{Kind: provider.ErrAuth, HTTPStatus: resp.StatusCode, Body: body, Message: snippet(body)}
	case resp.StatusCode == http.StatusNotFound:
		return nil, &provider.Error{Kind: provider.ErrNotFound, HTTPStatus: 404, Body: body, Message: snippet(body)}
	case resp.StatusCode >= 500:
		return nil, &provider.Error{Kind: provider.ErrTransient, HTTPStatus: resp.StatusCode, Body: body,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")), Message: snippet(body)}
	}
	if r.ExpectJSON && resp.StatusCode/100 == 2 && resp.StatusCode != http.StatusNoContent && len(body) > 0 && !isJSON(resp.Header.Get("Content-Type")) {
		return nil, &provider.Error{Kind: provider.ErrTransient, HTTPStatus: resp.StatusCode,
			Message: fmt.Sprintf("expected JSON, got %q: %s", resp.Header.Get("Content-Type"), snippet(body))}
	}
	return out, nil
}

func (c *Client) build(ctx context.Context, r Request) (*http.Request, error) {
	u, err := c.base.Parse(strings.TrimPrefix(r.Path, "/"))
	if err != nil {
		return nil, err
	}
	if len(r.Query) > 0 {
		q := u.Query()
		for k, vs := range r.Query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}
	var body io.Reader
	contentType := r.ContentType
	switch {
	case r.Form != nil:
		body = strings.NewReader(r.Form.Encode())
		contentType = "application/x-www-form-urlencoded"
	case r.JSON != nil:
		b, err := json.Marshal(r.JSON)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
		contentType = "application/json"
	case r.Multipart != nil:
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for k, v := range r.Multipart.Fields {
			if err := mw.WriteField(k, v); err != nil {
				return nil, err
			}
		}
		for _, f := range r.Multipart.Files {
			w, err := mw.CreateFormFile(f.Field, f.Filename)
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(f.Data); err != nil {
				return nil, err
			}
		}
		if err := mw.Close(); err != nil {
			return nil, err
		}
		body = &buf
		contentType = mw.FormDataContentType()
	case r.Body != nil:
		body = bytes.NewReader(r.Body)
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if !r.NoAuth && c.cfg.Auth != nil {
		c.cfg.Auth(req)
	}
	return req, nil
}

// classifyTransport wraps a transport-level failure. *url.Error messages
// include the full URL (query string and all), which may carry API tokens,
// so the URL is redacted and only the underlying cause is wrapped.
func classifyTransport(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		target := ue.URL
		if u, perr := url.Parse(ue.URL); perr == nil {
			u.RawQuery = ""
			u.User = nil
			target = u.String()
		}
		cause := ue.Err
		msg := fmt.Sprintf("%s %s: %v", ue.Op, target, cause)
		if errors.Is(cause, context.Canceled) {
			msg = "cancelled"
		}
		return &provider.Error{Kind: provider.ErrTransient, Message: msg, Err: cause}
	}
	if errors.Is(err, context.Canceled) {
		return &provider.Error{Kind: provider.ErrTransient, Message: "cancelled", Err: err}
	}
	return &provider.Error{Kind: provider.ErrTransient, Message: err.Error(), Err: err}
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func isJSON(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// snippet returns a short, trimmed excerpt of a response body for error messages.
func snippet(b []byte) string {
	const n = 200
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// BearerAuth returns an Auth hook setting "Authorization: Bearer <token>".
func BearerAuth(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// PerMinute builds a limiter allowing n requests per minute with a small burst.
func PerMinute(n int) *rate.Limiter {
	if n <= 0 {
		return nil
	}
	burst := n / 10
	if burst < 1 {
		burst = 1
	}
	return rate.NewLimiter(rate.Limit(float64(n)/60), burst)
}
