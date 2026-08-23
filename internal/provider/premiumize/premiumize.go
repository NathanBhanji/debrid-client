// Package premiumize implements provider.Provider for Premiumize.me.
//
// API notes (see docs/research/provider-premiumize.md):
//   - Base https://www.premiumize.me/api/, Bearer API key (or OAuth token).
//   - Every response is HTTP 200 JSON with status "success"|"error" (+ message, code).
//   - Transfers: POST /transfer/create (magnet), GET /transfer/list (status:
//     waiting|queued|running|finished|seeding|error|timeout|banned|deleted),
//     POST /transfer/delete.
//   - Finished transfers land in the cloud: folder_id (multi-file) or file_id
//     (single file). Items carry a direct "link" — no unrestrict step.
//   - POST /cache/check works (unlike RD/AD).
package premiumize

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/httpx"
)

// DefaultBaseURL is the production API endpoint.
const DefaultBaseURL = "https://www.premiumize.me/api/"

func init() {
	provider.Register(domain.ProviderPremiumize, New)
}

// Client is a Premiumize provider.
type Client struct {
	http *httpx.Client
}

// New constructs a Premiumize provider (APIKey or AccessToken required).
func New(creds domain.Credentials, opts provider.Options) (provider.Provider, error) {
	token := creds.APIKey
	if token == "" {
		token = creds.AccessToken
	}
	if token == "" {
		return nil, provider.Errorf(provider.ErrAuth, "", "premiumize: api key is required")
	}
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	hc, err := httpx.New(httpx.Config{
		BaseURL: base, UserAgent: opts.UserAgent, Auth: httpx.BearerAuth(token),
		Limiter: httpx.PerMinute(120), MaxAttempts: 3, Timeout: 60 * time.Second, HTTPClient: opts.HTTPClient,
	})
	if err != nil {
		return nil, err
	}
	return &Client{http: hc}, nil
}

// Kind implements provider.Provider.
func (c *Client) Kind() domain.ProviderKind { return domain.ProviderPremiumize }

// Caps implements provider.Provider.
func (c *Client) Caps() provider.Caps {
	return provider.Caps{SelectFiles: false, CacheCheck: true, DirectLinks: true, MaxConnections: 8}
}

// --- wire types --------------------------------------------------------------

type base struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

type accountInfo struct {
	base
	CustomerID   json.Number `json:"customer_id"`
	PremiumUntil int64       `json:"premium_until"`
	LimitUsed    float64     `json:"limit_used"`
}

type transfer struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Message  string  `json:"message"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Src      string  `json:"src"`
	FolderID *string `json:"folder_id"`
	FileID   *string `json:"file_id"`
}

type transferList struct {
	base
	Transfers []transfer `json:"transfers"`
}

type createResp struct {
	base
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type item struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // file|folder
	Size      int64  `json:"size"`
	Link      string `json:"link"`
	CreatedAt int64  `json:"created_at"`
}

type folderList struct {
	base
	Name    string `json:"name"`
	Content []item `json:"content"`
}

type itemDetails struct {
	base
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Link string `json:"link"`
}

type cacheCheck struct {
	base
	Response []bool `json:"response"`
}

// --- helpers -----------------------------------------------------------------

// call performs a request and checks the status field (HTTP is always 200).
func (c *Client) call(ctx context.Context, req httpx.Request, out interface{ statusOf() base }) error {
	req.ExpectJSON = true
	resp, err := c.http.Do(ctx, req)
	if err != nil {
		return err
	}
	if err := resp.JSON(out); err != nil {
		return err
	}
	if b := out.statusOf(); b.Status != "success" {
		return mapError(b.Code, b.Message, resp.StatusCode)
	}
	return nil
}

func (b base) statusOf() base { return b }

func mapError(code, msg string, status int) *provider.Error {
	kind := provider.ErrPermanent
	switch code {
	case "authentication_failed", "permission_denied":
		kind = provider.ErrAuth
	case "not_found":
		kind = provider.ErrNotFound
	case "rate_limit_reached":
		kind = provider.ErrRateLimited
	case "account_limit_reached", "service_limit_reached":
		kind = provider.ErrLimit
	case "link_generation_failed", "transient_error", "service_down", "semi_permanent_error", "unknown_error":
		kind = provider.ErrTransient
	case "":
		// Old-style responses only carry a message; guess from it.
		l := strings.ToLower(msg)
		switch {
		case strings.Contains(l, "not logged in"), strings.Contains(l, "invalid") && strings.Contains(l, "key"):
			kind = provider.ErrAuth
		case strings.Contains(l, "not found"):
			kind = provider.ErrNotFound
		case strings.Contains(l, "limit"):
			kind = provider.ErrLimit
		}
	}
	if msg == "" {
		msg = code
	}
	return &provider.Error{Kind: kind, Code: code, Message: msg, HTTPStatus: status}
}

func mapStatus(s, msg string) (domain.TorrentStatus, string) {
	switch s {
	case "waiting", "queued":
		return domain.TorrentProcessing, ""
	case "running":
		return domain.TorrentDownloading, msg
	case "finished", "seeding":
		return domain.TorrentFinished, ""
	case "error", "timeout", "banned", "deleted":
		return domain.TorrentError, firstNonEmpty(msg, s)
	}
	return domain.TorrentProcessing, firstNonEmpty(msg, s)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func mapTransfer(t transfer) provider.Torrent {
	status, msg := mapStatus(t.Status, t.Message)
	out := provider.Torrent{ID: t.ID, Name: t.Name, Status: status, RawStatus: t.Status, Message: msg, Progress: t.Progress}
	if status == domain.TorrentFinished {
		out.Progress = 1
	}
	return out
}

// --- Provider implementation ---------------------------------------------------

// User implements provider.Provider.
func (c *Client) User(ctx context.Context) (provider.User, error) {
	var a accountInfo
	if err := c.call(ctx, httpx.Request{Path: "account/info"}, &a); err != nil {
		return provider.User{}, err
	}
	u := provider.User{Username: a.CustomerID.String(), Premium: a.PremiumUntil > time.Now().Unix()}
	if a.PremiumUntil > 0 {
		t := time.Unix(a.PremiumUntil, 0).UTC()
		u.ExpiresAt = &t
	}
	if u.Premium {
		u.Plan = "premium"
	} else {
		u.Plan = "free"
	}
	return u, nil
}

// ListTorrents implements provider.Provider. Premiumize's list has no hashes;
// we extract the btih from the transfer's src magnet when available.
func (c *Client) ListTorrents(ctx context.Context) ([]provider.Torrent, error) {
	var l transferList
	if err := c.call(ctx, httpx.Request{Path: "transfer/list"}, &l); err != nil {
		return nil, err
	}
	out := make([]provider.Torrent, 0, len(l.Transfers))
	for _, t := range l.Transfers {
		pt := mapTransfer(t)
		pt.Hash = hashFromSrc(t.Src)
		out = append(out, pt)
	}
	return out, nil
}

func hashFromSrc(src string) string {
	const key = "xt=urn:btih:"
	i := strings.Index(strings.ToLower(src), key)
	if i < 0 {
		return ""
	}
	h := src[i+len(key):]
	if j := strings.IndexAny(h, "&#"); j >= 0 {
		h = h[:j]
	}
	if len(h) == 40 {
		return strings.ToLower(h)
	}
	return ""
}

// GetTorrent implements provider.Provider: the transfer plus its cloud files when finished.
func (c *Client) GetTorrent(ctx context.Context, id string) (provider.Torrent, error) {
	var l transferList
	if err := c.call(ctx, httpx.Request{Path: "transfer/list"}, &l); err != nil {
		return provider.Torrent{}, err
	}
	for _, t := range l.Transfers {
		if t.ID != id {
			continue
		}
		pt := mapTransfer(t)
		pt.Hash = hashFromSrc(t.Src)
		if pt.Status == domain.TorrentFinished {
			files, err := c.filesFor(ctx, t)
			if err != nil {
				return provider.Torrent{}, err
			}
			pt.Files = files
			for _, f := range files {
				pt.Size += f.Size
			}
		}
		return pt, nil
	}
	return provider.Torrent{}, &provider.Error{Kind: provider.ErrNotFound, Message: "transfer " + id}
}

// filesFor enumerates the cloud items produced by a finished transfer.
func (c *Client) filesFor(ctx context.Context, t transfer) ([]domain.File, error) {
	switch {
	case t.FolderID != nil && *t.FolderID != "":
		var out []domain.File
		if err := c.walk(ctx, *t.FolderID, "", &out); err != nil {
			return nil, err
		}
		return out, nil
	case t.FileID != nil && *t.FileID != "":
		var d itemDetails
		if err := c.call(ctx, httpx.Request{Path: "item/details", Query: url.Values{"id": {*t.FileID}}}, &d); err != nil {
			return nil, err
		}
		return []domain.File{{ID: d.ID, Path: d.Name, Size: d.Size, Selected: true, Link: d.Link}}, nil
	}
	return nil, nil // finished but not yet materialised; caller waits
}

func (c *Client) walk(ctx context.Context, folderID, prefix string, out *[]domain.File) error {
	var l folderList
	if err := c.call(ctx, httpx.Request{Path: "folder/list", Query: url.Values{"id": {folderID}}}, &l); err != nil {
		return err
	}
	if prefix == "" {
		prefix = l.Name
	}
	for _, it := range l.Content {
		p := path.Join(prefix, it.Name)
		if it.Type == "folder" {
			if err := c.walk(ctx, it.ID, p, out); err != nil {
				return err
			}
			continue
		}
		*out = append(*out, domain.File{ID: it.ID, Path: p, Size: it.Size, Selected: true, Link: it.Link})
	}
	return nil
}

// AddMagnet implements provider.Provider.
func (c *Client) AddMagnet(ctx context.Context, magnet string) (provider.AddResult, error) {
	var r createResp
	mp := &httpx.Multipart{Fields: map[string]string{"src": magnet}}
	if err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "transfer/create", Multipart: mp, NoRetry: true}, &r); err != nil {
		return provider.AddResult{}, err
	}
	if r.ID == "" {
		return provider.AddResult{}, &provider.Error{Kind: provider.ErrTransient, Message: "premiumize: create returned no id"}
	}
	return provider.AddResult{ID: r.ID, Hash: hashFromSrc(magnet)}, nil
}

// AddTorrentFile implements provider.Provider.
func (c *Client) AddTorrentFile(ctx context.Context, data []byte) (provider.AddResult, error) {
	var r createResp
	mp := &httpx.Multipart{Files: []httpx.MultipartFile{{Field: "file", Filename: "upload.torrent", Data: data}}}
	if err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "transfer/create", Multipart: mp, NoRetry: true}, &r); err != nil {
		return provider.AddResult{}, err
	}
	if r.ID == "" {
		return provider.AddResult{}, &provider.Error{Kind: provider.ErrTransient, Message: "premiumize: create returned no id"}
	}
	return provider.AddResult{ID: r.ID}, nil
}

// SelectFiles implements provider.Provider (no-op).
func (c *Client) SelectFiles(context.Context, string, []string) error { return nil }

// Links implements provider.Provider: cloud item links are already direct.
func (c *Client) Links(ctx context.Context, id string) ([]provider.Link, error) {
	t, err := c.GetTorrent(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status != domain.TorrentFinished || len(t.Files) == 0 {
		return nil, nil
	}
	out := make([]provider.Link, 0, len(t.Files))
	for _, f := range t.Files {
		out = append(out, provider.Link{FileID: f.ID, Path: f.Path, Size: f.Size, URL: f.Link})
	}
	return out, nil
}

// Unrestrict implements provider.Provider (identity: links are direct).
func (c *Client) Unrestrict(_ context.Context, link string) (provider.Direct, error) {
	u, err := url.Parse(link)
	if err != nil {
		return provider.Direct{}, provider.Errorf(provider.ErrPermanent, "", "premiumize: bad link %q", link)
	}
	return provider.Direct{URL: link, Filename: path.Base(u.Path), MaxConnections: 8}, nil
}

// Delete implements provider.Provider: removes the transfer (cloud files are kept).
func (c *Client) Delete(ctx context.Context, id string) error {
	var b base
	err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "transfer/delete", Form: url.Values{"id": {id}}}, &b)
	if err != nil && provider.KindOf(err) == provider.ErrNotFound {
		return nil
	}
	return err
}

// CheckCached implements provider.CacheChecker.
func (c *Client) CheckCached(ctx context.Context, hashes []string) (map[string]bool, error) {
	out := make(map[string]bool, len(hashes))
	if len(hashes) == 0 {
		return out, nil
	}
	form := url.Values{}
	for _, h := range hashes {
		form.Add("items[]", h)
	}
	var r cacheCheck
	if err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "cache/check", Form: form}, &r); err != nil {
		return nil, err
	}
	for i, h := range hashes {
		out[strings.ToLower(h)] = i < len(r.Response) && r.Response[i]
	}
	return out, nil
}

var (
	_ provider.Provider     = (*Client)(nil)
	_ provider.CacheChecker = (*Client)(nil)
)
