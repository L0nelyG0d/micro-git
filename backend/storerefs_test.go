package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

// refFixture is a user with one repository on whatever store the suite runs.
func refFixture(t *testing.T) (st Store, ctx context.Context, owner string, repo Repo) {
	t.Helper()
	s := testServer(t)
	st, ctx = s.Store, t.Context()
	owner = uniqueName(t)
	uid, err := st.CreateUser(ctx, owner, "hash")
	if err != nil {
		t.Fatal(err)
	}
	cleanupUser(t, s, owner)
	repo, err = st.CreateRepo(ctx, uid, "r", "", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRepo(context.Background(), repo.ID) })
	return st, ctx, owner, repo
}

func blobObj(s string) (treebuild.Object, string) {
	o := treebuild.Object{Type: gitobj.TypeBlob, Body: []byte(s)}
	return o, gitobj.Hash(o.Type, o.Body)
}

func hasObject(t *testing.T, st Store, ctx context.Context, repoID int64, oid string) bool {
	t.Helper()
	_, _, err := st.Objects(ctx, repoID).GetObject(oid)
	if err != nil && !errors.Is(err, treebuild.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// A stale compare-and-swap writes no objects, checked after InTx returns.
func TestStoreStaleSwapWritesNoObjects(t *testing.T) {
	st, ctx, _, repo := refFixture(t)
	a, aOID := blobObj("a")
	b, bOID := blobObj("b")
	c, cOID := blobObj("c")
	if err := st.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{a}, "main", "", aOID); err != nil {
		t.Fatal(err)
	}
	// Moving from a stale tip: the swap loses, fn returns the error, nothing is kept.
	err := st.InTx(ctx, func(tx Store) error {
		return tx.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{b}, "main", cOID, bOID)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale swap = %v, want ErrConflict", err)
	}
	if hasObject(t, st, ctx, repo.ID, bOID) {
		t.Error("object of a lost swap was kept")
	}
	// Creating an existing branch is a lost swap as well.
	err = st.InTx(ctx, func(tx Store) error {
		return tx.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{c}, "main", "", cOID)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("create over existing = %v, want ErrConflict", err)
	}
	if hasObject(t, st, ctx, repo.ID, cOID) {
		t.Error("object of a lost create was kept")
	}
	if got, err := st.GetRef(ctx, repo.ID, "main"); err != nil || got != aOID {
		t.Errorf("tip = %q, %v; want %q", got, err, aOID)
	}
	// The right old tip moves it.
	if err := st.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{b}, "main", aOID, bOID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetRef(ctx, repo.ID, "main"); got != bOID {
		t.Errorf("tip = %q, want %q", got, bOID)
	}
	// A swap from an existing-looking tip of a branch that does not exist loses.
	if err := st.UpdateRef(ctx, repo.ID, "nobranch", aOID, bOID); !errors.Is(err, ErrConflict) {
		t.Errorf("UpdateRef on a missing branch = %v, want ErrConflict", err)
	}
}

func TestStoreRefWritesRejectInvalidOID(t *testing.T) {
	st, ctx, _, repo := refFixture(t)
	o, oid := blobObj("a")
	long := strings.Repeat("a", 50)
	upper := strings.ToUpper(oid)
	for _, bad := range []string{long, upper, "abc"} {
		if err := st.UpdateRef(ctx, repo.ID, "main", "", bad); !errors.Is(err, ErrInvalidOID) {
			t.Errorf("UpdateRef new %q = %v, want ErrInvalidOID", bad, err)
		}
		if err := st.UpdateRef(ctx, repo.ID, "main", bad, oid); !errors.Is(err, ErrInvalidOID) {
			t.Errorf("UpdateRef old %q = %v, want ErrInvalidOID", bad, err)
		}
		if err := st.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{o}, "main", "", bad); !errors.Is(err, ErrInvalidOID) {
			t.Errorf("PutObjectsMoveRef new %q = %v, want ErrInvalidOID", bad, err)
		}
		if err := st.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{o}, "main", bad, oid); !errors.Is(err, ErrInvalidOID) {
			t.Errorf("PutObjectsMoveRef old %q = %v, want ErrInvalidOID", bad, err)
		}
	}
	if hasObject(t, st, ctx, repo.ID, oid) {
		t.Error("a rejected write stored objects")
	}
	if _, err := st.GetRef(ctx, repo.ID, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a rejected write created the ref: %v", err)
	}
}

// Every write to a deleted repository is ErrForeignKey, on every store.
func TestStoreWritesToDeletedRepoAreForeignKey(t *testing.T) {
	st, ctx, _, repo := refFixture(t)
	o, oid := blobObj("a")
	if err := st.DeleteRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Objects(ctx, repo.ID).PutObject(gitobj.TypeBlob, []byte("a")); !errors.Is(err, ErrForeignKey) {
		t.Errorf("PutObject = %v, want ErrForeignKey", err)
	}
	if err := st.Objects(ctx, repo.ID).(treebuild.BatchStore).PutObjects([]treebuild.Object{o}); !errors.Is(err, ErrForeignKey) {
		t.Errorf("PutObjects = %v, want ErrForeignKey", err)
	}
	for _, old := range []string{"", oid} {
		err := st.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{o}, "main", old, oid)
		if !errors.Is(err, ErrForeignKey) {
			t.Errorf("PutObjectsMoveRef old=%q = %v, want ErrForeignKey", old, err)
		}
	}
	if err := st.UpdateRef(ctx, repo.ID, "main", "", oid); !errors.Is(err, ErrForeignKey) {
		t.Errorf("UpdateRef create = %v, want ErrForeignKey", err)
	}
	if _, err := st.GetRef(ctx, repo.ID, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRef = %v, want ErrNotFound", err)
	}
	if _, err := st.ResolveCommit(ctx, repo.ID, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResolveCommit = %v, want ErrNotFound", err)
	}
}

// A ref must point at an object of its own repository.
func TestStoreRefNeedsObjectOfOwnRepo(t *testing.T) {
	st, ctx, owner, repo := refFixture(t)
	_, oid := blobObj("a")
	if err := st.UpdateRef(ctx, repo.ID, "main", "", oid); !errors.Is(err, ErrForeignKey) {
		t.Errorf("ref to a missing object = %v, want ErrForeignKey", err)
	}
	uid, _ := st.CreateUser(ctx, owner+"-2", "h")
	cleanupUser(t, &Server{Store: st}, owner+"-2")
	other, err := st.CreateRepo(ctx, uid, "other", "", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRepo(context.Background(), other.ID) })
	if _, err := st.Objects(ctx, other.ID).PutObject(gitobj.TypeBlob, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateRef(ctx, repo.ID, "main", "", oid); !errors.Is(err, ErrForeignKey) {
		t.Errorf("ref to another repo's object = %v, want ErrForeignKey", err)
	}
	if _, err := st.GetRef(ctx, repo.ID, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("failed write created the ref: %v", err)
	}
}

// GetRefCommit, ResolveCommit and FindRepoRef agree on a real commit.
func TestStoreCommitReads(t *testing.T) {
	st, ctx, owner, repo := refFixture(t)
	objs := st.Objects(ctx, repo.ID)
	tree, err := treebuild.Apply(objs, "", []treebuild.Change{treebuild.Upsert("a.txt", "hi\n")})
	if err != nil {
		t.Fatal(err)
	}
	sig := gitobj.Signature{Name: "u", Email: "u@x.invalid", Unix: 1700000000}
	body, err := gitobj.SerializeCommit(gitobj.Commit{Tree: tree, Author: sig, Committer: sig, Message: "m"})
	if err != nil {
		t.Fatal(err)
	}
	commit := treebuild.Object{Type: gitobj.TypeCommit, Body: body}
	oid := gitobj.Hash(commit.Type, commit.Body)
	if err := st.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{commit}, "main", "", oid); err != nil {
		t.Fatal(err)
	}
	gotOID, gotBody, err := st.GetRefCommit(ctx, repo.ID, "main")
	if err != nil || gotOID != oid || string(gotBody) != string(body) {
		t.Errorf("GetRefCommit = %q, %v", gotOID, err)
	}
	if _, _, err := st.GetRefCommit(ctx, repo.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRefCommit missing = %v, want ErrNotFound", err)
	}
	for ref, want := range map[string]string{"main": oid, oid: oid} {
		if got, err := st.ResolveCommit(ctx, repo.ID, ref); err != nil || got != want {
			t.Errorf("ResolveCommit(%q) = %q, %v", ref, got, err)
		}
	}
	for _, ref := range []string{"nope", tree, strings.Repeat("0", 40), strings.Repeat("a", 50)} {
		if _, err := st.ResolveCommit(ctx, repo.ID, ref); !errors.Is(err, ErrNotFound) {
			t.Errorf("ResolveCommit(%q) = %v, want ErrNotFound", ref, err)
		}
	}
	h, err := st.FindRepoRef(ctx, strings.ToUpper(owner), "R", "main")
	if err != nil || h.Repo.ID != repo.ID || !h.HasRef || h.Tip != oid || h.Repo.Empty {
		t.Fatalf("FindRepoRef = %+v, %v", h, err)
	}
	if c := h.Objects[oid]; c.Type != gitobj.TypeCommit || string(c.Body) != string(body) {
		t.Errorf("FindRepoRef commit = %+v", c)
	}
	if tr := h.Objects[tree]; tr.Type != gitobj.TypeTree || len(tr.Body) == 0 {
		t.Errorf("FindRepoRef tree = %+v", tr)
	}
	h, err = st.FindRepoRef(ctx, owner, "r", "nope")
	if err != nil || h.HasRef || len(h.Objects) != 0 {
		t.Errorf("FindRepoRef missing branch = %+v, %v", h, err)
	}
	if _, err := st.FindRepoRef(ctx, owner, "nope", "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindRepoRef missing repo = %v, want ErrNotFound", err)
	}
}

func TestStoreInTxUsesTransaction(t *testing.T) {
	st, ctx, _, repo := refFixture(t)
	o, oid := blobObj("a")
	boom := errors.New("boom")
	err := st.InTx(ctx, func(tx Store) error {
		if err := tx.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{o}, "main", "", oid); err != nil {
			return err
		}
		if got, err := tx.GetRef(ctx, repo.ID, "main"); err != nil || got != oid {
			t.Errorf("tx sees ref %q, %v", got, err)
		}
		// Nested InTx joins the transaction.
		return tx.InTx(ctx, func(Store) error { return boom })
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v, want boom", err)
	}
	if _, err := st.GetRef(ctx, repo.ID, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ref survived rollback: %v", err)
	}
	if hasObject(t, st, ctx, repo.ID, oid) {
		t.Error("object survived rollback")
	}
}

// Many writers swap the same tip: exactly one wins, the rest see ErrConflict
// and leave no objects, and nobody gets a driver error (SQLITE_BUSY).
func TestStoreConcurrentSwapSameTip(t *testing.T) {
	st, ctx, _, repo := refFixture(t)
	base, baseOID := blobObj("base")
	if err := st.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{base}, "main", "", baseOID); err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wins, conflicts atomic.Int32
	oids := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		o, oid := blobObj("w" + string(rune('a'+i)))
		oids[i] = oid
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := st.InTx(ctx, func(tx Store) error {
				return tx.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{o}, "main", baseOID, oid)
			})
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrConflict):
				conflicts.Add(1)
			default:
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 || conflicts.Load() != n-1 {
		t.Fatalf("wins=%d conflicts=%d, want 1 and %d", wins.Load(), conflicts.Load(), n-1)
	}
	tip, _ := st.GetRef(ctx, repo.ID, "main")
	for _, oid := range oids {
		if has := hasObject(t, st, ctx, repo.ID, oid); has != (oid == tip) {
			t.Errorf("object %s stored=%v, tip=%s", oid[:7], has, tip[:7])
		}
	}
}

// Many writers create the same branch: exactly one wins.
func TestStoreConcurrentCreateSameBranch(t *testing.T) {
	st, ctx, _, repo := refFixture(t)
	const n = 8
	var wins, conflicts atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		o, oid := blobObj("c" + string(rune('a'+i)))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := st.PutObjectsMoveRef(ctx, repo.ID, []treebuild.Object{o}, "main", "", oid)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrConflict):
				conflicts.Add(1)
			default:
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 || conflicts.Load() != n-1 {
		t.Fatalf("wins=%d conflicts=%d, want 1 and %d", wins.Load(), conflicts.Load(), n-1)
	}
}
