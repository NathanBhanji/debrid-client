// Package torbox implements provider.Provider for TorBox (https://torbox.app).
//
// API notes (see docs/research/provider-torbox.md):
//   - Base https://api.torbox.app/v1/api/, Bearer API key; requestdl takes the
//     key as ?token= instead.
//   - Envelope {success, error, detail, data}; HTTP 403 auth, 400 client, 500 server.
//   - mylist with no torrents returns HTTP 404 + ITEM_NOT_FOUND (treated as empty).
//   - No file selection: every file is fetched server-side; we filter client-side.
//   - Download links come from requestdl per file and are valid ~3h to start, so
//     Links() returns opaque references and Unrestrict() resolves them lazily.
package torbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/httpx"
)

// DefaultBaseURL is the production API endpoint.
const DefaultBaseURL = "https://api.torbox.app/v1/api/"

const linkScheme = "torbox"

func init() {
	provider.Register(domain.ProviderTorBox, New)
}

// Client is a TorBox provider.
type Client struct {
	http   *httpx.Client
	apiKey string
}

// New constructs a TorBox provider from credentials (APIKey required).
func New(creds domain.Credentials, opts provider.Options) (provider.Provider, error) {
	if creds.APIKey == "" {
		return nil, provider.Errorf(provider.ErrAuth, "", "torbox: api key is required")
	}
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	hc, err := httpx.New(httpx.Config{
		BaseURL:     base,
		UserAgent:   opts.UserAgent,
		Auth:        httpx.BearerAuth(creds.APIKey),
		Limiter:     httpx.PerMinute(300), // documented: 300 req/min per token
		MaxAttempts: 3,
		Timeout:     60 * time.Second,
		HTTPClient:  opts.HTTPClient,
	})
	if err != nil {
		return nil, err
	}
	return &Client{http: hc, apiKey: creds.APIKey}, nil
}

// Kind implements provider.Provider.
func (c *Client) Kind() domain.ProviderKind { return domain.ProviderTorBox }

// Caps implements provider.Provider.
func (c *Client) Caps() provider.Caps {
	return provider.Caps{SelectFiles: false, CacheCheck: true, DirectLinks: false, MaxConnections: 8}
}

// --- wire types --------------------------------------------------------------

type envelope struct {
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Detail  string          `json:"detail"`
	Data    json.RawMessage `json:"data"`
}

type userData struct {
	ID               int64  `json:"id"`
	Email            string `json:"email"`
	Plan             int    `json:"plan"`
	IsSubscribed     bool   `json:"is_subscribed"`
	PremiumExpiresAt string `json:"premium_expires_at"`
}

type torrentData struct {
	ID               int64      `json:"id"`
	Hash             string     `json:"hash"`
	Name             string     `json:"name"`
	Size             int64      `json:"size"`
	Active           bool       `json:"active"`
	Cached           bool       `json:"cached"`
	DownloadState    string     `json:"download_state"`
	DownloadFinished bool       `json:"download_finished"`
	DownloadPresent  bool       `json:"download_present"`
	Progress         float64    `json:"progress"`
	DownloadSpeed    int64      `json:"download_speed"`
	Seeds            int        `json:"seeds"`
	CreatedAt        string     `json:"created_at"`
	UpdatedAt        string     `json:"updated_at"`
	Files            []fileData `json:"files"`
}

type fileData struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	Size      int64  `json:"size"`
	Zipped    bool   `json:"zipped"`
}

type createData struct {
	TorrentID int64  `json:"torrent_id"`
	Hash      string `json:"hash"`
}

// --- helpers -----------------------------------------------------------------

// call performs a request and unwraps the TorBox envelope into data.
func (c *Client) call(ctx context.Context, req httpx.Request, out any) error {
	req.ExpectJSON = true
	resp, err := c.http.Do(ctx, req)
	if err != nil {
		// httpx already classified 401/403/404/429/5xx; if the body was a TorBox
		// envelope, surface its code/detail instead of the raw JSON.
		var pe *provider.Error
		if errors.As(err, &pe) && len(pe.Body) > 0 {
			var env envelope
			if json.Unmarshal(pe.Body, &env) == nil && env.Error != "" {
				mapped := mapError(env.Error, env.Detail, pe.HTTPStatus)
				if pe.Kind == provider.ErrAuth || pe.Kind == provider.ErrNotFound || pe.Kind == provider.ErrRateLimited {
					mapped.Kind = pe.Kind // trust the HTTP status for these
				}
				mapped.RetryAfter = pe.RetryAfter
				return mapped
			}
		}
		return err
	}
	var env envelope
	if err := resp.JSON(&env); err != nil {
		return err
	}
	if !env.Success {
		return mapError(env.Error, env.Detail, resp.StatusCode)
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return &provider.Error{Kind: provider.ErrTransient, Message: "decode data: " + err.Error(), Err: err}
		}
	}
	return nil
}

// mapError classifies a TorBox error code.
func mapError(code, detail string, status int) *provider.Error {
	kind := provider.ErrTransient
	switch code {
	case "NO_AUTH", "BAD_TOKEN", "AUTH_ERROR", "OAUTH_VERIFICATION_ERROR":
		kind = provider.ErrAuth
	case "ITEM_NOT_FOUND", "NOT_OWNER":
		kind = provider.ErrNotFound
	case "ACTIVE_LIMIT", "MONTHLY_LIMIT", "COOLDOWN_LIMIT", "DOWNLOAD_TOO_LARGE", "PLAN_RESTRICTED_FEATURE":
		kind = provider.ErrLimit
	case "INVALID_OPTION", "MISSING_REQUIRED_OPTION", "TOO_MANY_OPTIONS", "BOZO_TORRENT", "BOZO_NZB",
		"BOZO_FILE", "BOZO_RSS_FEED", "BOZO_REGEX", "TOO_MUCH_DATA", "UNSUPPORTED_SITE", "LINK_OFFLINE",
		"INVALID_HASH", "NAME_TOO_LONG", "NAME_TOO_SHORT", "ENDPOINT_NOT_FOUND", "DUPLICATE_ITEM", "INVALID_DEVICE":
		kind = provider.ErrPermanent
	default:
		// Convention: codes ending in ERROR are server-side → transient (already default).
		if !strings.HasSuffix(code, "ERROR") && code != "" && code != "UNKNOWN_ERROR" && code != "TEMPORARILY_DISABLED" && code != "VENDOR_DISABLED" {
			kind = provider.ErrPermanent
		}
	}
	return &provider.Error{Kind: kind, Code: code, Message: detail, HTTPStatus: status}
}

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04:05.999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

// mapStatus converts TorBox's download_state + flags to the neutral status.
// download_finished/download_present are authoritative per TorBox docs; the
// state string is qBittorrent-flavoured and only used for the in-progress phases.
func mapStatus(t torrentData) (domain.TorrentStatus, string) {
	state := strings.ToLower(t.DownloadState)
	switch {
	case t.DownloadFinished && t.DownloadPresent, t.Cached && t.DownloadPresent:
		return domain.TorrentFinished, ""
	case t.DownloadFinished && !t.DownloadPresent:
		return domain.TorrentUploading, "finished, waiting for files to become available"
	}
	switch {
	case strings.HasPrefix(state, "error"), strings.HasPrefix(state, "failed"), strings.HasPrefix(state, "missing"):
		return domain.TorrentError, t.DownloadState
	case state == "metadl", state == "checkingresumedata", state == "queueddl", state == "queued", state == "allocating", state == "checking":
		return domain.TorrentProcessing, ""
	case strings.HasPrefix(state, "stalled"), state == "paused", state == "downloading", state == "forceddl":
		return domain.TorrentDownloading, ""
	case state == "uploading", state == "completed", state == "processing", state == "moving":
		return domain.TorrentUploading, ""
	case state == "cached":
		return domain.TorrentFinished, ""
	}
	if !t.Active && !t.DownloadFinished {
		return domain.TorrentError, "inactive at provider: " + t.DownloadState
	}
	return domain.TorrentProcessing, t.DownloadState
}

func mapTorrent(t torrentData) provider.Torrent {
	status, msg := mapStatus(t)
	out := provider.Torrent{
		ID:        strconv.FormatInt(t.ID, 10),
		Hash:      strings.ToLower(t.Hash),
		Name:      t.Name,
		Size:      t.Size,
		Status:    status,
		RawStatus: t.DownloadState,
		Message:   msg,
		Progress:  t.Progress,
		Speed:     t.DownloadSpeed,
		Seeders:   t.Seeds,
		AddedAt:   parseTime(t.CreatedAt),
	}
	if status == domain.TorrentFinished {
		out.Progress = 1
		out.EndedAt = parseTime(t.UpdatedAt)
	}
	for _, f := range t.Files {
		p := strings.TrimPrefix(f.Name, "/")
		if p == "" {
			p = f.ShortName
		}
		out.Files = append(out.Files, domain.File{
			ID:       strconv.FormatInt(f.ID, 10),
			Path:     p,
			Size:     f.Size,
			Selected: true, // TorBox always fetches everything
			Link:     linkRef(t.ID, f.ID),
		})
	}
	return out
}

// linkRef encodes a (torrent, file) pair as an opaque link resolved by Unrestrict.
func linkRef(torrentID, fileID int64) string {
	return fmt.Sprintf("%s://torrent/%d/file/%d", linkScheme, torrentID, fileID)
}

func parseLinkRef(link string) (torrentID, fileID string, err error) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != linkScheme {
		return "", "", provider.Errorf(provider.ErrPermanent, "", "torbox: not a torbox link: %q", link)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Host != "torrent" || len(parts) != 3 || parts[1] != "file" {
		return "", "", provider.Errorf(provider.ErrPermanent, "", "torbox: malformed link: %q", link)
	}
	return parts[0], parts[2], nil
}

// --- Provider implementation ---------------------------------------------------

// User implements provider.Provider.
func (c *Client) User(ctx context.Context) (provider.User, error) {
	var u userData
	if err := c.call(ctx, httpx.Request{Path: "user/me", Query: url.Values{"settings": {"false"}}}, &u); err != nil {
		return provider.User{}, err
	}
	plans := map[int]string{0: "free", 1: "essential", 2: "pro", 3: "standard"}
	return provider.User{
		Username:  u.Email,
		Email:     u.Email,
		Premium:   u.Plan > 0,
		Plan:      plans[u.Plan],
		ExpiresAt: parseTime(u.PremiumExpiresAt),
	}, nil
}

// ListTorrents implements provider.Provider.
func (c *Client) ListTorrents(ctx context.Context) ([]provider.Torrent, error) {
	var list []torrentData
	err := c.call(ctx, httpx.Request{Path: "torrents/mylist", Query: url.Values{"bypass_cache": {"true"}}}, &list)
	if err != nil {
		if provider.KindOf(err) == provider.ErrNotFound { // empty list quirk
			return []provider.Torrent{}, nil
		}
		return nil, err
	}
	out := make([]provider.Torrent, 0, len(list))
	for _, t := range list {
		out = append(out, mapTorrent(t))
	}
	return out, nil
}

// GetTorrent implements provider.Provider.
func (c *Client) GetTorrent(ctx context.Context, id string) (provider.Torrent, error) {
	var t torrentData
	q := url.Values{"bypass_cache": {"true"}, "id": {id}}
	if err := c.call(ctx, httpx.Request{Path: "torrents/mylist", Query: q}, &t); err != nil {
		return provider.Torrent{}, err
	}
	if t.ID == 0 {
		return provider.Torrent{}, &provider.Error{Kind: provider.ErrNotFound, Message: "torrent " + id}
	}
	return mapTorrent(t), nil
}

// AddMagnet implements provider.Provider.
func (c *Client) AddMagnet(ctx context.Context, magnet string) (provider.AddResult, error) {
	return c.create(ctx, map[string]string{"magnet": magnet}, nil)
}

// AddTorrentFile implements provider.Provider.
func (c *Client) AddTorrentFile(ctx context.Context, data []byte) (provider.AddResult, error) {
	return c.create(ctx, nil, []httpx.MultipartFile{{Field: "file", Filename: "upload.torrent", Data: data}})
}

func (c *Client) create(ctx context.Context, fields map[string]string, files []httpx.MultipartFile) (provider.AddResult, error) {
	mp := &httpx.Multipart{Fields: map[string]string{
		"seed":      "1",     // account default
		"allow_zip": "false", // we always want individual files
	}, Files: files}
	for k, v := range fields {
		mp.Fields[k] = v
	}
	var cd createData
	// NoRetry: a timed-out create may still have succeeded; the engine re-lists by hash instead.
	err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "torrents/createtorrent", Multipart: mp, NoRetry: true}, &cd)
	if err != nil {
		return provider.AddResult{}, err
	}
	if cd.TorrentID == 0 {
		return provider.AddResult{}, &provider.Error{Kind: provider.ErrTransient, Message: "torbox: createtorrent returned no id"}
	}
	return provider.AddResult{ID: strconv.FormatInt(cd.TorrentID, 10), Hash: strings.ToLower(cd.Hash)}, nil
}

// SelectFiles implements provider.Provider (no-op: TorBox fetches all files).
func (c *Client) SelectFiles(context.Context, string, []string) error { return nil }

// Links implements provider.Provider.
func (c *Client) Links(ctx context.Context, id string) ([]provider.Link, error) {
	t, err := c.GetTorrent(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status != domain.TorrentFinished {
		return nil, nil
	}
	out := make([]provider.Link, 0, len(t.Files))
	for _, f := range t.Files {
		out = append(out, provider.Link{FileID: f.ID, Path: f.Path, Size: f.Size, URL: f.Link})
	}
	return out, nil
}

// Unrestrict implements provider.Provider: resolves a torbox:// reference via requestdl.
func (c *Client) Unrestrict(ctx context.Context, link string) (provider.Direct, error) {
	tid, fid, err := parseLinkRef(link)
	if err != nil {
		return provider.Direct{}, err
	}
	var raw string
	q := url.Values{"token": {c.apiKey}, "torrent_id": {tid}, "file_id": {fid}, "redirect": {"false"}}
	if err := c.call(ctx, httpx.Request{Path: "torrents/requestdl", Query: q, NoAuth: true}, &raw); err != nil {
		return provider.Direct{}, err
	}
	if raw == "" {
		return provider.Direct{}, &provider.Error{Kind: provider.ErrTransient, Message: "torbox: empty download url"}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return provider.Direct{}, &provider.Error{Kind: provider.ErrTransient, Message: "torbox: bad download url " + raw}
	}
	return provider.Direct{URL: raw, Filename: path.Base(u.Path), MaxConnections: 8}, nil
}

// Delete implements provider.Provider.
func (c *Client) Delete(ctx context.Context, id string) error {
	tid, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return provider.Errorf(provider.ErrPermanent, "", "torbox: bad torrent id %q", id)
	}
	body := map[string]any{"torrent_id": tid, "operation": "delete"}
	err = c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "torrents/controltorrent", JSON: body}, nil)
	if err != nil && provider.KindOf(err) == provider.ErrNotFound {
		return nil // already gone
	}
	return err
}

// CheckCached implements provider.CacheChecker.
func (c *Client) CheckCached(ctx context.Context, hashes []string) (map[string]bool, error) {
	out := make(map[string]bool, len(hashes))
	if len(hashes) == 0 {
		return out, nil
	}
	var raw json.RawMessage
	body := map[string]any{"hashes": hashes}
	q := url.Values{"format": {"object"}, "list_files": {"false"}}
	if err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "torrents/checkcached", Query: q, JSON: body}, &raw); err != nil {
		return nil, err
	}
	for _, h := range hashes {
		out[strings.ToLower(h)] = false
	}
	// Empty results have been observed as {} , [] and null; only an object carries hits.
	if len(raw) > 0 && raw[0] == '{' {
		var data map[string]json.RawMessage
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, &provider.Error{Kind: provider.ErrTransient, Message: "torbox: decode checkcached: " + err.Error(), Err: err}
		}
		for h := range data {
			out[strings.ToLower(h)] = true
		}
	}
	return out, nil
}

var (
	_ provider.Provider     = (*Client)(nil)
	_ provider.CacheChecker = (*Client)(nil)
)
