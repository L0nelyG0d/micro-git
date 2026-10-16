package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type histCommit struct {
	OID     string   `json:"oid"`
	Message string   `json:"message"`
	Author  string   `json:"author"`
	Time    string   `json:"time"`
	Parents []string `json:"parents"`
}

type histList struct {
	Ref     string       `json:"ref"`
	Page    int          `json:"page"`
	PerPage int          `json:"per_page"`
	HasMore bool         `json:"has_more"`
	Commits []histCommit `json:"commits"`
}

type histLine struct{ Type, Text string }
type histHunk struct {
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Lines    []histLine `json:"lines"`
}
type histFile struct {
	Path      string     `json:"path"`
	Status    string     `json:"status"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	Truncated bool       `json:"truncated"`
	Hunks     []histHunk `json:"hunks"`
}
type histDetail struct {
	histCommit
	Tree  string     `json:"tree"`
	Files []histFile `json:"files"`
}

func (d histDetail) file(t *testing.T, path string) histFile {
	t.Helper()
	for _, f := range d.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no file %q in %+v", path, d.Files)
	return histFile{}
}

func numbered(n int, prefix string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s%d\n", prefix, i)
	}
	return b.String()
}

// histFixture holds the repos shared by the history tests.
type histFixture struct {
	s          *Server
	owner      string
	base       string
	root, c2   commitResp // diff repo: root commit and the modifying one
	c3         commitResp
	other      string // base URL of a second repo
	otherOID   string // a commit in the second repo
	long       []string
	longBase   string
	emptyBase  string
	rootTree   string
	rootBlobID string
}

func (f *histFixture) get(t *testing.T, base, q string, out any) (int, string) {
	t.Helper()
	rec := do(f.s, http.MethodGet, base+q, "", nil)
	if rec.Code == http.StatusOK && out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", rec.Body, err)
		}
		return rec.Code, ""
	}
	return rec.Code, errMsg(rec.Body)
}

func newHistFixture(t *testing.T, withLong bool) *histFixture {
	t.Helper()
	s := testServer(t)
	tick := 0
	s.Now = func() time.Time { tick++; return t0.Add(time.Duration(tick) * time.Minute) }
	owner, c := newUserCookie(t, s)
	f := &histFixture{s: s, owner: owner, base: "/repos/" + owner + "/diff"}
	repo := createRepo(t, s, c, `{"name":"diff"}`)

	f.root = mustCommit(t, s, owner, c, repo, commitJSON("main", "", "root commit\n\nbody line",
		up("a.txt", "1\n2\n3\n"),
		up("empty.txt", ""),
		up("src/lib/a.go", "package lib\n"),
		up("gone/x.txt", "x\n"),
		up("nl.txt", "same"),
		up("big.txt", numbered(5001, "L")),
		up("wide.txt", numbered(5000, "W")),
		up("mv.txt", "moved\n"),
		up("far.txt", numbered(30, "f")),
		up("close.txt", numbered(20, "c")),
	))
	far := strings.Replace(numbered(30, "f"), "f3\n", "F3\n", 1)
	far = strings.Replace(far, "f28\n", "F28\n", 1)
	closeTxt := strings.Replace(numbered(20, "c"), "c5\n", "C5\n", 1)
	closeTxt = strings.Replace(closeTxt, "c10\n", "C10\n", 1)
	f.c2 = mustCommit(t, s, owner, c, repo, commitJSON("main", f.root.OID, "second",
		up("a.txt", "1\nTWO\n3\n4\n"),
		up("new.txt", "n1\nn2\n"),
		del("gone/x.txt"),
		up("nl.txt", "same\n"),
		up("src/lib/a.go", "package lib\n\nfunc A() {}\n"),
		up("big.txt", numbered(5001, "M")),
		up("wide.txt", numbered(5000, "V")),
		del("mv.txt"),
		up("moved.txt", "moved\n"),
		up("far.txt", far),
		up("close.txt", closeTxt),
		up("zero.txt", ""),
	))
	f.c3 = mustCommit(t, s, owner, c, repo, commitJSON("main", f.c2.OID, "third", up("a.txt", "1\nTWO\n3\n4\n5\n")))
	f.rootTree = f.root.Tree

	// A second repo with its own commit.
	o := createRepo(t, s, c, `{"name":"other"}`)
	f.other = "/repos/" + owner + "/other"
	f.otherOID = mustCommit(t, s, owner, c, o, commitJSON("main", "", "other", up("o.txt", "o\n"))).OID

	// Long history built directly in the object store: 120 commits on "long",
	// and a branch "fifty" at the 50th.
	createRepo(t, s, c, `{"name":"void"}`)
	f.emptyBase = "/repos/" + owner + "/void"
	if !withLong {
		return f
	}
	lr := createRepo(t, s, c, `{"name":"long"}`)
	f.longBase = "/repos/" + owner + "/long"
	rs := NewRepoStore(s.DB, lr.ID)
	st := rs.Objects(t.Context())
	treeOID, err := st.PutObject(gitobj.TypeTree, nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := ""
	for i := 1; i <= 120; i++ {
		com := gitobj.Commit{Tree: treeOID, Message: fmt.Sprintf("commit %d", i),
			Author:    gitobj.Signature{Name: owner, Email: owner + "@micro-git.local", Unix: t0.Unix() + int64(i)},
			Committer: gitobj.Signature{Name: owner, Email: owner + "@micro-git.local", Unix: t0.Unix() + int64(i)}}
		if parent != "" {
			com.Parents = []string{parent}
		}
		body, err := gitobj.SerializeCommit(com)
		if err != nil {
			t.Fatal(err)
		}
		if parent, err = st.PutObject(gitobj.TypeCommit, body); err != nil {
			t.Fatal(err)
		}
		f.long = append(f.long, parent)
		if i == 50 {
			if err := rs.CreateRef(t.Context(), "fifty", parent); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := rs.CreateRef(t.Context(), "main", parent); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCommitLog(t *testing.T) {
	f := newHistFixture(t, true)

	t.Run("linear history, default ref, no cookie", func(t *testing.T) {
		var l histList
		if code, msg := f.get(t, f.base+"/commits", "", &l); code != 200 {
			t.Fatalf("%d %s", code, msg)
		}
		if l.Ref != "main" || l.Page != 1 || l.PerPage != 50 || l.HasMore || len(l.Commits) != 3 {
			t.Fatalf("got %+v", l)
		}
		if l.Commits[0].OID != f.c3.OID || l.Commits[1].OID != f.c2.OID || l.Commits[2].OID != f.root.OID {
			t.Errorf("order wrong: %+v", l.Commits)
		}
		root := l.Commits[2]
		if root.Message != "root commit\n\nbody line" {
			t.Errorf("message %q", root.Message)
		}
		if root.Author != f.owner {
			t.Errorf("author %q", root.Author)
		}
		if !reflect.DeepEqual(root.Parents, []string{}) {
			t.Errorf("root parents %#v", root.Parents)
		}
		if !reflect.DeepEqual(l.Commits[0].Parents, []string{f.c2.OID}) {
			t.Errorf("parents %#v", l.Commits[0].Parents)
		}
		if _, err := time.Parse(time.RFC3339, root.Time); err != nil || !strings.HasSuffix(root.Time, "Z") {
			t.Errorf("time %q", root.Time)
		}
	})

	t.Run("ref as branch and as oid", func(t *testing.T) {
		for _, ref := range []string{"main", f.c2.OID} {
			var l histList
			if code, msg := f.get(t, f.base+"/commits", "?ref="+ref, &l); code != 200 {
				t.Fatalf("%s: %d %s", ref, code, msg)
			}
			want := 3
			if ref == f.c2.OID {
				want = 2
			}
			if l.Ref != ref || len(l.Commits) != want {
				t.Errorf("%s: got ref %q, %d commits", ref, l.Ref, len(l.Commits))
			}
		}
	})

	t.Run("not found cases", func(t *testing.T) {
		cases := []struct{ name, base, q, msg string }{
			{"unknown ref", f.base + "/commits", "?ref=nope", "ref not found"},
			{"empty ref", f.base + "/commits", "?ref=", "ref not found"},
			{"unknown oid", f.base + "/commits", "?ref=" + strings.Repeat("a", 40), "ref not found"},
			{"oid of other repo", f.base + "/commits", "?ref=" + f.otherOID, "ref not found"},
			{"tree oid", f.base + "/commits", "?ref=" + f.rootTree, "ref not found"},
			{"empty repo", f.emptyBase + "/commits", "", "ref not found"},
			{"unknown repo", "/repos/" + f.owner + "/zzz/commits", "", "repository not found"},
			{"unknown owner", "/repos/nobody-here/diff/commits", "", "repository not found"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				code, msg := f.get(t, tc.base, tc.q, nil)
				if code != 404 || msg != tc.msg {
					t.Errorf("got %d %q", code, msg)
				}
			})
		}
	})

	t.Run("invalid page", func(t *testing.T) {
		for _, p := range []string{"0", "-1", "abc", "", "1.5", "+1", " 1", "99999999999999999999999"} {
			code, msg := f.get(t, f.base+"/commits", "?page="+url.QueryEscape(p), nil)
			if code != 400 || msg != "invalid page" {
				t.Errorf("page=%q: got %d %q", p, code, msg)
			}
		}
	})

	t.Run("pagination of 120 commits", func(t *testing.T) {
		cases := []struct {
			q       string
			page, n int
			more    bool
			first   int // index into f.long of the first commit on the page
		}{
			{"", 1, 50, true, 119},
			{"?page=1", 1, 50, true, 119},
			{"?page=2", 2, 50, true, 69},
			{"?page=3", 3, 20, false, 19},
			{"?page=4", 4, 0, false, -1},
			{"?page=1000000", 1000000, 0, false, -1},
			{"?page=99999999999999", 99999999999999, 0, false, -1},
		}
		for _, tc := range cases {
			var l histList
			if code, msg := f.get(t, f.longBase+"/commits", tc.q, &l); code != 200 {
				t.Fatalf("%s: %d %s", tc.q, code, msg)
			}
			if l.Page != tc.page || l.PerPage != 50 || len(l.Commits) != tc.n || l.HasMore != tc.more || l.Ref != "main" {
				t.Errorf("%q: page %d per %d n %d more %v", tc.q, l.Page, l.PerPage, len(l.Commits), l.HasMore)
				continue
			}
			if l.Commits == nil {
				t.Errorf("%q: commits is nil", tc.q)
			}
			if tc.n > 0 && l.Commits[0].OID != f.long[tc.first] {
				t.Errorf("%q: first commit wrong", tc.q)
			}
		}
		// the raw body of an empty page carries [] not null
		rec := do(f.s, http.MethodGet, f.longBase+"/commits?page=4", "", nil)
		if !strings.Contains(rec.Body.String(), `"commits":[]`) {
			t.Errorf("body %s", rec.Body)
		}
	})

	t.Run("exactly 50 commits has no more", func(t *testing.T) {
		var l histList
		if code, msg := f.get(t, f.longBase+"/commits", "?ref=fifty", &l); code != 200 {
			t.Fatalf("%d %s", code, msg)
		}
		if len(l.Commits) != 50 || l.HasMore || l.Ref != "fifty" {
			t.Errorf("got n=%d more=%v", len(l.Commits), l.HasMore)
		}
		if code, _ := f.get(t, f.longBase+"/commits", "?ref=fifty&page=2", &l); code != 200 || len(l.Commits) != 0 || l.HasMore {
			t.Errorf("page 2: %d %+v", code, l)
		}
	})

	t.Run("wrong method", func(t *testing.T) {
		rec := do(f.s, http.MethodDelete, f.base+"/commits", "", nil)
		if rec.Code != 405 {
			t.Errorf("got %d", rec.Code)
		}
		rec = do(f.s, http.MethodPost, f.base+"/commits/"+f.root.OID, "", nil)
		if rec.Code != 405 {
			t.Errorf("detail POST: %d", rec.Code)
		}
	})
}

func TestCommitDetail(t *testing.T) {
	f := newHistFixture(t, false)
	detail := func(oid string) histDetail {
		t.Helper()
		var d histDetail
		if code, msg := f.get(t, f.base+"/commits/"+oid, "", &d); code != 200 {
			t.Fatalf("%s: %d %s", oid, code, msg)
		}
		return d
	}

	t.Run("errors", func(t *testing.T) {
		cases := []struct {
			name, oid string
			code      int
			msg       string
		}{
			{"short", f.root.OID[:7], 400, "invalid commit id"},
			{"uppercase", strings.ToUpper(f.root.OID), 400, "invalid commit id"},
			{"too long", f.root.OID + "0", 400, "invalid commit id"},
			{"not hex", strings.Repeat("g", 40), 400, "invalid commit id"},
			{"unknown", strings.Repeat("a", 40), 404, "commit not found"},
			{"tree", f.rootTree, 404, "commit not found"},
			{"other repo", f.otherOID, 404, "commit not found"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				code, msg := f.get(t, f.base+"/commits/"+tc.oid, "", nil)
				if code != tc.code || msg != tc.msg {
					t.Errorf("got %d %q", code, msg)
				}
			})
		}
		if code, msg := f.get(t, "/repos/"+f.owner+"/zzz/commits/"+f.root.OID, "", nil); code != 404 || msg != "repository not found" {
			t.Errorf("repo: %d %q", code, msg)
		}
	})

	t.Run("blob oid is not a commit", func(t *testing.T) {
		var tr struct {
			Entries []struct{ Name, Type, OID string }
		}
		if code, msg := f.get(t, f.base+"/tree/main", "", &tr); code != 200 {
			t.Fatalf("%d %s", code, msg)
		}
		for _, e := range tr.Entries {
			if e.Type == "blob" {
				if code, msg := f.get(t, f.base+"/commits/"+e.OID, "", nil); code != 404 || msg != "commit not found" {
					t.Errorf("blob: %d %q", code, msg)
				}
				return
			}
		}
		t.Fatal("no blob in tree")
	})

	t.Run("root commit", func(t *testing.T) {
		d := detail(f.root.OID)
		if d.OID != f.root.OID || d.Tree != f.root.Tree || d.Author != f.owner || d.Message != "root commit\n\nbody line" {
			t.Errorf("header %+v", d.histCommit)
		}
		if !reflect.DeepEqual(d.Parents, []string{}) {
			t.Errorf("parents %#v", d.Parents)
		}
		var paths []string
		for _, fl := range d.Files {
			paths = append(paths, fl.Path)
			if fl.Status != "added" {
				t.Errorf("%s: status %s", fl.Path, fl.Status)
			}
		}
		want := []string{"a.txt", "big.txt", "close.txt", "empty.txt", "far.txt", "gone/x.txt", "mv.txt",
			"nl.txt", "src/lib/a.go", "wide.txt"}
		if !reflect.DeepEqual(paths, want) {
			t.Errorf("paths %v", paths)
		}
		a := d.file(t, "a.txt")
		if a.Additions != 3 || a.Deletions != 0 || len(a.Hunks) != 1 {
			t.Fatalf("a.txt %+v", a)
		}
		h := a.Hunks[0]
		if h.OldStart != 0 || h.OldLines != 0 || h.NewStart != 1 || h.NewLines != 3 {
			t.Errorf("hunk %+v", h)
		}
	})

	d2 := detail(f.c2.OID)

	t.Run("modify add delete", func(t *testing.T) {
		if !reflect.DeepEqual(d2.Parents, []string{f.root.OID}) {
			t.Errorf("parents %v", d2.Parents)
		}
		a := d2.file(t, "a.txt")
		if a.Status != "modified" || a.Additions != 2 || a.Deletions != 1 {
			t.Errorf("a.txt %+v", a)
		}
		wantLines := []histLine{{"context", "1"}, {"del", "2"}, {"add", "TWO"}, {"context", "3"}, {"add", "4"}}
		if len(a.Hunks) != 1 || !reflect.DeepEqual(a.Hunks[0].Lines, wantLines) {
			t.Errorf("lines %+v", a.Hunks)
		}
		h := a.Hunks[0]
		if h.OldStart != 1 || h.OldLines != 3 || h.NewStart != 1 || h.NewLines != 4 {
			t.Errorf("hunk %+v", h)
		}
		n := d2.file(t, "new.txt")
		if n.Status != "added" || n.Additions != 2 || n.Deletions != 0 {
			t.Errorf("new.txt %+v", n)
		}
		g := d2.file(t, "gone/x.txt")
		if g.Status != "deleted" || g.Additions != 0 || g.Deletions != 1 || len(g.Hunks) != 1 {
			t.Fatalf("gone/x.txt %+v", g)
		}
		if h := g.Hunks[0]; h.NewStart != 0 || h.NewLines != 0 || h.OldStart != 1 || h.OldLines != 1 {
			t.Errorf("deleted hunk %+v", h)
		}
	})

	t.Run("files sorted, nested path, no rename detection", func(t *testing.T) {
		var paths []string
		for _, fl := range d2.Files {
			paths = append(paths, fl.Path)
		}
		want := []string{"a.txt", "big.txt", "close.txt", "far.txt", "gone/x.txt", "moved.txt", "mv.txt",
			"new.txt", "nl.txt", "src/lib/a.go", "wide.txt", "zero.txt"}
		if !reflect.DeepEqual(paths, want) {
			t.Errorf("paths %v", paths)
		}
		if got := d2.file(t, "src/lib/a.go"); got.Status != "modified" || got.Additions != 2 {
			t.Errorf("a.go %+v", got)
		}
		if mv := d2.file(t, "mv.txt"); mv.Status != "deleted" {
			t.Errorf("mv.txt %+v", mv)
		}
		if mv := d2.file(t, "moved.txt"); mv.Status != "added" {
			t.Errorf("moved.txt %+v", mv)
		}
	})

	t.Run("final newline only", func(t *testing.T) {
		nl := d2.file(t, "nl.txt")
		if nl.Status != "modified" || len(nl.Hunks) != 0 || nl.Additions != 0 || nl.Deletions != 0 || nl.Truncated {
			t.Errorf("nl.txt %+v", nl)
		}
		rec := do(f.s, http.MethodGet, f.base+"/commits/"+f.c2.OID, "", nil)
		if !strings.Contains(rec.Body.String(), `"path":"nl.txt","status":"modified","additions":0,"deletions":0,"truncated":false,"hunks":[]`) {
			t.Errorf("hunks must be [] in JSON: %s", rec.Body)
		}
	})

	t.Run("empty file added", func(t *testing.T) {
		z := d2.file(t, "zero.txt")
		if z.Status != "added" || len(z.Hunks) != 0 || z.Additions != 0 || z.Truncated {
			t.Errorf("zero.txt %+v", z)
		}
	})

	t.Run("truncation above 5000 lines", func(t *testing.T) {
		big := d2.file(t, "big.txt")
		if !big.Truncated || len(big.Hunks) != 0 {
			t.Errorf("big.txt %+v", big)
		}
		w := d2.file(t, "wide.txt") // exactly 5000 lines: still diffed
		if w.Truncated || len(w.Hunks) == 0 || w.Additions != 5000 || w.Deletions != 5000 {
			t.Errorf("wide.txt truncated=%v hunks=%d +%d -%d", w.Truncated, len(w.Hunks), w.Additions, w.Deletions)
		}
		if a := d2.file(t, "a.txt"); a.Truncated {
			t.Error("a.txt truncated")
		}
	})

	t.Run("hunk context and merging", func(t *testing.T) {
		far := d2.file(t, "far.txt") // changes at lines 3 and 28: two hunks
		if len(far.Hunks) != 2 {
			t.Fatalf("far.txt hunks %d", len(far.Hunks))
		}
		h := far.Hunks[0]
		if h.OldStart != 1 || h.OldLines != 6 || len(h.Lines) != 7 {
			t.Errorf("first hunk %+v", h)
		}
		ctx := 0
		for _, l := range far.Hunks[1].Lines {
			if l.Type == "context" {
				ctx++
			}
		}
		if ctx != 5 { // 3 before the change, only 2 lines left after line 28 of 30
			t.Errorf("second hunk context lines %d", ctx)
		}
		cl := d2.file(t, "close.txt") // changes at lines 5 and 10: merged into one
		if len(cl.Hunks) != 1 {
			t.Fatalf("close.txt hunks %d", len(cl.Hunks))
		}
		if h := cl.Hunks[0]; h.OldStart != 2 || h.OldLines != 12 || h.NewStart != 2 || h.NewLines != 12 {
			t.Errorf("merged hunk %+v", h)
		}
		for _, l := range cl.Hunks[0].Lines {
			if strings.HasPrefix(l.Text, "+") || strings.HasPrefix(l.Text, "-") || strings.Contains(l.Text, "\n") {
				t.Errorf("text %q", l.Text)
			}
		}
	})

	t.Run("deleting the last file removes the directory", func(t *testing.T) {
		g := d2.file(t, "gone/x.txt")
		if g.Status != "deleted" {
			t.Errorf("%+v", g)
		}
	})

	t.Run("third commit diffs only against first parent", func(t *testing.T) {
		d3 := detail(f.c3.OID)
		if len(d3.Files) != 1 || d3.Files[0].Path != "a.txt" || d3.Files[0].Additions != 1 || d3.Files[0].Deletions != 0 {
			t.Errorf("files %+v", d3.Files)
		}
	})
}

// stmtCounter is a pgx.QueryTracer that counts the statements a pool runs.
type stmtCounter struct{ n atomic.Int64 }

func (c *stmtCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}
func (c *stmtCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestCommitLogStatementCount(t *testing.T) {
	f := newHistFixture(t, true)
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	counter := &stmtCounter{}
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := NewServer(pool)
	s.JWTSecret = f.s.JWTSecret
	for _, q := range []string{"", "?page=3"} {
		counter.n.Store(0)
		rec := do(s, http.MethodGet, f.longBase+"/commits"+q, "", nil)
		if rec.Code != 200 {
			t.Fatalf("%q: %d %s", q, rec.Code, rec.Body)
		}
		if n := counter.n.Load(); n > 6 {
			t.Errorf("page %q issued %d statements, want at most 6", q, n)
		}
	}
}

func TestCommitLogHugePage(t *testing.T) {
	f := newHistFixture(t, true)
	for _, p := range []string{"50000000", "1099511627776", "1099511627777"} {
		var l histList
		if code, msg := f.get(t, f.longBase+"/commits", "?page="+p, &l); code != 200 {
			t.Fatalf("page %s: %d %s", p, code, msg)
		}
		if len(l.Commits) != 0 || l.HasMore || l.Commits == nil {
			t.Errorf("page %s: %+v", p, l)
		}
	}
}

func TestCommitLogHeaderOnly(t *testing.T) {
	f := newHistFixture(t, false)
	name, c := newUserCookie(t, f.s)
	repo := createRepo(t, f.s, c, `{"name":"hdr"}`)
	rs := NewRepoStore(f.s.DB, repo.ID)
	st := rs.Objects(t.Context())
	treeOID, err := st.PutObject(gitobj.TypeTree, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := gitobj.Signature{Name: "u", Email: "u@micro-git.local", Unix: t0.Unix()}
	put := func(msg string, parents ...string) string {
		t.Helper()
		body, err := gitobj.SerializeCommit(gitobj.Commit{Tree: treeOID, Parents: parents, Message: msg, Author: sig, Committer: sig})
		if err != nil {
			t.Fatal(err)
		}
		oid, err := st.PutObject(gitobj.TypeCommit, body)
		if err != nil {
			t.Fatal(err)
		}
		return oid
	}
	other := put("other root")
	root := put("sneaky\nparent " + other + "\nmore\n")
	merge := put("merge", root, other)
	missing := put("orphan", strings.Repeat("b", 40))
	for _, ref := range []struct{ name, oid string }{{"root", root}, {"merge", merge}, {"orphan", missing}} {
		if err := rs.CreateRef(t.Context(), ref.name, ref.oid); err != nil {
			t.Fatal(err)
		}
	}
	logURL := "/repos/" + name + "/hdr/commits"

	t.Run("message line is not a parent", func(t *testing.T) {
		var l histList
		if code, msg := f.get(t, logURL, "?ref=root", &l); code != 200 || len(l.Commits) != 1 || l.Commits[0].OID != root || l.HasMore {
			t.Errorf("%d %q %+v", code, msg, l)
		}
	})
	t.Run("merge follows the first parent only", func(t *testing.T) {
		var l histList
		if code, msg := f.get(t, logURL, "?ref=merge", &l); code != 200 || len(l.Commits) != 2 || l.Commits[1].OID != root {
			t.Errorf("%d %q %+v", code, msg, l)
		}
		if len(l.Commits[0].Parents) != 2 {
			t.Errorf("parents %v", l.Commits[0].Parents)
		}
	})
	t.Run("missing parent is an internal error", func(t *testing.T) {
		code, msg := f.get(t, logURL, "?ref=orphan", nil)
		if code != 500 || msg != "internal error" {
			t.Errorf("got %d %q", code, msg)
		}
	})
}
