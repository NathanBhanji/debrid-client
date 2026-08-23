package service

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/NathanBhanji/debrid-client/internal/domain"
)

// TorrentDir is the directory under root where a torrent's files live:
// <root>/[<category>/]<sanitised name>. Engine and service must agree on this.
func TorrentDir(root string, t domain.Torrent) string {
	name := SanitizeName(t.Name)
	if name == "" {
		name = t.Hash
	}
	if t.Category != "" {
		return filepath.Join(root, t.Category, name)
	}
	return filepath.Join(root, name)
}

// SanitizeName makes a torrent/file name safe as a single path component.
func SanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' || r == 0:
			b.WriteRune('_')
		case unicode.IsControl(r):
			// drop
		default:
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(b.String())
	s = strings.Trim(s, ".")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
