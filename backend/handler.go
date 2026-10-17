package main

import (
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Server holds the dependencies handlers share. Routes are registered in
// routes().
type Server struct {
	DB           *pgxpool.Pool
	Now          func() time.Time // injected clock
	JWTSecret    []byte           // HMAC key for session tokens
	CookieSecure bool             // Secure attribute on the session cookie
	limiter      *limiter         // login/register rate limiter

	// hook, if set (tests only), is called at named points of the read
	// handlers so a test can act between two steps without sleeping.
	hook func(point string)
}

func (s *Server) testHook(point string) {
	if s.hook != nil {
		s.hook(point)
	}
}

// NewServer builds a Server with the real clock.
func NewServer(db *pgxpool.Pool) *Server {
	s := &Server{DB: db, Now: time.Now}
	// The limiter reads s.Now at call time so a test can swap the clock.
	s.limiter = newLimiter(func() time.Time { return s.Now() })
	return s
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
	return safeHeaders(mux)
}

// safeHeaders sets the basic safe response headers before any handler runs, so
// 404 and 405 answers carry them too.
func safeHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// routes is the single place endpoints are registered.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/auth/register", allow(http.MethodPost, s.limitRegister(s.register)))
	mux.HandleFunc("/auth/login", allow(http.MethodPost, s.login))
	mux.HandleFunc("/auth/logout", allow(http.MethodPost, s.logout))
	mux.HandleFunc("/auth/me", allow(http.MethodGet, s.requireAuth(s.me)))
	mux.HandleFunc("/repos", methods{
		http.MethodGet:  s.listRepos,
		http.MethodPost: s.requireAuth(s.createRepo),
	}.serve)
	mux.HandleFunc("/repos/{owner}/{repo}", methods{
		http.MethodGet:    s.getRepo,
		http.MethodPatch:  s.requireAuth(s.renameRepo),
		http.MethodDelete: s.requireAuth(s.deleteRepo),
	}.serve)
	mux.HandleFunc("/repos/{owner}/{repo}/commits", methods{
		http.MethodGet:  s.listCommits,
		http.MethodPost: s.requireAuth(s.createCommit),
	}.serve)
	mux.HandleFunc("/repos/{owner}/{repo}/commits/{oid}", methods{
		http.MethodGet: s.getCommit,
	}.serve)
	mux.HandleFunc("/repos/{owner}/{repo}/compare", methods{
		http.MethodGet: s.compare,
	}.serve)
	mux.HandleFunc("/repos/{owner}/{repo}/branches", methods{
		http.MethodGet:  s.listBranches,
		http.MethodPost: s.requireAuth(s.createBranch),
	}.serve)
	mux.HandleFunc("/repos/{owner}/{repo}/branches/{branch}", methods{
		http.MethodDelete: s.requireAuth(s.deleteBranch),
	}.serve)
	mux.HandleFunc("/repos/{owner}/{repo}/merge", methods{
		http.MethodPost: s.requireAuth(s.mergeBranches),
	}.serve)
	for _, kind := range []struct {
		name string
		get  http.HandlerFunc
	}{{"tree", s.getTree}, {"blob", s.getBlob}} {
		h := methods{http.MethodGet: kind.get}.serve
		base := "/repos/{owner}/{repo}/" + kind.name + "/{ref}"
		mux.HandleFunc(base, h)
		mux.HandleFunc(base+"/{path...}", h)
	}
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

// methods dispatches by HTTP method; any other method gets 405 with an Allow
// header. Used for paths that serve several methods.
type methods map[string]http.HandlerFunc

func (m methods) serve(w http.ResponseWriter, r *http.Request) {
	if h, ok := m[r.Method]; ok {
		h(w, r)
		return
	}
	allowed := make([]string, 0, len(m))
	for k := range m {
		allowed = append(allowed, k)
	}
	sort.Strings(allowed)
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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
