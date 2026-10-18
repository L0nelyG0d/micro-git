package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// testServer returns a Server on the test database, or skips when
// TEST_DATABASE_URL is unset.
var userSeq atomic.Int64

// One pool per test binary, created on first use and closed after m.Run.
var (
	poolOnce sync.Once
	testPool *pgxpool.Pool
	poolErr  error
)

// TestMain makes bcrypt cheap for the whole test binary and closes the shared
// pool at the end. bcryptCost is only ever lowered here, never in production.
func TestMain(m *testing.M) {
	bcryptCost = bcrypt.MinCost
	// dummyHash was built at package init with the production cost; rebuild it
	// so unknown-user logins cost the same (cheap) bcrypt time as real ones.
	dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy password"), bcryptCost)
	code := m.Run()
	if testPool != nil {
		testPool.Close()
	}
	os.Exit(code)
}

func testServer(t *testing.T) *Server {
	t.Helper()
	switch kind := testStoreKind(); kind {
	case "memory":
		// a fresh empty store per test: nothing to clean up afterwards
		srv := NewServer(newMemStore())
		srv.JWTSecret = []byte("test-secret")
		return srv
	case "sqlite":
		return newSQLiteServer(t)
	case "postgres":
	default:
		t.Fatalf("TEST_STORE=%q: want memory, sqlite or postgres", kind)
	}
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping database test")
	}
	poolOnce.Do(func() { testPool, poolErr = NewPool(context.Background(), url, DefaultPoolConfig()) })
	if poolErr != nil {
		t.Fatalf("connect: %v", poolErr)
	}
	srv := NewServer(newPgxStore(testPool))
	srv.JWTSecret = []byte("test-secret")
	return srv
}

// sqliteGroups are the test files whose store methods gormStore has ported;
// every other file still skips under TEST_STORE=sqlite. Each issue that ports
// a group adds its files here (users: #85).
var sqliteGroups = map[string]string{"auth_test.go": "users", "session_test.go": "users"}

// newSQLiteServer builds a Server on a gormStore over a temp SQLite file, or
// skips the test when its group is not ported yet. testServer is the caller.
func newSQLiteServer(t *testing.T) *Server {
	t.Helper()
	_, file, _, _ := runtime.Caller(2)
	if _, ok := sqliteGroups[filepath.Base(file)]; !ok {
		t.Skip("TEST_STORE=sqlite: gormStore does not implement this group yet (#86 to #92)")
	}
	db := openSQLiteForTest(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	srv := NewServer(newGormStore(db))
	srv.JWTSecret = []byte("test-secret")
	return srv
}

// cleanupUser deletes the user row (any case) when the test ends. The memory
// store is new for every test, so there is nothing to delete.
func cleanupUser(t *testing.T, s *Server, username string) {
	t.Helper()
	pg, ok := s.Store.(*pgxStore)
	if !ok {
		return
	}
	db := pg.pool
	t.Cleanup(func() {
		_, err := db.Exec(context.Background(), `DELETE FROM users WHERE lower(username) = lower($1)`, username)
		if err != nil {
			t.Errorf("cleanup %s: %v", username, err)
		}
	})
}

// uniqueName returns a valid username unique to this run.
func uniqueName(t *testing.T) string {
	t.Helper()
	return "t-" + strconv.Itoa(os.Getpid()) + "-" + strconv.Itoa(int(userSeq.Add(1)))
}

func post(s *Server, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(body))
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func regBody(username, password string) string {
	b, _ := json.Marshal(map[string]string{"username": username, "password": password})
	return string(b)
}

func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	if got, want := rec.Body.String(), `{"error":"`+msg+`"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}
}

func TestRegisterSuccess(t *testing.T) {
	s := testServer(t)
	name := uniqueName(t)
	cleanupUser(t, s, name)

	rec := post(s, regBody(name, "correct horse"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}
	if c := rec.Header().Values("Set-Cookie"); len(c) != 0 {
		t.Errorf("unexpected Set-Cookie %v", c)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["username"] != name {
		t.Errorf("body = %s", rec.Body)
	}
	id, ok := got["id"].(float64)
	if !ok {
		t.Fatalf("id not a number: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "correct horse") {
		t.Error("password in response")
	}

	dbName, err := s.Store.UsernameByID(context.Background(), int64(id))
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := s.Store.UserForLogin(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if dbName != name {
		t.Errorf("stored username = %q", dbName)
	}
	if hash == "correct horse" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse")) != nil {
		t.Error("password_hash is not a bcrypt hash of the password")
	}
}

func TestRegisterLowercases(t *testing.T) {
	s := testServer(t)
	name := uniqueName(t)
	cleanupUser(t, s, name)

	rec := post(s, regBody(strings.ToUpper(name), "correct horse"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var got userResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Username != name {
		t.Errorf("username = %q, want %q", got.Username, name)
	}
	if stored, err := s.Store.UsernameByID(context.Background(), got.ID); err != nil || stored != name {
		t.Errorf("stored = %q, err %v", stored, err)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	s := testServer(t)
	name := uniqueName(t)
	cleanupUser(t, s, name)

	if rec := post(s, regBody(name, "correct horse")); rec.Code != http.StatusCreated {
		t.Fatalf("first register: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct{ label, username string }{
		{"same case", name},
		{"different case", strings.ToUpper(name)},
	} {
		t.Run(tc.label, func(t *testing.T) {
			wantError(t, post(s, regBody(tc.username, "correct horse")), http.StatusConflict, "username already taken")
		})
	}
}

func TestRegisterConcurrentDuplicate(t *testing.T) {
	s := testServer(t)
	name := uniqueName(t)
	cleanupUser(t, s, name)

	const n = 6
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = post(s, regBody(name, "correct horse")).Code
		}()
	}
	wg.Wait()
	created, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		}
	}
	if created != 1 || conflict != n-1 {
		t.Errorf("codes = %v, want one 201 and %d 409", codes, n-1)
	}
}

func TestRegisterPasswordLength(t *testing.T) {
	s := testServer(t)
	tests := []struct {
		name, password string
		wantOK         bool
	}{
		{"7 bytes", "1234567", false},
		{"8 bytes", "12345678", true},
		{"72 bytes", strings.Repeat("a", 72), true},
		{"73 bytes", strings.Repeat("a", 73), false},
		{"empty", "", false},
		{"4 chars 8 bytes", "éééé", true},
		{"37 chars 74 bytes", strings.Repeat("é", 37), false},
		{"7 chars 14 bytes is fine", strings.Repeat("é", 7), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name := uniqueName(t)
			cleanupUser(t, s, name)
			rec := post(s, regBody(name, tc.password))
			if tc.wantOK {
				if rec.Code != http.StatusCreated {
					t.Errorf("status = %d, body %s", rec.Code, rec.Body)
				}
				return
			}
			wantError(t, rec, http.StatusBadRequest, "password must be 8-72 bytes")
		})
	}
}

func TestRegisterBadUsername(t *testing.T) {
	s := testServer(t)
	const msg = "username must be 3-32 characters: a-z, 0-9 and -"
	tests := []struct{ name, username string }{
		{"too short", "ab"},
		{"too long", strings.Repeat("a", 33)},
		{"space", "al ice"},
		{"underscore", "al_ice"},
		{"empty", ""},
		{"unicode", "alicé"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantError(t, post(s, regBody(tc.username, "correct horse")), http.StatusBadRequest, msg)
		})
	}
}

func TestRegisterLengthBoundaries(t *testing.T) {
	s := testServer(t)
	for _, n := range []int{3, 32} {
		name := "z" + strings.Repeat("9", n-1)
		cleanupUser(t, s, name)
		if rec := post(s, regBody(name, "correct horse")); rec.Code != http.StatusCreated {
			t.Errorf("len %d: status %d body %s", n, rec.Code, rec.Body)
		}
	}
}

func TestRegisterReserved(t *testing.T) {
	s := testServer(t)
	for _, u := range []string{"api", "API", "Api"} {
		t.Run(u, func(t *testing.T) {
			wantError(t, post(s, regBody(u, "correct horse")), http.StatusBadRequest, "username is reserved")
		})
	}
}

// Body parsing happens before any database access, so these need no database.
func TestRegisterInvalidJSON(t *testing.T) {
	t.Parallel()
	s := NewServer(nil)
	tests := []struct{ name, body string }{
		{"malformed", `{"username":`},
		{"empty body", ``},
		{"wrong type", `{"username":1,"password":"correct horse"}`},
		{"unknown field", `{"username":"alice","password":"correct horse","admin":true}`},
		{"trailing data", `{"username":"alice","password":"correct horse"} {}`},
		{"not an object", `[]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantError(t, post(s, tc.body), http.StatusBadRequest, "invalid JSON body")
		})
	}
}

func TestRegisterWrongMethod(t *testing.T) {
	t.Parallel()
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		NewHandler().ServeHTTP(rec, httptest.NewRequest(m, "/auth/register", nil))
		wantError(t, rec, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// rawDB returns the pgx pool behind a test server, for tests that are about
// Postgres itself (the pgx store, statement counts). Tests that must also run on
// the memory store use the Store interface or the helpers in storetest_test.go.
func rawDB(s *Server) *pgxpool.Pool {
	return s.Store.(*pgxStore).pool
}
