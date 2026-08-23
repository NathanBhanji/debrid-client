// Package alldebrid implements provider.Provider for AllDebrid.
//
// API notes (see docs/research/provider-alldebrid.md):
//   - Base https://api.alldebrid.com/v4/ (some endpoints v4.1), Bearer API key.
//     12 req/s, 600 req/min.
//   - Envelope {"status":"success","data":...} | {"status":"error","error":{code,message}}.
//   - No file selection: the whole magnet is cached; files come from
//     POST /magnet/files as a tree, each file with a link that must be unlocked
//     via POST /link/unlock to get a direct URL.
//   - magnet/status statusCode: 0 queue, 1 downloading, 2 compressing, 3
//     uploading, 4 ready, >=5 error.
package alldebrid

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/httpx"
)

// DefaultBaseURL is the production API endpoint.
const DefaultBaseURL = "https://api.alldebrid.com/"

func init() {
	provider.Register(domain.ProviderAllDebrid, New)
}

// Client is an AllDebrid provider.
type Client struct {
	http *httpx.Client
}

// New constructs an AllDebrid provider (APIKey required).
func New(creds domain.Credentials, opts provider.Options) (provider.Provider, error) {
	if creds.APIKey == "" {
		return nil, provider.Errorf(provider.ErrAuth, "", "alldebrid: api key is required")
	}
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	hc, err := httpx.New(httpx.Config{
		BaseURL: base, UserAgent: opts.UserAgent, Auth: httpx.BearerAuth(creds.APIKey),
		Limiter: httpx.PerMinute(600), MaxAttempts: 3, Timeout: 60 * time.Second, HTTPClient: opts.HTTPClient,
	})
	if err != nil {
		return nil, err
	}
	return &Client{http: hc}, nil
}

// Kind implements provider.Provider.
func (c *Client) Kind() domain.ProviderKind { return domain.ProviderAllDebrid }

// Caps implements provider.Provider.
func (c *Client) Caps() provider.Caps {
	return provider.Caps{SelectFiles: false, CacheCheck: false, DirectLinks: false, MaxConnections: 8}
}

// --- wire types --------------------------------------------------------------

type envelope struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  *apiError       `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type userData struct {
	User struct {
		Username     string `json:"username"`
		Email        string `json:"email"`
		IsPremium    bool   `json:"isPremium"`
		IsTrial      bool   `json:"isTrial"`
		PremiumUntil int64  `json:"premiumUntil"`
	} `json:"user"`
}

type magnetStatus struct {
	ID             int64   `json:"id"`
	Filename       string  `json:"filename"`
	Size           int64   `json:"size"`
	Hash           string  `json:"hash"`
	Status         string  `json:"status"`
	StatusCode     int     `json:"statusCode"`
	Downloaded     int64   `json:"downloaded"`
	Seeders        int     `json:"seeders"`
	DownloadSpeed  int64   `json:"downloadSpeed"`
	UploadDate     int64   `json:"uploadDate"`
	CompletionDate int64   `json:"completionDate"`
	Links          []mLink `json:"links"` // legacy v4 only
}

type mLink struct {
	Link     string `json:"link"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

type uploadResult struct {
	Magnets []struct {
		Magnet string    `json:"magnet"`
		Hash   string    `json:"hash"`
		Name   string    `json:"name"`
		Size   int64     `json:"size"`
		Ready  bool      `json:"ready"`
		ID     int64     `json:"id"`
		Error  *apiError `json:"error"`
	} `json:"magnets"`
	Files []struct {
		File  string    `json:"file"`
		Name  string    `json:"name"`
		Hash  string    `json:"hash"`
		ID    int64     `json:"id"`
		Size  int64     `json:"size"`
		Ready bool      `json:"ready"`
		Error *apiError `json:"error"`
	} `json:"files"`
}

// fileNode is an entry of the /magnet/files tree: folders have "e", files have "l".
type fileNode struct {
	N string     `json:"n"`
	S int64      `json:"s"`
	L string     `json:"l"`
	E []fileNode `json:"e"`
}

type unlockData struct {
	Link     string `json:"link"`
	Filename string `json:"filename"`
	Filesize int64  `json:"filesize"`
	Delayed  int64  `json:"delayed"`
}

// --- helpers -----------------------------------------------------------------

func (c *Client) call(ctx context.Context, req httpx.Request, out any) error {
	req.ExpectJSON = true
	resp, err := c.http.Do(ctx, req)
	if err != nil {
		return err
	}
	var env envelope
	if err := resp.JSON(&env); err != nil {
		return err
	}
	if env.Status != "success" {
		if env.Error == nil {
			return &provider.Error{Kind: provider.ErrTransient, HTTPStatus: resp.StatusCode, Message: "alldebrid: malformed error response"}
		}
		return mapError(env.Error.Code, env.Error.Message, resp.StatusCode)
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return &provider.Error{Kind: provider.ErrTransient, Message: "alldebrid: decode data: " + err.Error(), Err: err}
		}
	}
	return nil
}

func mapError(code, msg string, status int) *provider.Error {
	kind := provider.ErrPermanent
	switch {
	case strings.HasPrefix(code, "AUTH_"), code == "PIN_EXPIRED", code == "PIN_INVALID":
		kind = provider.ErrAuth
	case code == "MAGNET_INVALID_ID", code == "DELAYED_INVALID_ID", code == "LINK_DOWN", code == "LINK_NOT_SUPPORTED":
		kind = provider.ErrNotFound
	case code == "MAGNET_TOO_MANY_ACTIVE", code == "LINK_TOO_MANY_DOWNLOADS", code == "LINK_HOST_LIMIT_REACHED",
		code == "LINK_HOST_FULL", code == "FREE_TRIAL_LIMIT_REACHED", code == "MAGNET_MUST_BE_PREMIUM", code == "MUST_BE_PREMIUM":
		kind = provider.ErrLimit
	case code == "MAGNET_NO_SERVER", code == "NO_SERVER", code == "LINK_TEMPORARY_UNAVAILABLE", code == "LINK_HOST_UNAVAILABLE",
		code == "MAGNET_PROCESSING", code == "LINK_ERROR", code == "MAGNET_FILE_UPLOAD_FAILED":
		kind = provider.ErrTransient
	}
	return &provider.Error{Kind: kind, Code: code, Message: msg, HTTPStatus: status}
}

func mapStatus(code int, raw string) (domain.TorrentStatus, string) {
	switch code {
	case 0:
		return domain.TorrentProcessing, ""
	case 1:
		return domain.TorrentDownloading, ""
	case 2, 3:
		return domain.TorrentUploading, ""
	case 4:
		return domain.TorrentFinished, ""
	}
	if code >= 5 {
		return domain.TorrentError, raw
	}
	return domain.TorrentProcessing, raw
}

func unixPtr(v int64) *time.Time {
	if v <= 0 {
		return nil
	}
	t := time.Unix(v, 0).UTC()
	return &t
}

func mapMagnet(m magnetStatus) provider.Torrent {
	status, msg := mapStatus(m.StatusCode, m.Status)
	out := provider.Torrent{
		ID: strconv.FormatInt(m.ID, 10), Hash: strings.ToLower(m.Hash), Name: m.Filename, Size: m.Size,
		Status: status, RawStatus: m.Status, Message: msg, Speed: m.DownloadSpeed, Seeders: m.Seeders,
		AddedAt: unixPtr(m.UploadDate), EndedAt: unixPtr(m.CompletionDate),
	}
	if m.Size > 0 {
		out.Progress = float64(m.Downloaded) / float64(m.Size)
	}
	if status == domain.TorrentFinished {
		out.Progress = 1
	}
	return out
}

// flatten walks the files tree producing files with stable ids (the path).
func flatten(prefix string, nodes []fileNode, out *[]domain.File) {
	for _, n := range nodes {
		p := n.N
		if prefix != "" {
			p = prefix + "/" + n.N
		}
		if n.L == "" { // folders have no link; files always do
			flatten(p, n.E, out)
			continue
		}
		*out = append(*out, domain.File{ID: p, Path: p, Size: n.S, Selected: true, Link: n.L})
	}
}

// --- Provider implementation ---------------------------------------------------

// User implements provider.Provider.
func (c *Client) User(ctx context.Context) (provider.User, error) {
	var u userData
	if err := c.call(ctx, httpx.Request{Path: "v4/user"}, &u); err != nil {
		return provider.User{}, err
	}
	plan := "free"
	switch {
	case u.User.IsPremium:
		plan = "premium"
	case u.User.IsTrial:
		plan = "trial"
	}
	return provider.User{Username: u.User.Username, Email: u.User.Email, Premium: u.User.IsPremium, Plan: plan, ExpiresAt: unixPtr(u.User.PremiumUntil)}, nil
}

// ListTorrents implements provider.Provider (one /v4.1/magnet/status call).
func (c *Client) ListTorrents(ctx context.Context) ([]provider.Torrent, error) {
	var data struct {
		Magnets []magnetStatus `json:"magnets"`
	}
	if err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "v4.1/magnet/status", Form: url.Values{}}, &data); err != nil {
		return nil, err
	}
	out := make([]provider.Torrent, 0, len(data.Magnets))
	for _, m := range data.Magnets {
		out = append(out, mapMagnet(m))
	}
	return out, nil
}

// GetTorrent implements provider.Provider (status + file tree).
func (c *Client) GetTorrent(ctx context.Context, id string) (provider.Torrent, error) {
	var data struct {
		Magnets json.RawMessage `json:"magnets"`
	}
	if err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "v4.1/magnet/status", Form: url.Values{"id": {id}}}, &data); err != nil {
		return provider.Torrent{}, err
	}
	// With an id AllDebrid may return a single object or a one-element array.
	var list []magnetStatus
	if len(data.Magnets) > 0 && data.Magnets[0] == '{' {
		var one magnetStatus
		if err := json.Unmarshal(data.Magnets, &one); err != nil {
			return provider.Torrent{}, &provider.Error{Kind: provider.ErrTransient, Message: "alldebrid: decode magnet: " + err.Error()}
		}
		list = []magnetStatus{one}
	} else if err := json.Unmarshal(data.Magnets, &list); err != nil {
		return provider.Torrent{}, &provider.Error{Kind: provider.ErrTransient, Message: "alldebrid: decode magnets: " + err.Error()}
	}
	if len(list) == 0 {
		return provider.Torrent{}, &provider.Error{Kind: provider.ErrNotFound, Message: "magnet " + id}
	}
	t := mapMagnet(list[0])
	if t.Status == domain.TorrentFinished {
		files, err := c.files(ctx, id)
		if err != nil {
			return provider.Torrent{}, err
		}
		t.Files = files
	}
	return t, nil
}

func (c *Client) files(ctx context.Context, id string) ([]domain.File, error) {
	var data struct {
		Magnets []struct {
			ID    json.Number `json:"id"`
			Files []fileNode  `json:"files"`
			Error *apiError   `json:"error"`
		} `json:"magnets"`
	}
	if err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "v4/magnet/files", Form: url.Values{"id[]": {id}}}, &data); err != nil {
		return nil, err
	}
	var out []domain.File
	for _, m := range data.Magnets {
		if m.Error != nil {
			return nil, mapError(m.Error.Code, m.Error.Message, 200)
		}
		flatten("", m.Files, &out)
	}
	return out, nil
}

// AddMagnet implements provider.Provider.
func (c *Client) AddMagnet(ctx context.Context, magnet string) (provider.AddResult, error) {
	var res uploadResult
	err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "v4/magnet/upload", Form: url.Values{"magnets[]": {magnet}}, NoRetry: true}, &res)
	if err != nil {
		return provider.AddResult{}, err
	}
	if len(res.Magnets) == 0 {
		return provider.AddResult{}, &provider.Error{Kind: provider.ErrTransient, Message: "alldebrid: empty upload response"}
	}
	m := res.Magnets[0]
	if m.Error != nil {
		return provider.AddResult{}, mapError(m.Error.Code, m.Error.Message, 200)
	}
	return provider.AddResult{ID: strconv.FormatInt(m.ID, 10), Hash: strings.ToLower(m.Hash)}, nil
}

// AddTorrentFile implements provider.Provider.
func (c *Client) AddTorrentFile(ctx context.Context, data []byte) (provider.AddResult, error) {
	var res uploadResult
	mp := &httpx.Multipart{Files: []httpx.MultipartFile{{Field: "files[]", Filename: "upload.torrent", Data: data}}}
	err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "v4/magnet/upload/file", Multipart: mp, NoRetry: true}, &res)
	if err != nil {
		return provider.AddResult{}, err
	}
	if len(res.Files) == 0 {
		return provider.AddResult{}, &provider.Error{Kind: provider.ErrTransient, Message: "alldebrid: empty upload response"}
	}
	f := res.Files[0]
	if f.Error != nil {
		return provider.AddResult{}, mapError(f.Error.Code, f.Error.Message, 200)
	}
	return provider.AddResult{ID: strconv.FormatInt(f.ID, 10), Hash: strings.ToLower(f.Hash)}, nil
}

// SelectFiles implements provider.Provider (no-op: AllDebrid fetches everything).
func (c *Client) SelectFiles(context.Context, string, []string) error { return nil }

// Links implements provider.Provider: one link per file from the files tree.
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

// Unrestrict implements provider.Provider via POST /link/unlock.
func (c *Client) Unrestrict(ctx context.Context, link string) (provider.Direct, error) {
	var u unlockData
	if err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "v4/link/unlock", Form: url.Values{"link": {link}}}, &u); err != nil {
		return provider.Direct{}, err
	}
	if u.Delayed != 0 && u.Link == "" {
		return provider.Direct{}, &provider.Error{Kind: provider.ErrTransient, Code: "DELAYED", Message: fmt.Sprintf("alldebrid: link generation delayed (id %d)", u.Delayed), RetryAfter: 15 * time.Second}
	}
	if u.Link == "" {
		return provider.Direct{}, &provider.Error{Kind: provider.ErrTransient, Message: "alldebrid: unlock returned no link"}
	}
	return provider.Direct{URL: u.Link, Filename: u.Filename, Size: u.Filesize, MaxConnections: 8}, nil
}

// Delete implements provider.Provider.
func (c *Client) Delete(ctx context.Context, id string) error {
	err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "v4/magnet/delete", Form: url.Values{"id": {id}}}, nil)
	if err != nil && provider.KindOf(err) == provider.ErrNotFound {
		return nil
	}
	return err
}

var _ provider.Provider = (*Client)(nil)
