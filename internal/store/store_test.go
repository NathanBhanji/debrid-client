package store

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func now() string { return FormatTime(time.Now()) }

func TestOpenAppliesMigrationsAndPragmas(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	var fk int
	if err := s.DB().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys pragma = %d, err %v", fk, err)
	}
	var jm string
	if err := s.DB().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&jm); err != nil || !strings.EqualFold(jm, "wal") {
		t.Fatalf("journal_mode = %q, err %v", jm, err)
	}
	// Re-opening the same file must be idempotent (migrations already applied).
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	for range 2 {
		s, err := Open(context.Background(), path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		_ = s.Close()
	}
}

func TestProviderAccountsCRUDAndDefaultUniqueness(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mk := func(id, name string, def int64) error {
		return s.InsertProviderAccount(ctx, sqlcgen.InsertProviderAccountParams{
			ID: id, Kind: "torbox", Name: name, Credentials: `{"api_key":"x"}`, Enabled: 1, IsDefault: def,
			CreatedAt: now(), UpdatedAt: now(),
		})
	}
	if err := mk("a", "first", 1); err != nil {
		t.Fatal(err)
	}
	if err := mk("b", "second", 1); err == nil {
		t.Fatal("two default accounts should violate the partial unique index")
	}
	if err := mk("b", "second", 0); err != nil {
		t.Fatal(err)
	}
	if err := mk("c", "first", 0); err == nil {
		t.Fatal("duplicate name should fail")
	}
	def, err := s.GetDefaultProviderAccount(ctx)
	if err != nil || def.ID != "a" {
		t.Fatalf("default = %+v err %v", def, err)
	}
	if err := s.WithTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.ClearDefaultProviderAccount(ctx, now()); err != nil {
			return err
		}
		return q.SetDefaultProviderAccount(ctx, sqlcgen.SetDefaultProviderAccountParams{UpdatedAt: now(), ID: "b"})
	}); err != nil {
		t.Fatalf("switch default: %v", err)
	}
	def, err = s.GetDefaultProviderAccount(ctx)
	if err != nil || def.ID != "b" {
		t.Fatalf("default after switch = %+v err %v", def, err)
	}
	list, err := s.ListProviderAccounts(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d err %v", len(list), err)
	}
	_, err = s.GetProviderAccount(ctx, "nope")
	if !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestTorrentForeignKeysAndCascade(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	insertTorrent := func(id, account string) error {
		return s.InsertTorrent(ctx, sqlcgen.InsertTorrentParams{
			ID: id, AccountID: account, Hash: "abc", Status: "queued", Files: "[]", Settings: "{}",
			PayloadKind: "magnet", Payload: []byte("magnet:?xt=urn:btih:abc"), AddedAt: now(), UpdatedAt: now(),
		})
	}
	if err := insertTorrent("t1", "missing"); err == nil {
		t.Fatal("FK to provider_accounts should be enforced")
	}
	if err := s.InsertProviderAccount(ctx, sqlcgen.InsertProviderAccountParams{
		ID: "acc", Kind: "torbox", Name: "n", Credentials: "{}", Enabled: 1, CreatedAt: now(), UpdatedAt: now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := insertTorrent("t1", "acc"); err != nil {
		t.Fatal(err)
	}
	dl := sqlcgen.InsertDownloadParams{
		ID: "d1", TorrentID: "t1", ProviderLink: "link1", RelPath: "a/b.mkv", Filename: "b.mkv", State: "pending",
		QueuedAt: now(), UpdatedAt: now(),
	}
	if n, err := s.InsertDownload(ctx, dl); err != nil || n != 1 {
		t.Fatalf("insert: n=%d err=%v", n, err)
	}
	// Same (torrent, link) again is a no-op (idempotent CreateDownloads) and reports 0 rows.
	dl.ID = "d2"
	if n, err := s.InsertDownload(ctx, dl); err != nil || n != 0 {
		t.Fatalf("duplicate insert: n=%d err=%v", n, err)
	}
	dls, err := s.ListDownloadsForTorrent(ctx, "t1")
	if err != nil || len(dls) != 1 {
		t.Fatalf("downloads = %d err %v", len(dls), err)
	}
	// Deleting the account while torrents reference it must be refused...
	if err := s.DeleteProviderAccount(ctx, "acc"); err == nil {
		t.Fatal("deleting an account with torrents should fail (RESTRICT)")
	}
	// ...and deleting the torrent cascades to downloads.
	if err := s.DeleteTorrent(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	dls, err = s.ListDownloadsForTorrent(ctx, "t1")
	if err != nil || len(dls) != 0 {
		t.Fatalf("downloads after cascade = %d err %v", len(dls), err)
	}
}

func TestSettingsUpsert(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, v := range []string{"1", "2"} {
		if err := s.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{Key: "k", Value: v, UpdatedAt: now()}); err != nil {
			t.Fatal(err)
		}
	}
	v, err := s.GetSetting(ctx, "k")
	if err != nil || v != "2" {
		t.Fatalf("value = %q err %v", v, err)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	errBoom := context.Canceled
	err := s.WithTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{Key: "k", Value: "v", UpdatedAt: now()}); err != nil {
			return err
		}
		return errBoom
	})
	if err != errBoom { //nolint:errorlint // identity check is intended
		t.Fatalf("expected fn error, got %v", err)
	}
	if _, err := s.GetSetting(ctx, "k"); !IsNotFound(err) {
		t.Fatalf("setting should have been rolled back, err=%v", err)
	}
}

func TestTimeFormatSortsLexically(t *testing.T) {
	base := time.Date(2026, 8, 23, 12, 34, 56, 0, time.UTC)
	times := []time.Time{
		base.Add(120 * time.Millisecond), base.Add(100 * time.Millisecond), base.Add(500 * time.Millisecond),
		base, base.Add(time.Second), base.Add(1), base.Add(999_999_999),
	}
	strs := make([]string, len(times))
	for i, tm := range times {
		strs[i] = FormatTime(tm)
	}
	sort.Strings(strs)
	for i := 1; i < len(strs); i++ {
		a, _ := ParseTime(strs[i-1])
		b, _ := ParseTime(strs[i])
		if a.After(b) {
			t.Fatalf("lexical order != chronological: %s before %s", strs[i-1], strs[i])
		}
	}
	if len(FormatTime(base)) != len(FormatTime(base.Add(1))) {
		t.Fatal("format must be fixed width")
	}
	// Legacy RFC3339Nano values still parse.
	if _, err := ParseTime("2026-08-23T12:34:56.5Z"); err != nil {
		t.Fatal(err)
	}
}

func TestWithTxRollsBackOnPanic(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	func() {
		defer func() { _ = recover() }()
		_ = s.WithTx(ctx, func(q *sqlcgen.Queries) error {
			_ = q.UpsertSetting(ctx, sqlcgen.UpsertSettingParams{Key: "k", Value: "v", UpdatedAt: now()})
			panic("boom")
		})
	}()
	// The single connection must be usable again and the write rolled back.
	done := make(chan error, 1)
	go func() { _, err := s.GetSetting(ctx, "k"); done <- err }()
	select {
	case err := <-done:
		if !IsNotFound(err) {
			t.Fatalf("expected rollback, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("connection leaked by panicking transaction")
	}
}

func TestBoolAndHashChecks(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO provider_accounts (id,kind,name,credentials,enabled,is_default,created_at,updated_at) VALUES ('x','torbox','n','{}',2,0,'t','t')`); err == nil {
		t.Fatal("enabled=2 should violate CHECK")
	}
	_ = s.InsertProviderAccount(ctx, sqlcgen.InsertProviderAccountParams{ID: "a", Kind: "torbox", Name: "n", Credentials: "{}", Enabled: 1, CreatedAt: now(), UpdatedAt: now()})
	if err := s.InsertTorrent(ctx, sqlcgen.InsertTorrentParams{ID: "t", AccountID: "a", Hash: "ABC", Status: "queued", Files: "[]", Settings: "{}", PayloadKind: "magnet", Payload: []byte("m"), AddedAt: now(), UpdatedAt: now()}); err == nil {
		t.Fatal("uppercase hash should violate CHECK")
	}
}

func TestTimeRoundTrip(t *testing.T) {
	in := time.Date(2026, 8, 23, 12, 34, 56, 789, time.FixedZone("x", 3600))
	out, err := ParseTime(FormatTime(in))
	if err != nil || !out.Equal(in) {
		t.Fatalf("round trip: %v %v", out, err)
	}
	if nt := NullTime(nil); nt.Valid {
		t.Fatal("nil time should be NULL")
	}
	p, err := ParseNullTime(NullTime(&in))
	if err != nil || p == nil || !p.Equal(in) {
		t.Fatalf("null round trip: %v %v", p, err)
	}
}
