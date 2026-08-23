// Package provider defines the interface every debrid service client
// implements, the shared error model, and a registry of constructors.
package provider

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
)

// Caps describes provider-specific behaviour so the engine can stay generic.
type Caps struct {
	// SelectFiles is true when the provider waits for an explicit file selection
	// (Real-Debrid). When false, SelectFiles is a no-op and filtering happens
	// client-side at download time.
	SelectFiles bool
	// CacheCheck is true when CheckCached is supported (TorBox, Premiumize).
	CacheCheck bool
	// DirectLinks is true when Links() already returns directly downloadable
	// URLs and Unrestrict is the identity (Premiumize, Debrid-Link, TorBox).
	DirectLinks bool
	// MaxConnections hints how many parallel connections per file the CDN tolerates (0 = unknown).
	MaxConnections int
}

// User is the account summary from the provider.
type User struct {
	Username  string
	Email     string
	Premium   bool
	Plan      string
	ExpiresAt *time.Time
}

// Torrent is a provider's view of a torrent, mapped to the neutral status set.
// Hash is lowercase hex; Progress is 0..1.
type Torrent struct {
	ID        string
	Hash      string
	Name      string
	Size      int64
	Status    domain.TorrentStatus // one of processing|waiting_selection|downloading|uploading|finished|error
	RawStatus string
	Message   string // provider-supplied detail for errors/stalls
	Progress  float64
	Speed     int64
	Seeders   int
	Files     []domain.File
	AddedAt   *time.Time
	EndedAt   *time.Time
}

// AddResult is returned after submitting a torrent.
type AddResult struct {
	ID   string
	Hash string
}

// Link is a downloadable reference to one file of a torrent.
type Link struct {
	FileID string
	Path   string
	Size   int64
	URL    string // restricted link (needs Unrestrict) or direct URL when Caps.DirectLinks
}

// Direct is an unrestricted, directly downloadable URL.
type Direct struct {
	URL            string
	Filename       string
	Size           int64
	MaxConnections int // 0 = unknown
}

// Provider is a client for one debrid service account.
type Provider interface {
	Kind() domain.ProviderKind
	Caps() Caps

	User(ctx context.Context) (User, error)

	// ListTorrents returns every torrent on the account. Implementations should
	// make exactly one list call; the engine polls this, never GetTorrent in a loop.
	ListTorrents(ctx context.Context) ([]Torrent, error)
	// GetTorrent returns one torrent with its full file list.
	GetTorrent(ctx context.Context, id string) (Torrent, error)

	// AddMagnet / AddTorrentFile submit a torrent. Providers typically dedupe
	// by info hash and return the existing torrent's id; the engine also
	// dedupes against ListTorrents before calling these. Implementations
	// should not retry these calls (a timed-out add may still have succeeded).
	AddMagnet(ctx context.Context, magnet string) (AddResult, error)
	AddTorrentFile(ctx context.Context, data []byte) (AddResult, error)

	// SelectFiles tells the provider which files to fetch. No-op unless Caps.SelectFiles.
	SelectFiles(ctx context.Context, id string, fileIDs []string) error
	// Links returns per-file links for a torrent. Contract: (nil, nil) means
	// "not ready yet — poll again" (torrent not finished, or finished but the
	// provider hasn't produced links yet); the engine bounds that wait with a
	// timeout. A finished torrent with links returns ≥1 Link. Errors are
	// classified (*Error), ErrNotFound when the torrent is gone.
	Links(ctx context.Context, id string) ([]Link, error)
	// Unrestrict turns a Link.URL into a direct download URL. Identity when Caps.DirectLinks.
	Unrestrict(ctx context.Context, link string) (Direct, error)

	Delete(ctx context.Context, id string) error
}

// CacheChecker is implemented by providers with Caps.CacheCheck.
type CacheChecker interface {
	// CheckCached reports which of the given info hashes are instantly
	// available. Result keys are lowercase hex.
	CheckCached(ctx context.Context, hashes []string) (map[string]bool, error)
}

// Options are passed to constructors.
type Options struct {
	// UserAgent sent on every request.
	UserAgent string
	// BaseURL overrides the provider's default API endpoint (tests, mirrors).
	BaseURL string
	// HTTPClient overrides the HTTP client (tests). Nil means a sane default.
	HTTPClient *http.Client
	// Logger for request-level debug logging. Nil means slog.Default().
	Logger *slog.Logger
}

// Constructor builds a Provider from credentials.
type Constructor func(creds domain.Credentials, opts Options) (Provider, error)

var (
	mu       sync.RWMutex
	registry = map[domain.ProviderKind]Constructor{}
)

// Register makes a constructor available to New. Typically called from an
// init() in each provider package.
func Register(kind domain.ProviderKind, c Constructor) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[kind]; dup {
		panic(fmt.Sprintf("provider: duplicate registration for %q", kind))
	}
	registry[kind] = c
}

// New constructs a provider of the given kind.
func New(kind domain.ProviderKind, creds domain.Credentials, opts Options) (Provider, error) {
	mu.RLock()
	c, ok := registry[kind]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider: unsupported kind %q", kind)
	}
	return c(creds, opts)
}

// Kinds lists registered provider kinds, sorted.
func Kinds() []domain.ProviderKind {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]domain.ProviderKind, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
