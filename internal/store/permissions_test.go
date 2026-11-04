//go:build !windows

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The database contains the API key and provider credentials in plaintext.
// Use an existing traversable directory, as in the shipped Docker image.
func TestDatabaseCredentialsAreOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debrid.db")
	for _, existing := range []bool{false, true} {
		if existing {
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		s, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			info, err := os.Stat(path + suffix)
			if err != nil {
				_ = s.Close()
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o077 != 0 {
				_ = s.Close()
				t.Fatal("database credential file is accessible to other users")
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRepairsExistingSidecarPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debrid.db")
	first, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	// Keep a valid WAL/SHM pair present while simulating legacy file modes.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	second, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatal("legacy database credential file remains accessible to other users")
		}
	}
}
