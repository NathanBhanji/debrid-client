package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

func ts(s string) time.Time  { t, _ := time.Parse(time.RFC3339Nano, s); return t.UTC() }
func tp(s string) *time.Time { t := ts(s); return &t }

func TestRoundTripThroughDB(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	acc := domain.ProviderAccount{
		ID: "acc1", Kind: domain.ProviderTorBox, Name: "main", Enabled: true, IsDefault: true,
		Credentials: domain.Credentials{APIKey: "k", ExpiresAt: tp("2030-01-01T00:00:00Z")},
		CreatedAt:   ts("2026-08-23T10:00:00.123456789Z"), UpdatedAt: ts("2026-08-23T10:00:01Z"),
	}
	ap, err := AccountInsertParams(acc)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InsertProviderAccount(ctx, ap); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetProviderAccount(ctx, "acc1")
	if err != nil {
		t.Fatal(err)
	}
	gotAcc, err := AccountFromRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotAcc, acc) {
		t.Fatalf("account mismatch:\n got %+v\nwant %+v", gotAcc, acc)
	}

	tor := domain.Torrent{
		ID: "t1", AccountID: "acc1", Hash: "deadbeef", Name: "Thing", Category: "tv",
		Status: domain.TorrentDownloading, StatusReason: "provider downloading", Progress: 0.5, Size: 123, Speed: 9, Seeders: 3,
		ProviderID: "p1", ProviderStatus: "downloading",
		Files:       []domain.File{{ID: "f1", Path: "a/b.mkv", Size: 100, Selected: true}},
		Settings:    domain.TorrentSettings{MinFileSize: 5, IncludeRegex: "x", FinishedAction: domain.FinishedKeep, FinishedDelay: time.Minute, DownloadRetries: 3, TorrentRetries: 1, Unpack: true},
		PayloadKind: domain.PayloadMagnet, Payload: []byte("magnet:?xt=urn:btih:deadbeef"),
		RetryCount: 1, AddedAt: ts("2026-08-23T10:00:00Z"), ProviderAddedAt: tp("2026-08-23T10:00:05Z"),
		UpdatedAt: ts("2026-08-23T10:00:06Z"),
	}
	tip, err := TorrentInsertParams(tor)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTorrent(ctx, tip); err != nil {
		t.Fatal(err)
	}
	trow, err := s.GetTorrent(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	gotTor, err := TorrentFromRow(trow)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotTor, tor) {
		t.Fatalf("torrent mismatch:\n got %+v\nwant %+v", gotTor, tor)
	}

	// Update path: change status & completed_at, round-trip again.
	tor.Status = domain.TorrentCompleted
	tor.CompletedAt = tp("2026-08-23T11:00:00Z")
	tor.Files = nil // nil files must persist as [] and come back as empty, not nil
	tup, err := TorrentUpdateParams(tor)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTorrent(ctx, tup); err != nil {
		t.Fatal(err)
	}
	trow, _ = s.GetTorrent(ctx, "t1")
	gotTor, _ = TorrentFromRow(trow)
	if gotTor.Status != domain.TorrentCompleted || gotTor.CompletedAt == nil || !gotTor.CompletedAt.Equal(*tor.CompletedAt) {
		t.Fatalf("update not applied: %+v", gotTor)
	}
	if gotTor.Files == nil || len(gotTor.Files) != 0 {
		t.Fatalf("files should round-trip as empty slice, got %#v", gotTor.Files)
	}

	dl := domain.Download{
		ID: "d1", TorrentID: "t1", FileID: "f1", ProviderLink: "L", DirectURL: "U", RelPath: "a/b.mkv", Filename: "b.mkv",
		Size: 100, BytesDone: 40, State: domain.DownloadDownloading, RetryCount: 2,
		QueuedAt: ts("2026-08-23T10:01:00Z"), StartedAt: tp("2026-08-23T10:01:01Z"), UpdatedAt: ts("2026-08-23T10:01:02Z"),
	}
	if _, err := s.InsertDownload(ctx, DownloadInsertParams(dl)); err != nil {
		t.Fatal(err)
	}
	drow, err := s.GetDownload(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	gotDl, err := DownloadFromRow(drow)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotDl, dl) {
		t.Fatalf("download mismatch:\n got %+v\nwant %+v", gotDl, dl)
	}
	dl.State = domain.DownloadDone
	dl.CompletedAt = tp("2026-08-23T10:02:00Z")
	if err := s.UpdateDownload(ctx, DownloadUpdateParams(dl)); err != nil {
		t.Fatal(err)
	}
	drow, _ = s.GetDownload(ctx, "d1")
	gotDl, _ = DownloadFromRow(drow)
	if !reflect.DeepEqual(gotDl, dl) {
		t.Fatalf("download update mismatch:\n got %+v\nwant %+v", gotDl, dl)
	}
}

func TestEmptySettingsDecodeToDefaults(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	_ = s.InsertProviderAccount(ctx, sqlcgen.InsertProviderAccountParams{ID: "a", Kind: "torbox", Name: "n", Credentials: "{}", Enabled: 1, CreatedAt: now(), UpdatedAt: now()})
	_ = s.InsertTorrent(ctx, sqlcgen.InsertTorrentParams{ID: "t", AccountID: "a", Hash: "h", Status: "queued", Files: "[]", Settings: "{}", PayloadKind: "magnet", Payload: []byte("m"), AddedAt: now(), UpdatedAt: now()})
	row, _ := s.GetTorrent(ctx, "t")
	tor, err := TorrentFromRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if tor.Settings != domain.DefaultTorrentSettings() {
		t.Fatalf("empty settings should decode to defaults, got %+v", tor.Settings)
	}
	// nil payload is coerced rather than failing NOT NULL.
	p, _ := TorrentInsertParams(domain.Torrent{ID: "t2", AccountID: "a", Hash: "h2", Status: domain.TorrentQueued, PayloadKind: domain.PayloadMagnet, AddedAt: ts("2026-01-01T00:00:00Z"), UpdatedAt: ts("2026-01-01T00:00:00Z")})
	if err := s.InsertTorrent(ctx, p); err != nil {
		t.Fatalf("nil payload insert: %v", err)
	}
}

func TestFromRowRejectsBadJSON(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO provider_accounts (id,kind,name,credentials,enabled,is_default,created_at,updated_at)
		VALUES ('x','torbox','n','not json',1,0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	row, _ := s.GetProviderAccount(ctx, "x")
	if _, err := AccountFromRow(row); err == nil {
		t.Fatal("expected JSON error")
	}
}
