// Command iosfixtures emits deterministic, credential-free Swift contract fixtures.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/api"
	"github.com/NathanBhanji/debrid-client/internal/apimodel"
	"github.com/NathanBhanji/debrid-client/internal/auth"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func generate() error {
	dir, err := os.MkdirTemp("", "debrid-ios-fixtures-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	st, err := store.Open(context.Background(), filepath.Join(dir, "fixture.db"))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	handler := api.New(nil, api.Options{APIKey: "fixture-only", Auth: auth.New(st)})
	fixtures := make(map[string]any)
	for name, path := range map[string]string{"health": "health", "auth_status": "auth/status", "identity": "auth/me"} {
		req := httptest.NewRequest("GET", "/api/v1/"+path, nil)
		req.Header.Set("Authorization", "Bearer fixture-only")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 200 {
			return fmt.Errorf("%s fixture status %d", name, response.Code)
		}
		fixtures[name] = json.RawMessage(response.Body.Bytes())
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	defaults := apimodel.TorrentSettings{DownloadRetries: 3, TorrentRetries: 1, Unpack: true}
	fixtures["torrent"] = apimodel.Torrent{
		ID: "fixture-transfer", AccountID: "fixture-account", Hash: "0000000000000000000000000000000000000000",
		Name: "The Open Ocean", Category: "Documentaries", Status: "finished", ProviderProgress: 1, LocalProgress: 0.64,
		Size: 10000000000, Files: []apimodel.File{{ID: "film", Path: "The Open Ocean.mkv", Size: 10000000000, Selected: true}},
		Settings: defaults, AddedAt: at,
		Downloads: []apimodel.Download{{ID: "fixture-download", TorrentID: "fixture-transfer", FileID: "film", Path: "The Open Ocean.mkv", Filename: "The Open Ocean.mkv", Size: 10000000000, BytesDone: 6400000000, Progress: 0.64, State: "downloading", QueuedAt: at, StartedAt: &at}},
	}
	fixtures["retry_review"] = apimodel.RetryReview{Kind: "torrent", ResourceID: "fixture-transfer", TorrentID: "fixture-transfer", Revision: strings.Repeat("a", 64), Allowed: true, WaitForSelection: true}
	fixtures["retry_acceptance"] = apimodel.RetryAcceptance{Kind: "download", ResourceID: "fixture-download", TorrentID: "fixture-transfer", AcceptedAt: at, TorrentExists: true}
	fixtures["account"] = apimodel.Account{ID: "fixture-account", Kind: "torbox", Name: "Home account", Enabled: true, IsDefault: true, CreatedAt: at, UpdatedAt: at}
	fixtures["settings"] = apimodel.Settings{TorrentDefaults: defaults, Categories: []string{"Documentaries", "Audio"}, UnpackMaxDepth: 2, Organize: apimodel.OrganizeSettings{Enabled: false}}
	fixtures["system_status"] = apimodel.Status{Version: "fixture", DownloadDir: "/downloads", Accounts: 1, Torrents: map[string]int{"finished": 1}, Downloads: map[string]int{"downloading": 1}, DiskFree: 500000000000, DiskTotal: 1000000000000}
	b, err := json.MarshalIndent(fixtures, "", "  ")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(b, '\n'))
	return err
}
