package store

import (
	"bytes"
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

func TestSubmissionMigrationPreservesLegacyDuplicateRowsAndPayloads(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	q := sqlcgen.New(db)
	at := time.Now().UTC()
	account := domain.ProviderAccount{ID: "legacy-account", Kind: domain.ProviderTorBox, Name: "Legacy", Credentials: domain.Credentials{APIKey: "fixture-secret"}, Enabled: true, IsDefault: true, CreatedAt: at, UpdatedAt: at}
	ap, err := AccountInsertParams(account)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InsertProviderAccount(ctx, ap); err != nil {
		t.Fatal(err)
	}
	payload := []byte("magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&tr=https%3a%2f%2ffixture.test%2fa%3ftoken%3doriginal")
	for _, id := range []string{"legacy-a", "legacy-b"} {
		torrent := domain.Torrent{ID: id, AccountID: account.ID, Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Legacy", Status: domain.TorrentQueued, Settings: domain.DefaultTorrentSettings(), PayloadKind: domain.PayloadMagnet, Payload: payload, AddedAt: at, UpdatedAt: at}
		p, err := TorrentInsertParams(torrent)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.InsertTorrent(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	download := domain.Download{ID: "download", TorrentID: "legacy-a", ProviderLink: "fixture-link", Filename: "fixture", RelPath: "fixture", Size: 100, BytesDone: 40, State: domain.DownloadDownloading, QueuedAt: at, UpdatedAt: at}
	if _, err := q.InsertDownload(ctx, DownloadInsertParams(download)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrated.Close() }()
	if count, _ := migrated.CountTorrents(ctx); count != 2 {
		t.Fatal("migration altered legacy duplicates")
	}
	row, err := migrated.GetTorrent(ctx, "legacy-a")
	if err != nil || !bytes.Equal(row.Payload, payload) {
		t.Fatal("migration changed original payload")
	}
	a, err := migrated.GetProviderAccount(ctx, account.ID)
	if err != nil || a.Credentials != ap.Credentials {
		t.Fatal("migration changed credentials")
	}
	d, err := migrated.GetDownload(ctx, "download")
	if err != nil || d.BytesDone != 40 || d.State != string(domain.DownloadDownloading) {
		t.Fatal("migration changed download")
	}
	if err := migrated.InsertTorrentSubmission(ctx, sqlcgen.InsertTorrentSubmissionParams{KeyHash: string(bytes.Repeat([]byte("a"), 64)), RequestHash: string(bytes.Repeat([]byte("b"), 64)), TorrentID: "legacy-a", CreatedAt: FormatTime(at)}); err != nil {
		t.Fatal(err)
	}
	if err := migrated.DeleteTorrent(ctx, "legacy-a"); err != nil {
		t.Fatal(err)
	}
	receipt, err := migrated.GetTorrentSubmission(ctx, string(bytes.Repeat([]byte("a"), 64)))
	if err != nil || receipt.TorrentExists {
		t.Fatal("accepted tombstone lost after deletion")
	}
}
