package linediff_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/linediff"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

func numbered(n int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	return sb.String()
}

func replaceLine(text string, n int, with string) string {
	ls := strings.Split(text, "\n")
	ls[n-1] = with
	return strings.Join(ls, "\n")
}

// render turns hunks into a unified-diff-like string for comparison.
func render(hs []linediff.Hunk) string {
	var sb strings.Builder
	for _, h := range hs {
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, l := range h.Lines {
			p := map[string]string{"context": " ", "add": "+", "del": "-"}[l.Type]
			sb.WriteString(p + l.Text + "\n")
		}
	}
	return sb.String()
}

func TestDiffBasics(t *testing.T) {
	t.Parallel()
	base := numbered(10)
	tests := []struct {
		name, old, new string
		want           string
		add, del       int
	}{
		{"identical", base, base, "", 0, 0},
		{"both empty", "", "", "", 0, 0},
		{"only final newline differs", "a\nb", "a\nb\n", "", 0, 0},
		{"empty to text", "", "a\nb\n", "@@ -0,0 +1,2 @@\n+a\n+b\n", 2, 0},
		{"text to empty", "a\nb\n", "", "@@ -1,2 +0,0 @@\n-a\n-b\n", 0, 2},
		{"single line change", "a\nb\nc\n", "a\nX\nc\n", "@@ -1,3 +1,3 @@\n a\n-b\n+X\n c\n", 1, 1},
		{"blank line is a line", "\n", "", "@@ -1,1 +0,0 @@\n-\n", 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := linediff.Diff(tc.old, tc.new)
			if got := render(r.Hunks); got != tc.want {
				t.Errorf("hunks:\n%s\nwant:\n%s", got, tc.want)
			}
			if r.Hunks == nil {
				t.Error("Hunks is nil, want empty slice")
			}
			if r.Additions != tc.add || r.Deletions != tc.del {
				t.Errorf("counts +%d -%d, want +%d -%d", r.Additions, r.Deletions, tc.add, tc.del)
			}
			if r.Truncated {
				t.Error("unexpected truncated")
			}
		})
	}
}

// Compares hunk headers and bodies with `git diff -U3` on fixtures whose LCS
// is unambiguous.
func TestDiffMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := numbered(40)
	fixtures := map[string][2]string{
		"one change":        {base, replaceLine(base, 20, "changed")},
		"two far changes":   {base, replaceLine(replaceLine(base, 5, "x"), 35, "y")},
		"overlapping ctx":   {base, replaceLine(replaceLine(base, 10, "x"), 15, "y")},
		"touching ctx":      {base, replaceLine(replaceLine(base, 10, "x"), 17, "y")},
		"first line":        {base, replaceLine(base, 1, "first")},
		"last line":         {base, replaceLine(base, 40, "last")},
		"insert middle":     {base, strings.Replace(base, "line 20\n", "line 20\nnew a\nnew b\n", 1)},
		"delete middle":     {base, strings.Replace(base, "line 20\nline 21\n", "", 1)},
		"insert at start":   {base, "top\n" + base},
		"append at end":     {base, base + "tail\n"},
		"delete everything": {base, ""},
	}
	hdr := regexp.MustCompile(`(?m)^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@.*$`)
	for name, f := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
			os.WriteFile(a, []byte(f[0]), 0o644)
			os.WriteFile(b, []byte(f[1]), 0o644)
			out, _ := exec.Command("git", "diff", "--no-index", "-U3", "--", a, b).Output()
			s := string(out)
			if i := strings.Index(s, "@@"); i >= 0 {
				s = s[i:]
			} else {
				s = ""
			}
			// Normalise git headers: drop the trailing section text, and
			// write omitted counts as 1.
			s = hdr.ReplaceAllStringFunc(s, func(m string) string {
				g := hdr.FindStringSubmatch(m)
				for _, i := range []int{2, 4} {
					if g[i] == "" {
						g[i] = "1"
					}
				}
				return fmt.Sprintf("@@ -%s,%s +%s,%s @@", g[1], g[2], g[3], g[4])
			})
			if got := render(linediff.Diff(f[0], f[1]).Hunks); got != s {
				t.Errorf("differs from git:\n%s\ngit:\n%s", got, s)
			}
		})
	}
}

func TestMergedHunks(t *testing.T) {
	t.Parallel()
	base := numbered(40)
	// Changes at 10 and 17: context 7..13 and 14..20 touch, so one hunk.
	if n := len(linediff.Diff(base, replaceLine(replaceLine(base, 10, "x"), 17, "y")).Hunks); n != 1 {
		t.Errorf("touching: %d hunks, want 1", n)
	}
	// Changes at 10 and 18: a gap of one line, so two hunks.
	if n := len(linediff.Diff(base, replaceLine(replaceLine(base, 10, "x"), 18, "y")).Hunks); n != 2 {
		t.Errorf("gap: %d hunks, want 2", n)
	}
}

func TestTruncation(t *testing.T) {
	t.Parallel()
	exact := numbered(linediff.MaxLines)
	r := linediff.Diff(exact, replaceLine(exact, 100, "x"))
	if r.Truncated || len(r.Hunks) != 1 || r.Additions != 1 || r.Deletions != 1 {
		t.Errorf("exactly %d lines: truncated=%v hunks=%d", linediff.MaxLines, r.Truncated, len(r.Hunks))
	}
	over := numbered(linediff.MaxLines + 1)
	for name, pair := range map[string][2]string{"old": {over, "a\n"}, "new": {"a\n", over}} {
		r := linediff.Diff(pair[0], pair[1])
		if !r.Truncated || len(r.Hunks) != 0 || r.Hunks == nil || r.Additions != 0 {
			t.Errorf("%s side over cap: %+v", name, r)
		}
	}
}

// --- tree diff ---

type tree map[string]string // path -> content

func build(t *testing.T, s treebuild.Store, files tree) string {
	t.Helper()
	var cs []treebuild.Change
	for p, c := range files {
		cs = append(cs, treebuild.Upsert(p, c))
	}
	if len(cs) == 0 {
		return ""
	}
	oid, err := treebuild.Apply(s, "", cs)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

type summary struct {
	Path, Status string
	Add, Del     int
}

func sum(fs []linediff.FileDiff) []summary {
	var out []summary
	for _, f := range fs {
		out = append(out, summary{f.Path, f.Status, f.Additions, f.Deletions})
	}
	return out
}

func TestTrees(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		old, new tree
		want     []summary
	}{
		{"root commit", nil, tree{"b.txt": "x\n", "a/c.txt": "1\n2\n"},
			[]summary{{"a/c.txt", "added", 2, 0}, {"b.txt", "added", 1, 0}}},
		{"to empty tree", tree{"a.txt": "x\ny\n"}, nil,
			[]summary{{"a.txt", "deleted", 0, 2}}},
		{"identical", tree{"a.txt": "x\n"}, tree{"a.txt": "x\n"}, nil},
		{"modify only", tree{"z.txt": "1\n", "d/e.txt": "1\n", "d/f.txt": "same\n"},
			tree{"z.txt": "1\n", "d/e.txt": "2\n", "d/f.txt": "same\n", "d/g.txt": "g\n"},
			[]summary{{"d/e.txt", "modified", 1, 1}, {"d/g.txt", "added", 1, 0}}},
		{"file becomes dir", tree{"a": "x\n"}, tree{"a/b": "1\n", "a/c": "2\n"},
			[]summary{{"a", "deleted", 0, 1}, {"a/b", "added", 1, 0}, {"a/c", "added", 1, 0}}},
		{"dir becomes file", tree{"a/b": "1\n", "a/c": "2\n"}, tree{"a": "x\n"},
			[]summary{{"a", "added", 1, 0}, {"a/b", "deleted", 0, 1}, {"a/c", "deleted", 0, 1}}},
		{"rename is delete plus add", tree{"old.txt": "same\n"}, tree{"new.txt": "same\n"},
			[]summary{{"new.txt", "added", 1, 0}, {"old.txt", "deleted", 0, 1}}},
		{"git order vs path order", tree{"a.txt": "1\n", "a/x": "1\n"}, tree{"a.txt": "2\n", "a/x": "2\n"},
			[]summary{{"a.txt", "modified", 1, 1}, {"a/x", "modified", 1, 1}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := treebuild.NewMemStore()
			o, n := build(t, s, tc.old), build(t, s, tc.new)
			got, err := linediff.Trees(s, o, n)
			if err != nil {
				t.Fatal(err)
			}
			if got == nil {
				t.Fatal("nil slice, want empty")
			}
			if !reflect.DeepEqual(sum(got), tc.want) {
				t.Errorf("got %+v, want %+v", sum(got), tc.want)
			}
		})
	}
}

func TestTreesHunksAndEmptyTreeOID(t *testing.T) {
	t.Parallel()
	s := treebuild.NewMemStore()
	n := build(t, s, tree{"f": "a\nb\nc\n"})
	for _, old := range []string{"", linediff.EmptyTreeOID} {
		got, err := linediff.Trees(s, old, n)
		if err != nil || len(got) != 1 {
			t.Fatalf("got %v, %v", got, err)
		}
		h := got[0].Hunks
		if len(h) != 1 || h[0].OldStart != 0 || h[0].OldLines != 0 || h[0].NewStart != 1 || h[0].NewLines != 3 {
			t.Errorf("added hunks: %+v", h)
		}
	}
	got, _ := linediff.Trees(s, n, "")
	h := got[0].Hunks
	if got[0].Status != "deleted" || len(h) != 1 || h[0].NewStart != 0 || h[0].NewLines != 0 || h[0].OldStart != 1 || h[0].OldLines != 3 {
		t.Errorf("deleted: %+v", got[0])
	}
}

func TestTreesFinalNewlineOnly(t *testing.T) {
	t.Parallel()
	s := treebuild.NewMemStore()
	o, n := build(t, s, tree{"f": "a\nb"}), build(t, s, tree{"f": "a\nb\n"})
	got, err := linediff.Trees(s, o, n)
	if err != nil || len(got) != 1 || got[0].Status != "modified" || len(got[0].Hunks) != 0 || got[0].Hunks == nil {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestTreesTruncated(t *testing.T) {
	t.Parallel()
	s := treebuild.NewMemStore()
	o := build(t, s, tree{"big": numbered(linediff.MaxLines + 1), "ok": numbered(linediff.MaxLines)})
	n := build(t, s, tree{"big": "x\n", "ok": "x\n"})
	got, err := linediff.Trees(s, o, n)
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	if !got[0].Truncated || len(got[0].Hunks) != 0 {
		t.Errorf("big: %+v", got[0])
	}
	if got[1].Truncated || len(got[1].Hunks) != 1 {
		t.Errorf("ok: %+v", got[1])
	}
}

// countingStore records which objects are read.
type countingStore struct {
	treebuild.Store
	reads map[string]int
}

func (c *countingStore) GetObject(oid string) (gitobj.Type, []byte, error) {
	c.reads[oid]++
	return c.Store.GetObject(oid)
}

func TestTreesSkipsUnchangedSubtrees(t *testing.T) {
	t.Parallel()
	mem := treebuild.NewMemStore()
	o := build(t, mem, tree{"keep/deep/x": "1\n", "keep/y": "2\n", "chg/z": "1\n"})
	n, err := treebuild.Apply(mem, o, []treebuild.Change{treebuild.Upsert("chg/z", "2\n")})
	if err != nil {
		t.Fatal(err)
	}
	keep := ""
	_, body, _ := mem.GetObject(o)
	es, _ := gitobj.ParseTree(body)
	for _, e := range es {
		if e.Name == "keep" {
			keep = e.OID
		}
	}
	cs := &countingStore{mem, map[string]int{}}
	got, err := linediff.Trees(cs, o, n)
	if err != nil || len(got) != 1 || got[0].Path != "chg/z" {
		t.Fatal(got, err)
	}
	if cs.reads[keep] != 0 {
		t.Errorf("unchanged subtree %s was read", keep)
	}
}

func TestTreesErrors(t *testing.T) {
	t.Parallel()
	s := treebuild.NewMemStore()
	missing := strings.Repeat("a", 40)
	if _, err := linediff.Trees(s, missing, ""); err == nil {
		t.Error("missing old tree: want error")
	}
	blob, _ := s.PutObject(gitobj.TypeBlob, []byte("x"))
	if _, err := linediff.Trees(s, blob, ""); err == nil {
		t.Error("blob as tree: want error")
	}
}

// lines returns n lines "<prefix> 1".."<prefix> n".
func lines(prefix string, n int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "%s %d\n", prefix, i)
	}
	return sb.String()
}

func TestTreesBudget(t *testing.T) {
	s := treebuild.NewMemStore()
	// a, b: 5000x5000 with every line different (25M cells each); only one
	// fits in the 32M budget. c and d are small edits that still fit.
	bigSame := numbered(linediff.MaxLines)
	o := build(t, s, tree{"a": lines("old", 5000), "b": lines("old", 5000), "c": bigSame, "d": "1\n2\n"})
	n := build(t, s, tree{"a": lines("new", 5000), "b": lines("new", 5000), "c": replaceLine(bigSame, 2500, "changed"), "d": "1\n3\n"})

	run := func() []linediff.FileDiff {
		got, err := linediff.Trees(s, o, n)
		if err != nil || len(got) != 4 {
			t.Fatal(got, err)
		}
		return got
	}
	got := run()
	if got[0].Truncated || got[0].Additions != 5000 || got[0].Deletions != 5000 {
		t.Errorf("a should be diffed: truncated=%v +%d -%d", got[0].Truncated, got[0].Additions, got[0].Deletions)
	}
	b := got[1]
	if !b.Truncated || len(b.Hunks) != 0 || b.Hunks == nil || b.Additions != 0 || b.Deletions != 0 || b.Status != linediff.StatusModified {
		t.Errorf("b should be truncated: %+v", b)
	}
	for _, f := range got[2:] {
		if f.Truncated || f.Additions != 1 || f.Deletions != 1 || len(f.Hunks) != 1 {
			t.Errorf("%s should be diffed after the budget ran low: %+v", f.Path, f)
		}
	}
	// Same input, same result.
	if again := run(); !reflect.DeepEqual(got, again) {
		t.Error("Trees is not deterministic")
	}
}

func TestTreesBudgetSmallDiffsNotPenalised(t *testing.T) {
	t.Parallel()
	s := treebuild.NewMemStore()
	files, edited := tree{}, tree{}
	text := numbered(linediff.MaxLines)
	for i := 0; i < 20; i++ { // 20 big files, one changed line each
		p := fmt.Sprintf("f%02d", i)
		files[p], edited[p] = text, replaceLine(text, 10+i, "x")
	}
	got, err := linediff.Trees(s, build(t, s, files), build(t, s, edited))
	if err != nil || len(got) != 20 {
		t.Fatal(len(got), err)
	}
	for _, f := range got {
		if f.Truncated || len(f.Hunks) != 1 {
			t.Errorf("%s: %+v", f.Path, f)
		}
	}
}
