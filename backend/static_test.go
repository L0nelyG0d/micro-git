package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSPAHandler(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "assets"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644))
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("api:" + r.URL.Path))
	})
	h := spaHandler(api, dir)

	cases := []struct {
		method, target string
		status         int
		body           string
	}{
		{"GET", "/", 200, "<html>app</html>"},
		{"GET", "/login", 200, "<html>app</html>"},
		{"GET", "/alice/repo/tree/main/src/x.go", 200, "<html>app</html>"},
		{"GET", "/assets/app.js", 200, "console.log(1)"},
		{"GET", "/assets/missing.js", 404, ""},
		{"GET", "/api/repos/o/n", 200, "api:/repos/o/n"},
		{"POST", "/api/auth/login", 200, "api:/auth/login"},
		{"GET", "/health", 200, "api:/health"},
		{"POST", "/login", 200, "api:/login"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.target, nil))
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != c.status {
			t.Errorf("%s %s: status %d, want %d", c.method, c.target, rec.Code, c.status)
		}
		if c.status == 200 && c.body != "" && string(body) != c.body {
			t.Errorf("%s %s: body %q, want %q", c.method, c.target, body, c.body)
		}
	}
}

func TestSPAHandlerOffWithoutDir(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	rec := httptest.NewRecorder()
	spaHandler(api, "").ServeHTTP(rec, httptest.NewRequest("GET", "/login", nil))
	if rec.Code != 204 {
		t.Fatalf("status %d, want API answer 204", rec.Code)
	}
}
