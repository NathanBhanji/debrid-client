// Package mcpserver exposes the debrid-client API as Model Context Protocol
// tools using the official Go SDK. Tools are implemented once on top of the
// generated HTTP API client, so the same server works over stdio (talking to a
// remote debrid-client) and mounted in-process at /mcp (via an in-memory
// transport into the API handler).
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NathanBhanji/debrid-client/internal/apiclient"
	"github.com/NathanBhanji/debrid-client/internal/buildinfo"
)

// New builds an MCP server whose tools call the API through cl.
func New(cl *apiclient.ClientWithResponses) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "debrid-client", Title: "debrid-client", Version: buildinfo.Version}, &mcp.ServerOptions{
		Instructions: "Manage torrents on debrid providers (TorBox, Real-Debrid, …) and download their files to the server's disk. " +
			"Typical flow: list_accounts (or add_account once) → add_torrent with a magnet link → list_torrents / get_torrent to watch progress → files land under the download directory. " +
			"Torrent ids and info hashes are interchangeable wherever an id is accepted.",
	})
	t := &tools{cl: cl}
	t.register(s)
	return s
}

// NewHTTPHandler returns a Streamable-HTTP handler for mounting on a mux
// (stateless: each request is independent, fine behind any proxy).
func NewHTTPHandler(cl *apiclient.ClientWithResponses) http.Handler {
	srv := New(cl)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true})
}

type tools struct {
	cl *apiclient.ClientWithResponses
}

// --- tool inputs -----------------------------------------------------------------

type listTorrentsIn struct {
	Status   string `json:"status,omitempty" jsonschema:"Filter by status: queued, adding, processing, waiting_selection, downloading, uploading, finished, completed or error"`
	Account  string `json:"account,omitempty" jsonschema:"Filter by account id or name"`
	Category string `json:"category,omitempty" jsonschema:"Filter by category"`
}
type torrentIDIn struct {
	ID string `json:"id" jsonschema:"Torrent id or info hash"`
}
type addTorrentIn struct {
	Magnet   string                     `json:"magnet" jsonschema:"Magnet URI (magnet:?xt=urn:btih:...)"`
	Account  string                     `json:"account,omitempty" jsonschema:"Account id or name; the default account when omitted"`
	Category string                     `json:"category,omitempty" jsonschema:"Category, i.e. sub-folder under the download directory"`
	Settings *apiclient.TorrentSettings `json:"settings,omitempty" jsonschema:"Per-torrent overrides (filters, retries, finished action); omit to use defaults"`
}
type deleteTorrentIn struct {
	ID                 string `json:"id" jsonschema:"Torrent id or info hash"`
	DeleteFiles        bool   `json:"delete_files,omitempty" jsonschema:"Also delete downloaded files from disk"`
	DeleteFromProvider bool   `json:"delete_from_provider,omitempty" jsonschema:"Also remove the torrent at the debrid provider"`
}
type selectFilesIn struct {
	ID      string   `json:"id" jsonschema:"Torrent id or info hash"`
	FileIDs []string `json:"file_ids" jsonschema:"Provider file ids to download (see get_torrent files[].id)"`
}
type updateTorrentIn struct {
	ID       string                     `json:"id" jsonschema:"Torrent id or info hash"`
	Category *string                    `json:"category,omitempty" jsonschema:"New category (only before downloads start)"`
	Settings *apiclient.TorrentSettings `json:"settings,omitempty" jsonschema:"Replacement per-torrent settings"`
}
type downloadIDIn struct {
	ID string `json:"id" jsonschema:"Download id (see get_torrent downloads[].id)"`
}
type accountIDIn struct {
	ID string `json:"id" jsonschema:"Account id or name"`
}
type addAccountIn struct {
	Kind       string `json:"kind" jsonschema:"Provider kind: torbox, realdebrid, alldebrid, premiumize or debridlink"`
	Name       string `json:"name,omitempty" jsonschema:"Display name; defaults to the kind"`
	APIKey     string `json:"api_key" jsonschema:"The provider's API key"`
	SetDefault bool   `json:"set_default,omitempty" jsonschema:"Make this the default account"`
}
type updateSettingsIn struct {
	Settings apiclient.Settings `json:"settings" jsonschema:"Full settings document as returned by get_settings"`
}

type okOut struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}
type torrentsOut struct {
	Torrents []apiclient.Torrent `json:"torrents"`
	Count    int                 `json:"count"`
}
type accountsOut struct {
	Accounts []apiclient.Account `json:"accounts"`
}

// --- registration -------------------------------------------------------------------

func (t *tools) register(s *mcp.Server) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	destructive := &mcp.ToolAnnotations{DestructiveHint: ptr(true)}
	idem := &mcp.ToolAnnotations{IdempotentHint: true}

	mcp.AddTool(s, &mcp.Tool{Name: "system_status", Description: "Server version, account count, torrent/download counts by state and free disk space.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, apiclient.Status, error) {
			r, err := t.cl.GetStatusWithResponse(ctx)
			return res(r, err, 200, func() apiclient.Status { return *r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_torrents", Description: "List torrents with status, progress and errors. Optionally filter by status, account or category.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listTorrentsIn) (*mcp.CallToolResult, torrentsOut, error) {
			p := &apiclient.ListTorrentsParams{}
			if in.Status != "" {
				p.Status = ptr(apiclient.ListTorrentsParamsStatus(in.Status))
			}
			if in.Account != "" {
				p.Account = &in.Account
			}
			if in.Category != "" {
				p.Category = &in.Category
			}
			r, err := t.cl.ListTorrentsWithResponse(ctx, p)
			return res(r, err, 200, func() torrentsOut { return torrentsOut{Torrents: *r.JSON200, Count: len(*r.JSON200)} })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_torrent", Description: "Get one torrent including its file list (with provider file ids) and per-file download progress.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in torrentIDIn) (*mcp.CallToolResult, apiclient.Torrent, error) {
			r, err := t.cl.GetTorrentWithResponse(ctx, in.ID)
			return res(r, err, 200, func() apiclient.Torrent { return *r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "add_torrent", Description: "Add a torrent from a magnet link. It is sent to the debrid provider and, once cached there, its files are downloaded to the server. Returns the new torrent (status 'queued'); poll get_torrent for progress."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in addTorrentIn) (*mcp.CallToolResult, apiclient.Torrent, error) {
			body := apiclient.AddTorrentJSONRequestBody{Magnet: in.Magnet, Settings: in.Settings}
			if in.Account != "" {
				body.Account = &in.Account
			}
			if in.Category != "" {
				body.Category = &in.Category
			}
			r, err := t.cl.AddTorrentWithResponse(ctx, body)
			return res(r, err, 201, func() apiclient.Torrent { return *r.JSON201 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "delete_torrent", Description: "Delete a torrent. By default only the local record is removed; set delete_files and/or delete_from_provider to also remove downloaded files or the provider-side torrent.", Annotations: destructive},
		func(ctx context.Context, _ *mcp.CallToolRequest, in deleteTorrentIn) (*mcp.CallToolResult, okOut, error) {
			r, err := t.cl.DeleteTorrentWithResponse(ctx, in.ID, &apiclient.DeleteTorrentParams{Files: &in.DeleteFiles, Provider: &in.DeleteFromProvider})
			return res(r, err, 204, func() okOut { return okOut{OK: true, Message: "deleted " + in.ID} })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "retry_torrent", Description: "Retry an errored (or completed) torrent from scratch: it is re-submitted to the provider and re-downloaded.", Annotations: idem},
		func(ctx context.Context, _ *mcp.CallToolRequest, in torrentIDIn) (*mcp.CallToolResult, apiclient.Torrent, error) {
			r, err := t.cl.RetryTorrentWithResponse(ctx, in.ID)
			return res(r, err, 200, func() apiclient.Torrent { return *r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "select_files", Description: "Restrict a torrent to specific provider file ids (from get_torrent). Unstarted downloads for other files are dropped."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in selectFilesIn) (*mcp.CallToolResult, apiclient.Torrent, error) {
			r, err := t.cl.SelectFilesWithResponse(ctx, in.ID, apiclient.SelectFilesJSONRequestBody{FileIds: in.FileIDs})
			return res(r, err, 200, func() apiclient.Torrent { return *r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "update_torrent", Description: "Change a torrent's category or per-torrent settings."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateTorrentIn) (*mcp.CallToolResult, apiclient.Torrent, error) {
			r, err := t.cl.UpdateTorrentWithResponse(ctx, in.ID, apiclient.UpdateTorrentJSONRequestBody{Category: in.Category, Settings: in.Settings})
			return res(r, err, 200, func() apiclient.Torrent { return *r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "retry_download", Description: "Retry a single failed file download (see get_torrent downloads[]).", Annotations: idem},
		func(ctx context.Context, _ *mcp.CallToolRequest, in downloadIDIn) (*mcp.CallToolResult, apiclient.Download, error) {
			r, err := t.cl.RetryDownloadWithResponse(ctx, in.ID)
			return res(r, err, 200, func() apiclient.Download { return *r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_accounts", Description: "List configured debrid provider accounts (no secrets).", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, accountsOut, error) {
			r, err := t.cl.ListAccountsWithResponse(ctx)
			return res(r, err, 200, func() accountsOut { return accountsOut{Accounts: *r.JSON200} })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "add_account", Description: "Add a debrid provider account. The API key is verified with the provider before saving. The first account becomes the default."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in addAccountIn) (*mcp.CallToolResult, apiclient.Account, error) {
			body := apiclient.AddAccountJSONRequestBody{Kind: apiclient.AddAccountInBodyKind(in.Kind), Credentials: apiclient.Credentials{ApiKey: &in.APIKey}}
			if in.Name != "" {
				body.Name = &in.Name
			}
			if in.SetDefault {
				body.SetDefault = &in.SetDefault
			}
			r, err := t.cl.AddAccountWithResponse(ctx, body)
			return res(r, err, 201, func() apiclient.Account { return *r.JSON201 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "test_account", Description: "Verify an account's credentials against its provider and return the provider user info.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in accountIDIn) (*mcp.CallToolResult, apiclient.User, error) {
			r, err := t.cl.TestAccountWithResponse(ctx, in.ID)
			return res(r, err, 200, func() apiclient.User { return *r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_settings", Description: "Get runtime settings: default per-torrent settings, categories, unpack depth.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, apiclient.Settings, error) {
			r, err := t.cl.GetSettingsWithResponse(ctx)
			return res(r, err, 200, func() apiclient.Settings { return *r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "update_settings", Description: "Replace runtime settings. Pass the full document from get_settings with your changes applied.", Annotations: idem},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateSettingsIn) (*mcp.CallToolResult, apiclient.Settings, error) {
			r, err := t.cl.UpdateSettingsWithResponse(ctx, in.Settings)
			return res(r, err, 200, func() apiclient.Settings { return *r.JSON200 })
		})
}

// statusResp is implemented by every generated *Response type.
type statusResp interface{ StatusCode() int }

// res converts an API response into a tool result: transport errors and
// non-expected statuses become tool errors (IsError=true) with the API's
// problem detail as the message.
func res[R statusResp, Out any](r R, err error, want int, out func() Out) (*mcp.CallToolResult, Out, error) { //nolint:unparam // SDK handler signature; a nil result means "derive from output"
	var zero Out
	if err != nil {
		return nil, zero, fmt.Errorf("api request failed: %w", err)
	}
	if r.StatusCode() != want {
		return nil, zero, fmt.Errorf("%s", problem(r.StatusCode(), bodyOf(r)))
	}
	return nil, out(), nil
}

// bodyOf reads the exported `Body []byte` field every generated response has.
func bodyOf(r any) []byte {
	v := reflect.ValueOf(r)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	f := v.FieldByName("Body")
	if !f.IsValid() || f.Kind() != reflect.Slice || f.Type().Elem().Kind() != reflect.Uint8 {
		return nil
	}
	return f.Bytes()
}

func problem(status int, body []byte) string {
	var em apiclient.ErrorModel
	if json.Unmarshal(body, &em) == nil && (em.Detail != nil || em.Title != nil) {
		parts := []string{}
		if em.Title != nil {
			parts = append(parts, *em.Title)
		}
		if em.Detail != nil {
			parts = append(parts, *em.Detail)
		}
		msg := strings.Join(parts, ": ")
		if em.Errors != nil {
			for _, e := range *em.Errors {
				if e.Message != nil {
					msg += "; " + *e.Message
					if e.Location != nil {
						msg += " (" + *e.Location + ")"
					}
				}
			}
		}
		return msg
	}
	if len(body) > 0 {
		return fmt.Sprintf("http %d: %s", status, strings.TrimSpace(string(body)))
	}
	return fmt.Sprintf("http %d", status)
}

func ptr[T any](v T) *T { return &v }
