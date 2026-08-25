// Package domain holds the core types and pure logic of the download manager:
// torrents, downloads, provider accounts, their states and valid transitions.
// It has no I/O and no dependencies on storage or providers.
package domain

import (
	"errors"
	"fmt"
	"time"
)

// ProviderKind identifies a debrid service.
type ProviderKind string

const (
	ProviderTorBox     ProviderKind = "torbox"
	ProviderRealDebrid ProviderKind = "realdebrid"
	ProviderAllDebrid  ProviderKind = "alldebrid"
	ProviderPremiumize ProviderKind = "premiumize"
	ProviderDebridLink ProviderKind = "debridlink"
)

// AllProviderKinds lists every supported provider kind.
func AllProviderKinds() []ProviderKind {
	return []ProviderKind{ProviderTorBox, ProviderRealDebrid, ProviderAllDebrid, ProviderPremiumize, ProviderDebridLink}
}

// Valid reports whether k is a known provider kind.
func (k ProviderKind) Valid() bool {
	for _, v := range AllProviderKinds() {
		if v == k {
			return true
		}
	}
	return false
}

// ProviderAccount is a configured credential set for a provider.
type ProviderAccount struct {
	ID          string
	Kind        ProviderKind
	Name        string
	Credentials Credentials
	Enabled     bool
	IsDefault   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Credentials holds provider secrets. Only the fields relevant to the provider
// kind are populated.
type Credentials struct {
	APIKey       string     `json:"api_key,omitempty"`
	AccessToken  string     `json:"access_token,omitempty"`
	RefreshToken string     `json:"refresh_token,omitempty"`
	ClientID     string     `json:"client_id,omitempty"`
	ClientSecret string     `json:"client_secret,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// TorrentStatus is the provider-neutral lifecycle state of a torrent.
type TorrentStatus string

const (
	// TorrentQueued: accepted locally, not yet sent to the provider.
	TorrentQueued TorrentStatus = "queued"
	// TorrentAdding: an add request to the provider is in flight.
	TorrentAdding TorrentStatus = "adding"
	// TorrentProcessing: provider has it (metadata / magnet conversion / queued there).
	TorrentProcessing TorrentStatus = "processing"
	// TorrentWaitingSelection: provider needs files selected before it starts.
	TorrentWaitingSelection TorrentStatus = "waiting_selection"
	// TorrentDownloading: provider is fetching the content.
	TorrentDownloading TorrentStatus = "downloading"
	// TorrentUploading: provider is moving/compressing/uploading to its CDN.
	TorrentUploading TorrentStatus = "uploading"
	// TorrentFinished: content is available at the provider; local downloads may be running.
	TorrentFinished TorrentStatus = "finished"
	// TorrentCompleted: all local work is done.
	TorrentCompleted TorrentStatus = "completed"
	// TorrentError: terminal failure (may be retried explicitly).
	TorrentError TorrentStatus = "error"
)

// IsTerminal reports whether no further automatic progress is expected.
func (s TorrentStatus) IsTerminal() bool {
	return s == TorrentCompleted || s == TorrentError
}

// AtProvider reports whether the torrent exists at the provider in this state.
func (s TorrentStatus) AtProvider() bool {
	switch s {
	case TorrentProcessing, TorrentWaitingSelection, TorrentDownloading, TorrentUploading, TorrentFinished, TorrentCompleted:
		return true
	}
	return false
}

// ErrInvalidTransition is returned when a state change is not permitted.
var ErrInvalidTransition = errors.New("invalid state transition")

var torrentTransitions = map[TorrentStatus][]TorrentStatus{
	// queued → provider states directly covers "adopted an existing provider torrent" (dedupe by hash).
	TorrentQueued:           {TorrentAdding, TorrentProcessing, TorrentWaitingSelection, TorrentDownloading, TorrentUploading, TorrentFinished, TorrentError},
	TorrentAdding:           {TorrentQueued, TorrentProcessing, TorrentWaitingSelection, TorrentDownloading, TorrentUploading, TorrentFinished, TorrentError},
	TorrentProcessing:       {TorrentWaitingSelection, TorrentDownloading, TorrentUploading, TorrentFinished, TorrentError},
	TorrentWaitingSelection: {TorrentProcessing, TorrentDownloading, TorrentUploading, TorrentFinished, TorrentError},
	TorrentDownloading:      {TorrentProcessing, TorrentUploading, TorrentFinished, TorrentError},
	TorrentUploading:        {TorrentDownloading, TorrentFinished, TorrentError},
	TorrentFinished:         {TorrentCompleted, TorrentError},
	TorrentCompleted:        {TorrentQueued},
	TorrentError:            {TorrentQueued, TorrentFinished}, // finished: resume local work after a download-level error
}

// CanTransition reports whether a torrent may move from one status to another.
// Staying in the same status is always allowed (progress updates).
func (s TorrentStatus) CanTransition(to TorrentStatus) bool {
	if s == to {
		return true
	}
	for _, t := range torrentTransitions[s] {
		if t == to {
			return true
		}
	}
	return false
}

// DownloadState is the lifecycle of a single local file download.
type DownloadState string

const (
	DownloadPending       DownloadState = "pending"
	DownloadUnrestricting DownloadState = "unrestricting"
	DownloadDownloading   DownloadState = "downloading"
	DownloadDownloaded    DownloadState = "downloaded"
	DownloadUnpacking     DownloadState = "unpacking"
	DownloadDone          DownloadState = "done"
	DownloadError         DownloadState = "error"
)

// IsTerminal reports whether the download needs no more work.
func (s DownloadState) IsTerminal() bool { return s == DownloadDone || s == DownloadError }

var downloadTransitions = map[DownloadState][]DownloadState{
	DownloadPending:       {DownloadUnrestricting, DownloadDownloading, DownloadError},
	DownloadUnrestricting: {DownloadDownloading, DownloadPending, DownloadError},
	DownloadDownloading:   {DownloadDownloaded, DownloadPending, DownloadError},
	DownloadDownloaded:    {DownloadUnpacking, DownloadDone, DownloadError},
	DownloadUnpacking:     {DownloadDone, DownloadError},
	DownloadDone:          {},
	DownloadError:         {DownloadPending},
}

// CanTransition reports whether a download may move from one state to another.
func (s DownloadState) CanTransition(to DownloadState) bool {
	if s == to {
		return true
	}
	for _, t := range downloadTransitions[s] {
		if t == to {
			return true
		}
	}
	return false
}

// PayloadKind says what Torrent.Payload contains.
type PayloadKind string

const (
	PayloadMagnet PayloadKind = "magnet"
	PayloadFile   PayloadKind = "file"
)

// FinishedAction is what happens at the provider once local work completes.
type FinishedAction string

const (
	// FinishedKeep leaves the torrent at the provider.
	FinishedKeep FinishedAction = "keep"
	// FinishedRemoveFromProvider deletes it from the provider after FinishedDelay.
	FinishedRemoveFromProvider FinishedAction = "remove_from_provider"
)

// TorrentSettings are per-torrent knobs, usually copied from defaults at add time.
type TorrentSettings struct {
	// MinFileSize excludes files smaller than this many bytes (0 = no minimum).
	MinFileSize int64 `json:"min_file_size,omitempty"`
	// IncludeRegex, when set, selects only matching file paths (wins over ExcludeRegex).
	IncludeRegex string `json:"include_regex,omitempty"`
	// ExcludeRegex drops matching file paths.
	ExcludeRegex string `json:"exclude_regex,omitempty"`
	// ManualFiles, when non-empty, selects exactly these provider file IDs.
	ManualFiles []string `json:"manual_files,omitempty"`
	// FinishedAction and FinishedDelay control provider-side cleanup.
	FinishedAction FinishedAction `json:"finished_action,omitempty"`
	FinishedDelay  time.Duration  `json:"finished_delay,omitempty"`
	// Organize overrides the global library-organization toggle for this
	// torrent (nil = inherit).
	Organize *bool `json:"organize,omitempty"`
	// DownloadRetries is the max automatic retries per file.
	DownloadRetries int `json:"download_retries"`
	// TorrentRetries is the max automatic re-adds of the whole torrent after a provider error.
	TorrentRetries int `json:"torrent_retries"`
	// DeleteOnError removes the torrent this long after a terminal error (0 = never).
	DeleteOnError time.Duration `json:"delete_on_error,omitempty"`
	// Lifetime fails the torrent if it hasn't finished at the provider within this long (0 = never).
	Lifetime time.Duration `json:"lifetime,omitempty"`
	// Unpack controls whether archives are extracted after download.
	Unpack bool `json:"unpack"`
}

// DefaultTorrentSettings are the built-in defaults.
func DefaultTorrentSettings() TorrentSettings {
	return TorrentSettings{
		FinishedAction:  FinishedKeep,
		DownloadRetries: 3,
		TorrentRetries:  1,
		Unpack:          true,
	}
}

// File is a file inside a torrent as reported by the provider.
type File struct {
	ID       string `json:"id"`   // provider file id
	Path     string `json:"path"` // path within the torrent, "/"-separated, no leading slash
	Size     int64  `json:"size"`
	Selected bool   `json:"selected"`
	Link     string `json:"link,omitempty"` // provider (restricted) link when known
}

// Torrent is a torrent tracked by this client.
type Torrent struct {
	ID        string
	AccountID string
	Hash      string
	Name      string
	// DirName is the sanitised folder path under the download dir (a single
	// component, or "a/b" segments when organized), frozen by the engine when
	// local downloads start (Name may still change before that).
	DirName string
	// Organized marks a DirName produced by library organization: such
	// directories may be shared between torrents, so deletion is per-file.
	Organized bool
	Category  string

	Status       TorrentStatus
	StatusReason string
	Error        string
	Progress     float64 // provider-side 0..1
	Size         int64
	Speed        int64
	Seeders      int

	ProviderID     string
	ProviderStatus string
	Files          []File
	Settings       TorrentSettings

	PayloadKind PayloadKind
	Payload     []byte

	RetryCount      int
	AddedAt         time.Time
	ProviderAddedAt *time.Time
	ProviderEndedAt *time.Time
	FilesSelectedAt *time.Time
	CompletedAt     *time.Time
	RetryAt         *time.Time
	UpdatedAt       time.Time
}

// Transition moves the torrent to a new status, validating the change.
func (t *Torrent) Transition(to TorrentStatus, reason string) error {
	if !t.Status.CanTransition(to) {
		return fmt.Errorf("%w: torrent %s → %s", ErrInvalidTransition, t.Status, to)
	}
	t.Status = to
	t.StatusReason = reason
	return nil
}

// Download is one file being fetched from the provider to local disk.
type Download struct {
	ID           string
	TorrentID    string
	FileID       string
	ProviderLink string
	DirectURL    string
	RelPath      string
	Filename     string
	Size         int64
	BytesDone    int64
	State        DownloadState
	Error        string
	RetryCount   int
	// ExtractedPaths are files this download's archive produced on unpack
	// ("/"-separated, relative to the torrent dir); used for precise deletes.
	ExtractedPaths []string

	QueuedAt         time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
	UnpackStartedAt  *time.Time
	UnpackFinishedAt *time.Time
	CompletedAt      *time.Time
	UpdatedAt        time.Time
}

// Transition moves the download to a new state, validating the change.
func (d *Download) Transition(to DownloadState) error {
	if !d.State.CanTransition(to) {
		return fmt.Errorf("%w: download %s → %s", ErrInvalidTransition, d.State, to)
	}
	d.State = to
	return nil
}

// Progress returns 0..1 completion of the local download.
func (d Download) Progress() float64 {
	if d.State == DownloadDone {
		return 1
	}
	if d.Size <= 0 {
		return 0
	}
	p := float64(d.BytesDone) / float64(d.Size)
	if p > 1 {
		return 1
	}
	return p
}
