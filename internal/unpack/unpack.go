// Package unpack extracts archives (rar incl. multi-volume, zip, 7z, tar.*)
// safely into a destination directory, with nested-archive handling.
//
// Extraction goes to a temporary sibling directory first and is moved into
// place only on success, so a crash never leaves half-written files where the
// rest of the system might pick them up.
package unpack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/mholt/archives"
)

// Options configure extraction.
type Options struct {
	// MaxDepth bounds nested extraction (archive inside archive). 0 = only the
	// top-level archive; 1 = one level of nesting, etc.
	MaxDepth int
	// Password for encrypted rar/7z archives.
	Password string
	// DeleteArchives removes each archive (and all its volumes) after a
	// successful extraction.
	DeleteArchives bool
	// Overwrite allows replacing files that already exist in dest.
	Overwrite bool
}

// Result summarises what was extracted.
type Result struct {
	Files   []string // paths relative to dest
	Bytes   int64
	Deleted []string // archive files removed
}

var (
	// ErrNotArchive is returned when the file is not a supported archive.
	ErrNotArchive = errors.New("unpack: not a supported archive")
	// ErrUnsafePath is returned when an entry would escape the destination.
	ErrUnsafePath = errors.New("unpack: unsafe path in archive")
	// ErrExists is returned when an entry already exists and Overwrite is false.
	ErrExists = errors.New("unpack: destination exists")
)

var (
	// .r00, .r01 … (.s00 … past 100) and .part2.rar, .part02.rar … are secondary RAR volumes.
	rarSecondary = regexp.MustCompile(`(?i)(\.[rs]\d{2}$|\.part(0*[2-9]|0*[1-9]\d+)\.rar$)`)
	// .7z.002 … secondary 7z volumes. (Split zips — .z01 — are not supported.)
	otherSecondary = regexp.MustCompile(`(?i)\.7z\.0*([2-9]|[1-9]\d+)$`)
	primaryExts    = []string{".rar", ".zip", ".7z", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz", ".tar.zst", ".7z.001"}
)

// IsArchive reports whether name looks like the *first* volume of a supported
// archive (secondary volumes like .r00 or .part2.rar return false so they are
// never extracted on their own).
func IsArchive(name string) bool {
	lower := strings.ToLower(name)
	if rarSecondary.MatchString(lower) || otherSecondary.MatchString(lower) {
		return false
	}
	for _, ext := range primaryExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// IsSecondaryVolume reports whether name is a continuation volume of a
// multi-part archive (it should be deleted together with the primary).
func IsSecondaryVolume(name string) bool {
	lower := strings.ToLower(name)
	return rarSecondary.MatchString(lower) || otherSecondary.MatchString(lower)
}

// Extract unpacks archive into dest (created if needed). Nested archives found
// in the output are extracted in place up to opts.MaxDepth.
func Extract(ctx context.Context, archive, dest string, opts Options) (Result, error) {
	var res Result
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return res, err
	}
	if err := extractInto(ctx, archive, dest, opts, 0, &res); err != nil {
		return res, err
	}
	return res, nil
}

func extractInto(ctx context.Context, archive, dest string, opts Options, depth int, res *Result) error {
	tmp, err := os.MkdirTemp(dest, ".unpack-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	extracted, err := extractOne(ctx, archive, tmp, opts)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Nested archives: extract them inside tmp before moving anything.
	consumed := map[string]bool{}
	if depth < opts.MaxDepth {
		for _, rel := range extracted {
			if !IsArchive(rel) {
				continue
			}
			inner := filepath.Join(tmp, rel)
			var innerRes Result
			innerOpts := Options{MaxDepth: opts.MaxDepth, Password: opts.Password, DeleteArchives: true, Overwrite: opts.Overwrite}
			if err := extractInto(ctx, inner, filepath.Dir(inner), innerOpts, depth+1, &innerRes); err != nil {
				return fmt.Errorf("nested %s: %w", rel, err)
			}
			consumed[rel] = true
			// innerRes.Files are relative to the inner archive's dir; rebase to tmp.
			for _, f := range innerRes.Files {
				extracted = append(extracted, filepath.Join(filepath.Dir(rel), f))
			}
			// Inner archives were deleted inside tmp; report them relative to dest.
			for _, d := range innerRes.Deleted {
				if r, err := filepath.Rel(tmp, d); err == nil {
					res.Deleted = append(res.Deleted, filepath.Join(dest, r))
				}
			}
		}
	}

	// All-or-nothing: refuse before moving anything if any file would collide.
	if !opts.Overwrite {
		if err := checkConflicts(tmp, dest); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := moveInto(filepath.Join(tmp, e.Name()), filepath.Join(dest, e.Name()), opts.Overwrite); err != nil {
			return err
		}
	}
	for _, rel := range extracted {
		if consumed[rel] {
			continue
		}
		full := filepath.Join(dest, rel)
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			res.Files = append(res.Files, rel)
			res.Bytes += fi.Size()
		}
	}

	if opts.DeleteArchives {
		for _, v := range Volumes(archive) {
			if err := os.Remove(v); err == nil {
				res.Deleted = append(res.Deleted, v)
			}
		}
	}
	return nil
}

// checkConflicts returns ErrExists if any regular file under tmp already
// exists under dest (directories may merge).
func checkConflicts(tmp, dest string) error {
	return filepath.WalkDir(tmp, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(tmp, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("%w: %s", ErrExists, target)
		}
		return nil
	})
}

// extractOne extracts a single archive into dir and returns the relative paths
// of regular files written.
func extractOne(ctx context.Context, archive, dir string, opts Options) ([]string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var files []string
	// writeEntry writes one archive entry (regular file) to dir, honouring ctx
	// between reads so a cancelled multi-GB entry stops promptly.
	writeEntry := func(name string, isDir bool, mode fs.FileMode, open func() (io.ReadCloser, error)) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := safeRel(name)
		if err != nil {
			return err
		}
		if rel == "" {
			return nil
		}
		target := filepath.Join(dir, rel)
		if isDir {
			return os.MkdirAll(target, 0o755)
		}
		if mode&(os.ModeSymlink|os.ModeDevice|os.ModeNamedPipe|os.ModeSocket) != 0 {
			return nil // never materialise symlinks/special files from untrusted archives
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		r, err := open()
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		w, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, &ctxReader{ctx: ctx, r: r}); err != nil {
			_ = w.Close()
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	}

	// Multi-volume 7z (.7z.001) needs the file-based opener, which chains volumes.
	if strings.HasSuffix(strings.ToLower(archive), ".7z.001") {
		zr, err := sevenzip.OpenReaderWithPassword(archive, opts.Password)
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", filepath.Base(archive), err)
		}
		defer func() { _ = zr.Close() }()
		for _, zf := range zr.File {
			if err := writeEntry(zf.Name, zf.Mode().IsDir(), zf.Mode(), zf.Open); err != nil {
				return nil, fmt.Errorf("extract %s: %w", filepath.Base(archive), err)
			}
		}
		return files, nil
	}

	format, input, err := archives.Identify(ctx, archive, f)
	if err != nil {
		if errors.Is(err, archives.NoMatch) {
			return nil, fmt.Errorf("%w: %s", ErrNotArchive, filepath.Base(archive))
		}
		return nil, err
	}
	var ex archives.Extractor
	switch fmtT := format.(type) {
	case archives.Rar:
		// Name+FS lets rardecode find subsequent volumes (.r00 / .part2.rar).
		ex = archives.Rar{Password: opts.Password, Name: filepath.Base(archive), FS: os.DirFS(filepath.Dir(archive))}
	case archives.SevenZip:
		ex = archives.SevenZip{Password: opts.Password}
	default:
		var ok bool
		ex, ok = fmtT.(archives.Extractor)
		if !ok {
			return nil, fmt.Errorf("%w: %s (%s)", ErrNotArchive, filepath.Base(archive), format.Extension())
		}
	}
	handler := func(_ context.Context, fi archives.FileInfo) error {
		if fi.LinkTarget != "" {
			return nil // symlinks and tar hardlinks are skipped
		}
		return writeEntry(fi.NameInArchive, fi.IsDir(), fi.Mode(), func() (io.ReadCloser, error) { return fi.Open() })
	}
	if err := ex.Extract(ctx, input, handler); err != nil {
		return nil, fmt.Errorf("extract %s: %w", filepath.Base(archive), err)
	}
	return files, nil
}

// ctxReader aborts reads once ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// safeRel cleans an archive entry name and rejects anything escaping the root.
func safeRel(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	// Drop Windows drive / UNC prefixes ("C:/x", "//server/share/x").
	if len(name) >= 2 && name[1] == ':' && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) {
		name = name[2:]
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+name), "/") // drops leading slashes and "./"
	if cleaned == "." || cleaned == "" {
		return "", nil
	}
	return filepath.FromSlash(cleaned), nil
}

// moveInto renames src to dst, merging directories.
func moveInto(src, dst string, overwrite bool) error {
	sfi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	dfi, err := os.Lstat(dst)
	switch {
	case err != nil && os.IsNotExist(err):
		return os.Rename(src, dst)
	case err != nil:
		return err
	case sfi.IsDir() && dfi.IsDir():
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := moveInto(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), overwrite); err != nil {
				return err
			}
		}
		return os.Remove(src)
	case !overwrite:
		return fmt.Errorf("%w: %s", ErrExists, dst)
	case sfi.IsDir() != dfi.IsDir():
		// Never replace a directory tree with a file (or vice versa) on overwrite.
		return fmt.Errorf("%w: %s (type mismatch)", ErrExists, dst)
	default:
		if err := os.Remove(dst); err != nil {
			return err
		}
		return os.Rename(src, dst)
	}
}

// Volumes returns the archive itself plus its sibling continuation volumes,
// matched by an exact per-style pattern derived from the primary's name so
// that "foo.rar" never claims "foobar.r00" or "foo.sample.r00":
//   - stem.partN.rar → stem.part<digits>.rar (same digit width)
//   - stem.rar       → stem.r00 … stem.r99, stem.s00 …
//   - stem.7z.001    → stem.7z.002 …
func Volumes(archive string) []string {
	out := []string{archive}
	dir, base := filepath.Split(archive)
	re := volumePattern(base)
	if re == nil {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || strings.EqualFold(e.Name(), base) {
			continue
		}
		if re.MatchString(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

var partRe = regexp.MustCompile(`(?i)^(.*)\.part(\d+)\.rar$`)

// volumePattern returns a case-insensitive regexp matching the continuation
// volumes of the given primary archive name, or nil if it has none.
func volumePattern(base string) *regexp.Regexp {
	lower := strings.ToLower(base)
	switch {
	case partRe.MatchString(base):
		m := partRe.FindStringSubmatch(base)
		return regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(m[1]) + `\.part\d{` + strconv.Itoa(len(m[2])) + `}\.rar$`)
	case strings.HasSuffix(lower, ".rar"):
		return regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(base[:len(base)-4]) + `\.[rs]\d{2}$`)
	case strings.HasSuffix(lower, ".7z.001"):
		return regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(base[:len(base)-4]) + `\.\d{3}$`)
	}
	return nil
}

// FindArchives walks root and returns primary archive files, relative to root.
func FindArchives(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".unpack-") {
				return filepath.SkipDir
			}
			return nil
		}
		if IsArchive(d.Name()) {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}
