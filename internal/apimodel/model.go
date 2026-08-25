// Package apimodel defines the JSON shapes exposed by the HTTP API and MCP
// server, plus conversions from domain/service types. Keeping them separate
// from domain lets the internal model evolve without breaking clients.
package apimodel

import (
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/organize"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/service"
)

// Account is a provider account without secrets.
type Account struct {
	ID        string    `json:"id" doc:"Account id"`
	Kind      string    `json:"kind" enum:"torbox,realdebrid,alldebrid,premiumize,debridlink" doc:"Provider kind"`
	Name      string    `json:"name" doc:"Display name (unique)"`
	Enabled   bool      `json:"enabled"`
	IsDefault bool      `json:"is_default" doc:"Used when a torrent is added without an account"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	User      *User     `json:"user,omitempty" doc:"Live provider user info when available"`
}

// User is provider account info.
type User struct {
	Username  string     `json:"username,omitempty"`
	Email     string     `json:"email,omitempty"`
	Premium   bool       `json:"premium"`
	Plan      string     `json:"plan,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Credentials are provider secrets (write-only).
type Credentials struct {
	APIKey string `json:"api_key,omitempty" doc:"API key / token for the provider"`
}

// TorrentSettings are per-torrent knobs.
type TorrentSettings struct {
	MinFileSize     int64    `json:"min_file_size,omitempty" minimum:"0" doc:"Skip files smaller than this many bytes"`
	IncludeRegex    string   `json:"include_regex,omitempty" doc:"Only download paths matching this (case-insensitive); overrides exclude"`
	ExcludeRegex    string   `json:"exclude_regex,omitempty" doc:"Skip paths matching this (case-insensitive)"`
	ManualFiles     []string `json:"manual_files,omitempty" nullable:"false" doc:"Download exactly these provider file ids"`
	FinishedAction  string   `json:"finished_action,omitempty" enum:"keep,remove_from_provider" doc:"What to do at the provider after local completion"`
	FinishedDelay   string   `json:"finished_delay,omitempty" doc:"Delay before finished_action, e.g. 10m (Go duration)"`
	DownloadRetries int      `json:"download_retries" minimum:"0" required:"false" doc:"Automatic retries per file"`
	TorrentRetries  int      `json:"torrent_retries" minimum:"0" required:"false" doc:"Automatic re-adds after a provider error"`
	DeleteOnError   string   `json:"delete_on_error,omitempty" doc:"Remove the torrent (files, provider) this long after a terminal error, e.g. 24h; empty = never"`
	Lifetime        string   `json:"lifetime,omitempty" doc:"Fail if not finished at the provider within this long, e.g. 72h; empty = never"`
	Unpack          bool     `json:"unpack" required:"false" doc:"Extract archives after download"`
	Organize        *bool    `json:"organize,omitempty" doc:"Override the global library-organization toggle for this torrent (absent = inherit)"`
}

// OrganizeSettings control library-style directory layout for new torrents.
type OrganizeSettings struct {
	Enabled       bool   `json:"enabled" required:"false" doc:"Lay out new torrents as a media library (Movie Name (Year)/...)"`
	MovieTemplate string `json:"movie_template,omitempty" doc:"Movie path template; empty = '{title} ({year})'"`
	TVTemplate    string `json:"tv_template,omitempty" doc:"TV path template; empty = '{title} ({year})/Season {season:02}'"`
}

// File is a file within a torrent.
type File struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Selected bool   `json:"selected"`
}

// Download is one local file download.
type Download struct {
	ID          string     `json:"id"`
	TorrentID   string     `json:"torrent_id"`
	FileID      string     `json:"file_id,omitempty"`
	Path        string     `json:"path" doc:"Path relative to the torrent directory"`
	Filename    string     `json:"filename"`
	Size        int64      `json:"size"`
	BytesDone   int64      `json:"bytes_done"`
	Progress    float64    `json:"progress" doc:"0..1"`
	State       string     `json:"state" enum:"pending,unrestricting,downloading,downloaded,unpacking,done,error"`
	Error       string     `json:"error,omitempty"`
	RetryCount  int        `json:"retry_count"`
	QueuedAt    time.Time  `json:"queued_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Torrent is the API view of a torrent.
type Torrent struct {
	ID               string          `json:"id"`
	AccountID        string          `json:"account_id"`
	Hash             string          `json:"hash"`
	Name             string          `json:"name"`
	Category         string          `json:"category,omitempty"`
	Status           string          `json:"status" enum:"queued,adding,processing,waiting_selection,downloading,uploading,finished,completed,error"`
	StatusReason     string          `json:"status_reason,omitempty" doc:"Human-readable detail about the current state"`
	Error            string          `json:"error,omitempty"`
	ProviderProgress float64         `json:"provider_progress" doc:"0..1 progress at the debrid provider"`
	LocalProgress    float64         `json:"local_progress" doc:"0..1 progress of local downloads"`
	Size             int64           `json:"size"`
	Speed            int64           `json:"speed" doc:"Provider-side bytes/sec"`
	Seeders          int             `json:"seeders"`
	ProviderID       string          `json:"provider_id,omitempty"`
	ProviderStatus   string          `json:"provider_status,omitempty" doc:"Raw provider status string"`
	Files            []File          `json:"files"`
	Settings         TorrentSettings `json:"settings"`
	RetryCount       int             `json:"retry_count"`
	AddedAt          time.Time       `json:"added_at"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
	Downloads        []Download      `json:"downloads"`
}

// Settings are runtime settings.
type Settings struct {
	TorrentDefaults TorrentSettings  `json:"torrent_defaults"`
	Categories      []string         `json:"categories" required:"false" nullable:"false"`
	UnpackMaxDepth  int              `json:"unpack_max_depth" minimum:"0" maximum:"5" required:"false"`
	Organize        OrganizeSettings `json:"organize" required:"false"`
}

// Status is the system summary.
type Status struct {
	Version     string         `json:"version"`
	DownloadDir string         `json:"download_dir"`
	Accounts    int            `json:"accounts"`
	Torrents    map[string]int `json:"torrents" doc:"Count by torrent status"`
	Downloads   map[string]int `json:"downloads" doc:"Count by download state"`
	DiskFree    int64          `json:"disk_free_bytes"`
	DiskTotal   int64          `json:"disk_total_bytes"`
}

// --- conversions --------------------------------------------------------------

func FromAccount(a service.AccountView) Account {
	out := Account{ID: a.ID, Kind: string(a.Kind), Name: a.Name, Enabled: a.Enabled, IsDefault: a.IsDefault, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
	if a.User != nil {
		u := FromUser(*a.User)
		out.User = &u
	}
	return out
}

func FromUser(u provider.User) User {
	return User{Username: u.Username, Email: u.Email, Premium: u.Premium, Plan: u.Plan, ExpiresAt: u.ExpiresAt}
}

func (c Credentials) ToDomain() domain.Credentials { return domain.Credentials{APIKey: c.APIKey} }

func FromTorrentSettings(s domain.TorrentSettings) TorrentSettings {
	return TorrentSettings{
		MinFileSize: s.MinFileSize, IncludeRegex: s.IncludeRegex, ExcludeRegex: s.ExcludeRegex, ManualFiles: s.ManualFiles,
		FinishedAction: string(s.FinishedAction), FinishedDelay: durStr(s.FinishedDelay),
		DownloadRetries: s.DownloadRetries, TorrentRetries: s.TorrentRetries,
		DeleteOnError: durStr(s.DeleteOnError), Lifetime: durStr(s.Lifetime), Unpack: s.Unpack,
		Organize: s.Organize,
	}
}

// ToDomain converts settings, validating durations.
func (s TorrentSettings) ToDomain() (domain.TorrentSettings, error) {
	fd, err := parseDur(s.FinishedDelay)
	if err != nil {
		return domain.TorrentSettings{}, err
	}
	de, err := parseDur(s.DeleteOnError)
	if err != nil {
		return domain.TorrentSettings{}, err
	}
	lt, err := parseDur(s.Lifetime)
	if err != nil {
		return domain.TorrentSettings{}, err
	}
	fa := domain.FinishedAction(s.FinishedAction)
	if fa == "" {
		fa = domain.FinishedKeep
	}
	return domain.TorrentSettings{
		MinFileSize: s.MinFileSize, IncludeRegex: s.IncludeRegex, ExcludeRegex: s.ExcludeRegex, ManualFiles: s.ManualFiles,
		FinishedAction: fa, FinishedDelay: fd, DownloadRetries: s.DownloadRetries, TorrentRetries: s.TorrentRetries,
		DeleteOnError: de, Lifetime: lt, Unpack: s.Unpack, Organize: s.Organize,
	}, nil
}

func FromDownload(d domain.Download) Download {
	return Download{
		ID: d.ID, TorrentID: d.TorrentID, FileID: d.FileID, Path: d.RelPath, Filename: d.Filename, Size: d.Size, BytesDone: d.BytesDone,
		Progress: d.Progress(), State: string(d.State), Error: d.Error, RetryCount: d.RetryCount,
		QueuedAt: d.QueuedAt, StartedAt: d.StartedAt, FinishedAt: d.FinishedAt, CompletedAt: d.CompletedAt,
	}
}

func FromTorrent(d service.TorrentDetail) Torrent {
	t := d.Torrent
	out := Torrent{
		ID: t.ID, AccountID: t.AccountID, Hash: t.Hash, Name: t.Name, Category: t.Category,
		Status: string(t.Status), StatusReason: t.StatusReason, Error: t.Error,
		ProviderProgress: t.Progress, LocalProgress: d.LocalProgress(), Size: t.Size, Speed: t.Speed, Seeders: t.Seeders,
		ProviderID: t.ProviderID, ProviderStatus: t.ProviderStatus, Settings: FromTorrentSettings(t.Settings),
		RetryCount: t.RetryCount, AddedAt: t.AddedAt, CompletedAt: t.CompletedAt,
		Files: make([]File, 0, len(t.Files)), Downloads: make([]Download, 0, len(d.Downloads)),
	}
	for _, f := range t.Files {
		out.Files = append(out.Files, File{ID: f.ID, Path: f.Path, Size: f.Size, Selected: f.Selected})
	}
	for _, dl := range d.Downloads {
		out.Downloads = append(out.Downloads, FromDownload(dl))
	}
	return out
}

func FromSettings(s service.Settings) Settings {
	return Settings{
		TorrentDefaults: FromTorrentSettings(s.TorrentDefaults), Categories: s.Categories, UnpackMaxDepth: s.UnpackMaxDepth,
		Organize: OrganizeSettings{Enabled: s.Organize.Enabled, MovieTemplate: s.Organize.MovieTemplate, TVTemplate: s.Organize.TVTemplate},
	}
}

func (s Settings) ToService() (service.Settings, error) {
	td, err := s.TorrentDefaults.ToDomain()
	if err != nil {
		return service.Settings{}, err
	}
	return service.Settings{
		TorrentDefaults: td, Categories: s.Categories, UnpackMaxDepth: s.UnpackMaxDepth,
		Organize: organize.Settings{Enabled: s.Organize.Enabled, MovieTemplate: s.Organize.MovieTemplate, TVTemplate: s.Organize.TVTemplate},
	}, nil
}

func FromStatus(s service.SystemStatus) Status {
	out := Status{Version: s.Version, DownloadDir: s.DownloadDir, Accounts: s.Accounts, DiskFree: s.DiskFree, DiskTotal: s.DiskTotal,
		Torrents: map[string]int{}, Downloads: map[string]int{}}
	for k, v := range s.Torrents {
		out.Torrents[string(k)] = v
	}
	for k, v := range s.Downloads {
		out.Downloads[string(k)] = v
	}
	return out
}

func durStr(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.String()
}

func parseDur(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
