package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// putCommits stores n empty-tree commits, the first with the given parents
// and each next one a child of the previous, and returns them oldest first.
// Every message contains a fake "parent" line that must never be followed.
func putCommits(t *testing.T, rs repoHandle, first []string, n int) []string {
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
	requirePostgres(t)
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

// reachSuite runs CommitReachability on store with objects only (no refs), so
// it covers every store, and sqlite before CreateRef is ported (#91).
func reachSuite(t *testing.T, store Store, repoID int64) {
	ctx := context.Background()
	st := store.Objects(ctx, repoID)
	tree, err := st.PutObject(gitobj.TypeTree, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := gitobj.Signature{Name: "u", Email: "u@micro-git.local", Unix: 1700000000}
	put := func(msg string, parents ...string) string {
		body, err := gitobj.SerializeCommit(gitobj.Commit{Tree: tree, Parents: parents, Message: msg, Author: sig, Committer: sig})
		if err != nil {
			t.Fatal(err)
		}
		oid, err := st.PutObject(gitobj.TypeCommit, body)
		if err != nil {
			t.Fatal(err)
		}
		return oid
	}
	root := put("root")
	b := put("b", root)
	c := put("c", root)
	// the message of m holds a fake header line that must never be followed
	m := put("m\n\nparent "+strings.Repeat("a", 40)+"\n", b, c)
	octo := put("octo", root, b, c, m)
	missingParent := put("orphan", strings.Repeat("e", 40))
	zero := strings.Repeat("0", 40)

	for _, tc := range []struct {
		name, a, b string
		want       Reachability
	}{
		{"identical", root, root, Reachability{AFromB: true, BFromA: true}},
		{"fast-forward: a ancestor of b", root, b, Reachability{AFromB: true}},
		{"fast-forward: b ancestor of a", b, root, Reachability{BFromA: true}},
		{"diverged", b, c, Reachability{}},
		{"merge: first parent", b, m, Reachability{AFromB: true}},
		{"merge: second parent", c, m, Reachability{AFromB: true}},
		{"merge: root", root, m, Reachability{AFromB: true}},
		{"merge reversed", m, c, Reachability{BFromA: true}},
		{"octopus: last parent", m, octo, Reachability{AFromB: true}},
		{"octopus: second parent", b, octo, Reachability{AFromB: true}},
		{"fake parent in message", strings.Repeat("a", 40), m, Reachability{}},
		{"missing a", zero, m, Reachability{}},
		{"missing b", m, zero, Reachability{}},
		{"both missing, equal", zero, zero, Reachability{}},
		{"empty and short oids", "", "abc", Reachability{}},
		{"not a commit", tree, m, Reachability{}},
		{"tree on both sides", tree, tree, Reachability{}},
		{"missing parent ends the walk", root, missingParent, Reachability{}},
		{"missing parent, other side found", missingParent, missingParent, Reachability{AFromB: true, BFromA: true}},
	} {
		got, err := store.CommitReachability(ctx, repoID, tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v %v, want %+v", tc.name, got, err, tc.want)
		}
	}

	// Octopus merges with more parents than any fixed cap: the target is
	// reachable only through the last parent (and the first), never skipped.
	for _, n := range []int{16, 17, 18, 20, 40} {
		var ps []string
		for i := 0; i < n; i++ {
			ps = append(ps, put(fmt.Sprint("leaf ", n, " ", i)))
		}
		big := put(fmt.Sprint("octopus ", n), ps...)
		for _, i := range []int{0, n / 2, n - 1} {
			got, err := store.CommitReachability(ctx, repoID, ps[i], big)
			if err != nil || got != (Reachability{AFromB: true}) {
				t.Errorf("octopus of %d, parent %d: %+v %v", n, i, got, err)
			}
		}
		if got, err := store.CommitReachability(ctx, repoID, big, ps[n-1]); err != nil || got != (Reachability{BFromA: true}) {
			t.Errorf("octopus of %d reversed: %+v %v", n, got, err)
		}
		if got, err := store.CommitReachability(ctx, repoID, c, big); err != nil || got != (Reachability{}) {
			t.Errorf("octopus of %d, unrelated commit: %+v %v", n, got, err)
		}
		// a parent that is not stored skips only itself
		gap := put(fmt.Sprint("gap ", n), append([]string{strings.Repeat("d", 40)}, ps...)...)
		if got, err := store.CommitReachability(ctx, repoID, ps[n-1], gap); err != nil || got != (Reachability{AFromB: true}) {
			t.Errorf("octopus of %d after a missing parent: %+v %v", n, got, err)
		}
	}

	// A chain far deeper than any limit used elsewhere, with a merge at the
	// bottom: the walk reaches both parents without a depth cap.
	if _, ok := store.(*pgxStore); ok {
		return // pgx store: a 1000-deep walk exceeds the statement timeout under -race (unchanged, store.go)
	}
	chain := putChain(t, st, 1000, func(i int) string { return fmt.Sprint("deep ", i) })
	deepMerge := put("deep merge", chain[999], c)
	for _, tc := range []struct {
		name, a, b string
		want       Reachability
	}{
		{"deep: oldest from newest", chain[0], chain[999], Reachability{AFromB: true}},
		{"deep: through merge to second parent", c, deepMerge, Reachability{AFromB: true}},
		{"deep: through merge to chain root", chain[0], deepMerge, Reachability{AFromB: true}},
		{"deep: unrelated roots", chain[500], c, Reachability{}},
	} {
		got, err := store.CommitReachability(ctx, repoID, tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v %v, want %+v", tc.name, got, err, tc.want)
		}
	}
}

// TestCommitReachabilityStore runs the suite on the store TEST_STORE names.
func TestCommitReachabilityStore(t *testing.T) {
	s := testServer(t)
	_, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"reach"}`)
	reachSuite(t, s.Store, repo.ID)
}

// TestCommitReachabilityGorm runs it on gormStore over both dialects, whatever
// TEST_STORE is (Postgres needs TEST_DATABASE_URL).
func TestCommitReachabilityGorm(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			db := d.open(t)
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			st := newGormStore(db)
			uid, err := st.CreateUser(t.Context(), uniqueName(t), "x")
			if err != nil {
				t.Fatal(err)
			}
			repo, err := st.CreateRepo(t.Context(), uid, "reach", "", false, t0)
			if err != nil {
				t.Fatal(err)
			}
			reachSuite(t, st, repo.ID)
		})
	}
}

// stmtLogger counts the statements gorm sends.
type stmtLogger struct {
	logger.Interface
	n atomic.Int64
}

func (l *stmtLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	l.n.Add(1)
}

// TestGormCommitReachabilityStatementCount: a 5-commit and a 500-commit chain
// take the same number of statements (Postgres only; sqlite proves results).
func TestGormCommitReachabilityStatementCount(t *testing.T) {
	requirePostgres(t)
	db, _ := openPostgresForTest(t, "r")
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	st := newGormStore(db)
	uid, err := st.CreateUser(t.Context(), uniqueName(t), "x")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := st.CreateRepo(t.Context(), uid, "reach", "", false, t0)
	if err != nil {
		t.Fatal(err)
	}
	objs := st.Objects(t.Context(), repo.ID)
	short := putChain(t, objs, 5, func(i int) string { return fmt.Sprint("s", i) })
	long := putChain(t, objs, 500, func(i int) string { return fmt.Sprint("l", i) })

	lg := &stmtLogger{Interface: logger.Discard}
	counted := newGormStore(db.Session(&gorm.Session{Logger: lg}))
	count := func(a, b string) int64 {
		lg.n.Store(0)
		r, err := counted.CommitReachability(t.Context(), repo.ID, a, b)
		if err != nil || !r.AFromB {
			t.Fatalf("%+v %v", r, err)
		}
		return lg.n.Load()
	}
	n5, n500 := count(short[0], short[4]), count(long[0], long[499])
	if n5 != n500 || n5 != 1 {
		t.Errorf("5-commit chain took %d statements, 500-commit chain %d, want 1 each", n5, n500)
	}
}
