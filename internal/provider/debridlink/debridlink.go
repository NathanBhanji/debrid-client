// Package debridlink implements provider.Provider for Debrid-Link.
//
// API notes (see docs/research/provider-debridlink.md):
//   - Base https://debrid-link.com/api/v2/, Bearer token (OAuth access token
//     or a private API key from /webapp/apikey used the same way).
//   - Envelope {success:true, value, pagination?} | {success:false, error, error_description}.
//   - Seedbox: GET /seedbox/list (files carry direct downloadUrl), POST
//     /seedbox/add, DELETE /seedbox/:ids/remove. status: 0 paused, 1 queued,
//     2 verification, 4 downloading, 8 seeding, 100 finished (bitmask-ish);
//     a file is ready when files[].downloadPercent == 100.
//   - Many-file torrents list only a zip unless fetched by id (?ids=); the
//     listing reports no files for those (isZip) and GetTorrent/Links use ?ids=.
//   - SelectFiles is a no-op (we add without wait=true). A torrent that was
//     added elsewhere with wait=true stays paused at the provider.
//   - Datacenter/VPN IPs are blocked by default (serverNotAllowed → auth
//     error: whitelist the server's IP in the Debrid-Link account).
package debridlink

import (
	"context"
	"encoding/json"
	"errors"
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
const DefaultBaseURL = "https://debrid-link.com/api/v2/"

func init() {
	provider.Register(domain.ProviderDebridLink, New)
}

// Client is a Debrid-Link provider.
type Client struct {
	http *httpx.Client
}

// New constructs a Debrid-Link provider (APIKey or AccessToken required).
func New(creds domain.Credentials, opts provider.Options) (provider.Provider, error) {
	token := creds.APIKey
	if token == "" {
		token = creds.AccessToken
	}
	if token == "" {
		return nil, provider.Errorf(provider.ErrAuth, "", "debridlink: api key is required")
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
func (c *Client) Kind() domain.ProviderKind { return domain.ProviderDebridLink }

// Caps implements provider.Provider.
func (c *Client) Caps() provider.Caps {
	return provider.Caps{SelectFiles: false, CacheCheck: false, DirectLinks: true, MaxConnections: 8}
}

// --- wire types --------------------------------------------------------------

type envelope struct {
	Success          bool            `json:"success"`
	Value            json.RawMessage `json:"value"`
	Error            string          `json:"error"`
	ErrorDescription string          `json:"error_description"`
}

type account struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	AccountType int    `json:"accountType"`
	PremiumLeft int64  `json:"premiumLeft"`
}

type torrent struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Created         int64   `json:"created"`
	HashString      string  `json:"hashString"`
	Wait            bool    `json:"wait"`
	PeersConnected  int     `json:"peersConnected"`
	Status          int     `json:"status"`
	TotalSize       int64   `json:"totalSize"`
	DownloadPercent float64 `json:"downloadPercent"`
	DownloadSpeed   int64   `json:"downloadSpeed"`
	IsZip           bool    `json:"isZip"`
	SrvMaint        bool    `json:"srvMaint"`
	Files           []file  `json:"files"`
}

type file struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Size            int64   `json:"size"`
	DownloadURL     string  `json:"downloadUrl"`
	DownloadPercent float64 `json:"downloadPercent"`
}

// --- helpers -----------------------------------------------------------------

func (c *Client) call(ctx context.Context, req httpx.Request, out any) error {
	req.ExpectJSON = true
	resp, err := c.http.Do(ctx, req)
	if err != nil {
		return enrich(err)
	}
	var env envelope
	if err := resp.JSON(&env); err != nil {
		return err
	}
	if !env.Success {
		return mapError(env.Error, env.ErrorDescription, resp.StatusCode)
	}
	if out != nil && len(env.Value) > 0 && string(env.Value) != "null" {
		if err := json.Unmarshal(env.Value, out); err != nil {
			return &provider.Error{Kind: provider.ErrTransient, Message: "debridlink: decode value: " + err.Error(), Err: err}
		}
	}
	return nil
}

// enrich pulls Debrid-Link's error code out of httpx-classified 4xx bodies.
func enrich(err error) error {
	var pe *provider.Error
	if !errors.As(err, &pe) || len(pe.Body) == 0 {
		return err
	}
	var env envelope
	if json.Unmarshal(pe.Body, &env) != nil || env.Error == "" {
		return err
	}
	m := mapError(env.Error, env.ErrorDescription, pe.HTTPStatus)
	if pe.Kind == provider.ErrAuth || pe.Kind == provider.ErrNotFound || pe.Kind == provider.ErrRateLimited {
		m.Kind = pe.Kind
	}
	if pe.RetryAfter > 0 { // header wins; else keep the code-derived hint (floodDetected → 1h)
		m.RetryAfter = pe.RetryAfter
	}
	return m
}

func mapError(code, desc string, status int) *provider.Error {
	kind := provider.ErrPermanent
	switch code {
	case "badToken", "badSign", "hidedToken", "accountLocked", "maxAttempts", "serverNotAllowed":
		kind = provider.ErrAuth
	case "badId", "unknowR", "fileNotFound", "fileNotAvailable":
		kind = provider.ErrNotFound
	case "floodDetected":
		kind = provider.ErrRateLimited
	case "maxTorrent", "maxTransfer", "maxLink", "maxLinkHost", "maxData", "maxDataHost", "freeServerOverload", "notFreeHost":
		kind = provider.ErrLimit
	case "internalError", "maintenanceHost", "noServerHost", "disabledServerHost":
		kind = provider.ErrTransient
	}
	pe := &provider.Error{Kind: kind, Code: code, Message: desc, HTTPStatus: status}
	if code == "floodDetected" {
		pe.RetryAfter = time.Hour
	}
	return pe
}

func mapStatus(t torrent) (domain.TorrentStatus, string) {
	switch {
	case t.Status == 100 || t.DownloadPercent >= 100:
		return domain.TorrentFinished, ""
	case t.Wait:
		return domain.TorrentWaitingSelection, "waiting for file selection"
	case t.Status&8 != 0: // seeding (content fully fetched but not flagged 100 yet)
		return domain.TorrentUploading, ""
	case t.Status&4 != 0:
		return domain.TorrentDownloading, ""
	case t.Status&2 != 0:
		return domain.TorrentProcessing, "verification"
	case t.Status == 0:
		if t.SrvMaint {
			return domain.TorrentProcessing, "server maintenance"
		}
		return domain.TorrentProcessing, "paused at provider"
	case t.Status&1 != 0:
		if t.SrvMaint {
			return domain.TorrentProcessing, "server maintenance"
		}
		return domain.TorrentProcessing, ""
	}
	return domain.TorrentProcessing, strconv.Itoa(t.Status)
}

func mapTorrent(t torrent) provider.Torrent {
	status, msg := mapStatus(t)
	out := provider.Torrent{
		ID: t.ID, Hash: strings.ToLower(t.HashString), Name: t.Name, Size: t.TotalSize, Status: status, RawStatus: strconv.Itoa(t.Status),
		Message: msg, Progress: t.DownloadPercent / 100, Speed: t.DownloadSpeed, Seeders: t.PeersConnected,
	}
	if t.Created > 0 {
		ts := time.Unix(t.Created, 0).UTC()
		out.AddedAt = &ts
	}
	// The plain listing collapses many-file torrents into a single ZIP entry
	// (isZip); only the by-id fetch returns the real files. Report no files in
	// that case so the engine keeps/fetches the full list instead of a zip stub.
	if t.IsZip {
		return out
	}
	for _, f := range t.Files {
		out.Files = append(out.Files, domain.File{ID: f.ID, Path: strings.TrimPrefix(f.Name, "/"), Size: f.Size, Selected: true, Link: f.DownloadURL})
	}
	return out
}

// --- Provider implementation ---------------------------------------------------

// User implements provider.Provider.
func (c *Client) User(ctx context.Context) (provider.User, error) {
	var a account
	if err := c.call(ctx, httpx.Request{Path: "account/infos"}, &a); err != nil {
		return provider.User{}, err
	}
	u := provider.User{Username: a.Username, Email: a.Email, Premium: a.PremiumLeft > 0}
	if a.PremiumLeft > 0 {
		t := time.Now().UTC().Add(time.Duration(a.PremiumLeft) * time.Second)
		u.ExpiresAt = &t
		u.Plan = "premium"
	} else {
		u.Plan = "free"
	}
	return u, nil
}

// ListTorrents implements provider.Provider (pages through /seedbox/list).
func (c *Client) ListTorrents(ctx context.Context) ([]provider.Torrent, error) {
	out := []provider.Torrent{}
	for page := 0; ; page++ {
		resp, err := c.http.Do(ctx, httpx.Request{Path: "seedbox/list", Query: url.Values{"page": {strconv.Itoa(page)}, "perPage": {"100"}}, ExpectJSON: true})
		if err != nil {
			return nil, enrich(err)
		}
		var env struct {
			envelope
			Pagination *struct {
				Page  int  `json:"page"`
				Pages int  `json:"pages"`
				Next  *int `json:"next"`
			} `json:"pagination"`
		}
		if err := resp.JSON(&env); err != nil {
			return nil, err
		}
		if !env.Success {
			return nil, mapError(env.Error, env.ErrorDescription, resp.StatusCode)
		}
		var list []torrent
		if len(env.Value) > 0 && string(env.Value) != "null" {
			if err := json.Unmarshal(env.Value, &list); err != nil {
				return nil, &provider.Error{Kind: provider.ErrTransient, Message: "debridlink: decode list: " + err.Error()}
			}
		}
		for _, t := range list {
			out = append(out, mapTorrent(t))
		}
		p := env.Pagination
		if p == nil || len(list) == 0 || p.Next == nil || *p.Next <= page || (p.Pages > 0 && p.Page+1 >= p.Pages) || page > 1000 {
			break
		}
	}
	return out, nil
}

// GetTorrent implements provider.Provider (fetch by id to get the full file list).
func (c *Client) GetTorrent(ctx context.Context, id string) (provider.Torrent, error) {
	var list []torrent
	if err := c.call(ctx, httpx.Request{Path: "seedbox/list", Query: url.Values{"ids": {id}}}, &list); err != nil {
		return provider.Torrent{}, err
	}
	if len(list) == 0 {
		return provider.Torrent{}, &provider.Error{Kind: provider.ErrNotFound, Message: "torrent " + id}
	}
	return mapTorrent(list[0]), nil
}

// AddMagnet implements provider.Provider.
func (c *Client) AddMagnet(ctx context.Context, magnet string) (provider.AddResult, error) {
	var t torrent
	err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "seedbox/add", Form: url.Values{"url": {magnet}}, NoRetry: true}, &t)
	if err != nil {
		return provider.AddResult{}, err
	}
	if t.ID == "" {
		return provider.AddResult{}, &provider.Error{Kind: provider.ErrTransient, Message: "debridlink: add returned no id"}
	}
	return provider.AddResult{ID: t.ID, Hash: strings.ToLower(t.HashString)}, nil
}

// AddTorrentFile implements provider.Provider.
func (c *Client) AddTorrentFile(ctx context.Context, data []byte) (provider.AddResult, error) {
	var t torrent
	mp := &httpx.Multipart{Files: []httpx.MultipartFile{{Field: "file", Filename: "upload.torrent", Data: data}}}
	err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "seedbox/add", Multipart: mp, NoRetry: true}, &t)
	if err != nil {
		return provider.AddResult{}, err
	}
	if t.ID == "" {
		return provider.AddResult{}, &provider.Error{Kind: provider.ErrTransient, Message: "debridlink: add returned no id"}
	}
	return provider.AddResult{ID: t.ID, Hash: strings.ToLower(t.HashString)}, nil
}

// SelectFiles implements provider.Provider (no-op: we add without wait=true).
func (c *Client) SelectFiles(context.Context, string, []string) error { return nil }

// Links implements provider.Provider: files already carry direct download URLs.
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
		if f.Link == "" {
			continue
		}
		out = append(out, provider.Link{FileID: f.ID, Path: f.Path, Size: f.Size, URL: f.Link})
	}
	return out, nil
}

// Unrestrict implements provider.Provider (identity).
func (c *Client) Unrestrict(_ context.Context, link string) (provider.Direct, error) {
	u, err := url.Parse(link)
	if err != nil {
		return provider.Direct{}, provider.Errorf(provider.ErrPermanent, "", "debridlink: bad link %q", link)
	}
	return provider.Direct{URL: link, Filename: path.Base(u.Path), MaxConnections: 8}, nil
}

// Delete implements provider.Provider.
func (c *Client) Delete(ctx context.Context, id string) error {
	err := c.call(ctx, httpx.Request{Method: http.MethodDelete, Path: "seedbox/" + url.PathEscape(id) + "/remove"}, nil)
	if err != nil && provider.KindOf(err) == provider.ErrNotFound {
		return nil
	}
	return err
}

var _ provider.Provider = (*Client)(nil)
