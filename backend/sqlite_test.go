package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func sqliteServer(t *testing.T, path string) (*Server, func()) {
	t.Helper()
	st, closeStore, err := openStore(t.Context(), Config{Store: StoreDB, DBDriver: DriverSQLite, SQLitePath: path})
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	if _, ok := st.(*gormStore); !ok {
		t.Fatalf("store is %T, want *gormStore", st)
	}
	srv := NewServer(st)
	srv.JWTSecret = []byte("test-secret")
	return srv, closeStore
}

// Everything written through the API on a file-backed SQLite store is still
// there after the store is closed and opened again, the way a process restart
// does it. The data directory is created when missing.
func TestSQLiteDataSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "data", "mg.db")

	s, closeStore := sqliteServer(t, path)
	if rec := do(s, http.MethodPost, "/auth/register", regBody("alice", "correct horse"), nil); rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	c := login(t, s, "alice")
	repo := createRepo(t, s, c, `{"name":"proj","description":"kept"}`)
	first := mustCommit(t, s, "alice", c, repo, commitJSON("main", "", "first", up("a.txt", "hello")))
	second := mustCommit(t, s, "alice", c, repo, commitJSON("main", first.OID, "second", up("b/c.txt", "world")))
	if rec := do(s, http.MethodPost, branchesURL("alice", repo), `{"name":"dev","from":"main"}`, c); rec.Code != http.StatusCreated {
		t.Fatalf("branch: %d %s", rec.Code, rec.Body)
	}
	closeStore()

	s, closeStore = sqliteServer(t, path)
	defer closeStore()
	c = login(t, s, "alice")
	if rec := do(s, http.MethodGet, "/repos/alice/proj", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"kept"`) {
		t.Errorf("repo: %d %s", rec.Code, rec.Body)
	}
	branches := listBranchesT(t, s, "alice", repo)
	if len(branches) != 2 {
		t.Fatalf("branches = %+v, want main and dev", branches)
	}
	for _, b := range branches {
		if b.OID != second.OID {
			t.Errorf("branch %s at %s, want %s", b.Name, b.OID, second.OID)
		}
	}
	rec := do(s, http.MethodGet, "/repos/alice/proj/commits?ref=main", "", nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, second.OID) || !strings.Contains(body, first.OID) {
		t.Errorf("history: %d %s", rec.Code, body)
	}
	for path, want := range map[string]string{"a.txt": "hello", "b/c.txt": "world"} {
		rec := do(s, http.MethodGet, "/repos/alice/proj/blob/main/"+path, "", nil)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("blob %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	// the reopened store still accepts writes on top of the old history
	mustCommit(t, s, "alice", c, repo, commitJSON("main", second.OID, "third", up("d.txt", "again")))
}

func TestOpenSQLiteRejectsBadPath(t *testing.T) {
	for _, p := range []string{"", "x.db?mode=ro", "x#.db"} {
		if db, err := OpenSQLite(p); err == nil {
			CloseGorm(db)
			t.Errorf("OpenSQLite(%q) succeeded", p)
		}
	}
}
