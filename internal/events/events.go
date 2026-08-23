// Package events is a small in-process pub/sub bus used to push state changes
// to API clients (SSE) and to wake the engine.
package events

import (
	"context"
	"sync"
	"time"
)

// Type identifies what happened.
type Type string

const (
	TorrentAdded    Type = "torrent.added"
	TorrentUpdated  Type = "torrent.updated"
	TorrentDeleted  Type = "torrent.deleted"
	DownloadUpdated Type = "download.updated"
	AccountChanged  Type = "account.changed"
	SettingsChanged Type = "settings.changed"
)

// Event is a notification. Payloads are kept small; subscribers re-fetch.
type Event struct {
	Type       Type      `json:"type"`
	TorrentID  string    `json:"torrent_id,omitempty"`
	DownloadID string    `json:"download_id,omitempty"`
	AccountID  string    `json:"account_id,omitempty"`
	At         time.Time `json:"at"`
}

// Bus fans events out to subscribers. Slow subscribers drop events rather
// than block publishers.
type Bus struct {
	mu   sync.RWMutex
	subs map[int]chan Event
	next int
}

// New creates a Bus.
func New() *Bus { return &Bus{subs: map[int]chan Event{}} }

// Publish delivers e to all subscribers without blocking.
func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default: // drop for slow consumer
		}
	}
}

// Subscribe returns a channel receiving events until ctx is done.
func (b *Bus) Subscribe(ctx context.Context, buffer int) <-chan Event {
	if buffer <= 0 {
		buffer = 64
	}
	ch := make(chan Event, buffer)
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
		close(ch)
	}()
	return ch
}
