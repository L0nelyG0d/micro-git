package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReadEndpointsGolden pins the exact bytes of every read endpoint (tree,
// blob, commit log, commit detail, compare). The golden files were recorded
// before the round-trip reduction of #49; the bodies must stay identical.
// Commit oids (they depend on the random test username) and the owner name are
// replaced by placeholders. Re-record with GOLDEN_UPDATE=1.
func TestReadEndpointsGolden(t *testing.T) {
	s := testServer(t)
	s.Now = func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"gold"}`)
	base := "/repos/" + owner + "/gold"

	c1 := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "first",
		up("README.md", "hello\nworld\n"), up("a/b/c/deep.txt", "deep\n"), up("a/b/x.txt", "x\n"),
		up("a/old.txt", "gone\n"), up("z.txt", "z1\nz2\nz3\n"))).OID
	c2 := mustCommit(t, s, owner, c, repo, commitJSON("main", c1, "second",
		up("README.md", "hello\nthere\n"), up("a/b/c/deep.txt", "deeper\n"), del("a/old.txt"),
		up("n/new.txt", "new\n"), del("z.txt"))).OID
	oids := map[string]string{c1: "{c1}", c2: "{c2}"}

	cases := []struct{ name, path string }{
		{"tree_root", "/tree/main"},
		{"tree_root_by_oid", "/tree/" + c1},
		{"tree_depth1", "/tree/main/a"},
		{"tree_depth3", "/tree/main/a/b/c"},
		{"tree_trailing_slash", "/tree/main/a/b/"},
		{"tree_is_file", "/tree/main/README.md"},
		{"tree_missing", "/tree/main/a/nope"},
		{"tree_through_file", "/tree/main/README.md/x"},
		{"blob_root", "/blob/main/README.md"},
		{"blob_deep", "/blob/main/a/b/c/deep.txt"},
		{"blob_old_commit", "/blob/" + c1 + "/z.txt"},
		{"blob_is_dir", "/blob/main/a"},
		{"blob_missing", "/blob/main/a/b/none.txt"},
		{"tree_bad_ref", "/tree/nope"},
		{"commits", "/commits?ref=main"},
		{"commit_root", "/commits/" + c1},
		{"commit_child", "/commits/" + c2},
		{"commit_missing", "/commits/" + strings.Repeat("0", 40)},
		{"compare", "/compare?base=" + c1 + "&head=main"},
		{"compare_reverse", "/compare?base=main&head=" + c1},
		{"compare_same", "/compare?base=main&head=main"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(s, http.MethodGet, base+tc.path, "", nil)
			got := rec.Body.String()
			for oid, ph := range oids {
				got = strings.ReplaceAll(got, oid, ph)
			}
			got = strings.ReplaceAll(got, owner, "{owner}")
			got = http.StatusText(rec.Code) + "\n" + got
			file := filepath.Join("testdata", "golden", tc.name+".txt")
			if os.Getenv("GOLDEN_UPDATE") != "" {
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("response changed\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// TestReadStatementCounts guards the round-trip reduction of #49.
func TestReadStatementCounts(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"cnt"}`)
	base := "/repos/" + owner + "/cnt"
	c1 := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "first",
		up("README.md", "hi\n"), up("a/b/c/deep.txt", "deep\n"), up("a/b/x.txt", "x\n"))).OID
	c2 := mustCommit(t, s, owner, c, repo, commitJSON("main", c1, "second",
		up("a/b/c/deep.txt", "deeper\n"), up("a/b/x.txt", "y\n"), up("k.txt", "k\n"))).OID
	buf := captureTimingLog(t)
	cases := []struct {
		path string
		max  int
	}{
		{"/tree/main", 1},
		{"/tree/main/a/b/c", 4},
		{"/blob/main/a/b/c/deep.txt", 5},
		{"/commits/" + c2, 6},                      // bundle, a, a/b, a/b/c levels, blobs
		{"/compare?base=" + c1 + "&head=main", 11}, // compare.go itself is untouched here (#50)
	}
	for _, tc := range cases {
		buf.Reset()
		if rec := do(s, http.MethodGet, base+tc.path, "", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body)
		}
		if _, n := lastTiming(t, buf); n > tc.max {
			t.Errorf("%s: %d statements, want <= %d", tc.path, n, tc.max)
		}
	}
}
