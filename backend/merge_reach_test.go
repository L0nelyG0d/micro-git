package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/jackc/pgx/v5/pgxpool"
)

// putCommits stores n empty-tree commits, the first with the given parents
// and each next one a child of the previous, and returns them oldest first.
// Every message contains a fake "parent" line that must never be followed.
func putCommits(t *testing.T, rs *RepoStore, first []string, n int) []string {
	t.Helper()
	st := rs.Objects(context.Background())
	tree, err := st.PutObject(gitobj.TypeTree, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := gitobj.Signature{Name: "u", Email: "u@micro-git.local", Unix: 1700000000}
	parents := first
	var out []string
	for i := 0; i < n; i++ {
		msg := fmt.Sprint("c", i, "\n\nparent ", strings.Repeat("a", 40), "\n")
		body, err := gitobj.SerializeCommit(gitobj.Commit{Tree: tree, Parents: parents, Message: msg, Author: sig, Committer: sig})
		if err != nil {
			t.Fatal(err)
		}
		oid, err := st.PutObject(gitobj.TypeCommit, body)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, oid)
		parents = []string{oid}
	}
	return out
}

func TestMergeStatementCountConstant(t *testing.T) {
	f := newMergeFix(t)
	ctx := context.Background()
	chain := putCommits(t, f.rs, []string{f.a}, 200)
	if err := f.rs.CreateRef(ctx, "long", chain[len(chain)-1]); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	counter := &stmtCounter{}
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := NewServer(newPgxStore(pool))
	s.JWTSecret = f.s.JWTSecret
	rec := do(s, "POST", mergeURL(f.owner, f.repo), mergeBody("main", "long"), f.c)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// Lookups and the compare-and-swap, plus one reachability statement; the
	// count must not grow with the 200 commits.
	if n := counter.n.Load(); n > 10 {
		t.Errorf("merge of 200 commits issued %d statements, want a constant at most 10", n)
	}
}

func TestCommitReachabilityParents(t *testing.T) {
	f := newMergeFix(t)
	m := putCommits(t, f.rs, []string{f.b, f.cc}, 1)[0]
	ctx := context.Background()
	for _, tc := range []struct {
		name, a, b string
		want       Reachability
	}{
		{"second parent", f.cc, m, Reachability{AFromB: true}},
		{"first parent", f.b, m, Reachability{AFromB: true}},
		{"root", f.a, m, Reachability{AFromB: true}},
		{"reverse", m, f.cc, Reachability{BFromA: true}},
		{"diverged", f.b, f.cc, Reachability{}},
		{"equal", f.a, f.a, Reachability{AFromB: true, BFromA: true}},
		{"missing", strings.Repeat("0", 40), m, Reachability{}},
		{"fake parent in message", strings.Repeat("a", 40), m, Reachability{}},
	} {
		got, err := f.rs.CommitReachability(ctx, tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v %v, want %+v", tc.name, got, err, tc.want)
		}
	}
}
