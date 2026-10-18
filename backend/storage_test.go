package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

// Handlers go through the Store interface only: no handler file may import
// pgx or run SQL.
func TestHandlerFilesDoNotUsePgxOrSQL(t *testing.T) {
	for _, f := range []string{"handler.go", "session.go", "repos.go", "rename.go", "reading.go",
		"commits.go", "history.go", "merge.go", "branches.go", "auth.go", "compare.go"} {
		src, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"jackc/pgx", "pgconn", "pgxpool", "QueryRow(", ".Exec(", "SELECT ", "INSERT INTO"} {
			if strings.Contains(string(src), bad) {
				t.Errorf("%s contains %q", f, bad)
			}
		}
	}
}

// Each sentinel is produced by the pgx store for its driver error.
func TestStoreSentinels(t *testing.T) {
	s := testServer(t)
	st := s.Store
	ctx := t.Context()
	name := uniqueName(t)
	uid, err := st.CreateUser(ctx, name, "hash")
	if err != nil {
		t.Fatal(err)
	}
	cleanupUser(t, rawDB(s), name)

	t.Run("duplicate user is ErrConflict", func(t *testing.T) {
		if _, err := st.CreateUser(ctx, name, "hash"); !errors.Is(err, ErrConflict) {
			t.Errorf("got %v, want ErrConflict", err)
		}
	})
	t.Run("unknown user is ErrNotFound", func(t *testing.T) {
		if _, err := st.UsernameByID(ctx, -1); !errors.Is(err, ErrNotFound) {
			t.Errorf("UsernameByID: got %v, want ErrNotFound", err)
		}
		if _, _, err := st.UserForLogin(ctx, "no-such-user-"+name); !errors.Is(err, ErrNotFound) {
			t.Errorf("UserForLogin: got %v, want ErrNotFound", err)
		}
	})
	repo, err := st.CreateRepo(ctx, uid, "r", "", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRepo(context.Background(), repo.ID) })

	t.Run("duplicate repo is ErrConflict", func(t *testing.T) {
		if _, err := st.CreateRepo(ctx, uid, "R", "", false, time.Now()); !errors.Is(err, ErrConflict) {
			t.Errorf("got %v, want ErrConflict", err)
		}
	})
	t.Run("unknown repo is ErrNotFound", func(t *testing.T) {
		if _, err := st.FindRepo(ctx, name, "nope"); !errors.Is(err, ErrNotFound) {
			t.Errorf("FindRepo: got %v, want ErrNotFound", err)
		}
		if _, err := st.FindRepoRef(ctx, name, "nope", "main"); !errors.Is(err, ErrNotFound) {
			t.Errorf("FindRepoRef: got %v, want ErrNotFound", err)
		}
		missing := "nope"
		if _, err := st.UpdateRepo(ctx, repo.ID, RepoPatch{DefaultBranch: &missing}); !errors.Is(err, ErrNotFound) {
			t.Errorf("UpdateRepo to a missing branch: got %v, want ErrNotFound", err)
		}
	})
	t.Run("missing branch is ErrNotFound, lost swap is ErrConflict", func(t *testing.T) {
		if _, err := st.GetRef(ctx, repo.ID, "main"); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetRef: got %v, want ErrNotFound", err)
		}
		a, err := st.Objects(ctx, repo.ID).PutObject(gitobj.TypeBlob, []byte("a"))
		if err != nil {
			t.Fatal(err)
		}
		b, err := st.Objects(ctx, repo.ID).PutObject(gitobj.TypeBlob, []byte("b"))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpdateRef(ctx, repo.ID, "main", "", a); err != nil {
			t.Fatal(err)
		}
		if err := st.UpdateRef(ctx, repo.ID, "main", "", b); !errors.Is(err, ErrConflict) {
			t.Errorf("UpdateRef: got %v, want ErrConflict", err)
		}
	})
	t.Run("write to a deleted repo is ErrForeignKey", func(t *testing.T) {
		gone, err := st.CreateRepo(ctx, uid, "gone", "", false, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := st.DeleteRepo(ctx, gone.ID); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateRef(ctx, gone.ID, "main", "a"); !errors.Is(err, ErrForeignKey) {
			t.Errorf("got %v, want ErrForeignKey", err)
		}
		if ok, err := st.RepoExists(ctx, gone.ID); err != nil || ok {
			t.Errorf("RepoExists = %v, %v; want false, nil", ok, err)
		}
	})
}

// InTx commits what fn did, or rolls all of it back when fn fails.
func TestStoreInTxAtomic(t *testing.T) {
	s := testServer(t)
	st := s.Store
	ctx := t.Context()
	name := uniqueName(t)
	uid, err := st.CreateUser(ctx, name, "hash")
	if err != nil {
		t.Fatal(err)
	}
	cleanupUser(t, rawDB(s), name)
	repo, err := st.CreateRepo(ctx, uid, "r", "", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRepo(context.Background(), repo.ID) })

	blob := treebuild.Object{Type: gitobj.TypeBlob, Body: []byte("hello")}
	oid := gitobj.Hash(blob.Type, blob.Body)

	boom := errors.New("boom")
	err = st.InTx(ctx, func(tx Store) error {
		if err := tx.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{blob}, "main", "", oid); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v, want boom", err)
	}
	if _, err := st.GetRef(ctx, repo.ID, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ref survived a rolled-back transaction: %v", err)
	}
	if _, _, err := st.Objects(ctx, repo.ID).GetObject(oid); !errors.Is(err, treebuild.ErrNotFound) {
		t.Errorf("object survived a rolled-back transaction: %v", err)
	}

	err = st.InTx(ctx, func(tx Store) error {
		return tx.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{blob}, "main", "", oid)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.GetRef(ctx, repo.ID, "main"); err != nil || got != oid {
		t.Errorf("GetRef = %q, %v; want %q", got, err, oid)
	}
}
