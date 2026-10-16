package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONBodyLimit(t *testing.T) {
	t.Parallel()
	s := NewServer(nil)
	const limit = 1 << 20
	for path, wantMsg := range map[string]string{
		"/auth/register": "password must be 8-72 bytes",
		"/auth/login":    "username and password are required",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			do := func(body string) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
				return rec
			}
			// Valid JSON padded with spaces to limit+1 bytes.
			head := `{"username":"alice","password":""}`
			wantError(t, do(head+strings.Repeat(" ", limit+1-len(head))), http.StatusRequestEntityTooLarge, "request too large")
			// Garbage over the limit is still "too large", not "invalid JSON".
			wantError(t, do(strings.Repeat("x", limit+1)), http.StatusRequestEntityTooLarge, "request too large")
			// Just under the limit, valid JSON: decoded normally (400 for the
			// empty password, not 413).
			wantError(t, do(head+strings.Repeat(" ", limit-len(head))), http.StatusBadRequest, wantMsg)
		})
	}
}
