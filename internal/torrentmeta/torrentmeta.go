// Package torrentmeta parses magnet links and .torrent files into the bits we
// need: info hash, display name, size and file list.
package torrentmeta

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/NathanBhanji/debrid-client/internal/domain"
)

// Meta is the parsed result.
type Meta struct {
	Hash  string // lowercase hex v1 info hash
	Name  string
	Size  int64
	Files []domain.File // empty for magnets (unknown until the provider reports)
}

// ErrInvalid is returned for unparsable input.
var ErrInvalid = errors.New("torrentmeta: invalid")

// ParseMagnet extracts the info hash and display name from a magnet URI.
func ParseMagnet(uri string) (Meta, error) {
	uri = strings.TrimSpace(uri)
	if !strings.HasPrefix(strings.ToLower(uri), "magnet:?") {
		return Meta{}, fmt.Errorf("%w: not a magnet uri", ErrInvalid)
	}
	m, err := metainfo.ParseMagnetV2Uri(uri)
	if err != nil {
		return Meta{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if !m.InfoHash.Ok {
		return Meta{}, fmt.Errorf("%w: magnet has no v1 info hash (btih)", ErrInvalid)
	}
	return Meta{Hash: m.InfoHash.Value.HexString(), Name: m.DisplayName}, nil
}

// ParseTorrent parses .torrent file bytes.
func ParseTorrent(data []byte) (Meta, error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return Meta{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return Meta{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if !info.HasV1() {
		return Meta{}, fmt.Errorf("%w: v2-only torrents are not supported by debrid providers", ErrInvalid)
	}
	out := Meta{Hash: mi.HashInfoBytes().HexString(), Name: info.BestName(), Size: info.TotalLength()}
	for i, f := range info.UpvertedFiles() {
		p := strings.Join(f.BestPath(), "/")
		if info.IsDir() {
			p = info.BestName() + "/" + p
		} else if p == "" {
			p = info.BestName() // single-file torrent: the file is the torrent name
		}
		out.Files = append(out.Files, domain.File{ID: fmt.Sprint(i), Path: p, Size: f.Length})
	}
	return out, nil
}

// IsMagnet reports whether s looks like a magnet link.
func IsMagnet(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "magnet:?")
}
