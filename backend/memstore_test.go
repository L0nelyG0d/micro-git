package main

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

// memFixture is a memory store with one user and one repository.
type memFixture struct {
	st   *memStore
	uid  int64
	repo Repo
}

func newMemFixture(t *testing.T) memFixture {
	t.Helper()
	st := newMemStore()
	uid, err := st.CreateUser(t.Context(), "alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := st.CreateRepo(t.Context(), uid, "r", "", false, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	return memFixture{st, uid, repo}
}

func (f memFixture) blob(t *testing.T, s string) string {
	t.Helper()
	oid, err := f.st.Objects(t.Context(), f.repo.ID).PutObject(gitobj.TypeBlob, []byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

// commit stores a commit with the given parents on an empty tree.
func (f memFixture) commit(t *testing.T, msg string, parents ...string) string {
	t.Helper()
	objs := f.st.Objects(t.Context(), f.repo.ID)
	tree, err := objs.PutObject(gitobj.TypeTree, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := gitobj.Signature{Name: "a", Email: "a@micro-git.local", Unix: 1, TZ: "+0000"}
	body, err := gitobj.SerializeCommit(gitobj.Commit{Tree: tree, Parents: parents, Message: msg, Author: sig, Committer: sig})
	if err != nil {
		t.Fatal(err)
	}
	oid, err := objs.PutObject(gitobj.TypeCommit, body)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func TestMemUniqueness(t *testing.T) {
	f := newMemFixture(t)
	ctx := t.Context()
	if _, err := f.st.CreateUser(ctx, "ALICE", "x"); !errors.Is(err, ErrConflict) {
		t.Errorf("username differing in case: %v, want ErrConflict", err)
	}
	if id, h, err := f.st.UserForLogin(ctx, "alice"); err != nil || id != f.uid || h != "hash" {
		t.Errorf("UserForLogin = %d %q %v", id, h, err)
	}
	if _, err := f.st.CreateRepo(ctx, f.uid, "R", "", false, time.Now()); !errors.Is(err, ErrConflict) {
		t.Errorf("repo name differing in case: %v, want ErrConflict", err)
	}
	bob, _ := f.st.CreateUser(ctx, "bob", "h")
	if _, err := f.st.CreateRepo(ctx, bob, "r", "", false, time.Now()); err != nil {
		t.Errorf("same repo name under another owner: %v", err)
	}
	other, err := f.st.CreateRepo(ctx, f.uid, "other", "", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	taken := "R"
	if _, err := f.st.UpdateRepo(ctx, other.ID, RepoPatch{Name: &taken}); !errors.Is(err, ErrConflict) {
		t.Errorf("rename onto an existing name: %v, want ErrConflict", err)
	}
	same := "OTHER"
	if _, err := f.st.UpdateRepo(ctx, other.ID, RepoPatch{Name: &same}); err != nil {
		t.Errorf("rename to a different case of its own name: %v", err)
	}
	if _, err := f.st.UpdateRepo(ctx, 999, RepoPatch{Name: &same}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update of a missing repo: %v, want ErrNotFound", err)
	}
	nobranch := "nope"
	if _, err := f.st.UpdateRepo(ctx, f.repo.ID, RepoPatch{DefaultBranch: &nobranch}); !errors.Is(err, ErrNotFound) {
		t.Errorf("default branch that does not exist: %v, want ErrNotFound", err)
	}
}

func TestMemCompareAndSwap(t *testing.T) {
	f := newMemFixture(t)
	ctx, id := t.Context(), f.repo.ID
	a, b, c := f.blob(t, "a"), f.blob(t, "b"), f.blob(t, "c")

	if err := f.st.UpdateRef(ctx, id, "main", "", a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := f.st.UpdateRef(ctx, id, "main", "", b); !errors.Is(err, ErrConflict) {
		t.Errorf("empty old on an existing branch: %v, want ErrConflict", err)
	}
	if err := f.st.UpdateRef(ctx, id, "main", b, c); !errors.Is(err, ErrConflict) {
		t.Errorf("stale old: %v, want ErrConflict", err)
	}
	if err := f.st.UpdateRef(ctx, id, "ghost", a, b); !errors.Is(err, ErrConflict) {
		t.Errorf("swap on a missing branch: %v, want ErrConflict", err)
	}
	if got, _ := f.st.GetRef(ctx, id, "main"); got != a {
		t.Errorf("a lost swap moved the ref to %s", got)
	}
	if err := f.st.UpdateRef(ctx, id, "main", a, b); err != nil {
		t.Fatalf("swap: %v", err)
	}

	// PutObjectsMoveRef has the same rules
	obj := []treebuild.Object{{Type: gitobj.TypeBlob, Body: []byte("new")}}
	if err := f.st.PutObjectsMoveRef(ctx, id, obj, "main", a, c); !errors.Is(err, ErrConflict) {
		t.Errorf("PutObjectsMoveRef stale: %v, want ErrConflict", err)
	}
	if err := f.st.PutObjectsMoveRef(ctx, id, obj, "main", b, gitobj.Hash(gitobj.TypeBlob, []byte("new"))); err != nil {
		t.Errorf("PutObjectsMoveRef: %v", err)
	}
	if err := f.st.PutObjectsMoveRef(ctx, id, nil, "main", "", a); !errors.Is(err, ErrConflict) {
		t.Errorf("PutObjectsMoveRef create over existing: %v, want ErrConflict", err)
	}
	if err := f.st.PutObjectsMoveRef(ctx, id, nil, "fresh", "", a); err != nil {
		t.Errorf("PutObjectsMoveRef create: %v", err)
	}

	// exactly one of many racing creators wins
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.st.UpdateRef(ctx, id, "race", "", a) == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("%d creators won, want 1", wins.Load())
	}
}

func TestMemRefNeedsObject(t *testing.T) {
	f := newMemFixture(t)
	ctx, id := t.Context(), f.repo.ID
	other, _ := f.st.CreateRepo(ctx, f.uid, "o", "", false, time.Now())
	elsewhere, _ := f.st.Objects(ctx, other.ID).PutObject(gitobj.TypeBlob, []byte("x"))
	for name, oid := range map[string]string{"missing": "1111111111111111111111111111111111111111", "another repo": elsewhere} {
		if err := f.st.CreateRef(ctx, id, "x", oid); !errors.Is(err, ErrForeignKey) {
			t.Errorf("CreateRef, %s: %v, want ErrForeignKey", name, err)
		}
		if err := f.st.UpdateRef(ctx, id, "x", "", oid); !errors.Is(err, ErrForeignKey) {
			t.Errorf("UpdateRef, %s: %v, want ErrForeignKey", name, err)
		}
	}
	if refs, _ := f.st.ListRefs(ctx, id); len(refs) != 0 {
		t.Errorf("refs after rejected writes: %v", refs)
	}
}

func TestMemInTxRollback(t *testing.T) {
	f := newMemFixture(t)
	ctx, id := t.Context(), f.repo.ID
	a := f.blob(t, "a")
	if err := f.st.CreateRef(ctx, id, "main", a); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")

	t.Run("lost swap removes the objects written before it", func(t *testing.T) {
		objs := []treebuild.Object{{Type: gitobj.TypeBlob, Body: []byte("rolled back")}}
		oid := gitobj.Hash(gitobj.TypeBlob, []byte("rolled back"))
		err := f.st.InTx(ctx, func(ts Store) error {
			return ts.PutObjectsMoveRef(ctx, id, objs, "main", "stale", oid)
		})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		if _, _, err := f.st.Objects(ctx, id).GetObject(oid); !errors.Is(err, treebuild.ErrNotFound) {
			t.Errorf("object survived the rollback: %v", err)
		}
	})
	t.Run("error from fn reverts every kind of write", func(t *testing.T) {
		err := f.st.InTx(ctx, func(ts Store) error {
			b, _ := ts.Objects(ctx, id).PutObject(gitobj.TypeBlob, []byte("b"))
			if err := ts.CreateRef(ctx, id, "dev", b); err != nil {
				return err
			}
			if err := ts.UpdateRef(ctx, id, "main", a, b); err != nil {
				return err
			}
			if _, err := ts.CreateUser(ctx, "carol", "h"); err != nil {
				return err
			}
			if _, err := ts.CreateRepo(ctx, f.uid, "tmp", "", false, time.Now()); err != nil {
				return err
			}
			desc := "changed"
			if _, err := ts.UpdateRepo(ctx, id, RepoPatch{Description: &desc}); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
		if got, _ := f.st.GetRef(ctx, id, "main"); got != a {
			t.Errorf("main = %s, want %s", got, a)
		}
		if _, err := f.st.GetRef(ctx, id, "dev"); !errors.Is(err, ErrNotFound) {
			t.Errorf("dev survived: %v", err)
		}
		if _, _, err := f.st.UserForLogin(ctx, "carol"); !errors.Is(err, ErrNotFound) {
			t.Errorf("user survived: %v", err)
		}
		if _, err := f.st.FindRepo(ctx, "alice", "tmp"); !errors.Is(err, ErrNotFound) {
			t.Errorf("repo survived: %v", err)
		}
		if r, _ := f.st.FindRepo(ctx, "alice", "r"); r.Description != "" {
			t.Errorf("description = %q", r.Description)
		}
	})
	t.Run("a deleted repo is restored by the rollback", func(t *testing.T) {
		_ = f.st.InTx(ctx, func(ts Store) error {
			if err := ts.DeleteRepo(ctx, id); err != nil {
				return err
			}
			return boom
		})
		if ok, _ := f.st.RepoExists(ctx, id); !ok {
			t.Error("repo not restored")
		}
		if got, _ := f.st.GetRef(ctx, id, "main"); got != a {
			t.Errorf("ref after restore = %q", got)
		}
	})
	t.Run("success commits", func(t *testing.T) {
		err := f.st.InTx(ctx, func(ts Store) error { return ts.CreateRef(ctx, id, "kept", a) })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.st.GetRef(ctx, id, "kept"); err != nil {
			t.Errorf("committed ref missing: %v", err)
		}
	})
	t.Run("concurrent transactions are serialized", func(t *testing.T) {
		var inside, overlap atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = f.st.InTx(ctx, func(Store) error {
					if inside.Add(1) > 1 {
						overlap.Add(1)
					}
					for k := 0; k < 100; k++ {
						runtime.Gosched() // widen the window a broken lock would show
					}
					inside.Add(-1)
					return nil
				})
			}()
		}
		wg.Wait()
		if overlap.Load() != 0 {
			t.Errorf("%d overlapping transactions", overlap.Load())
		}
	})
	t.Run("a repo deleted mid-transaction gives ErrForeignKey", func(t *testing.T) {
		g := newMemFixture(t)
		err := g.st.InTx(ctx, func(ts Store) error {
			if err := g.st.DeleteRepo(ctx, g.repo.ID); err != nil { // outside the transaction
				return err
			}
			objs := []treebuild.Object{{Type: gitobj.TypeBlob, Body: []byte("x")}}
			return ts.PutObjectsMoveRef(ctx, g.repo.ID, objs, "main", "", gitobj.Hash(gitobj.TypeBlob, []byte("x")))
		})
		if !errors.Is(err, ErrForeignKey) {
			t.Errorf("err = %v, want ErrForeignKey", err)
		}
	})
}

func TestMemDeleteRepoCascade(t *testing.T) {
	f := newMemFixture(t)
	ctx, id := t.Context(), f.repo.ID
	a := f.blob(t, "a")
	if err := f.st.CreateRef(ctx, id, "main", a); err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteRepo(ctx, id); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.st.RepoExists(ctx, id); ok {
		t.Error("repo still exists")
	}
	if _, err := f.st.FindRepo(ctx, "alice", "r"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindRepo: %v", err)
	}
	if _, _, err := f.st.Objects(ctx, id).GetObject(a); !errors.Is(err, treebuild.ErrNotFound) {
		t.Errorf("object after delete: %v", err)
	}
	if _, err := f.st.GetRef(ctx, id, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ref after delete: %v", err)
	}
	if refs, err := f.st.ListRefs(ctx, id); err != nil || refs == nil || len(refs) != 0 {
		t.Errorf("ListRefs = %v, %v, want empty non-nil", refs, err)
	}
	if _, err := f.st.Objects(ctx, id).PutObject(gitobj.TypeBlob, []byte("x")); !errors.Is(err, ErrForeignKey) {
		t.Errorf("PutObject on a deleted repo: %v, want ErrForeignKey", err)
	}
	err := f.st.PutObjectsMoveRef(ctx, id, []treebuild.Object{{Type: gitobj.TypeBlob, Body: []byte("x")}}, "main", "", a)
	if !errors.Is(err, ErrForeignKey) {
		t.Errorf("PutObjectsMoveRef on a deleted repo: %v, want ErrForeignKey", err)
	}
	if err := f.st.DeleteRepo(ctx, id); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

func TestMemObjectIsolationAndImmutability(t *testing.T) {
	f := newMemFixture(t)
	ctx := t.Context()
	other, _ := f.st.CreateRepo(ctx, f.uid, "o", "", false, time.Now())
	oid := f.blob(t, "same")
	if _, _, err := f.st.Objects(ctx, other.ID).GetObject(oid); !errors.Is(err, treebuild.ErrNotFound) {
		t.Errorf("object visible from another repo: %v", err)
	}
	if got, err := f.st.Objects(ctx, other.ID).PutObject(gitobj.TypeBlob, []byte("same")); err != nil || got != oid {
		t.Errorf("same body in another repo = %q, %v", got, err)
	}
	if got, err := f.st.Objects(ctx, other.ID).(treebuild.BatchStore).GetObjects([]string{oid, "1111111111111111111111111111111111111111"}); err != nil || len(got) != 1 {
		t.Errorf("GetObjects = %v, %v", got, err)
	}
	// callers cannot change stored bytes through what they got back
	_, body, _ := f.st.Objects(ctx, f.repo.ID).GetObject(oid)
	body[0] = 'X'
	if _, again, _ := f.st.Objects(ctx, f.repo.ID).GetObject(oid); string(again) != "same" {
		t.Errorf("stored body changed to %q", again)
	}
	if typ, body, err := f.st.Objects(ctx, f.repo.ID).GetObject(mustPut(t, f, gitobj.TypeBlob, nil)); err != nil || typ != gitobj.TypeBlob || len(body) != 0 {
		t.Errorf("empty blob = %q %q %v", typ, body, err)
	}
}

func mustPut(t *testing.T, f memFixture, typ gitobj.Type, body []byte) string {
	t.Helper()
	oid, err := f.st.Objects(t.Context(), f.repo.ID).PutObject(typ, body)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func TestMemListReposVisibility(t *testing.T) {
	f := newMemFixture(t)
	ctx := t.Context()
	bob, _ := f.st.CreateUser(ctx, "bob", "h")
	if _, err := f.st.CreateRepo(ctx, bob, "secret", "", true, time.Unix(2000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateRepo(ctx, bob, "open", "", false, time.Unix(3000, 0)); err != nil {
		t.Fatal(err)
	}
	names := func(viewer int64, owner *string, limit, offset int) []string {
		rs, err := f.st.ListRepos(ctx, viewer, owner, limit, offset)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range rs {
			out = append(out, r.Owner+"/"+r.Name)
		}
		return out
	}
	if got := fmt.Sprint(names(0, nil, 10, 0)); got != "[bob/open alice/r]" {
		t.Errorf("anonymous: %s", got)
	}
	if got := fmt.Sprint(names(f.uid, nil, 10, 0)); got != "[bob/open alice/r]" {
		t.Errorf("another user: %s", got)
	}
	if got := fmt.Sprint(names(bob, nil, 10, 0)); got != "[bob/open bob/secret alice/r]" {
		t.Errorf("owner: %s", got)
	}
	who := "BOB"
	if got := fmt.Sprint(names(0, &who, 10, 0)); got != "[bob/open]" {
		t.Errorf("owner filter: %s", got)
	}
	if got := fmt.Sprint(names(bob, nil, 1, 1)); got != "[bob/secret]" {
		t.Errorf("limit/offset: %s", got)
	}
	if got := names(bob, nil, 10, 50); got == nil || len(got) != 0 {
		t.Errorf("past the end: %v", got)
	}
}

func TestMemDeleteNonDefaultRef(t *testing.T) {
	f := newMemFixture(t)
	ctx, id := t.Context(), f.repo.ID
	a := f.blob(t, "a")
	for _, n := range []string{"main", "dev"} {
		if err := f.st.CreateRef(ctx, id, n, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.DeleteNonDefaultRef(ctx, id, "main"); !errors.Is(err, errDefaultBranch) {
		t.Errorf("default: %v, want errDefaultBranch", err)
	}
	if err := f.st.DeleteNonDefaultRef(ctx, id, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v, want ErrNotFound", err)
	}
	if err := f.st.DeleteNonDefaultRef(ctx, id, "dev"); err != nil {
		t.Errorf("dev: %v", err)
	}
	if err := f.st.DeleteRef(ctx, id, "dev"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteRef twice: %v", err)
	}
}

func TestMemHistoryAndAncestry(t *testing.T) {
	f := newMemFixture(t)
	ctx, id := t.Context(), f.repo.ID
	c1 := f.commit(t, "one")
	c2 := f.commit(t, "two", c1)
	side := f.commit(t, "side", c1)
	merge := f.commit(t, "merge", c2, side)
	blob := f.blob(t, "x")

	rows, err := f.st.WalkFirstParents(ctx, id, merge, 10)
	if err != nil || len(rows) != 3 || rows[0].OID != merge || rows[0].Parent != c2 || rows[2].OID != c1 || rows[2].Parent != "" {
		t.Errorf("walk = %+v, %v", rows, err)
	}
	if rows, _ := f.st.WalkFirstParents(ctx, id, merge, 2); len(rows) != 2 {
		t.Errorf("limit 2 gave %d rows", len(rows))
	}
	if rows, err := f.st.WalkFirstParents(ctx, id, merge, 0); err != nil || rows == nil || len(rows) != 0 {
		t.Errorf("limit 0 = %v, %v", rows, err)
	}
	for _, tip := range []string{"1111111111111111111111111111111111111111", blob} {
		if _, err := f.st.WalkFirstParents(ctx, id, tip, 5); !errors.Is(err, treebuild.ErrNotFound) {
			t.Errorf("walk from %s: %v, want treebuild.ErrNotFound", tip, err)
		}
	}

	cases := []struct {
		name, a, b     string
		aFromB, bFromA bool
	}{
		{"ancestor", c1, merge, true, false},
		{"second parent", side, merge, true, false},
		{"siblings", c2, side, false, false},
		{"same", c2, c2, true, true},
		{"missing", "1111111111111111111111111111111111111111", merge, false, false},
		{"not a commit", blob, merge, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.st.CommitReachability(ctx, id, tc.a, tc.b)
			if err != nil || got != (Reachability{AFromB: tc.aFromB, BFromA: tc.bFromA}) {
				t.Errorf("got %+v, %v", got, err)
			}
		})
	}

	// ResolveCommit, CommitBundle and the read head
	if err := f.st.CreateRef(ctx, id, "main", merge); err != nil {
		t.Fatal(err)
	}
	if got, err := f.st.ResolveCommit(ctx, id, "main"); err != nil || got != merge {
		t.Errorf("ResolveCommit(branch) = %q, %v", got, err)
	}
	if got, err := f.st.ResolveCommit(ctx, id, c1); err != nil || got != c1 {
		t.Errorf("ResolveCommit(oid) = %q, %v", got, err)
	}
	for _, ref := range []string{blob, "nope", c1[:7], ""} {
		if _, err := f.st.ResolveCommit(ctx, id, ref); !errors.Is(err, ErrNotFound) {
			t.Errorf("ResolveCommit(%q) = %v, want ErrNotFound", ref, err)
		}
	}
	objs, found, err := f.st.CommitBundle(ctx, id, c2)
	if err != nil || !found || len(objs) != 3 { // c2, its parent c1, the shared empty tree
		t.Errorf("CommitBundle = %d objects, found %v, %v", len(objs), found, err)
	}
	if _, found, _ := f.st.CommitBundle(ctx, id, blob); found {
		t.Error("CommitBundle found a blob")
	}
	h, err := f.st.FindRepoRef(ctx, "ALICE", "R", "main")
	if err != nil || !h.HasRef || h.Tip != merge || len(h.Objects) != 2 || h.Repo.Empty {
		t.Errorf("FindRepoRef = %+v, %v", h, err)
	}
	if h, err := f.st.FindRepoRef(ctx, "alice", "r", "ghost"); err != nil || h.HasRef || h.Objects != nil {
		t.Errorf("FindRepoRef(missing branch) = %+v, %v", h, err)
	}
	if _, err := f.st.FindRepoRef(ctx, "alice", "zzz", "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindRepoRef(missing repo) = %v", err)
	}
	if oid, body, err := f.st.GetRefCommit(ctx, id, "main"); err != nil || oid != merge || len(body) == 0 {
		t.Errorf("GetRefCommit = %q, %d bytes, %v", oid, len(body), err)
	}
}

// Many goroutines mixing reads and writes: the race detector must stay quiet.
func TestMemConcurrentUse(t *testing.T) {
	f := newMemFixture(t)
	ctx, id := t.Context(), f.repo.ID
	base := f.commit(t, "base")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				name := fmt.Sprintf("b%d-%d", i, j)
				_ = f.st.InTx(ctx, func(ts Store) error { return ts.CreateRef(ctx, id, name, base) })
				_, _ = f.st.ListRefs(ctx, id)
				_, _ = f.st.ListRepos(ctx, 0, nil, 10, 0)
				_, _ = f.st.CommitReachability(ctx, id, base, base)
				_, _ = f.st.CreateUser(ctx, fmt.Sprintf("u%d-%d", i, j), "h")
				_ = f.st.DeleteRef(ctx, id, name)
			}
		}()
	}
	wg.Wait()
}
