package api

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/buildinfo"
	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/organize"
	"github.com/NathanBhanji/debrid-client/internal/relname"
	"github.com/NathanBhanji/debrid-client/internal/service"
)

// --- request/response types ----------------------------------------------------

type healthOut struct {
	Body struct {
		OK           bool     `json:"ok"`
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities" doc:"Additive feature identifiers; missing features must not be assumed"`
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
	Status   string `query:"status" enum:"queued,adding,processing,waiting_selection,downloading,uploading,finished,completed,error,cancelled"`
	Account  string `query:"account" doc:"Account id or name"`
	Category string `query:"category"`
}
type listTorrentsOut struct{ Body []apimodel.Torrent }
type torrentOut struct{ Body apimodel.Torrent }
type torrentIDIn struct {
	ID string `path:"id" doc:"Torrent id or info hash"`
}
type addTorrentOut struct {
	Body     apimodel.Torrent
	Replayed bool `header:"X-Debrid-Submission-Replayed" doc:"True when this key replays a prior accepted local enqueue"`
}
type submissionIn struct {
	Key string `path:"key" maxLength:"38" doc:"Original UUIDv4 submission key"`
}
type submissionOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         apimodel.TorrentSubmission
}
type addTorrentIn struct {
	SubmissionKey string `header:"Idempotency-Key" maxLength:"38" doc:"Optional fresh UUIDv4 (bare or quoted). Persist and reuse only with the identical submitted intent until reconciled."`
	Body          struct {
		Magnet   string                    `json:"magnet" doc:"Magnet URI"`
		Account  string                    `json:"account,omitempty" doc:"Account id or name; default account when empty"`
		Category string                    `json:"category,omitempty"`
		Settings *apimodel.TorrentSettings `json:"settings,omitempty" doc:"Full per-torrent settings replacing the configured defaults (not merged)"`
	}
}
type addTorrentFileIn struct {
	SubmissionKey string `header:"Idempotency-Key" maxLength:"38" doc:"Optional fresh UUIDv4 (bare or quoted); same reconciliation contract as magnet adds"`
	RawBody       huma.MultipartFormFiles[struct {
		File     huma.FormFile `form:"file" contentType:"application/x-bittorrent,application/octet-stream" required:"true"`
		Account  string        `form:"account" required:"false"`
		Category string        `form:"category" required:"false"`
		Settings string        `form:"settings" required:"false" maxLength:"16384" doc:"Optional JSON TorrentSettings object, full replacement of defaults, not merged (16 KiB maximum)"`
	}]
}
type deleteTorrentIn struct {
	IfMatch  string `header:"If-Match" maxLength:"66" doc:"Optional single strong quoted revision from removal-review; native clients require this. Weak/wildcard/list values are rejected."`
	ID       string `path:"id"`
	Files    bool   `query:"files" doc:"Delete downloaded files"`
	Provider bool   `query:"provider" doc:"Delete at the debrid provider too"`
}
type updateTorrentIn struct {
	ID   string `path:"id"`
	Body struct {
		Category *string                   `json:"category,omitempty"`
		Settings *apimodel.TorrentSettings `json:"settings,omitempty" doc:"Full settings replacement except the preserved manual selection/hold; choose the hold at add time and confirm through file-selection"`
	}
}
type selectFilesIn struct {
	ID   string `path:"id"`
	Body struct {
		FileIDs []string `json:"file_ids" minItems:"1" nullable:"false"`
	}
}
type fileSelectionOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         apimodel.FileSelection
}
type confirmFileSelectionIn struct {
	ID   string `path:"id"`
	Body struct {
		Revision string   `json:"revision" minLength:"64" maxLength:"64" pattern:"^[a-f0-9]{64}$"`
		FileIDs  []string `json:"file_ids" minItems:"1" maxItems:"20000" nullable:"false"`
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
	settingsSchema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[apimodel.TorrentSettings](), true, "")

	huma.Register(api, huma.Operation{OperationID: "health", Method: http.MethodGet, Path: p + "/health", Summary: "Health check", Tags: []string{"system"}, Security: []map[string][]string{}, Metadata: map[string]any{metaPublic: true}},
		func(context.Context, *struct{}) (*healthOut, error) {
			out := &healthOut{}
			out.Body.OK = true
			out.Body.Version = buildinfo.Version
			out.Body.Capabilities = []string{"torrent_submissions_v1", "multipart_torrent_settings_v1", "manual_file_selection_v1", "native_retry_v1", "native_cancellation_v1", "native_removal_precondition_v1"}
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
	huma.Register(api, huma.Operation{OperationID: "add-torrent", Method: http.MethodPost, Path: p + "/torrents", Summary: "Add a torrent from a magnet link", Description: submissionContract, Tags: []string{"torrents"}, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *addTorrentIn) (*addTorrentOut, error) {
			req := service.AddTorrentInput{SubmissionKey: in.SubmissionKey, Magnet: in.Body.Magnet, Account: in.Body.Account, Category: in.Body.Category}
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
			return &addTorrentOut{Body: apimodel.FromTorrent(t), Replayed: t.SubmissionReplayed}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "add-torrent-file", Method: http.MethodPost, Path: p + "/torrents/file", Summary: "Add a torrent from a .torrent file (multipart)", Description: submissionContract, Tags: []string{"torrents"}, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *addTorrentFileIn) (*addTorrentOut, error) {
			data := in.RawBody.Data()
			b, err := io.ReadAll(io.LimitReader(data.File, h.opts.MaxUploadBytes+1))
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("read file: " + err.Error())
			}
			if int64(len(b)) > h.opts.MaxUploadBytes {
				return nil, huma.Error413RequestEntityTooLarge("torrent file too large")
			}
			req := service.AddTorrentInput{SubmissionKey: in.SubmissionKey, TorrentFile: b, Account: data.Account, Category: data.Category}
			if len(in.RawBody.Form.File["settings"]) > 0 || len(in.RawBody.Form.Value["settings"]) > 1 {
				return nil, huma.Error422UnprocessableEntity("settings must be one JSON text field")
			}
			if _, present := in.RawBody.Form.Value["settings"]; present {
				settings, err := decodeUploadSettings(data.Settings, settingsSchema, api.OpenAPI().Components.Schemas)
				if err != nil {
					return nil, err
				}
				req.Settings = &settings
			}
			t, err := h.svc.AddTorrent(ctx, req)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &addTorrentOut{Body: apimodel.FromTorrent(t), Replayed: t.SubmissionReplayed}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-torrent-submission", Method: http.MethodGet, Path: p + "/torrent-submissions/{key}", Summary: "Reconcile an accepted torrent submission", Description: "200 proves a prior local enqueue, even after deletion. 404 means no committed receipt is visible: an original request may still be running. Retry only the same immutable intent with the same key; never mint another key automatically.", Tags: []string{"torrents"}}, func(ctx context.Context, in *submissionIn) (*submissionOut, error) {
		receipt, err := h.svc.GetTorrentSubmission(ctx, in.Key)
		if err != nil {
			return nil, huma.ErrorWithHeaders(h.mapErr(err), http.Header{"Cache-Control": []string{"no-store"}})
		}
		return &submissionOut{CacheControl: "no-store", Body: apimodel.TorrentSubmission{TorrentID: receipt.TorrentID, AcceptedAt: receipt.CreatedAt, TorrentExists: receipt.TorrentExists}}, nil
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
	huma.Register(api, huma.Operation{OperationID: "delete-torrent", Method: http.MethodDelete, Path: p + "/torrents/{id}", Summary: "Delete a torrent", Tags: []string{"torrents"}, DefaultStatus: http.StatusNoContent, Middlewares: huma.Middlewares{retryNoStore, h.validateRemovalIfMatch}},
		func(ctx context.Context, in *deleteTorrentIn) (*struct{}, error) {
			var revision string
			if in.IfMatch != "" {
				var err error
				revision, err = parseRemovalIfMatch(in.IfMatch)
				if err != nil {
					return nil, err
				}
			}
			return nil, h.mapErr(h.svc.DeleteTorrent(ctx, in.ID, service.DeleteOptions{DeleteFiles: in.Files, DeleteFromProvider: in.Provider, ExpectedRevision: revision}))
		})
	huma.Register(api, huma.Operation{OperationID: "retry-torrent", Method: http.MethodPost, Path: p + "/torrents/{id}/retry", Summary: "Retry an errored, completed or cancelled torrent from scratch", Tags: []string{"torrents"}},
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
	huma.Register(api, huma.Operation{OperationID: "get-file-selection", Method: http.MethodGet, Path: p + "/torrents/{id}/file-selection", Summary: "Review authoritative provider files before NAS download", Tags: []string{"torrents"}}, func(ctx context.Context, in *torrentIDIn) (*fileSelectionOut, error) {
		view, err := h.svc.GetFileSelection(ctx, in.ID)
		if err != nil {
			return nil, h.mapErr(err)
		}
		return &fileSelectionOut{CacheControl: "no-store", Body: apimodel.FromFileSelection(view)}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "confirm-file-selection", Method: http.MethodPut, Path: p + "/torrents/{id}/file-selection", Summary: "Confirm one immutable reviewed file selection", Tags: []string{"torrents"}}, func(ctx context.Context, in *confirmFileSelectionIn) (*fileSelectionOut, error) {
		view, err := h.svc.ConfirmFileSelection(ctx, in.ID, in.Body.Revision, in.Body.FileIDs)
		if err != nil {
			return nil, h.mapErr(err)
		}
		return &fileSelectionOut{CacheControl: "no-store", Body: apimodel.FromFileSelection(view)}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "retry-download", Method: http.MethodPost, Path: p + "/downloads/{id}/retry", Summary: "Retry a failed download", Tags: []string{"torrents"}},
		func(ctx context.Context, in *downloadIDIn) (*downloadOut, error) {
			d, err := h.svc.RetryDownload(ctx, in.ID)
			if err != nil {
				return nil, h.mapErr(err)
			}
			return &downloadOut{Body: apimodel.FromDownload(d)}, nil
		})

	h.registerRetryRoutes(p)
	h.registerCancellationRoutes(p)
	h.registerRemovalReviewRoute(p)

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

	// Preview where a release name would land under organization; templates
	// in the request override the saved ones so the UI can try edits before
	// saving. Enabled is implied — previewing is the point.
	huma.Register(api, huma.Operation{OperationID: "organize-preview", Method: http.MethodPost, Path: p + "/organize/preview", Summary: "Preview the organized folder for a release name", Tags: []string{"settings"}},
		func(ctx context.Context, in *organizePreviewIn) (*organizePreviewOut, error) {
			for _, tpl := range []string{in.Body.MovieTemplate, in.Body.TVTemplate} {
				if tpl != "" {
					if err := organize.ValidateTemplate(tpl); err != nil {
						return nil, huma.Error422UnprocessableEntity(err.Error())
					}
				}
			}
			st, err := h.svc.GetSettings(ctx)
			if err != nil {
				return nil, h.mapErr(err)
			}
			org := st.Organize
			org.Enabled = true
			if in.Body.MovieTemplate != "" {
				org.MovieTemplate = in.Body.MovieTemplate
			}
			if in.Body.TVTemplate != "" {
				org.TVTemplate = in.Body.TVTemplate
			}
			path, organized := service.DirPathFor(domain.Torrent{Name: in.Body.Name, Hash: "preview"}, org)
			out := &organizePreviewOut{}
			out.Body.Path = path
			out.Body.Organized = organized
			if info, ok := relname.Parse(in.Body.Name); ok {
				out.Body.Parsed = &organizeParsed{
					Title: info.Title, Year: info.Year, Season: info.Season, Episode: info.Episode,
					Resolution: info.Resolution, Source: info.Source, Codec: info.Codec, Group: info.Group, TV: info.IsTV,
				}
			}
			return out, nil
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

type organizePreviewIn struct {
	Body struct {
		Name          string `json:"name" minLength:"1" maxLength:"512" doc:"Release name to preview"`
		MovieTemplate string `json:"movie_template,omitempty" doc:"Override the saved movie template"`
		TVTemplate    string `json:"tv_template,omitempty" doc:"Override the saved TV template"`
	}
}

type organizeParsed struct {
	Title      string `json:"title"`
	Year       int    `json:"year,omitempty"`
	Season     int    `json:"season,omitempty"`
	Episode    int    `json:"episode,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Source     string `json:"source,omitempty"`
	Codec      string `json:"codec,omitempty"`
	Group      string `json:"group,omitempty"`
	TV         bool   `json:"tv,omitempty"`
}

type organizePreviewOut struct {
	Body struct {
		Path      string          `json:"path" doc:"Folder path the torrent would use (relative to the download dir / category)"`
		Organized bool            `json:"organized" doc:"False when the name would keep the raw-name folder"`
		Parsed    *organizeParsed `json:"parsed,omitempty" doc:"Release metadata when the name parsed"`
	}
}
