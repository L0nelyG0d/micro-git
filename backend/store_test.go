package main

import (
	"context"
	"errors"
	"testing"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
	"github.com/jackc/pgx/v5/pgconn"
)

// newRepoStore creates a user and a repo row and returns the repo's store.
func newRepoStore(t *testing.T, s *Server) *RepoStore {
	t.Helper()
	uid := newUser(t, s, uniqueName(t), "correct horse")
	var id int64
	err := rawDB(s).QueryRow(context.Background(),
		`INSERT INTO repos (owner_id, name) VALUES ($1, 'r') RETURNING id`, uid).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // runs before the user cleanup (LIFO)
		if _, err := rawDB(s).Exec(context.Background(), `DELETE FROM repos WHERE id = $1`, id); err != nil {
			t.Errorf("cleanup repo: %v", err)
		}
	})
	return NewRepoStore(rawDB(s), id)
}

func TestObjectStore(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	rs := newRepoStore(t, s)
	other := newRepoStore(t, s)
	objs := rs.Objects(ctx)

	oid, err := objs.PutObject(gitobj.TypeBlob, []byte("test content\n"))
	if err != nil || oid != "d670460b4b4aece5915caf5c68d12f560a9fe3e4" {
		t.Fatalf("put = %q, %v", oid, err)
	}
	if again, err := objs.PutObject(gitobj.TypeBlob, []byte("test content\n")); err != nil || again != oid {
		t.Fatalf("second put = %q, %v", again, err)
	}
	var n int
	if err := rawDB(s).QueryRow(ctx, `SELECT count(*) FROM objects WHERE repo_id = $1`, rs.repoID).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d, err %v, want 1", n, err)
	}

	typ, body, err := objs.GetObject(oid)
	if err != nil || typ != gitobj.TypeBlob || string(body) != "test content\n" {
		t.Errorf("get = %q %q %v", typ, body, err)
	}
	if _, _, err := objs.GetObject("0000000000000000000000000000000000000000"); !errors.Is(err, treebuild.ErrNotFound) {
		t.Errorf("missing oid: err = %v, want ErrNotFound", err)
	}
	if _, _, err := other.Objects(ctx).GetObject(oid); !errors.Is(err, treebuild.ErrNotFound) {
		t.Errorf("object visible from another repo: err = %v", err)
	}

	// An empty body (nil) is stored as an empty value.
	empty, err := objs.PutObject(gitobj.TypeBlob, nil)
	if err != nil || empty != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
		t.Errorf("empty blob = %q, %v", empty, err)
	}
	if _, body, err := objs.GetObject(empty); err != nil || len(body) != 0 {
		t.Errorf("empty blob get = %q, %v", body, err)
	}
	tree, err := objs.PutObject(gitobj.TypeTree, nil)
	if err != nil || tree != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
		t.Errorf("empty tree = %q, %v", tree, err)
	}
}

func TestRefStore(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	rs := newRepoStore(t, s)
	// refs have a foreign key to objects, so the targets must exist first
	objs := rs.Objects(ctx)
	var oids []string
	for _, x := range []string{"a", "b", "c"} {
		oid, err := objs.PutObject(gitobj.TypeBlob, []byte(x))
		if err != nil {
			t.Fatal(err)
		}
		oids = append(oids, oid)
	}
	a, b, c := oids[0], oids[1], oids[2]

	if _, err := rs.GetRef(ctx, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing: %v", err)
	}
	if refs, err := rs.ListRefs(ctx); err != nil || refs == nil || len(refs) != 0 {
		t.Errorf("list empty = %v, %v", refs, err)
	}
	if err := rs.DeleteRef(ctx, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing: %v", err)
	}

	// first commit: "must not exist"
	if err := rs.UpdateRef(ctx, "main", "", a); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := rs.UpdateRef(ctx, "main", "", b); !errors.Is(err, ErrConflict) {
		t.Errorf("must-not-exist on existing ref: %v", err)
	}
	if err := rs.UpdateRef(ctx, "main", b, c); !errors.Is(err, ErrConflict) {
		t.Errorf("stale swap: %v", err)
	}
	if got, _ := rs.GetRef(ctx, "main"); got != a {
		t.Errorf("ref changed by failed swap: %s", got)
	}
	if err := rs.UpdateRef(ctx, "other", a, b); !errors.Is(err, ErrConflict) {
		t.Errorf("swap on missing ref: %v", err)
	}
	if err := rs.UpdateRef(ctx, "main", a, b); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if got, _ := rs.GetRef(ctx, "main"); got != b {
		t.Errorf("ref = %s, want %s", got, b)
	}

	if err := rs.CreateRef(ctx, "dev", b); err != nil {
		t.Fatal(err)
	}
	if err := rs.CreateRef(ctx, "dev", c); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate create: %v", err)
	}
	refs, err := rs.ListRefs(ctx)
	if err != nil || len(refs) != 2 || refs[0] != (Ref{"dev", b}) || refs[1] != (Ref{"main", b}) {
		t.Errorf("list = %v, %v", refs, err)
	}
	if err := rs.DeleteRef(ctx, "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.GetRef(ctx, "dev"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted ref still there: %v", err)
	}
}

func TestResolveCommit(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	rs := newRepoStore(t, s)
	other := newRepoStore(t, s)
	objs := rs.Objects(ctx)

	blob, _ := objs.PutObject(gitobj.TypeBlob, []byte("x"))
	tree, _ := objs.PutObject(gitobj.TypeTree, nil)
	sig := gitobj.Signature{Name: "alice", Email: "alice@micro-git.local", Unix: 1, TZ: "+0000"}
	body, err := gitobj.SerializeCommit(gitobj.Commit{Tree: tree, Author: sig, Committer: sig, Message: "m"})
	if err != nil {
		t.Fatal(err)
	}
	commit, _ := objs.PutObject(gitobj.TypeCommit, body)
	if err := rs.CreateRef(ctx, "main", commit); err != nil {
		t.Fatal(err)
	}
	if err := rs.CreateRef(ctx, "pointing-at-blob", blob); err != nil {
		t.Fatal(err)
	}
	missing := "1111111111111111111111111111111111111111"

	tests := []struct {
		name, ref string
		store     *RepoStore
		want      string
		wantErr   error
	}{
		{"branch", "main", rs, commit, nil},
		{"full oid", commit, rs, commit, nil},
		{"branch pointing at any oid is returned as is", "pointing-at-blob", rs, blob, nil},
		{"unknown branch", "nope", rs, "", ErrNotFound},
		{"oid of a blob", blob, rs, "", ErrNotFound},
		{"oid of a tree", tree, rs, "", ErrNotFound},
		{"missing oid", missing, rs, "", ErrNotFound},
		{"abbreviated oid", commit[:7], rs, "", ErrNotFound},
		{"uppercase oid", "ABCDEF" + commit[6:], rs, "", ErrNotFound},
		{"oid from another repo", commit, other, "", ErrNotFound},
		{"empty", "", rs, "", ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.store.ResolveCommit(ctx, tc.ref)
			if !errors.Is(err, tc.wantErr) || got != tc.want {
				t.Errorf("got %q, %v; want %q, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}

	// branch name wins over a same-named oid-looking ref
	if _, err := other.Objects(ctx).PutObject(gitobj.TypeBlob, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := other.CreateRef(ctx, commit, blob); err != nil {
		t.Fatal(err)
	}
	if got, err := other.ResolveCommit(ctx, commit); err != nil || got != blob {
		t.Errorf("branch should win: %q, %v", got, err)
	}
}

// A ref cannot point at an oid that is not stored in the same repository
// (foreign key refs_object_fk, issue #23).
func TestRefNeedsObject(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	rs := newRepoStore(t, s)
	other := newRepoStore(t, s)
	missing := "1111111111111111111111111111111111111111"
	inOther, err := other.Objects(ctx).PutObject(gitobj.TypeBlob, []byte("elsewhere"))
	if err != nil {
		t.Fatal(err)
	}
	valid, err := rs.Objects(ctx).PutObject(gitobj.TypeBlob, []byte("here"))
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.CreateRef(ctx, "keep", valid); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		do   func() error
	}{
		{"create, missing oid", func() error { return rs.CreateRef(ctx, "x", missing) }},
		{"create, oid of another repo", func() error { return rs.CreateRef(ctx, "x", inOther) }},
		{"first update, missing oid", func() error { return rs.UpdateRef(ctx, "x", "", missing) }},
		{"swap, missing oid", func() error { return rs.UpdateRef(ctx, "keep", valid, missing) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.do()
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
				t.Errorf("err = %v, want foreign key violation 23503", err)
			}
		})
	}
	if refs, err := rs.ListRefs(ctx); err != nil || len(refs) != 1 || refs[0] != (Ref{"keep", valid}) {
		t.Errorf("refs after rejected writes = %v, %v", refs, err)
	}
}
