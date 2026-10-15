package main

import (
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Server holds the dependencies handlers share. Later tasks add fields here
// (JWT secret, cookie settings) and register routes in routes().
type Server struct {
	DB  *pgxpool.Pool
	Now func() time.Time // injected clock
}

// NewServer builds a Server with the real clock.
func NewServer(db *pgxpool.Pool) *Server {
	return &Server{DB: db, Now: time.Now}
}

// NewHandler returns the routes for a Server without a database (health only
// works). Kept for tests that need no database.
func NewHandler() http.Handler {
	return NewServer(nil).Handler()
}

// Handler builds the HTTP routes. It is separate from the listener so tests
// need no real port.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return mux
}

// routes is the single place endpoints are registered.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/auth/register", allow(http.MethodPost, s.register))
	mux.HandleFunc("/", notFound)
}

// allow answers 405 with an Allow header for any method but the given one.
func allow(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h(w, r)
	}
}

func health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not found")
}

func logError(err error) { log.Printf("error: %v", err) }
