package service

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/organize"
	"github.com/NathanBhanji/debrid-client/internal/relname"
)

// TorrentDir is the directory under root where a torrent's files live:
// <root>/[<category>/]<dir name>. The dir name is t.DirName once the engine has
// frozen it (at first local download), else derived from the current Name.
// Engine and service must agree on this.
func TorrentDir(root string, t domain.Torrent) string {
	name := filepath.FromSlash(DirNameFor(t))
	if t.Category != "" {
		return filepath.Join(root, t.Category, name)
	}
	return filepath.Join(root, name)
}

// DirNameFor returns the folder name for a torrent: the frozen DirName, else
// the sanitised Name, else the hash.
func DirNameFor(t domain.Torrent) string {
	if t.DirName != "" {
		return t.DirName
	}
	if name := SanitizeName(t.Name); name != "" {
		return name
	}
	return t.Hash
}

// DirPathFor computes the directory path to freeze for a torrent: the
// organized "/"-separated library path when organization applies and the
// release name parses confidently, else the plain sanitized name. organized
// reports which one was chosen (organized dirs may be shared, so deletion
// must be per-file).
func DirPathFor(t domain.Torrent, org organize.Settings) (path string, organized bool) {
	enabled := org.Enabled
	if t.Settings.Organize != nil {
		enabled = *t.Settings.Organize
	}
	if !enabled {
		return DirNameFor(t), false
	}
	info, ok := relname.Parse(t.Name)
	if !ok {
		return DirNameFor(t), false
	}
	segs, ok := organize.Render(org.Template(info), info)
	if !ok {
		return DirNameFor(t), false
	}
	clean := make([]string, 0, len(segs))
	for _, seg := range segs {
		if s := SanitizeName(seg); s != "" {
			clean = append(clean, s)
		}
	}
	if len(clean) == 0 {
		return DirNameFor(t), false
	}
	return strings.Join(clean, "/"), true
}

// maxNameLen bounds a path component (most filesystems allow 255 bytes).
const maxNameLen = 200

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// SanitizeName makes a torrent/file name safe as a single path component:
// path separators and other characters illegal on common filesystems become
// "_", control/format characters are dropped, surrounding dots/spaces are
// trimmed, Windows reserved names are prefixed, and the result is truncated
// on a rune boundary. Returns "" if nothing usable remains.
func SanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' || r == 0:
			b.WriteRune('_')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r): // incl. bidi overrides
			// drop
		default:
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > maxNameLen {
		cut := maxNameLen
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	s = strings.Trim(strings.TrimSpace(s), ". ")
	if s == "" {
		return ""
	}
	base := strings.ToUpper(s)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if windowsReserved[base] {
		s = "_" + s
	}
	return s
}
