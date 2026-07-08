package main

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// The SvelteKit static build is embedded at compile time. Use `all:` so files
// under _app/ (leading underscore) are included. Run `npm run build` in
// frontend/ to (re)generate frontend/build before `go build`.
//
//go:embed all:frontend/build
var buildFS embed.FS

// staticHandler serves the embedded SvelteKit build with an SPA fallback:
// unknown navigation routes fall back to index.html for client-side routing.
//
// Asset requests are treated differently from navigation requests. The hashed
// build artifacts under _app/ (e.g. start.GzRRONfo.js) are renamed on every
// frontend build — that churn is expected, but it means a stale client still
// running an old index.html can request an asset that no longer exists. Such a
// miss must 404, not fall back to index.html: serving HTML in place of a
// .js/.css yields a MIME-type error that breaks the page silently (and the
// client should treat the 404 as a signal to reload). Only extensionless,
// non-_app paths — genuine navigation routes — fall back to index.html.
func staticHandler() http.Handler {
	sub, err := fs.Sub(buildFS, "frontend/build")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, statErr := fs.Stat(sub, p); statErr != nil {
			if path.Ext(p) != "" || strings.HasPrefix(p, "_app/") {
				http.NotFound(w, r) // missing asset → 404, never the HTML shell
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/" // navigation route → SPA fallback to index.html
		}
		fileServer.ServeHTTP(w, r)
	})
}
