package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

// The shared API suite runs against the store named by TEST_STORE:
//
//	postgres (default)  the local Postgres in TEST_DATABASE_URL; tests skip when it is unset
//	memory              a new memStore per test, no database needed
//	sqlite              not built yet (#93); every test skips
//
// Tests reach the store through Server.Store. What the interface cannot do
// (seed a row, drop objects, count rows) goes through the helpers below, which
// know both stores; tests that are about Postgres itself call requirePostgres.

func testStoreKind() string {
	if v := strings.ToLower(os.Getenv("TEST_STORE")); v != "" {
		return v
	}
	return "postgres"
}

// requirePostgres skips a test that is about Postgres itself (statement
// counts, the pgx store, pool settings) when another store is under test.
func requirePostgres(t *testing.T) {
	t.Helper()
	if k := testStoreKind(); k != "postgres" {
		t.Skipf("needs Postgres (statement counts, pgx store); TEST_STORE=%s", k)
	}
}

// repoHandle is one repository's refs and objects through the Store interface.
type repoHandle struct {
	st Store
	id int64
}

func viewOf(s *Server, repoID int64) repoHandle { return repoHandle{s.Store, repoID} }

func (h repoHandle) Objects(ctx context.Context) treebuild.Store { return h.st.Objects(ctx, h.id) }
func (h repoHandle) GetRef(ctx context.Context, name string) (string, error) {
	return h.st.GetRef(ctx, h.id, name)
}
func (h repoHandle) ListRefs(ctx context.Context) ([]Ref, error) { return h.st.ListRefs(ctx, h.id) }
func (h repoHandle) CreateRef(ctx context.Context, name, oid string) error {
	return h.st.CreateRef(ctx, h.id, name, oid)
}
func (h repoHandle) UpdateRef(ctx context.Context, name, oldOID, newOID string) error {
	return h.st.UpdateRef(ctx, h.id, name, oldOID, newOID)
}
func (h repoHandle) DeleteRef(ctx context.Context, name string) error {
	return h.st.DeleteRef(ctx, h.id, name)
}
func (h repoHandle) CommitReachability(ctx context.Context, a, b string) (Reachability, error) {
	return h.st.CommitReachability(ctx, h.id, a, b)
}

// deleteRepo removes a repository behind the handlers' back, as a concurrent
// request would.
func deleteRepo(t *testing.T, s *Server, repoID int64) {
	t.Helper()
	if err := s.Store.DeleteRepo(context.Background(), repoID); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
}

// deleteUser removes a user row by username.
func deleteUser(t *testing.T, s *Server, name string) {
	t.Helper()
	switch st := s.Store.(type) {
	case *pgxStore:
		if _, err := st.pool.Exec(context.Background(), `DELETE FROM users WHERE username = $1`, name); err != nil {
			t.Fatal(err)
		}
	case *memStore:
		st.d.mu.Lock()
		defer st.d.mu.Unlock()
		id := st.d.userByName[strings.ToLower(name)]
		delete(st.d.userByName, strings.ToLower(name))
		delete(st.d.users, id)
	default:
		t.Fatalf("deleteUser: unknown store %T", s.Store)
	}
}

// countObjects returns how many objects the repository stores.
func countObjects(t *testing.T, s *Server, repoID int64) int {
	t.Helper()
	switch st := s.Store.(type) {
	case *pgxStore:
		var n int
		if err := st.pool.QueryRow(context.Background(), `SELECT count(*) FROM objects WHERE repo_id = $1`, repoID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	case *memStore:
		st.d.mu.RLock()
		defer st.d.mu.RUnlock()
		if r, ok := st.d.repos[repoID]; ok {
			return len(r.objects)
		}
		return 0
	}
	t.Fatalf("countObjects: unknown store %T", s.Store)
	return 0
}

// dropBlobs deletes every blob of a repository, leaving a damaged repository
// whose trees point at objects that are gone.
func dropBlobs(t *testing.T, s *Server, repoID int64) {
	t.Helper()
	switch st := s.Store.(type) {
	case *pgxStore:
		if _, err := st.pool.Exec(context.Background(), `DELETE FROM objects WHERE repo_id = $1 AND type = 'blob'`, repoID); err != nil {
			t.Fatal(err)
		}
	case *memStore:
		st.d.mu.Lock()
		defer st.d.mu.Unlock()
		for oid, o := range st.d.repos[repoID].objects {
			if o.typ == gitobj.TypeBlob {
				delete(st.d.repos[repoID].objects, oid)
			}
		}
	default:
		t.Fatalf("dropBlobs: unknown store %T", s.Store)
	}
}
