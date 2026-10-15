package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, method, path string
		wantStatus         int
		wantBody           string
	}{
		{"health", http.MethodGet, "/health", 200, "OK"},
		{"unknown route", http.MethodGet, "/nope", 404, `{"error":"not found"}`},
		{"wrong method", http.MethodPost, "/health", 405, `{"error":"method not allowed"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			NewHandler().ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Errorf("body = %q, want %q", got, tc.wantBody)
			}
		})
	}
}

func TestHealthIsPlainText(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	NewHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}
