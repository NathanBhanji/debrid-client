// Command iosmagnetfixtures captures the real backend parser's behavior for native intake tests.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/NathanBhanji/debrid-client/internal/torrentmeta"
)

type fixture struct {
	Name           string `json:"name"`
	URI            string `json:"uri"`
	NativeAccepted bool   `json:"native_accepted"`
	ServerAccepted bool   `json:"server_accepted"`
	Hash           string `json:"hash,omitempty"`
	DisplayName    string `json:"display_name,omitempty"`
}

func main() {
	hash := "0123456789ABCDEF0123456789ABCDEF01234567"
	base := "magnet:?xt=urn:btih:" + hash
	v2 := "urn:btmh:1220" + strings.Repeat("0", 64)
	cases := []fixture{
		{Name: "hex", URI: base, NativeAccepted: true},
		{Name: "base32", URI: "magnet:?xt=urn:btih:AEBAGBAFAYDQQCIKBMGA2DQPCAIREEYU", NativeAccepted: true},
		{Name: "case scheme", URI: "MAGNET:?xt=urn:btih:" + hash, NativeAccepted: true},
		{Name: "surrounding whitespace", URI: "\u2003\t" + base + "\r\n", NativeAccepted: true},
		{Name: "encoded hash and key", URI: "magnet:?%78%74=urn%3Abtih%3A" + hash, NativeAccepted: true},
		{Name: "repeated trackers and plus", URI: base + "&dn=My+Show%2BNotes&tr=udp%3A%2F%2Ftracker.example.test%3A80%2Fannounce&tr=https%3a%2f%2ftracker.example.test%2fa%3fpasskey%3dfixture&x.pe=peer.example.test%3A1234", NativeAccepted: true},
		{Name: "first display name", URI: base + "&dn=First&dn=Second", NativeAccepted: true},
		{Name: "unknown topic and hybrid", URI: base + "&xt=urn:sha1:ignored&xt=" + v2, NativeAccepted: true},
		{Name: "v2 only", URI: "magnet:?xt=" + v2},
		{Name: "duplicate v1", URI: base + "&xt=urn:btih:" + hash},
		{Name: "duplicate v2", URI: base + "&xt=" + v2 + "&xt=" + v2},
		{Name: "bad v2", URI: base + "&xt=urn:btmh:1220abcd"},
		{Name: "lowercase base32", URI: "magnet:?xt=urn:btih:aebagbafaydqqcikbmga2dqpcaireeyu"},
		{Name: "bad hex", URI: "magnet:?xt=urn:btih:" + strings.Repeat("z", 40)},
		{Name: "wrong scheme", URI: "https://example.test/?xt=urn:btih:" + hash},
		{Name: "missing v1", URI: "magnet:?dn=NoHash"},
		{Name: "uppercase topic", URI: "magnet:?xt=URN:BTIH:" + hash},
		{Name: "short hash", URI: "magnet:?xt=urn:btih:0123"},
		{Name: "fragment stricter native", URI: base + "#fragment"},
		{Name: "raw space stricter native", URI: base + "&dn=Raw Space"},
		{Name: "bad unknown escape stricter native", URI: base + "&ignored=%zz"},
		{Name: "semicolon stricter native", URI: base + "&ignored=a;b"},
		{Name: "invalid utf8 stricter native", URI: base + "&dn=%ff"},
		{Name: "encoded control stricter native", URI: base + "&dn=Line%0aBreak"},
	}
	for i := range cases {
		meta, err := torrentmeta.ParseMagnet(cases[i].URI)
		cases[i].ServerAccepted = err == nil
		if err == nil {
			cases[i].Hash = meta.Hash
			cases[i].DisplayName = meta.Name
		}
		if cases[i].NativeAccepted && err != nil {
			fmt.Fprintln(os.Stderr, "native policy exceeds backend support:", cases[i].Name)
			os.Exit(1)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(cases); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
