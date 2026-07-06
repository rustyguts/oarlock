// Package webui embeds the static SvelteKit build (web/build) and serves it
// with SPA fallback semantics: real files are served as-is, anything else
// gets index.html and the client router takes over. The image build copies
// web/build into dist/ before compiling; a checked-in placeholder index.html
// keeps plain `go build ./...` working when the UI hasn't been built.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves the embedded UI. Mount it at "/" — API routes registered on
// more specific patterns take precedence in http.ServeMux.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("webui: embedded dist missing: " + err.Error())
	}
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if info, err := fs.Stat(sub, path); err == nil && !info.IsDir() {
				// SvelteKit emits content-hashed assets under /_app/immutable —
				// safe to cache forever.
				if strings.HasPrefix(path, "_app/immutable/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: serve index.html for /, client routes, and unknown
		// paths. Never cached, so deploys pick up the new asset manifest.
		w.Header().Set("Cache-Control", "no-cache")
		index, err := sub.Open("index.html")
		if err != nil {
			http.Error(w, "ui not embedded in this build", http.StatusNotFound)
			return
		}
		defer index.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "ui not embedded in this build", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	})
}
