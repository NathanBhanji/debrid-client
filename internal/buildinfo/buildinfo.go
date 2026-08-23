// Package buildinfo exposes version metadata injected at build time via -ldflags.
package buildinfo

import "runtime/debug"

// Set via -ldflags "-X github.com/NathanBhanji/debrid-client/internal/buildinfo.Version=..." etc.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// String returns a human-readable version line.
func String() string {
	v := Version
	if Commit == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" && len(s.Value) >= 7 {
					Commit = s.Value[:7]
				}
			}
		}
	}
	if Commit != "" {
		v += " (" + Commit + ")"
	}
	if Date != "" {
		v += " built " + Date
	}
	return v
}
