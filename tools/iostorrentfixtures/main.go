// Command iostorrentfixtures records native intake policy against the real backend parser.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anacrolix/torrent/bencode"

	"github.com/NathanBhanji/debrid-client/internal/torrentmeta"
)

type fixture struct {
	Name           string `json:"name"`
	Data           []byte `json:"data"`
	NativeAccepted bool   `json:"native_accepted"`
	ServerAccepted bool   `json:"server_accepted"`
	Hash           string `json:"hash,omitempty"`
	DisplayName    string `json:"display_name,omitempty"`
	Size           int64  `json:"size"`
	Files          int    `json:"files"`
}

func encode(value any) []byte {
	data, err := bencode.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
func single() map[string]any {
	return map[string]any{"name": "Fixture Ocean.mkv", "length": int64(1234), "piece length": int64(16384), "pieces": make([]byte, 20)}
}
func torrent(info map[string]any) []byte {
	return encode(map[string]any{"announce": "https://tracker.example.test/announce?token=fixture", "info": info})
}
func main() {
	cases := []fixture{}
	add := func(name string, info map[string]any, accepted bool) {
		cases = append(cases, fixture{Name: name, Data: torrent(info), NativeAccepted: accepted})
	}
	add("single", single(), true)
	zero := single()
	zero["length"] = int64(0)
	zero["pieces"] = []byte{}
	add("empty single", zero, true)
	utf8 := single()
	utf8["name.utf-8"] = ""
	add("empty utf8 name fallback", utf8, true)
	multi := map[string]any{"name": "Fixture Series", "piece length": int64(16384), "pieces": make([]byte, 20), "files": []any{map[string]any{"length": int64(10), "path": []string{"episode.mkv"}, "path.utf-8": []string{}}, map[string]any{"length": int64(5), "path": []string{"Subs", "episode.srt"}}}}
	add("multi and empty utf8 path fallback", multi, true)
	wrongPath := map[string]any{"name": "Wrong path type", "piece length": int64(16384), "pieces": make([]byte, 20), "files": []any{map[string]any{"length": int64(1), "path": []string{"good"}, "path.utf-8": int64(3)}}}
	add("wrong utf8 path type", wrongPath, false)

	hybrid := map[string]any{"name": "Hybrid fixture", "meta version": int64(2), "piece length": int64(16384), "pieces": make([]byte, 40), "files": []any{map[string]any{"length": int64(1), "path": []string{"a"}}, map[string]any{"length": int64(16383), "path": []string{".pad", "16383"}, "attr": "p"}, map[string]any{"length": int64(1), "path": []string{"b"}}}, "file tree": map[string]any{"a": map[string]any{"": map[string]any{"length": int64(1), "pieces root": make([]byte, 32)}}, "b": map[string]any{"": map[string]any{"length": int64(1), "pieces root": make([]byte, 32)}}}}
	cases = append(cases, fixture{Name: "hybrid padding and binary dictionary keys", Data: encode(map[string]any{"info": hybrid, "piece layers": map[string]any{string(append([]byte{255}, make([]byte, 31)...)): make([]byte, 32)}}), NativeAccepted: true})
	v2 := map[string]any{"name": "V2 fixture", "meta version": int64(2), "piece length": int64(16384), "file tree": map[string]any{"file": map[string]any{"": map[string]any{"length": int64(1)}}}}
	add("v2 only", v2, false)
	bad := single()
	bad["length"] = int64(-1)
	add("negative length", bad, false)
	bad = single()
	bad["pieces"] = []byte("short")
	add("short pieces", bad, false)
	bad = single()
	bad["piece length"] = int64(0)
	add("zero piece length", bad, false)
	bad = single()
	bad["length"] = int64(16385)
	add("piece count mismatch stricter native", bad, false)
	bad = single()
	bad["name"] = strings.Repeat("x", 4097)
	add("overlong name stricter native", bad, false)
	cases = append(cases, fixture{Name: "trailing garbage stricter native", Data: append(torrent(single()), []byte("extra")...)}, fixture{Name: "noncanonical integer", Data: []byte("d4:infod6:lengthi01e4:name1:a12:piece lengthi1e6:pieces20:00000000000000000000ee")}, fixture{Name: "duplicate key", Data: []byte("d4:infod4:name1:a4:name1:bee")})
	for i := range cases {
		m, err := torrentmeta.ParseTorrent(cases[i].Data)
		cases[i].ServerAccepted = err == nil
		if err == nil {
			cases[i].Hash = m.Hash
			cases[i].DisplayName = m.Name
			cases[i].Size = m.Size
			cases[i].Files = len(m.Files)
		}
		if cases[i].NativeAccepted && err != nil {
			fmt.Fprintln(os.Stderr, "native exceeds backend support:", cases[i].Name)
			os.Exit(1)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(cases); err != nil {
		panic(err)
	}
}
