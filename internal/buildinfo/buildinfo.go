// Package buildinfo exposes version metadata injected at build time via -ldflags.
package buildinfo

import (
	"runtime/debug"
	"sync"
)

// Set via -ldflags "-X github.com/NathanBhanji/debrid-client/internal/buildinfo.Version=..." etc.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

var (
	once     sync.Once
	resolved string
)

// String returns a human-readable version line. When Commit was not injected
// it is resolved once from the embedded VCS info (go install / go build).
func String() string {
	once.Do(func() {
		commit, dirty := Commit, false
		if commit == "" {
			if bi, ok := debug.ReadBuildInfo(); ok {
				for _, s := range bi.Settings {
					switch s.Key {
					case "vcs.revision":
						if len(s.Value) >= 7 {
							commit = s.Value[:7]
						}
					case "vcs.modified":
						dirty = s.Value == "true"
					}
				}
			}
		}
		v := Version
		if commit != "" {
			if dirty {
				commit += "-dirty"
			}
			v += " (" + commit + ")"
		}
		if Date != "" {
			v += " built " + Date
		}
		resolved = v
	})
	return resolved
}
