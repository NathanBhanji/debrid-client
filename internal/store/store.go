// Package store owns the SQLite database: opening with the right pragmas,
// running embedded migrations, and exposing sqlc-generated queries.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the database handle and generated queries.
//
// SQLite permits a single writer; we run all statements through one
// connection pool capped at one open connection so writers serialise in-process
// instead of fighting over the file lock (WAL lets other *processes* read
// concurrently). Consequences callers must respect:
//   - Inside WithTx, use only the *sqlcgen.Queries passed to the callback.
//     Calling s.Queries or s.DB() there would wait for the single connection
//     held by the transaction and deadlock.
//   - Do not hold *sql.Rows open while issuing another query.
type Store struct {
	db *sql.DB
	*sqlcgen.Queries
}

// Open opens (creating if needed) the database file at path and applies
// migrations. Tests should use a file in t.TempDir() (":memory:" databases are
// per-connection and not supported).
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// One connection: serialises writers in-process (see Store doc).
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, Queries: sqlcgen.New(db)}, nil
}

func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_txlock", "immediate")
	return "file:" + path + "?" + q.Encode()
}

func migrate(ctx context.Context, db *sql.DB) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// DB exposes the underlying handle for callers that need raw SQL.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// WithTx runs fn inside a transaction using transaction-scoped queries.
// The transaction is committed if fn returns nil and rolled back otherwise
// (including on panic, so the single connection is never leaked).
// fn must only use the queries it is given — see Store.
func (s *Store) WithTx(ctx context.Context, fn func(q *sqlcgen.Queries) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) && err == nil {
			err = rbErr
		}
	}()
	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// IsNotFound reports whether err means a row was not found.
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// Timestamps are stored as fixed-width UTC strings with nanosecond precision
// ("2006-01-02T15:04:05.000000000Z") so they sort lexically (SQLite TEXT
// collation) and round-trip exactly. RFC3339Nano is NOT used: it trims
// trailing zeros, producing variable-width strings that mis-sort.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// FormatTime converts a time to its stored representation.
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// ParseTime converts a stored timestamp back to a time (accepts RFC 3339 too).
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	return t, err
}

// NullTime converts an optional time to its stored representation.
func NullTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: FormatTime(*t), Valid: true}
}

// ParseNullTime converts a stored optional timestamp back to a *time.Time.
func ParseNullTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid {
		return nil, nil
	}
	t, err := ParseTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
