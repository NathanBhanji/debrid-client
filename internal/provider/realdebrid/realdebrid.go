// Package realdebrid implements provider.Provider for Real-Debrid.
//
// API notes (see docs/research/provider-realdebrid.md):
//   - Base https://api.real-debrid.com/rest/1.0/, Bearer token (private API
//     token from the control panel, or an OAuth access token). 250 req/min.
//   - Errors: HTTP 4xx/5xx with {"error": msg, "error_code": int}.
//   - Torrents need explicit SelectFiles after add (status waiting_files_selection).
//   - Finished torrents expose links[] (real-debrid.com/d/...), normally one
//     per selected file in file order. RD may also repack files into fewer
//     archives, or split very large files into several links; in either case
//     the count differs from the selection and the links are reported with
//     placeholder paths (the unrestricted filename is authoritative).
//     Each link must be unrestricted via /unrestrict/link for a direct URL.
//   - Credentials: the private API token from the RD control panel. OAuth
//     access tokens are accepted but not refreshed (they expire after ~1h);
//     device-flow refresh is a follow-up.
package realdebrid

import (
	"context"
	"encoding/json"
	"errors"
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
const DefaultBaseURL = "https://api.real-debrid.com/rest/1.0/"

func init() {
	provider.Register(domain.ProviderRealDebrid, New)
}

// Client is a Real-Debrid provider.
type Client struct {
	http *httpx.Client
}

// New constructs a Real-Debrid provider. APIKey (private token) or AccessToken is required.
func New(creds domain.Credentials, opts provider.Options) (provider.Provider, error) {
	token := creds.APIKey
	if token == "" {
		token = creds.AccessToken
	}
	if token == "" {
		return nil, provider.Errorf(provider.ErrAuth, "", "realdebrid: api token is required")
	}
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	hc, err := httpx.New(httpx.Config{
		BaseURL: base, UserAgent: opts.UserAgent, Auth: httpx.BearerAuth(token),
		Limiter: httpx.PerMinute(250), MaxAttempts: 3, Timeout: 60 * time.Second, HTTPClient: opts.HTTPClient,
	})
	if err != nil {
		return nil, err
	}
	return &Client{http: hc}, nil
}

// Kind implements provider.Provider.
func (c *Client) Kind() domain.ProviderKind { return domain.ProviderRealDebrid }

// Caps implements provider.Provider.
func (c *Client) Caps() provider.Caps {
	return provider.Caps{SelectFiles: true, CacheCheck: false, DirectLinks: false, MaxConnections: 16}
}

// --- wire types --------------------------------------------------------------

type apiError struct {
	Error     string `json:"error"`
	ErrorCode int    `json:"error_code"`
}

type userData struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	Email      string `json:"email"`
	Type       string `json:"type"`
	Premium    int64  `json:"premium"`
	Expiration string `json:"expiration"`
}

type torrentData struct {
	ID       string     `json:"id"`
	Filename string     `json:"filename"`
	Hash     string     `json:"hash"`
	Bytes    int64      `json:"bytes"`
	Split    int        `json:"split"`
	Progress float64    `json:"progress"`
	Status   string     `json:"status"`
	Added    string     `json:"added"`
	Ended    string     `json:"ended"`
	Speed    int64      `json:"speed"`
	Seeders  int        `json:"seeders"`
	Links    []string   `json:"links"`
	Files    []fileData `json:"files"`
}

type fileData struct {
	ID       int    `json:"id"`
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Selected int    `json:"selected"`
}

type addData struct {
	ID  string `json:"id"`
	URI string `json:"uri"`
}

type unrestrictData struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Filesize int64  `json:"filesize"`
	Download string `json:"download"`
	Chunks   int    `json:"chunks"`
}

// --- helpers -----------------------------------------------------------------

// call performs a request and maps RD error bodies. For 2xx with a body, out is decoded.
func (c *Client) call(ctx context.Context, req httpx.Request, out any) error {
	resp, err := c.http.Do(ctx, req)
	if err != nil {
		return enrich(err)
	}
	if resp.StatusCode/100 != 2 {
		return mapBody(resp.StatusCode, resp.Body)
	}
	if out != nil && len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, out); err != nil {
			return &provider.Error{Kind: provider.ErrTransient, HTTPStatus: resp.StatusCode, Message: "decode: " + err.Error(), Err: err}
		}
	}
	return nil
}

// enrich refines httpx-classified errors (401/403/404/429/5xx) using RD's
// error body. RD uses 403 for many non-auth conditions (infringing file,
// torrent too big, permission denied…), so the error_code wins over the HTTP
// status except for genuine auth codes.
func enrich(err error) error {
	var pe *provider.Error
	if !errors.As(err, &pe) || len(pe.Body) == 0 {
		return err
	}
	var ae apiError
	if json.Unmarshal(pe.Body, &ae) != nil || ae.Error == "" {
		return err
	}
	m := mapCode(ae, pe.HTTPStatus)
	if pe.Kind == provider.ErrRateLimited || (pe.Kind == provider.ErrNotFound && m.Kind == provider.ErrPermanent) {
		m.Kind = pe.Kind
	}
	m.RetryAfter = pe.RetryAfter
	return m
}

func mapBody(status int, body []byte) *provider.Error {
	var ae apiError
	if json.Unmarshal(body, &ae) == nil && ae.Error != "" {
		return mapCode(ae, status)
	}
	kind := provider.ErrTransient
	if status >= 400 && status < 500 {
		kind = provider.ErrPermanent
	}
	return &provider.Error{Kind: kind, HTTPStatus: status, Message: strings.TrimSpace(string(body))}
}

// mapCode classifies RD error_code values (see docs/research/provider-realdebrid.md).
func mapCode(ae apiError, status int) *provider.Error {
	kind := provider.ErrPermanent
	switch ae.ErrorCode {
	case 8, 9, 10, 11, 12, 13, 14, 15, 22: // bad token, permission, 2FA, login, locked, not activated, IP not allowed
		kind = provider.ErrAuth
	case 5, 34: // slow down, too many requests
		kind = provider.ErrRateLimited
	case 7, 24: // resource not found, file unavailable
		kind = provider.ErrNotFound
	case 18, 21, 23, 36: // hoster limit, too many active downloads, traffic exhausted, fair usage — account-level, clear with time
		kind = provider.ErrLimit
	case 26, 29: // upload too big, torrent too big — never clears for this torrent
		kind = provider.ErrPermanent
	case -1, 6, 17, 19, 25, 27, 37: // internal, unreachable, hoster maintenance/unavailable, service unavailable, upload error, disabled endpoint
		kind = provider.ErrTransient
	case 33: // torrent already active → caller dedupes by hash; treat as permanent (no retry)
		kind = provider.ErrPermanent
	}
	if status >= 500 {
		kind = provider.ErrTransient
	}
	return &provider.Error{Kind: kind, Code: strconv.Itoa(ae.ErrorCode), Message: ae.Error, HTTPStatus: status}
}

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		t = t.UTC()
		return &t
	}
	return nil
}

func mapStatus(s string) (domain.TorrentStatus, string) {
	switch s {
	case "magnet_conversion", "queued":
		return domain.TorrentProcessing, ""
	case "waiting_files_selection":
		return domain.TorrentWaitingSelection, ""
	case "downloading":
		return domain.TorrentDownloading, ""
	case "compressing", "uploading":
		return domain.TorrentUploading, ""
	case "downloaded":
		return domain.TorrentFinished, ""
	case "magnet_error":
		return domain.TorrentError, "invalid or unresolvable magnet"
	case "virus":
		return domain.TorrentError, "flagged as virus by provider"
	case "dead":
		return domain.TorrentError, "dead torrent (no seeders)"
	case "error":
		return domain.TorrentError, "provider error"
	}
	return domain.TorrentProcessing, s
}

func mapTorrent(t torrentData) provider.Torrent {
	status, msg := mapStatus(t.Status)
	out := provider.Torrent{
		ID: t.ID, Hash: strings.ToLower(t.Hash), Name: t.Filename, Size: t.Bytes,
		Status: status, RawStatus: t.Status, Message: msg, Progress: t.Progress / 100,
		Speed: t.Speed, Seeders: t.Seeders, AddedAt: parseTime(t.Added), EndedAt: parseTime(t.Ended),
	}
	for _, f := range t.Files {
		out.Files = append(out.Files, domain.File{
			ID: strconv.Itoa(f.ID), Path: strings.TrimPrefix(f.Path, "/"), Size: f.Bytes, Selected: f.Selected == 1,
		})
	}
	return out
}

// --- Provider implementation ---------------------------------------------------

// User implements provider.Provider.
func (c *Client) User(ctx context.Context) (provider.User, error) {
	var u userData
	if err := c.call(ctx, httpx.Request{Path: "user", ExpectJSON: true}, &u); err != nil {
		return provider.User{}, err
	}
	return provider.User{Username: u.Username, Email: u.Email, Premium: u.Type == "premium", Plan: u.Type, ExpiresAt: parseTime(u.Expiration)}, nil
}

// ListTorrents implements provider.Provider. Pages through /torrents (limit 5000).
func (c *Client) ListTorrents(ctx context.Context) ([]provider.Torrent, error) {
	var out []provider.Torrent
	for page := 1; ; page++ {
		var list []torrentData
		q := url.Values{"page": {strconv.Itoa(page)}, "limit": {"5000"}}
		if err := c.call(ctx, httpx.Request{Path: "torrents", Query: q, ExpectJSON: true}, &list); err != nil {
			return nil, err
		}
		for _, t := range list {
			out = append(out, mapTorrent(t))
		}
		if len(list) < 5000 {
			break
		}
	}
	if out == nil {
		out = []provider.Torrent{}
	}
	return out, nil
}

// fetch loads /torrents/info/{id} (files + links).
func (c *Client) fetch(ctx context.Context, id string) (torrentData, error) {
	var t torrentData
	if err := c.call(ctx, httpx.Request{Path: "torrents/info/" + url.PathEscape(id), ExpectJSON: true}, &t); err != nil {
		return torrentData{}, err
	}
	return t, nil
}

// GetTorrent implements provider.Provider (includes files).
func (c *Client) GetTorrent(ctx context.Context, id string) (provider.Torrent, error) {
	t, err := c.fetch(ctx, id)
	if err != nil {
		return provider.Torrent{}, err
	}
	return mapTorrent(t), nil
}

// AddMagnet implements provider.Provider.
func (c *Client) AddMagnet(ctx context.Context, magnet string) (provider.AddResult, error) {
	var a addData
	err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "torrents/addMagnet", Form: url.Values{"magnet": {magnet}}, NoRetry: true, ExpectJSON: true}, &a)
	if err != nil {
		return provider.AddResult{}, err
	}
	return provider.AddResult{ID: a.ID}, nil
}

// AddTorrentFile implements provider.Provider.
func (c *Client) AddTorrentFile(ctx context.Context, data []byte) (provider.AddResult, error) {
	var a addData
	err := c.call(ctx, httpx.Request{Method: http.MethodPut, Path: "torrents/addTorrent", Body: data, ContentType: "application/x-bittorrent", NoRetry: true, ExpectJSON: true}, &a)
	if err != nil {
		return provider.AddResult{}, err
	}
	return provider.AddResult{ID: a.ID}, nil
}

// SelectFiles implements provider.Provider. At least one id is required (the
// engine always filters first; "select nothing" must not mean "select all").
func (c *Client) SelectFiles(ctx context.Context, id string, fileIDs []string) error {
	if len(fileIDs) == 0 {
		return provider.Errorf(provider.ErrPermanent, "", "realdebrid: no files selected")
	}
	files := strings.Join(fileIDs, ",")
	// 204 on success, 202 when already done (both 2xx → nil); 404 code 7 for bad ids.
	return c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "torrents/selectFiles/" + url.PathEscape(id), Form: url.Values{"files": {files}}}, nil)
}

// Links implements provider.Provider. When the torrent is downloaded RD
// returns one link per selected file, in file order, and we map them 1:1.
// If the counts differ (RD repacked files into archives, or split a large
// file into several links) the mapping is unknowable, so every link is
// reported with a placeholder path and Size 0; the engine downloads all of
// them and takes the real filename from the unrestricted link.
func (c *Client) Links(ctx context.Context, id string) ([]provider.Link, error) {
	td, err := c.fetch(ctx, id)
	if err != nil {
		return nil, err
	}
	t := mapTorrent(td)
	if t.Status != domain.TorrentFinished || len(td.Links) == 0 {
		return nil, nil
	}
	var selected []domain.File
	for _, f := range t.Files {
		if f.Selected {
			selected = append(selected, f)
		}
	}
	out := make([]provider.Link, 0, len(td.Links))
	if len(td.Links) == len(selected) {
		for i, l := range td.Links {
			out = append(out, provider.Link{FileID: selected[i].ID, Path: selected[i].Path, Size: selected[i].Size, URL: l})
		}
		return out, nil
	}
	for i, l := range td.Links {
		out = append(out, provider.Link{
			FileID: "link-" + strconv.Itoa(i+1),
			Path:   fmt.Sprintf("%s/%s.part%d", t.Name, t.Name, i+1), // placeholder; real name comes from Unrestrict
			URL:    l,
		})
	}
	return out, nil
}

// Unrestrict implements provider.Provider.
func (c *Client) Unrestrict(ctx context.Context, link string) (provider.Direct, error) {
	var u unrestrictData
	err := c.call(ctx, httpx.Request{Method: http.MethodPost, Path: "unrestrict/link", Form: url.Values{"link": {link}}, ExpectJSON: true}, &u)
	if err != nil {
		return provider.Direct{}, err
	}
	if u.Download == "" {
		return provider.Direct{}, &provider.Error{Kind: provider.ErrTransient, Message: "realdebrid: unrestrict returned no download url"}
	}
	return provider.Direct{URL: u.Download, Filename: u.Filename, Size: u.Filesize, MaxConnections: u.Chunks}, nil
}

// Delete implements provider.Provider.
func (c *Client) Delete(ctx context.Context, id string) error {
	err := c.call(ctx, httpx.Request{Method: http.MethodDelete, Path: "torrents/delete/" + url.PathEscape(id)}, nil)
	if err != nil && provider.KindOf(err) == provider.ErrNotFound {
		return nil
	}
	return err
}

var _ provider.Provider = (*Client)(nil)
