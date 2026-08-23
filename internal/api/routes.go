package api

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/buildinfo"
	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/service"
)

// --- request/response types ----------------------------------------------------

type healthOut struct {
	Body struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
}

type statusOut struct{ Body apimodel.Status }

type listAccountsOut struct{ Body []apimodel.Account }
type accountOut struct{ Body apimodel.Account }
type accountIDIn struct {
	ID string `path:"id" doc:"Account id or name"`
}
type addAccountIn struct {
	Body struct {
		Kind        string               `json:"kind" enum:"torbox,realdebrid,alldebrid,premiumize,debridlink"`
		Name        string               `json:"name,omitempty" doc:"Display name; defaults to the kind"`
		Credentials apimodel.Credentials `json:"credentials"`
		SetDefault  bool                 `json:"set_default,omitempty"`
	}
}
type updateAccountIn struct {
	ID   string `path:"id"`
	Body struct {
		Name        *string               `json:"name,omitempty"`
		Credentials *apimodel.Credentials `json:"credentials,omitempty"`
		Enabled     *bool                 `json:"enabled,omitempty"`
		SetDefault  bool                  `json:"set_default,omitempty"`
	}
}
type deleteAccountIn struct {
	ID    string `path:"id"`
	Force bool   `query:"force" doc:"Also delete the account's torrents (locally)"`
}
type userOut struct{ Body apimodel.User }

type listTorrentsIn struct {
	Status   string `query:"status" enum:"queued,adding,processing,waiting_selection,downloading,uploading,finished,completed,error"`
	Account  string `query:"account" doc:"Account id or name"`
	Category string `query:"category"`
}
type listTorrentsOut struct{ Body []apimodel.Torrent }
type torrentOut struct{ Body apimodel.Torrent }
type torrentIDIn struct {
	ID string `path:"id" doc:"Torrent id or info hash"`
}
type addTorrentIn struct {
	Body struct {
		Magnet   string                    `json:"magnet" doc:"Magnet URI"`
		Account  string                    `json:"account,omitempty" doc:"Account id or name; default account when empty"`
		Category string                    `json:"category,omitempty"`
		Settings *apimodel.TorrentSettings `json:"settings,omitempty" doc:"Full per-torrent settings replacing the configured defaults (not merged)"`
	}
}
type addTorrentFileIn struct {
	RawBody huma.MultipartFormFiles[struct {
		File     huma.FormFile `form:"file" contentType:"application/x-bittorrent,application/octet-stream" required:"true"`
		Account  string        `form:"account" required:"false"`
		Category string        `form:"category" required:"false"`
	}]
}
type deleteTorrentIn struct {
	ID       string `path:"id"`
	Files    bool   `query:"files" doc:"Delete downloaded files"`
	Provider bool   `query:"provider" doc:"Delete at the debrid provider too"`
}
type updateTorrentIn struct {
	ID   string `path:"id"`
	Body struct {
		Category *string                   `json:"category,omitempty"`
		Settings *apimodel.TorrentSettings `json:"settings,omitempty" doc:"Full replacement of the torrent's settings (manual file selection is preserved; use the files endpoint for that)"`
	}
}
type selectFilesIn struct {
	ID   string `path:"id"`
	Body struct {
		FileIDs []string `json:"file_ids" minItems:"1" nullable:"false"`
	}
}
type downloadIDIn struct {
	ID string `path:"id"`
}
type downloadOut struct{ Body apimodel.Download }
type settingsOut struct{ Body apimodel.Settings }
type settingsIn struct{ Body apimodel.Settings }

// --- registration --------------------------------------------------------------

func (h *Handler) registerRoutes(p string) {
	api := h.Huma

	huma.Register(api, huma.Operation{OperationID: "health", Method: http.MethodGet, Path: p + "/health", Summary: "Health check", Tags: []string{"system"}, Security: []map[string][]string{}, Metadata: map[string]any{metaPublic: true}},
		func(context.Context, *struct{}) (*healthOut, error) {
			out := &healthOut{}
			out.Body.OK = true
			out.Body.Version = buildinfo.Version
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-status", Method: http.MethodGet, Path: p + "/system/status", Summary: "System status", Tags: []string{"system"}},
		func(ctx context.Context, _ *struct{}) (*statusOut, error) {
			st, err := h.svc.Status(ctx)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &statusOut{Body: apimodel.FromStatus(st)}, nil
		})

	// Accounts
	huma.Register(api, huma.Operation{OperationID: "list-accounts", Method: http.MethodGet, Path: p + "/accounts", Summary: "List provider accounts", Tags: []string{"accounts"}},
		func(ctx context.Context, _ *struct{}) (*listAccountsOut, error) {
			accs, err := h.svc.ListAccounts(ctx)
			if err != nil {
				return nil, h.mapErr(err)
			}
			out := &listAccountsOut{Body: make([]apimodel.Account, 0, len(accs))}
			for _, a := range accs {
				out.Body = append(out.Body, apimodel.FromAccount(a))
			}
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "add-account", Method: http.MethodPost, Path: p + "/accounts", Summary: "Add a provider account", Description: "Credentials are verified against the provider before saving. The first account becomes the default.", Tags: []string{"accounts"}, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *addAccountIn) (*accountOut, error) {
			a, err := h.svc.AddAccount(ctx, service.AddAccountInput{Kind: domain.ProviderKind(in.Body.Kind), Name: in.Body.Name, Credentials: in.Body.Credentials.ToDomain(), SetDefault: in.Body.SetDefault})
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &accountOut{Body: apimodel.FromAccount(a)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-account", Method: http.MethodGet, Path: p + "/accounts/{id}", Summary: "Get an account", Tags: []string{"accounts"}},
		func(ctx context.Context, in *accountIDIn) (*accountOut, error) {
			a, err := h.svc.GetAccount(ctx, in.ID)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &accountOut{Body: apimodel.FromAccount(a)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-account", Method: http.MethodPatch, Path: p + "/accounts/{id}", Summary: "Update an account", Tags: []string{"accounts"}},
		func(ctx context.Context, in *updateAccountIn) (*accountOut, error) {
			upd := service.UpdateAccountInput{Name: in.Body.Name, Enabled: in.Body.Enabled, SetDefault: in.Body.SetDefault}
			if in.Body.Credentials != nil {
				c := in.Body.Credentials.ToDomain()
				upd.Credentials = &c
			}
			a, err := h.svc.UpdateAccount(ctx, in.ID, upd)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &accountOut{Body: apimodel.FromAccount(a)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-account", Method: http.MethodDelete, Path: p + "/accounts/{id}", Summary: "Delete an account", Tags: []string{"accounts"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *deleteAccountIn) (*struct{}, error) {
			return nil, h.mapErr(h.svc.DeleteAccount(ctx, in.ID, in.Force))
		})
	huma.Register(api, huma.Operation{OperationID: "test-account", Method: http.MethodPost, Path: p + "/accounts/{id}/test", Summary: "Verify an account against its provider", Tags: []string{"accounts"}},
		func(ctx context.Context, in *accountIDIn) (*userOut, error) {
			u, err := h.svc.TestAccount(ctx, in.ID)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &userOut{Body: apimodel.FromUser(u)}, nil
		})

	// Torrents
	huma.Register(api, huma.Operation{OperationID: "list-torrents", Method: http.MethodGet, Path: p + "/torrents", Summary: "List torrents", Tags: []string{"torrents"}},
		func(ctx context.Context, in *listTorrentsIn) (*listTorrentsOut, error) {
			ts, err := h.svc.ListTorrents(ctx, service.ListFilter{Status: domain.TorrentStatus(in.Status), Account: in.Account, Category: in.Category})
			if err != nil {
				return nil, h.mapErr(err)
			}
			out := &listTorrentsOut{Body: make([]apimodel.Torrent, 0, len(ts))}
			for _, t := range ts {
				out.Body = append(out.Body, apimodel.FromTorrent(t))
			}
			return out, nil
		})
	huma.Register(api, huma.Operation{OperationID: "add-torrent", Method: http.MethodPost, Path: p + "/torrents", Summary: "Add a torrent from a magnet link", Tags: []string{"torrents"}, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *addTorrentIn) (*torrentOut, error) {
			req := service.AddTorrentInput{Magnet: in.Body.Magnet, Account: in.Body.Account, Category: in.Body.Category}
			if in.Body.Settings != nil {
				s, err := in.Body.Settings.ToDomain()
				if err != nil {
					return nil, huma.Error422UnprocessableEntity("settings: " + err.Error())
				}
				req.Settings = &s
			}
			t, err := h.svc.AddTorrent(ctx, req)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &torrentOut{Body: apimodel.FromTorrent(t)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "add-torrent-file", Method: http.MethodPost, Path: p + "/torrents/file", Summary: "Add a torrent from a .torrent file (multipart)", Tags: []string{"torrents"}, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *addTorrentFileIn) (*torrentOut, error) {
			data := in.RawBody.Data()
			b, err := io.ReadAll(io.LimitReader(data.File, h.opts.MaxUploadBytes+1))
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("read file: " + err.Error())
			}
			if int64(len(b)) > h.opts.MaxUploadBytes {
				return nil, huma.Error413RequestEntityTooLarge("torrent file too large")
			}
			t, err := h.svc.AddTorrent(ctx, service.AddTorrentInput{TorrentFile: b, Account: data.Account, Category: data.Category})
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &torrentOut{Body: apimodel.FromTorrent(t)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-torrent", Method: http.MethodGet, Path: p + "/torrents/{id}", Summary: "Get a torrent", Tags: []string{"torrents"}},
		func(ctx context.Context, in *torrentIDIn) (*torrentOut, error) {
			t, err := h.svc.GetTorrent(ctx, in.ID)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &torrentOut{Body: apimodel.FromTorrent(t)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-torrent", Method: http.MethodPatch, Path: p + "/torrents/{id}", Summary: "Update a torrent's category or settings", Tags: []string{"torrents"}},
		func(ctx context.Context, in *updateTorrentIn) (*torrentOut, error) {
			upd := service.UpdateTorrentInput{Category: in.Body.Category}
			if in.Body.Settings != nil {
				s, err := in.Body.Settings.ToDomain()
				if err != nil {
					return nil, huma.Error422UnprocessableEntity("settings: " + err.Error())
				}
				upd.Settings = &s
			}
			t, err := h.svc.UpdateTorrent(ctx, in.ID, upd)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &torrentOut{Body: apimodel.FromTorrent(t)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-torrent", Method: http.MethodDelete, Path: p + "/torrents/{id}", Summary: "Delete a torrent", Tags: []string{"torrents"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *deleteTorrentIn) (*struct{}, error) {
			return nil, h.mapErr(h.svc.DeleteTorrent(ctx, in.ID, service.DeleteOptions{DeleteFiles: in.Files, DeleteFromProvider: in.Provider}))
		})
	huma.Register(api, huma.Operation{OperationID: "retry-torrent", Method: http.MethodPost, Path: p + "/torrents/{id}/retry", Summary: "Retry an errored or completed torrent from scratch", Tags: []string{"torrents"}},
		func(ctx context.Context, in *torrentIDIn) (*torrentOut, error) {
			t, err := h.svc.RetryTorrent(ctx, in.ID)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &torrentOut{Body: apimodel.FromTorrent(t)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "select-files", Method: http.MethodPut, Path: p + "/torrents/{id}/files", Summary: "Select which files to download", Tags: []string{"torrents"}},
		func(ctx context.Context, in *selectFilesIn) (*torrentOut, error) {
			t, err := h.svc.SelectFiles(ctx, in.ID, in.Body.FileIDs)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &torrentOut{Body: apimodel.FromTorrent(t)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "retry-download", Method: http.MethodPost, Path: p + "/downloads/{id}/retry", Summary: "Retry a failed download", Tags: []string{"torrents"}},
		func(ctx context.Context, in *downloadIDIn) (*downloadOut, error) {
			d, err := h.svc.RetryDownload(ctx, in.ID)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &downloadOut{Body: apimodel.FromDownload(d)}, nil
		})

	// Settings
	huma.Register(api, huma.Operation{OperationID: "get-settings", Method: http.MethodGet, Path: p + "/settings", Summary: "Get runtime settings", Tags: []string{"settings"}},
		func(ctx context.Context, _ *struct{}) (*settingsOut, error) {
			s, err := h.svc.GetSettings(ctx)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &settingsOut{Body: apimodel.FromSettings(s)}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-settings", Method: http.MethodPut, Path: p + "/settings", Summary: "Replace runtime settings", Tags: []string{"settings"}},
		func(ctx context.Context, in *settingsIn) (*settingsOut, error) {
			s, err := in.Body.ToService()
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			got, err := h.svc.UpdateSettings(ctx, s)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &settingsOut{Body: apimodel.FromSettings(got)}, nil
		})

	// Events (SSE)
	// huma/sse picks the SSE event name by the Go type of the payload, so each
	// event name gets its own (identical) type.
	sse.Register(api, huma.Operation{OperationID: "events", Method: http.MethodGet, Path: p + "/events", Summary: "Stream change events (Server-Sent Events)",
		Description: "Event names: torrent.added, torrent.updated, torrent.deleted, download.updated, account.changed, settings.changed, heartbeat (every 15s). " +
			"Payloads are small notifications; re-fetch the resource. Slow consumers may miss events — re-sync on reconnect. " +
			"Authenticate with the Bearer header or ?api_key= (EventSource cannot set headers).",
		Tags: []string{"system"}, Metadata: map[string]any{metaQueryKey: true}},
		map[string]any{
			string(events.TorrentAdded): evTorrentAdded{}, string(events.TorrentUpdated): evTorrentUpdated{}, string(events.TorrentDeleted): evTorrentDeleted{},
			string(events.DownloadUpdated): evDownloadUpdated{}, string(events.AccountChanged): evAccountChanged{}, string(events.SettingsChanged): evSettingsChanged{},
			"heartbeat": heartbeat{},
		},
		func(ctx context.Context, _ *struct{}, send sse.Sender) {
			ch := h.svc.Events().Subscribe(ctx, 128)
			tick := time.NewTicker(15 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if err := send.Data(heartbeat{At: time.Now().UTC()}); err != nil {
						return
					}
				case e, ok := <-ch:
					if !ok {
						return
					}
					if err := send.Data(typedEvent(e)); err != nil {
						return
					}
				}
			}
		})
}

type heartbeat struct {
	At time.Time `json:"at"`
}

type (
	evTorrentAdded    events.Event
	evTorrentUpdated  events.Event
	evTorrentDeleted  events.Event
	evDownloadUpdated events.Event
	evAccountChanged  events.Event
	evSettingsChanged events.Event
)

// typedEvent wraps an event in the type registered for its name.
func typedEvent(e events.Event) any {
	switch e.Type {
	case events.TorrentAdded:
		return evTorrentAdded(e)
	case events.TorrentUpdated:
		return evTorrentUpdated(e)
	case events.TorrentDeleted:
		return evTorrentDeleted(e)
	case events.DownloadUpdated:
		return evDownloadUpdated(e)
	case events.AccountChanged:
		return evAccountChanged(e)
	case events.SettingsChanged:
		return evSettingsChanged(e)
	}
	return evTorrentUpdated(e)
}
