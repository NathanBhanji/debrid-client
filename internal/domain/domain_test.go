package domain

import (
	"errors"
	"testing"
)

func TestTorrentTransitions(t *testing.T) {
	ok := [][2]TorrentStatus{
		{TorrentQueued, TorrentAdding},
		{TorrentAdding, TorrentProcessing},
		{TorrentAdding, TorrentFinished}, // cached torrent
		{TorrentAdding, TorrentQueued},   // add timed out → re-queue
		{TorrentProcessing, TorrentWaitingSelection},
		{TorrentWaitingSelection, TorrentDownloading},
		{TorrentDownloading, TorrentUploading},
		{TorrentUploading, TorrentFinished},
		{TorrentFinished, TorrentCompleted},
		{TorrentFinished, TorrentError},
		{TorrentError, TorrentQueued},     // retry
		{TorrentError, TorrentFinished},   // retry a single download
		{TorrentQueued, TorrentFinished},  // adopted an already-cached provider torrent
		{TorrentCompleted, TorrentQueued}, // explicit re-download
		{TorrentDownloading, TorrentDownloading},
	}
	for _, c := range ok {
		if !c[0].CanTransition(c[1]) {
			t.Errorf("%s → %s should be allowed", c[0], c[1])
		}
	}
	bad := [][2]TorrentStatus{
		{TorrentQueued, TorrentCompleted},
		{TorrentCompleted, TorrentFinished},
		{TorrentError, TorrentDownloading},
		{TorrentFinished, TorrentDownloading},
		{TorrentProcessing, TorrentQueued},
	}
	for _, c := range bad {
		if c[0].CanTransition(c[1]) {
			t.Errorf("%s → %s should be rejected", c[0], c[1])
		}
	}

	tor := &Torrent{Status: TorrentQueued}
	if err := tor.Transition(TorrentAdding, "sending to provider"); err != nil || tor.Status != TorrentAdding || tor.StatusReason == "" {
		t.Fatalf("transition failed: %v %+v", err, tor)
	}
	if err := tor.Transition(TorrentCompleted, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestTorrentStatusPredicates(t *testing.T) {
	if !TorrentCompleted.IsTerminal() || !TorrentError.IsTerminal() || TorrentFinished.IsTerminal() {
		t.Fatal("IsTerminal wrong")
	}
	if TorrentQueued.AtProvider() || TorrentAdding.AtProvider() || !TorrentDownloading.AtProvider() || !TorrentCompleted.AtProvider() {
		t.Fatal("AtProvider wrong")
	}
}

func TestDownloadTransitions(t *testing.T) {
	d := &Download{State: DownloadPending}
	steps := []DownloadState{DownloadUnrestricting, DownloadDownloading, DownloadDownloaded, DownloadUnpacking, DownloadDone}
	for _, s := range steps {
		if err := d.Transition(s); err != nil {
			t.Fatalf("→ %s: %v", s, err)
		}
	}
	if err := d.Transition(DownloadPending); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("done must be terminal")
	}
	e := &Download{State: DownloadDownloading}
	if err := e.Transition(DownloadError); err != nil {
		t.Fatal(err)
	}
	if err := e.Transition(DownloadPending); err != nil {
		t.Fatal("error → pending (retry) should be allowed")
	}
	// A link that expired mid-download goes back to pending to re-unrestrict.
	if !DownloadDownloading.CanTransition(DownloadPending) {
		t.Fatal("downloading → pending should be allowed")
	}
}

func TestDownloadProgress(t *testing.T) {
	cases := []struct {
		d    Download
		want float64
	}{
		{Download{Size: 0, BytesDone: 10}, 0},
		{Download{Size: 100, BytesDone: 25}, 0.25},
		{Download{Size: 100, BytesDone: 150}, 1},
		{Download{State: DownloadDone}, 1},
	}
	for _, c := range cases {
		if got := c.d.Progress(); got != c.want {
			t.Errorf("%+v progress = %v want %v", c.d, got, c.want)
		}
	}
}

func TestProviderKind(t *testing.T) {
	if !ProviderTorBox.Valid() || ProviderKind("nope").Valid() {
		t.Fatal("Valid wrong")
	}
	if len(AllProviderKinds()) != 5 {
		t.Fatal("expected 5 provider kinds")
	}
}

func TestExplicitCancellationIsTerminalAndOnlyWholeRetryReleasesIt(t *testing.T) {
	for status := range torrentTransitions {
		if status.IsTerminal() {
			if status.CanTransition(TorrentCancelled) {
				t.Fatal("terminal result replaced", status)
			}
		} else if !status.CanTransition(TorrentCancelled) {
			t.Fatal("active state cannot stop", status)
		}
	}
	if !TorrentCancelled.IsTerminal() || TorrentCancelled.AtProvider() || !TorrentCancelled.CanTransition(TorrentQueued) || TorrentCancelled.CanTransition(TorrentFinished) {
		t.Fatal("cancelled parent predicates")
	}
	for state := range downloadTransitions {
		if state.IsTerminal() {
			if state.CanTransition(DownloadCancelled) {
				t.Fatal("terminal file replaced", state)
			}
		} else if !state.CanTransition(DownloadCancelled) {
			t.Fatal("active file cannot stop", state)
		}
	}
	if !DownloadCancelled.IsTerminal() || DownloadCancelled.CanTransition(DownloadPending) {
		t.Fatal("individual retry must not release cancellation")
	}
	if TorrentStatus("future").CanTransition(TorrentCancelled) || DownloadState("future").CanTransition(DownloadCancelled) {
		t.Fatal("unknown states accepted")
	}
}
