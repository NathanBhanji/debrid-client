package unpack

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // fixture checksum
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func makeZip(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if entries[n] == nil { // directory
			if _, err := zw.Create(n + "/"); err != nil {
				t.Fatal(err)
			}
			continue
		}
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entries[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestIsArchive(t *testing.T) {
	yes := []string{"a.rar", "a.part1.rar", "a.part01.rar", "A.ZIP", "x.7z", "x.7z.001", "t.tar.gz", "t.tgz", "t.tar"}
	no := []string{"a.r00", "a.r01", "a.part2.rar", "a.part02.rar", "x.7z.002", "x.z01", "movie.mkv", "readme.txt", "a.rar.txt"}
	for _, n := range yes {
		if !IsArchive(n) {
			t.Errorf("%s should be archive", n)
		}
	}
	for _, n := range no {
		if IsArchive(n) {
			t.Errorf("%s should NOT be primary archive", n)
		}
	}
	if !IsSecondaryVolume("a.r00") || !IsSecondaryVolume("a.part2.rar") || IsSecondaryVolume("a.rar") {
		t.Fatal("IsSecondaryVolume")
	}
}

func TestExtractZipIntoDestAndDelete(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "in", "pack.zip")
	makeZip(t, arc, map[string][]byte{"Show/": nil, "Show/e01.mkv": []byte("video1"), "Show/Subs/e01.srt": []byte("sub"), "readme.txt": []byte("hi")})
	dest := filepath.Join(dir, "out")
	res, err := Extract(context.Background(), arc, dest, Options{DeleteArchives: true})
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dest, "Show", "e01.mkv")) != "video1" || readFile(t, filepath.Join(dest, "Show", "Subs", "e01.srt")) != "sub" || readFile(t, filepath.Join(dest, "readme.txt")) != "hi" {
		t.Fatal("contents wrong")
	}
	if len(res.Files) != 3 || res.Bytes != int64(len("video1")+len("sub")+len("hi")) {
		t.Fatalf("result %+v", res)
	}
	if _, err := os.Stat(arc); !os.IsNotExist(err) {
		t.Fatal("archive should be deleted")
	}
	if len(res.Deleted) != 1 {
		t.Fatalf("deleted %v", res.Deleted)
	}
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		if e.Name()[0] == '.' {
			t.Fatalf("temp dir left behind: %s", e.Name())
		}
	}
}

func TestNestedArchivesDepth(t *testing.T) {
	dir := t.TempDir()
	innerZip := filepath.Join(dir, "inner.zip")
	makeZip(t, innerZip, map[string][]byte{"deep/file.txt": []byte("deep")})
	inner, _ := os.ReadFile(innerZip)
	outer := filepath.Join(dir, "outer.zip")
	makeZip(t, outer, map[string][]byte{"folder/inner.zip": inner, "folder/top.txt": []byte("top")})

	// Depth 0: inner archive is kept as a file.
	dest0 := filepath.Join(dir, "d0")
	res, err := Extract(context.Background(), outer, dest0, Options{MaxDepth: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest0, "folder", "inner.zip")); err != nil {
		t.Fatal("inner.zip should remain at depth 0")
	}
	if len(res.Files) != 2 {
		t.Fatalf("files %v", res.Files)
	}

	// Depth 1: inner archive extracted next to itself and removed.
	dest1 := filepath.Join(dir, "d1")
	res, err = Extract(context.Background(), outer, dest1, Options{MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dest1, "folder", "deep", "file.txt")) != "deep" {
		t.Fatal("nested content missing")
	}
	if _, err := os.Stat(filepath.Join(dest1, "folder", "inner.zip")); !os.IsNotExist(err) {
		t.Fatal("inner.zip should be consumed")
	}
	sort.Strings(res.Files)
	want := []string{filepath.Join("folder", "deep", "file.txt"), filepath.Join("folder", "top.txt")}
	if len(res.Files) != 2 || res.Files[0] != want[0] || res.Files[1] != want[1] {
		t.Fatalf("files %v want %v", res.Files, want)
	}
}

func TestZipSlipRejected(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "evil.zip")
	makeZip(t, arc, map[string][]byte{"../../escape.txt": []byte("x")})
	_, err := Extract(context.Background(), arc, filepath.Join(dir, "out"), Options{})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("file escaped destination")
	}
}

func TestExistingFilesAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "a.zip")
	makeZip(t, arc, map[string][]byte{"f.txt": []byte("new"), "sub/g.txt": []byte("g")})
	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(filepath.Join(dest, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "f.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Extract(context.Background(), arc, dest, Options{})
	if !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	if readFile(t, filepath.Join(dest, "f.txt")) != "old" {
		t.Fatal("existing file must be untouched without Overwrite")
	}
	if _, err := Extract(context.Background(), arc, dest, Options{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(dest, "f.txt")) != "new" || readFile(t, filepath.Join(dest, "sub", "g.txt")) != "g" {
		t.Fatal("overwrite/merge failed")
	}
}

func TestNotArchive(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "movie.mkv")
	_ = os.WriteFile(p, []byte("not an archive"), 0o644)
	if _, err := Extract(context.Background(), p, filepath.Join(dir, "out"), Options{}); !errors.Is(err, ErrNotArchive) {
		t.Fatalf("expected ErrNotArchive, got %v", err)
	}
}

func TestMultiVolumeRar(t *testing.T) {
	// testdata/test.part01.rar + test.part02.rar: `seq 0 2000 > test.txt; rar a -v1k test.rar test.txt`
	dir := t.TempDir()
	for _, n := range []string{"test.part01.rar", "test.part02.rar"} {
		b, err := os.ReadFile(filepath.Join("testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	vols := Volumes(filepath.Join(dir, "test.part01.rar"))
	if len(vols) != 2 {
		t.Fatalf("volumes %v", vols)
	}
	dest := filepath.Join(dir, "out")
	res, err := Extract(context.Background(), filepath.Join(dir, "test.part01.rar"), dest, Options{DeleteArchives: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(b) //nolint:gosec // fixture checksum
	if hex.EncodeToString(sum[:]) != "4da7f88f69b44a3fdb705667019a65f4c6e058a3" {
		t.Fatalf("content mismatch: %d bytes", len(b))
	}
	if len(res.Deleted) != 2 {
		t.Fatalf("both volumes should be deleted, got %v", res.Deleted)
	}
}

func TestFindArchives(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a/x.rar", "a/x.r00", "b/y.zip", "c.mkv", ".unpack-tmp/z.zip"} {
		p := filepath.Join(dir, n)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}
	got, err := FindArchives(dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if len(got) != 2 || got[0] != filepath.Join("a", "x.rar") || got[1] != filepath.Join("b", "y.zip") {
		t.Fatalf("got %v", got)
	}
}

func TestSafeRel(t *testing.T) {
	ok := map[string]string{"a/b.txt": filepath.Join("a", "b.txt"), "/abs/x": filepath.Join("abs", "x"), "./a//b": filepath.Join("a", "b"), `win\path\f`: filepath.Join("win", "path", "f"), ".": "", "": ""}
	for in, want := range ok {
		got, err := safeRel(in)
		if err != nil || got != want {
			t.Errorf("%q → %q,%v want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"../x", "a/../../x", "..", "a\x00b"} {
		if _, err := safeRel(in); err == nil {
			t.Errorf("%q should be rejected", in)
		}
	}
}

func TestVolumesIgnoresDecoys(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"foo.rar", "foo.r00", "foo.r01", "foo.s00", "foobar.r00", "foo.sample.r00", "foo2.part2.rar", "foo.part2.rar",
		"show.part1.rar", "show.part2.rar", "show.part10.rar", "showx.part2.rar", "show.sample.part2.rar", "show.part02.rar",
		"big.7z.001", "big.7z.002", "bigger.7z.002"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	got := func(primary string) []string {
		vs := Volumes(filepath.Join(dir, primary))
		out := make([]string, 0, len(vs))
		for _, v := range vs {
			out = append(out, filepath.Base(v))
		}
		sort.Strings(out)
		return out
	}
	want := func(a, b []string) {
		t.Helper()
		sort.Strings(b)
		if strings.Join(a, ",") != strings.Join(b, ",") {
			t.Fatalf("got %v want %v", a, b)
		}
	}
	want(got("foo.rar"), []string{"foo.rar", "foo.r00", "foo.r01", "foo.s00"})
	want(got("show.part1.rar"), []string{"show.part1.rar", "show.part2.rar"}) // same digit width only
	want(got("big.7z.001"), []string{"big.7z.001", "big.7z.002"})
	want(got("foo.zip"), []string{"foo.zip"})
}

func TestConflictCheckIsAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "a.zip")
	makeZip(t, arc, map[string][]byte{"a.txt": []byte("a"), "b.txt": []byte("b"), "c.txt": []byte("c")})
	dest := filepath.Join(dir, "out")
	_ = os.MkdirAll(dest, 0o755)
	_ = os.WriteFile(filepath.Join(dest, "b.txt"), []byte("old"), 0o644)
	if _, err := Extract(context.Background(), arc, dest, Options{}); !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	for _, n := range []string{"a.txt", "c.txt"} {
		if _, err := os.Stat(filepath.Join(dest, n)); !os.IsNotExist(err) {
			t.Fatalf("%s should not have been moved on conflict", n)
		}
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 1 {
		t.Fatalf("dest should be untouched (plus no temp dirs): %v", entries)
	}
	// Retry with Overwrite succeeds.
	if _, err := Extract(context.Background(), arc, dest, Options{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
}

func TestOverwriteNeverReplacesDirWithFile(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "a.zip")
	makeZip(t, arc, map[string][]byte{"data": []byte("file")})
	dest := filepath.Join(dir, "out")
	_ = os.MkdirAll(filepath.Join(dest, "data", "important"), 0o755)
	_ = os.WriteFile(filepath.Join(dest, "data", "important", "keep.txt"), []byte("k"), 0o644)
	if _, err := Extract(context.Background(), arc, dest, Options{Overwrite: true}); !errors.Is(err, ErrExists) {
		t.Fatalf("expected type-mismatch ErrExists, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "data", "important", "keep.txt")); err != nil {
		t.Fatal("directory tree was destroyed")
	}
}

func TestCancelMidEntry(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "big.zip")
	makeZip(t, arc, map[string][]byte{"big.bin": bytes.Repeat([]byte("z"), 64<<20)})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := Extract(ctx, arc, filepath.Join(dir, "out"), Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel not honoured promptly")
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "big.bin")); !os.IsNotExist(err) {
		t.Fatal("partial file should not be moved into dest")
	}
}

func TestMultiVolume7z(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 6; i++ {
		n := fmt.Sprintf("multi.7z.%03d", i)
		b, err := os.ReadFile(filepath.Join("testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, n), b, 0o644)
	}
	dest := filepath.Join(dir, "out")
	res, err := Extract(context.Background(), filepath.Join(dir, "multi.7z.001"), dest, Options{DeleteArchives: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) == 0 || res.Bytes == 0 {
		t.Fatalf("nothing extracted: %+v", res)
	}
	if len(res.Deleted) != 6 {
		t.Fatalf("all 6 volumes should be deleted, got %v", res.Deleted)
	}
}

func TestNestedAccounting(t *testing.T) {
	dir := t.TempDir()
	innermost := filepath.Join(dir, "innermost.zip")
	makeZip(t, innermost, map[string][]byte{"deep.txt": []byte("deep")})
	innermostBytes, _ := os.ReadFile(innermost)
	inner := filepath.Join(dir, "inner.zip")
	makeZip(t, inner, map[string][]byte{"mid.txt": []byte("mid-mid"), "innermost.zip": innermostBytes})
	innerBytes, _ := os.ReadFile(inner)
	outer := filepath.Join(dir, "outer.zip")
	makeZip(t, outer, map[string][]byte{"top.txt": []byte("top"), "sub/inner.zip": innerBytes})

	dest := filepath.Join(dir, "out")
	res, err := Extract(context.Background(), outer, dest, Options{MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Depth 1: inner.zip consumed; innermost.zip left as a file and must be reported.
	sort.Strings(res.Files)
	want := []string{filepath.Join("sub", "innermost.zip"), filepath.Join("sub", "mid.txt"), "top.txt"}
	if strings.Join(res.Files, ",") != strings.Join(want, ",") {
		t.Fatalf("files %v want %v", res.Files, want)
	}
	var total int64
	for _, f := range res.Files {
		fi, _ := os.Stat(filepath.Join(dest, f))
		total += fi.Size()
	}
	if res.Bytes != total {
		t.Fatalf("bytes %d want %d (no double counting)", res.Bytes, total)
	}
	if len(res.Deleted) != 1 || !strings.HasSuffix(res.Deleted[0], filepath.Join("sub", "inner.zip")) {
		t.Fatalf("consumed inner archive should be reported as deleted relative to dest: %v", res.Deleted)
	}
}

func TestSafeRelStripsDrive(t *testing.T) {
	got, err := safeRel(`C:\Windows\x.txt`)
	if err != nil || got != filepath.Join("Windows", "x.txt") {
		t.Fatalf("%q %v", got, err)
	}
}
