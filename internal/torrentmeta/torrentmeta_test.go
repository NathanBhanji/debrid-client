package torrentmeta

import (
	"bytes"
	"errors"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func TestParseMagnet(t *testing.T) {
	m, err := ParseMagnet("magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=My+Show&tr=udp%3A%2F%2Ft")
	if err != nil || m.Hash != "0123456789abcdef0123456789abcdef01234567" || m.Name != "My Show" {
		t.Fatalf("%+v %v", m, err)
	}
	// base32 hash form
	if m, err := ParseMagnet("magnet:?xt=urn:btih:AEBAGBAFAYDQQCIKBMGA2DQPCAIREEYU"); err != nil || len(m.Hash) != 40 {
		t.Fatalf("base32: %+v %v", m, err)
	}
	for _, bad := range []string{"http://x", "magnet:?dn=nohash", ""} {
		if _, err := ParseMagnet(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q should be invalid, got %v", bad, err)
		}
	}
	if !IsMagnet(" MAGNET:?xt=1") || IsMagnet("http://") {
		t.Fatal("IsMagnet")
	}
}

func buildTorrent(t *testing.T, info metainfo.Info) []byte {
	t.Helper()
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: ib, Announce: "udp://tracker"}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseTorrentSingleAndMulti(t *testing.T) {
	single := buildTorrent(t, metainfo.Info{Name: "movie.mkv", Length: 1234, PieceLength: 16384, Pieces: make([]byte, 20)})
	m, err := ParseTorrent(single)
	if err != nil || m.Name != "movie.mkv" || m.Size != 1234 || len(m.Hash) != 40 || len(m.Files) != 1 || m.Files[0].Path != "movie.mkv" {
		t.Fatalf("single: %+v %v", m, err)
	}
	multi := buildTorrent(t, metainfo.Info{Name: "Show", PieceLength: 16384, Pieces: make([]byte, 20), Files: []metainfo.FileInfo{
		{Path: []string{"e01.mkv"}, Length: 10}, {Path: []string{"Subs", "e01.srt"}, Length: 5},
	}})
	m, err = ParseTorrent(multi)
	if err != nil || m.Name != "Show" || m.Size != 15 || len(m.Files) != 2 || m.Files[1].Path != "Show/Subs/e01.srt" || m.Files[1].ID != "1" {
		t.Fatalf("multi: %+v %v", m, err)
	}
	if _, err := ParseTorrent([]byte("garbage")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("garbage should be invalid: %v", err)
	}
}
