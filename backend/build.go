package main

import "net/http"

// buildID names the running build for the X-Build response header: the
// Render commit, else APP_COMMIT, else "dev". Deploy smoke checks compare it
// to the commit they expect, so they never pass against a stale instance.
func buildID(getenv func(string) string) string {
	for _, k := range []string{"RENDER_GIT_COMMIT", "APP_COMMIT"} {
		if v := getenv(k); v != "" {
			return v
		}
	}
	return "dev"
}

// withBuildHeader adds X-Build to every response.
func withBuildHeader(next http.Handler, id string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Build", id)
		next.ServeHTTP(w, r)
	})
}
