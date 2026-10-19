package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

// objectStores runs fn against every Store that implements Objects and
// CommitBundle: memory, gormStore on SQLite and on Postgres, and the pgx store
// when TEST_STORE=postgres. Each gets one user and two repositories.
func objectStores(t *testing.T, fn func(t *testing.T, st Store, repoID, otherID int64)) {
	t.Helper()
	setup := func(t *testing.T, st Store) (int64, int64) {
		t.Helper()
		name := uniqueName(t)
		uid, err := st.CreateUser(t.Context(), name, "hash")
		if err != nil {
			t.Fatal(err)
		}
		cleanupUser(t, &Server{Store: st}, name)
		var ids [2]int64
		for i, n := range []string{"a", "b"} {
			r, err := st.CreateRepo(t.Context(), uid, n, "", false, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			ids[i] = r.ID
			t.Cleanup(func() { st.DeleteRepo(context.Background(), r.ID) })
		}
		return ids[0], ids[1]
	}
	t.Run("memory", func(t *testing.T) {
		st := newMemStore()
		a, b := setup(t, st)
		fn(t, st, a, b)
	})
	for _, d := range dialects {
		t.Run("gorm-"+d.name, func(t *testing.T) {
			db := d.open(t)
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			st := newGormStore(db)
			a, b := setup(t, st)
			fn(t, st, a, b)
		})
	}
	t.Run("pgx", func(t *testing.T) {
		requirePostgres(t)
		st := testServer(t).Store
		a, b := setup(t, st)
		fn(t, st, a, b)
	})
}

func batchOf(t *testing.T, st Store, repoID int64) treebuild.BatchStore {
	t.Helper()
	b, ok := st.Objects(t.Context(), repoID).(treebuild.BatchStore)
	if !ok {
		t.Fatal("Objects is not a BatchStore")
	}
	return b
}

func TestStoreObjects(t *testing.T) {
	objectStores(t, func(t *testing.T, st Store, repo, other int64) {
		objs := st.Objects(t.Context(), repo)

		oid, err := objs.PutObject(gitobj.TypeBlob, []byte("test content\n"))
		if err != nil || oid != "d670460b4b4aece5915caf5c68d12f560a9fe3e4" {
			t.Fatalf("put = %q, %v", oid, err)
		}
		if again, err := objs.PutObject(gitobj.TypeBlob, []byte("test content\n")); err != nil || again != oid {
			t.Fatalf("second put = %q, %v", again, err)
		}
		if typ, body, err := objs.GetObject(oid); err != nil || typ != gitobj.TypeBlob || string(body) != "test content\n" {
			t.Errorf("get = %q %q %v", typ, body, err)
		}
		if _, _, err := objs.GetObject(strings.Repeat("0", 40)); !errors.Is(err, treebuild.ErrNotFound) {
			t.Errorf("missing oid: err = %v, want ErrNotFound", err)
		}
		if _, _, err := st.Objects(t.Context(), other).GetObject(oid); !errors.Is(err, treebuild.ErrNotFound) {
			t.Errorf("object visible from another repo: err = %v", err)
		}
		if got, err := batchOf(t, st, other).GetObjects([]string{oid}); err != nil || len(got) != 0 {
			t.Errorf("batch read from another repo = %v, %v", got, err)
		}
		// the same body in the other repo is its own row
		if again, err := st.Objects(t.Context(), other).PutObject(gitobj.TypeBlob, []byte("test content\n")); err != nil || again != oid {
			t.Errorf("same body in other repo = %q, %v", again, err)
		}

		// empty bodies are empty values, not NULL
		empty, err := objs.PutObject(gitobj.TypeBlob, nil)
		if err != nil || empty != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
			t.Errorf("empty blob = %q, %v", empty, err)
		}
		if _, body, err := objs.GetObject(empty); err != nil || len(body) != 0 {
			t.Errorf("empty blob get = %q, %v", body, err)
		}
		if tree, err := objs.PutObject(gitobj.TypeTree, nil); err != nil || tree != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
			t.Errorf("empty tree = %q, %v", tree, err)
		}

		// bytes round trip, every byte value
		all := make([]byte, 256)
		for i := range all {
			all[i] = byte(i)
		}
		id, err := objs.PutObject(gitobj.TypeBlob, all)
		if err != nil {
			t.Fatal(err)
		}
		if _, body, err := objs.GetObject(id); err != nil || !bytes.Equal(body, all) {
			t.Errorf("256-byte round trip = %v, %v", body, err)
		}

		// callers cannot change stored bytes through what they got back
		_, body, _ := objs.GetObject(oid)
		body[0] = 'X'
		if _, again, _ := objs.GetObject(oid); string(again) != "test content\n" {
			t.Errorf("stored body changed to %q", again)
		}

		// PutObjects is idempotent and GetObjects omits unknown oids
		batch := batchOf(t, st, repo)
		list := []treebuild.Object{{Type: gitobj.TypeBlob, Body: []byte("one")}, {Type: gitobj.TypeBlob, Body: []byte("two")}, {Type: gitobj.TypeBlob, Body: []byte("one")}}
		for range 2 {
			if err := batch.PutObjects(list); err != nil {
				t.Fatalf("PutObjects: %v", err)
			}
		}
		if err := batch.PutObjects(nil); err != nil {
			t.Errorf("PutObjects(nil): %v", err)
		}
		one, two := gitobj.Hash(gitobj.TypeBlob, []byte("one")), gitobj.Hash(gitobj.TypeBlob, []byte("two"))
		got, err := batch.GetObjects([]string{one, two, strings.Repeat("1", 40)})
		if err != nil || len(got) != 2 || string(got[one].Body) != "one" || got[two].Type != gitobj.TypeBlob {
			t.Errorf("GetObjects = %v, %v", got, err)
		}
		if got, err := batch.GetObjects(nil); err != nil || len(got) != 0 {
			t.Errorf("GetObjects(nil) = %v, %v", got, err)
		}
	})
}

func TestStoreObjectsLargeBatches(t *testing.T) {
	objectStores(t, func(t *testing.T, st Store, repo, _ int64) {
		const n = 2300 // above the 2000-oid read chunk and the insert chunk
		list := make([]treebuild.Object, n)
		oids := make([]string, n)
		for i := range list {
			body := []byte(fmt.Sprintf("blob %d", i))
			list[i] = treebuild.Object{Type: gitobj.TypeBlob, Body: body}
			oids[i] = gitobj.Hash(gitobj.TypeBlob, body)
		}
		batch := batchOf(t, st, repo)
		if err := batch.PutObjects(list); err != nil {
			t.Fatal(err)
		}
		got, err := batch.GetObjects(oids)
		if err != nil || len(got) != n {
			t.Fatalf("GetObjects = %d objects, %v, want %d", len(got), err, n)
		}
		if string(got[oids[n-1]].Body) != fmt.Sprintf("blob %d", n-1) {
			t.Errorf("last body = %q", got[oids[n-1]].Body)
		}
	})
}

// A 50-char (or otherwise malformed) oid is rejected on the read side of every
// store; Postgres char(40) would reject it anyway, SQLite would not.
func TestStoreInvalidOID(t *testing.T) {
	bad := map[string]string{
		"50 chars":  strings.Repeat("a", 50),
		"41 chars":  strings.Repeat("a", 41),
		"39 chars":  strings.Repeat("a", 39),
		"empty":     "",
		"uppercase": strings.Repeat("A", 40),
		"not hex":   strings.Repeat("g", 40),
	}
	objectStores(t, func(t *testing.T, st Store, repo, _ int64) {
		good := strings.Repeat("a", 40)
		for name, oid := range bad {
			t.Run(name, func(t *testing.T) {
				if _, err := batchOf(t, st, repo).GetObjects([]string{good, oid}); !errors.Is(err, ErrInvalidOID) {
					t.Errorf("GetObjects err = %v, want ErrInvalidOID", err)
				}
				if _, _, err := st.CommitBundle(t.Context(), repo, oid); !errors.Is(err, ErrInvalidOID) {
					t.Errorf("CommitBundle err = %v, want ErrInvalidOID", err)
				}
			})
		}
		if err := checkOID(good); err != nil {
			t.Errorf("checkOID(valid) = %v", err)
		}
		if _, err := batchOf(t, st, repo).GetObjects([]string{good}); err != nil {
			t.Errorf("valid oid: %v", err)
		}
	})
}

func TestStoreCommitBundle(t *testing.T) {
	objectStores(t, func(t *testing.T, st Store, repo, other int64) {
		ctx := t.Context()
		objs := st.Objects(ctx, repo)
		sig := gitobj.Signature{Name: "a", Email: "a@micro-git.local", Unix: 1, TZ: "+0000"}
		mkTree := func(name string) string {
			blob, err := objs.PutObject(gitobj.TypeBlob, []byte(name))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := gitobj.SerializeTree([]gitobj.TreeEntry{{Mode: gitobj.ModeFile, Name: name, OID: blob}})
			if err != nil {
				t.Fatal(err)
			}
			id, err := objs.PutObject(gitobj.TypeTree, raw)
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		mkCommit := func(tree string, parents ...string) string {
			body, err := gitobj.SerializeCommit(gitobj.Commit{Tree: tree, Parents: parents, Message: "m", Author: sig, Committer: sig})
			if err != nil {
				t.Fatal(err)
			}
			id, err := objs.PutObject(gitobj.TypeCommit, body)
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		t1, t2 := mkTree("f1"), mkTree("f2")
		c1 := mkCommit(t1)
		c2 := mkCommit(t2, c1)

		got, found, err := st.CommitBundle(ctx, repo, c2)
		if err != nil || !found || len(got) != 4 {
			t.Fatalf("bundle = %d objects, found %v, err %v, want 4", len(got), found, err)
		}
		for oid, typ := range map[string]gitobj.Type{c2: gitobj.TypeCommit, c1: gitobj.TypeCommit, t1: gitobj.TypeTree, t2: gitobj.TypeTree} {
			if got[oid].Type != typ || len(got[oid].Body) == 0 {
				t.Errorf("object %s = %+v, want a %s", oid, got[oid], typ)
			}
		}
		if got, found, err := st.CommitBundle(ctx, repo, c1); err != nil || !found || len(got) != 2 {
			t.Errorf("root bundle = %d objects, found %v, err %v, want 2", len(got), found, err)
		}
		// a tree oid is not a commit; an unknown oid is not found
		for _, oid := range []string{t1, strings.Repeat("0", 40)} {
			if got, found, err := st.CommitBundle(ctx, repo, oid); err != nil || found || len(got) != 0 {
				t.Errorf("CommitBundle(%s) = %d objects, found %v, err %v", oid, len(got), found, err)
			}
		}
		// other repositories do not see it
		if _, found, err := st.CommitBundle(ctx, other, c2); err != nil || found {
			t.Errorf("other repo: found %v, err %v", found, err)
		}
	})
}

func TestStoreObjectsRepoGone(t *testing.T) {
	objectStores(t, func(t *testing.T, st Store, repo, _ int64) {
		oid, err := st.Objects(t.Context(), repo).PutObject(gitobj.TypeBlob, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.DeleteRepo(t.Context(), repo); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Objects(t.Context(), repo).PutObject(gitobj.TypeBlob, []byte("y")); !errors.Is(err, ErrForeignKey) {
			t.Errorf("put into deleted repo: %v, want ErrForeignKey", err)
		}
		if _, _, err := st.Objects(t.Context(), repo).GetObject(oid); !errors.Is(err, treebuild.ErrNotFound) {
			t.Errorf("get after delete: %v, want ErrNotFound", err)
		}
	})
}
