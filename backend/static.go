package main

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// spaHandler serves the built frontend from dir next to the API.
//
// The frontend calls the API under /api (see frontend/src/services/http.ts),
// so requests to /api/... are stripped of the prefix and sent to api. /health
// is answered by api directly. Any other GET or HEAD request is a static file
// from dir when it exists; otherwise index.html is served so the client-side
// router can handle the path (SPA fallback). Paths under /assets/ never fall
// back, so a missing hashed bundle is a 404 and not HTML.
//
// An empty dir turns static serving off and returns api unchanged (local
// `go run` and the tests).
func spaHandler(api http.Handler, dir string) http.Handler {
	if dir == "" {
		return api
	}
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
			r2.URL.RawPath = ""
			api.ServeHTTP(w, r2)
			return
		}
		if r.URL.Path == "/health" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			api.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		clean := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(clean, "/assets/") || isRegularFile(filepath.Join(dir, filepath.FromSlash(clean))) {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index)
	})
}

// isRegularFile reports whether p names an existing regular file.
func isRegularFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
