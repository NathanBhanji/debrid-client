package domain

import (
	"slices"
	"testing"
)

func TestSelectionRevisionBindsIdentityAndMetadataNotPresentation(t *testing.T) {
	tor := Torrent{ID: "t", AccountID: "a", ProviderID: "p"}
	files := []File{{ID: "2", Path: "b.mkv", Size: 2}, {ID: "1", Path: "a.mkv", Size: 1}}
	tor.Hash = "abcdef"
	if !tor.SelectionSourceMatches("p", "ABCDEF") || tor.SelectionSourceMatches("p", "wrong") || tor.SelectionSourceMatches("other", "") {
		t.Fatal("provider content identity")
	}
	revision, err := FileSelectionRevision(tor, files)
	if err != nil {
		t.Fatal(err)
	}
	changed := slices.Clone(files)
	slices.Reverse(changed)
	changed[0].Selected = true
	changed[0].Link = "private-provider-link"
	if got, err := FileSelectionRevision(tor, changed); err != nil || got != revision {
		t.Fatal("presentation changed revision", err)
	}
	for _, mutate := range []func(*Torrent){func(t *Torrent) { t.ID = "other" }, func(t *Torrent) { t.AccountID = "other" }, func(t *Torrent) { t.Hash = "other" }, func(t *Torrent) { t.ProviderID = "other" }, func(t *Torrent) { t.RetryCount++ }} {
		x := tor
		mutate(&x)
		if got, _ := FileSelectionRevision(x, files); got == revision {
			t.Fatal("identity did not change revision")
		}
	}
	changed[0].Path = "renamed.mkv"
	if got, _ := FileSelectionRevision(tor, changed); got == revision {
		t.Fatal("path did not change revision")
	}
	for _, bad := range [][]File{nil, {{ID: "", Path: "a"}}, {{ID: "1"}, {ID: "1"}}, {{ID: "1", Size: -1}}} {
		if _, err := FileSelectionRevision(tor, bad); err == nil {
			t.Fatal("accepted ambiguous metadata")
		}
	}
	if _, err := StrictSelectedFiles(files, []string{"1", "missing"}); err == nil {
		t.Fatal("partial manual match")
	}
	if _, err := CanonicalFileIDs([]string{"1", "1"}); err == nil {
		t.Fatal("duplicate request")
	}
	tor.Settings.WaitForSelection = true
	if tor.SelectionReady() {
		t.Fatal("hold without proof")
	}
	tor.Settings.ManualFiles = []string{"1"}
	tor.Settings.Selection = &FileSelectionReceipt{Version: 1, ProviderID: "p", Revision: revision, FileIDs: []string{"1"}}
	if !tor.SelectionReady() {
		t.Fatal("valid proof rejected")
	}
	tor.RetryCount++
	if tor.SelectionReady() {
		t.Fatal("old proof survived retry")
	}
}
