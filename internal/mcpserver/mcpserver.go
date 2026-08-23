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

	"github.com/google/jsonschema-go/jsonschema"
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
	// A panicking tool handler must never take down the process (the SDK has no
	// recovery of its own, and in-process mounting means no http.Server safety net).
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (res mcp.Result, err error) {
			defer func() {
				if r := recover(); r != nil {
					res, err = nil, fmt.Errorf("internal error handling %s: %v", method, r)
				}
			}()
			return next(ctx, method, req)
		}
	})
	t := &tools{cl: cl}
	t.register(s)
	return s
}

// NewHTTPHandler returns a Streamable-HTTP handler for mounting on a mux
// (stateless: each request is independent, fine behind any proxy).
func NewHTTPHandler(cl *apiclient.ClientWithResponses) http.Handler {
	srv := New(cl)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
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

// torrentSettingsIn mirrors the API's TorrentSettings with documentation for
// the model (the generated client type carries no descriptions or enums).
type torrentSettingsIn struct {
	MinFileSize     int64    `json:"min_file_size,omitempty" jsonschema:"Skip files smaller than this many bytes"`
	IncludeRegex    string   `json:"include_regex,omitempty" jsonschema:"Only download paths matching this regex (case-insensitive); overrides exclude_regex"`
	ExcludeRegex    string   `json:"exclude_regex,omitempty" jsonschema:"Skip paths matching this regex (case-insensitive)"`
	FinishedAction  string   `json:"finished_action,omitempty" jsonschema:"What to do at the provider after local completion: keep or remove_from_provider"`
	FinishedDelay   string   `json:"finished_delay,omitempty" jsonschema:"Delay before finished_action as a Go duration, e.g. 10m or 2h"`
	DownloadRetries int      `json:"download_retries,omitempty" jsonschema:"Automatic retries per file (default 3)"`
	TorrentRetries  int      `json:"torrent_retries,omitempty" jsonschema:"Automatic re-adds after a provider-side error (default 1)"`
	DeleteOnError   string   `json:"delete_on_error,omitempty" jsonschema:"Remove the torrent (files and provider copy) this long after a terminal error, e.g. 24h; empty = never"`
	Lifetime        string   `json:"lifetime,omitempty" jsonschema:"Fail if not finished at the provider within this long, e.g. 72h; empty = never"`
	Unpack          *bool    `json:"unpack,omitempty" jsonschema:"Extract archives after download (default true)"`
	ManualFiles     []string `json:"manual_files,omitempty" jsonschema:"Ignored here; use select_files"`
}

func (t torrentSettingsIn) toAPI() apiclient.TorrentSettings {
	dr, tr := int64(t.DownloadRetries), int64(t.TorrentRetries)
	out := apiclient.TorrentSettings{DownloadRetries: &dr, TorrentRetries: &tr}
	if t.MinFileSize != 0 {
		out.MinFileSize = &t.MinFileSize
	}
	if t.IncludeRegex != "" {
		out.IncludeRegex = &t.IncludeRegex
	}
	if t.ExcludeRegex != "" {
		out.ExcludeRegex = &t.ExcludeRegex
	}
	if t.FinishedAction != "" {
		fa := apiclient.TorrentSettingsFinishedAction(t.FinishedAction)
		out.FinishedAction = &fa
	}
	if t.FinishedDelay != "" {
		out.FinishedDelay = &t.FinishedDelay
	}
	if t.DeleteOnError != "" {
		out.DeleteOnError = &t.DeleteOnError
	}
	if t.Lifetime != "" {
		out.Lifetime = &t.Lifetime
	}
	unpack := t.Unpack == nil || *t.Unpack
	out.Unpack = &unpack
	return out
}

type addTorrentIn struct {
	Magnet   string             `json:"magnet" jsonschema:"Magnet URI (magnet:?xt=urn:btih:...)"`
	Account  string             `json:"account,omitempty" jsonschema:"Account id or name; the default account when omitted"`
	Category string             `json:"category,omitempty" jsonschema:"Category, i.e. sub-folder under the download directory"`
	Settings *torrentSettingsIn `json:"settings,omitempty" jsonschema:"Per-torrent settings replacing the configured defaults (whole object); omit to use defaults"`
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
	ID       string             `json:"id" jsonschema:"Torrent id or info hash"`
	Category *string            `json:"category,omitempty" jsonschema:"New category (only before downloads start)"`
	Settings *torrentSettingsIn `json:"settings,omitempty" jsonschema:"Replacement per-torrent settings (whole object; manual file selection is preserved)"`
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
	TorrentDefaults *torrentSettingsIn `json:"torrent_defaults,omitempty" jsonschema:"Default per-torrent settings (whole object replaces the current defaults)"`
	Categories      []string           `json:"categories,omitempty" jsonschema:"Known categories (sub-folders); replaces the current list"`
	UnpackMaxDepth  *int64             `json:"unpack_max_depth,omitempty" jsonschema:"How deep nested archives are extracted (0-5)"`
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
			return res(r, err, 200, func() *apiclient.Status { return r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_torrents", Description: "List torrents with status, progress and errors. Optionally filter by status, account or category.", Annotations: ro,
		InputSchema: withEnum[listTorrentsIn]("status", "queued", "adding", "processing", "waiting_selection", "downloading", "uploading", "finished", "completed", "error")},
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
			return res(r, err, 200, func() *torrentsOut {
				if r.JSON200 == nil {
					return nil
				}
				return &torrentsOut{Torrents: *r.JSON200, Count: len(*r.JSON200)}
			})
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_torrent", Description: "Get one torrent including its file list (with provider file ids) and per-file download progress.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in torrentIDIn) (*mcp.CallToolResult, apiclient.Torrent, error) {
			r, err := t.cl.GetTorrentWithResponse(ctx, in.ID)
			return res(r, err, 200, func() *apiclient.Torrent { return r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "add_torrent", Description: "Add a torrent from a magnet link. It is sent to the debrid provider and, once cached there, its files are downloaded to the server. Returns the new torrent (status 'queued'); poll get_torrent for progress."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in addTorrentIn) (*mcp.CallToolResult, apiclient.Torrent, error) {
			body := apiclient.AddTorrentJSONRequestBody{Magnet: in.Magnet}
			if in.Settings != nil {
				st := in.Settings.toAPI()
				body.Settings = &st
			}
			if in.Account != "" {
				body.Account = &in.Account
			}
			if in.Category != "" {
				body.Category = &in.Category
			}
			r, err := t.cl.AddTorrentWithResponse(ctx, body)
			return res(r, err, 201, func() *apiclient.Torrent { return r.JSON201 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "delete_torrent", Description: "Delete a torrent. By default only the local record is removed; set delete_files and/or delete_from_provider to also remove downloaded files or the provider-side torrent.", Annotations: destructive},
		func(ctx context.Context, _ *mcp.CallToolRequest, in deleteTorrentIn) (*mcp.CallToolResult, okOut, error) {
			r, err := t.cl.DeleteTorrentWithResponse(ctx, in.ID, &apiclient.DeleteTorrentParams{Files: &in.DeleteFiles, Provider: &in.DeleteFromProvider})
			return res(r, err, 204, func() *okOut { return &okOut{OK: true, Message: "deleted " + in.ID} })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "retry_torrent", Description: "Retry an errored (or completed) torrent from scratch: it is re-submitted to the provider and re-downloaded.", Annotations: idem},
		func(ctx context.Context, _ *mcp.CallToolRequest, in torrentIDIn) (*mcp.CallToolResult, apiclient.Torrent, error) {
			r, err := t.cl.RetryTorrentWithResponse(ctx, in.ID)
			return res(r, err, 200, func() *apiclient.Torrent { return r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "select_files", Description: "Restrict a torrent to specific provider file ids (from get_torrent). Unstarted downloads for other files are dropped."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in selectFilesIn) (*mcp.CallToolResult, apiclient.Torrent, error) {
			r, err := t.cl.SelectFilesWithResponse(ctx, in.ID, apiclient.SelectFilesJSONRequestBody{FileIds: in.FileIDs})
			return res(r, err, 200, func() *apiclient.Torrent { return r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "update_torrent", Description: "Change a torrent's category or per-torrent settings."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateTorrentIn) (*mcp.CallToolResult, apiclient.Torrent, error) {
			body := apiclient.UpdateTorrentJSONRequestBody{Category: in.Category}
			if in.Settings != nil {
				st := in.Settings.toAPI()
				body.Settings = &st
			}
			r, err := t.cl.UpdateTorrentWithResponse(ctx, in.ID, body)
			return res(r, err, 200, func() *apiclient.Torrent { return r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "retry_download", Description: "Retry a single failed file download (see get_torrent downloads[]).", Annotations: idem},
		func(ctx context.Context, _ *mcp.CallToolRequest, in downloadIDIn) (*mcp.CallToolResult, apiclient.Download, error) {
			r, err := t.cl.RetryDownloadWithResponse(ctx, in.ID)
			return res(r, err, 200, func() *apiclient.Download { return r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "list_accounts", Description: "List configured debrid provider accounts (no secrets).", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, accountsOut, error) {
			r, err := t.cl.ListAccountsWithResponse(ctx)
			return res(r, err, 200, func() *accountsOut {
				if r.JSON200 == nil {
					return nil
				}
				return &accountsOut{Accounts: *r.JSON200}
			})
		})

	mcp.AddTool(s, &mcp.Tool{Name: "add_account", Description: "Add a debrid provider account. The API key is verified with the provider before saving. The first account becomes the default.",
		InputSchema: withEnum[addAccountIn]("kind", "torbox", "realdebrid", "alldebrid", "premiumize", "debridlink")},
		func(ctx context.Context, _ *mcp.CallToolRequest, in addAccountIn) (*mcp.CallToolResult, apiclient.Account, error) {
			body := apiclient.AddAccountJSONRequestBody{Kind: apiclient.AddAccountInBodyKind(in.Kind), Credentials: apiclient.Credentials{ApiKey: &in.APIKey}}
			if in.Name != "" {
				body.Name = &in.Name
			}
			if in.SetDefault {
				body.SetDefault = &in.SetDefault
			}
			r, err := t.cl.AddAccountWithResponse(ctx, body)
			return res(r, err, 201, func() *apiclient.Account { return r.JSON201 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "test_account", Description: "Verify an account's credentials against its provider and return the provider user info.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in accountIDIn) (*mcp.CallToolResult, apiclient.User, error) {
			r, err := t.cl.TestAccountWithResponse(ctx, in.ID)
			return res(r, err, 200, func() *apiclient.User { return r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_settings", Description: "Get runtime settings: default per-torrent settings, categories, unpack depth.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, apiclient.Settings, error) {
			r, err := t.cl.GetSettingsWithResponse(ctx)
			return res(r, err, 200, func() *apiclient.Settings { return r.JSON200 })
		})

	mcp.AddTool(s, &mcp.Tool{Name: "update_settings", Description: "Update runtime settings. Only the fields you pass are changed (torrent_defaults, when given, replaces the defaults as a whole).", Annotations: idem},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateSettingsIn) (*mcp.CallToolResult, apiclient.Settings, error) {
			cur, err := t.cl.GetSettingsWithResponse(ctx)
			if err != nil || cur.StatusCode() != 200 || cur.JSON200 == nil {
				return res(cur, err, 200, func() *apiclient.Settings { return cur.JSON200 })
			}
			body := *cur.JSON200
			if in.TorrentDefaults != nil {
				body.TorrentDefaults = in.TorrentDefaults.toAPI()
			}
			if in.Categories != nil {
				body.Categories = &in.Categories
			}
			if in.UnpackMaxDepth != nil {
				body.UnpackMaxDepth = in.UnpackMaxDepth
			}
			r, err := t.cl.UpdateSettingsWithResponse(ctx, body)
			return res(r, err, 200, func() *apiclient.Settings { return r.JSON200 })
		})
}

// withEnum infers the input schema for T and constrains one property to enum values.
func withEnum[T any](prop string, values ...string) *jsonschema.Schema {
	sch, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	if p, ok := sch.Properties[prop]; ok {
		p.Enum = make([]any, len(values))
		for i, v := range values {
			p.Enum[i] = v
		}
	}
	return sch
}

// statusResp is implemented by every generated *Response type.
type statusResp interface{ StatusCode() int }

// res converts an API response into a tool result: transport errors and
// non-expected statuses become tool errors (IsError=true) with the API's
// problem detail as the message. out returns the decoded body pointer; nil
// (a 2xx that wasn't JSON — wrong host, proxy page) is an error, not a panic.
func res[R statusResp, Out any](r R, err error, want int, out func() *Out) (*mcp.CallToolResult, Out, error) { //nolint:unparam // SDK handler signature; a nil result means "derive from output"
	var zero Out
	if err != nil {
		return nil, zero, fmt.Errorf("api request failed: %w", err)
	}
	if r.StatusCode() != want {
		return nil, zero, fmt.Errorf("%s", problem(r.StatusCode(), bodyOf(r)))
	}
	v := out()
	if v == nil {
		return nil, zero, fmt.Errorf("unexpected non-JSON response from the debrid-client API (http %d) — is the server URL correct?", r.StatusCode())
	}
	stripSchema(v)
	return nil, *v, nil
}

// stripSchema clears huma's "$schema" link field (reflectively, on the value
// and one level of nested structs/slices) so tool results don't carry a URL
// the model might try to fetch.
func stripSchema(v any) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}
	clear := func(sv reflect.Value) {
		if sv.Kind() != reflect.Struct {
			return
		}
		if f := sv.FieldByName("Schema"); f.IsValid() && f.CanSet() && f.Kind() == reflect.Pointer {
			f.Set(reflect.Zero(f.Type()))
		}
	}
	clear(rv)
	if rv.Kind() == reflect.Struct {
		for i := 0; i < rv.NumField(); i++ {
			f := rv.Field(i)
			switch f.Kind() {
			case reflect.Slice:
				for j := 0; j < f.Len(); j++ {
					clear(f.Index(j))
				}
			case reflect.Struct:
				clear(f)
			}
		}
	}
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
