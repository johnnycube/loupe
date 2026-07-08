package main

import (
	"net/http"
	"runtime"
	"runtime/debug"
)

// Build metadata, injected at link time via -ldflags "-X main.buildTime=… …"
// (see the Makefile `build` target). They stay empty for `go run`/`go build`
// without ldflags; in that case versionInfo() backfills the commit, build/commit
// time, and dirty flag from the build info the Go toolchain embeds automatically
// (Go 1.18+), so the About page is still useful in development.
var (
	buildTime string
	gitCommit string
	gitTag    string
)

type VersionInfo struct {
	BuildTime string `json:"buildTime"` // RFC3339; build time for releases, commit time in dev
	Commit    string `json:"commit"`
	Tag       string `json:"tag"`
	Dirty     bool   `json:"dirty"` // built from a tree with uncommitted changes
	GoVersion string `json:"goVersion"`
}

func versionInfo() VersionInfo {
	v := VersionInfo{
		BuildTime: buildTime,
		Commit:    gitCommit,
		Tag:       gitTag,
		GoVersion: runtime.Version(),
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if v.Commit == "" {
					v.Commit = s.Value
				}
			case "vcs.time":
				if v.BuildTime == "" {
					v.BuildTime = s.Value
				}
			case "vcs.modified":
				if s.Value == "true" {
					v.Dirty = true
				}
			}
		}
	}
	return v
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, versionInfo())
}
