package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildID(t *testing.T) {
	t.Parallel()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		env  map[string]string
		want string
	}{
		{nil, "dev"},
		{map[string]string{"APP_COMMIT": "app"}, "app"},
		{map[string]string{"APP_COMMIT": "app", "RENDER_GIT_COMMIT": "r"}, "r"},
	}
	for _, c := range cases {
		if got := buildID(env(c.env)); got != c.want {
			t.Errorf("%v: got %q want %q", c.env, got, c.want)
		}
	}
}

func TestWithBuildHeader(t *testing.T) {
	t.Parallel()
	h := withBuildHeader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}), "abc123")
	for _, path := range []string{"/health", "/nope"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if got := rec.Header().Get("X-Build"); got != "abc123" {
			t.Errorf("%s: X-Build = %q", path, got)
		}
	}
}
