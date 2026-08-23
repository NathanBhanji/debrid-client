package events

import (
	"context"
	"testing"
	"time"
)

func TestPublishSubscribe(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	ch := b.Subscribe(ctx, 2)
	b.Publish(Event{Type: TorrentAdded, TorrentID: "1"})
	b.Publish(Event{Type: TorrentUpdated, TorrentID: "1"})
	b.Publish(Event{Type: TorrentDeleted, TorrentID: "1"}) // dropped: buffer full
	select {
	case e := <-ch:
		if e.Type != TorrentAdded || e.At.IsZero() {
			t.Fatalf("got %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	<-ch
	select {
	case e, ok := <-ch:
		if ok {
			t.Fatalf("unexpected third event %+v", e)
		}
	default:
	}
	cancel()
	// Channel closes after unsubscribe.
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after cancel")
		}
	}
}
