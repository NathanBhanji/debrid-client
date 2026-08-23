// Package providertest provides an in-memory Provider for tests of the
// engine, service and API layers.
package providertest

import (
	"context"
	"crypto/sha1" //nolint:gosec // info hashes are SHA-1 by spec
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
)

// Fake is a scriptable in-memory provider. All methods are safe for
// concurrent use. Tests drive state via the exported helpers; the engine
// sees a normal Provider.
type Fake struct {
	mu       sync.Mutex
	kind     domain.ProviderKind
	caps     provider.Caps
	torrents map[string]*provider.Torrent
	links    map[string][]provider.Link
	nextID   int
	calls    map[string]int

	// Hooks let tests inject failures. Return a non-nil error to fail the call.
	OnAdd        func(magnetOrHash string) error
	OnList       func() error
	OnUnrestrict func(link string) (provider.Direct, error)
	// Err, when set, is returned by every call (simulates auth/rate-limit outages).
	Err error
	// UserInfo is returned by User.
	UserInfo provider.User
}

// New returns a Fake of the given kind with direct links and no file selection,
// i.e. TorBox-like behaviour.
func New(kind domain.ProviderKind) *Fake {
	return &Fake{
		kind:     kind,
		caps:     provider.Caps{DirectLinks: true, MaxConnections: 8},
		torrents: map[string]*provider.Torrent{},
		links:    map[string][]provider.Link{},
		calls:    map[string]int{},
		UserInfo: provider.User{Username: "fake", Premium: true},
	}
}

// SetCaps overrides capabilities (e.g. SelectFiles: true to emulate Real-Debrid).
func (f *Fake) SetCaps(c provider.Caps) { f.mu.Lock(); f.caps = c; f.mu.Unlock() }

// Calls returns how many times a method was invoked.
func (f *Fake) Calls(method string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls[method] }

func (f *Fake) count(m string) { f.calls[m]++ }

// Kind implements Provider.
func (f *Fake) Kind() domain.ProviderKind { return f.kind }

// Caps implements Provider.
func (f *Fake) Caps() provider.Caps { f.mu.Lock(); defer f.mu.Unlock(); return f.caps }

// User implements Provider.
func (f *Fake) User(context.Context) (provider.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count("User")
	if f.Err != nil {
		return provider.User{}, f.Err
	}
	return f.UserInfo, nil
}

// ListTorrents implements Provider.
func (f *Fake) ListTorrents(context.Context) ([]provider.Torrent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count("ListTorrents")
	if f.Err != nil {
		return nil, f.Err
	}
	if f.OnList != nil {
		if err := f.OnList(); err != nil {
			return nil, err
		}
	}
	out := make([]provider.Torrent, 0, len(f.torrents))
	for _, t := range f.torrents {
		out = append(out, *t)
	}
	return out, nil
}

// GetTorrent implements Provider.
func (f *Fake) GetTorrent(_ context.Context, id string) (provider.Torrent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count("GetTorrent")
	if f.Err != nil {
		return provider.Torrent{}, f.Err
	}
	t, ok := f.torrents[id]
	if !ok {
		return provider.Torrent{}, &provider.Error{Kind: provider.ErrNotFound, Message: id}
	}
	return *t, nil
}

// AddMagnet implements Provider. The hash is parsed from the magnet (xt=urn:btih:)
// or derived from the string when absent.
func (f *Fake) AddMagnet(_ context.Context, magnet string) (provider.AddResult, error) {
	return f.add(magnet, hashFromMagnet(magnet))
}

// AddTorrentFile implements Provider. The hash is SHA-1 of the bytes (not a real
// info hash, but stable for tests).
func (f *Fake) AddTorrentFile(_ context.Context, data []byte) (provider.AddResult, error) {
	sum := sha1.Sum(data) //nolint:gosec // test stand-in for an info hash
	return f.add(string(data), hex.EncodeToString(sum[:]))
}

func (f *Fake) add(src, hash string) (provider.AddResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count("Add")
	if f.Err != nil {
		return provider.AddResult{}, f.Err
	}
	if f.OnAdd != nil {
		if err := f.OnAdd(src); err != nil {
			return provider.AddResult{}, err
		}
	}
	for id, t := range f.torrents {
		if t.Hash == hash {
			return provider.AddResult{ID: id, Hash: hash}, nil // providers dedupe by hash
		}
	}
	f.nextID++
	id := fmt.Sprintf("fake-%d", f.nextID)
	status := domain.TorrentProcessing
	if f.caps.SelectFiles {
		status = domain.TorrentWaitingSelection
	}
	f.torrents[id] = &provider.Torrent{ID: id, Hash: hash, Name: "torrent-" + hash[:8], Status: status, RawStatus: string(status)}
	return provider.AddResult{ID: id, Hash: hash}, nil
}

// SelectFiles implements Provider.
func (f *Fake) SelectFiles(_ context.Context, id string, fileIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count("SelectFiles")
	if f.Err != nil {
		return f.Err
	}
	t, ok := f.torrents[id]
	if !ok {
		return &provider.Error{Kind: provider.ErrNotFound, Message: id}
	}
	want := map[string]bool{}
	for _, fid := range fileIDs {
		want[fid] = true
	}
	for i := range t.Files {
		t.Files[i].Selected = len(fileIDs) == 0 || want[t.Files[i].ID]
	}
	if t.Status == domain.TorrentWaitingSelection {
		t.Status = domain.TorrentDownloading
		t.RawStatus = "downloading"
	}
	return nil
}

// Links implements Provider.
func (f *Fake) Links(_ context.Context, id string) ([]provider.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count("Links")
	if f.Err != nil {
		return nil, f.Err
	}
	t, ok := f.torrents[id]
	if !ok {
		return nil, &provider.Error{Kind: provider.ErrNotFound, Message: id}
	}
	if t.Status != domain.TorrentFinished {
		return nil, nil
	}
	return append([]provider.Link(nil), f.links[id]...), nil
}

// Unrestrict implements Provider.
func (f *Fake) Unrestrict(_ context.Context, link string) (provider.Direct, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count("Unrestrict")
	if f.Err != nil {
		return provider.Direct{}, f.Err
	}
	if f.OnUnrestrict != nil {
		return f.OnUnrestrict(link)
	}
	return provider.Direct{URL: link, MaxConnections: f.caps.MaxConnections}, nil
}

// Delete implements Provider.
func (f *Fake) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count("Delete")
	if f.Err != nil {
		return f.Err
	}
	if _, ok := f.torrents[id]; !ok {
		return &provider.Error{Kind: provider.ErrNotFound, Message: id}
	}
	delete(f.torrents, id)
	delete(f.links, id)
	return nil
}

// --- test helpers -----------------------------------------------------------

// SetStatus moves a torrent to a status (optionally with progress) as if the
// provider had progressed it.
func (f *Fake) SetStatus(id string, status domain.TorrentStatus, progress float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.torrents[id]; ok {
		t.Status, t.RawStatus, t.Progress = status, string(status), progress
	}
}

// SetFiles sets the file list for a torrent.
func (f *Fake) SetFiles(id string, files []domain.File) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.torrents[id]; ok {
		t.Files = append([]domain.File(nil), files...)
		t.Size = 0
		for _, fl := range files {
			t.Size += fl.Size
		}
	}
}

// Finish marks a torrent finished with the given per-file links.
func (f *Fake) Finish(id string, links []provider.Link) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.torrents[id]; ok {
		t.Status, t.RawStatus, t.Progress = domain.TorrentFinished, "finished", 1
		f.links[id] = append([]provider.Link(nil), links...)
	}
}

// Fail marks a torrent errored at the provider.
func (f *Fake) Fail(id, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.torrents[id]; ok {
		t.Status, t.RawStatus, t.Message = domain.TorrentError, "error", msg
	}
}

// Remove deletes a torrent server-side without going through Delete (simulates
// the user removing it in the provider UI).
func (f *Fake) Remove(id string) {
	f.mu.Lock()
	delete(f.torrents, id)
	delete(f.links, id)
	f.mu.Unlock()
}

// IDs returns the ids of all torrents at the provider.
func (f *Fake) IDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.torrents))
	for id := range f.torrents {
		out = append(out, id)
	}
	return out
}

func hashFromMagnet(m string) string {
	const key = "xt=urn:btih:"
	if i := strings.Index(strings.ToLower(m), key); i >= 0 {
		h := m[i+len(key):]
		if j := strings.IndexAny(h, "&#"); j >= 0 {
			h = h[:j]
		}
		if len(h) == 40 {
			return strings.ToLower(h)
		}
	}
	sum := sha1.Sum([]byte(m)) //nolint:gosec // test stand-in
	return hex.EncodeToString(sum[:])
}

var _ provider.Provider = (*Fake)(nil)
