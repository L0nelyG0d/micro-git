package treebuild_test

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func apply(t *testing.T, s *treebuild.MemStore, root string, cs ...treebuild.Change) string {
	t.Helper()
	oid, err := treebuild.Apply(s, root, cs)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return oid
}

func entries(t *testing.T, s *treebuild.MemStore, oid string) map[string]gitobj.TreeEntry {
	t.Helper()
	typ, body, err := s.GetObject(oid)
	if err != nil || typ != gitobj.TypeTree {
		t.Fatalf("GetObject(%s): %v %v", oid, typ, err)
	}
	es, err := gitobj.ParseTree(body)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]gitobj.TreeEntry{}
	for _, e := range es {
		m[e.Name] = e
	}
	return m
}

func TestCreateInEmptyTree(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"", emptyTree} {
		s := treebuild.NewMemStore()
		if root != "" {
			s.PutObject(gitobj.TypeTree, nil)
		}
		got := apply(t, s, root, treebuild.Upsert("a.txt", "test content\n"))
		want, _ := gitobj.SerializeTree([]gitobj.TreeEntry{{Mode: gitobj.ModeFile, Name: "a.txt", OID: "d670460b4b4aece5915caf5c68d12f560a9fe3e4"}})
		if got != gitobj.Hash(gitobj.TypeTree, want) {
			t.Errorf("root %q: oid %s", root, got)
		}
		if e := entries(t, s, got)["a.txt"]; e.OID != "d670460b4b4aece5915caf5c68d12f560a9fe3e4" {
			t.Errorf("blob oid %s", e.OID)
		}
		if _, body, err := s.GetObject(e(t, s, got).OID); err != nil || string(body) != "test content\n" {
			t.Errorf("blob not stored: %v", err)
		}
	}
}

func e(t *testing.T, s *treebuild.MemStore, oid string) gitobj.TreeEntry {
	return entries(t, s, oid)["a.txt"]
}

func TestNestedCreateAndEdit(t *testing.T) {
	t.Parallel()
	s := treebuild.NewMemStore()
	r1 := apply(t, s, "", treebuild.Upsert("a/b/c.txt", "1"), treebuild.Upsert("a/x.txt", "x"), treebuild.Upsert("z/y.txt", "y"), treebuild.Upsert("top", "t"))
	a := entries(t, s, r1)["a"]
	if !a.IsDir() {
		t.Fatal("a not a dir")
	}
	b := entries(t, s, a.OID)["b"]
	if !b.IsDir() || entries(t, s, b.OID)["c.txt"].OID != gitobj.HashBlob([]byte("1")) {
		t.Fatal("a/b/c.txt missing")
	}
	before := s.Len()
	r2 := apply(t, s, r1, treebuild.Upsert("a/b/c.txt", "2"))
	// c blob + tree a/b + tree a + root = 4 new objects, nothing else.
	if got := s.Len() - before; got != 4 {
		t.Errorf("wrote %d objects, want 4", got)
	}
	old, nw := entries(t, s, r1), entries(t, s, r2)
	if old["z"].OID != nw["z"].OID || old["top"].OID != nw["top"].OID {
		t.Error("untouched siblings changed")
	}
	if old["a"].OID == nw["a"].OID {
		t.Error("a unchanged")
	}
	oa, na := entries(t, s, old["a"].OID), entries(t, s, nw["a"].OID)
	if oa["x.txt"].OID != na["x.txt"].OID {
		t.Error("a/x.txt changed")
	}
	if oa["b"].OID == na["b"].OID {
		t.Error("a/b unchanged")
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	s := treebuild.NewMemStore()
	r1 := apply(t, s, "", treebuild.Upsert("a/b/c.txt", "1"), treebuild.Upsert("a/x", "x"), treebuild.Upsert("k", "k"))
	r2 := apply(t, s, r1, treebuild.Remove("a/b/c.txt"))
	a := entries(t, s, r2)["a"]
	if _, ok := entries(t, s, a.OID)["b"]; ok {
		t.Error("empty dir a/b kept")
	}
	r3 := apply(t, s, r2, treebuild.Remove("a/x"))
	if _, ok := entries(t, s, r3)["a"]; ok {
		t.Error("empty dir a kept")
	}
	r4 := apply(t, s, r3, treebuild.Remove("k"))
	if r4 != emptyTree || !s.Has(emptyTree) {
		t.Errorf("all-deleted root = %s, want empty tree stored", r4)
	}
	// Whole subtree removed in one call.
	r5 := apply(t, s, r1, treebuild.Remove("a/b/c.txt"), treebuild.Remove("a/x"))
	if _, ok := entries(t, s, r5)["a"]; ok {
		t.Error("a kept")
	}
}

func TestOrderIndependent(t *testing.T) {
	t.Parallel()
	base := treebuild.NewMemStore()
	r0 := apply(t, base, "", treebuild.Upsert("d/old", "o"), treebuild.Upsert("d/keep", "k"), treebuild.Upsert("e/f", "f"))
	cs := []treebuild.Change{
		treebuild.Upsert("d/new", "n"), treebuild.Remove("d/old"), treebuild.Upsert("d/keep", "K"),
		treebuild.Remove("e/f"), treebuild.Upsert("g/h/i", "i"), treebuild.Remove("missing"), treebuild.Upsert("foo.txt", "1"), treebuild.Upsert("foo/bar", "2"),
	}
	want := apply(t, base, r0, cs...)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		p := append([]treebuild.Change(nil), cs...)
		rng.Shuffle(len(p), func(a, b int) { p[a], p[b] = p[b], p[a] })
		if got := apply(t, base, r0, p...); got != want {
			t.Fatalf("order changed oid: %s vs %s", got, want)
		}
	}
}

func TestNoChanges(t *testing.T) {
	t.Parallel()
	s := treebuild.NewMemStore()
	r1 := apply(t, s, "", treebuild.Upsert("a/b", "x"))
	n := s.Len()
	for name, cs := range map[string][]treebuild.Change{
		"same content":       {treebuild.Upsert("a/b", "x")},
		"delete missing":     {treebuild.Remove("nope")},
		"delete under file":  {treebuild.Remove("a/b/c")},
		"delete missing dir": {treebuild.Remove("q/w/e")},
		"mixed no-ops":       {treebuild.Upsert("a/b", "x"), treebuild.Remove("zz")},
	} {
		_, err := treebuild.Apply(s, r1, cs)
		var te *treebuild.Error
		if !errors.Is(err, treebuild.ErrNoChanges) || !errors.As(err, &te) || err.Error() != "no changes" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if s.Len() != n {
		t.Error("no-op wrote objects")
	}
	// Partial no-ops: the rest applies.
	r2 := apply(t, s, r1, treebuild.Upsert("a/b", "x"), treebuild.Remove("zz"), treebuild.Upsert("c", "c"))
	if _, ok := entries(t, s, r2)["c"]; !ok || entries(t, s, r2)["a"].OID != entries(t, s, r1)["a"].OID {
		t.Error("partial no-op handling wrong")
	}
}

func TestValidationErrors(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }
	s := treebuild.NewMemStore()
	root := apply(t, s, "", treebuild.Upsert("file", "f"), treebuild.Upsert("dir/x", "x"))
	n := s.Len()
	long := strings.Repeat("a", 256)
	tests := []struct {
		name string
		cs   []treebuild.Change
		kind error
		path string
		msg  string
	}{
		{"leading slash", []treebuild.Change{treebuild.Upsert("/a", "")}, treebuild.ErrInvalidPath, "/a", "invalid path: /a"},
		{"trailing slash", []treebuild.Change{treebuild.Upsert("a/", "")}, treebuild.ErrInvalidPath, "a/", ""},
		{"empty", []treebuild.Change{treebuild.Upsert("", "")}, treebuild.ErrInvalidPath, "", ""},
		{"empty segment", []treebuild.Change{treebuild.Upsert("a//b", "")}, treebuild.ErrInvalidPath, "a//b", ""},
		{"dot", []treebuild.Change{treebuild.Upsert("a/./b", "")}, treebuild.ErrInvalidPath, "a/./b", ""},
		{"dotdot", []treebuild.Change{treebuild.Upsert("../b", "")}, treebuild.ErrInvalidPath, "../b", ""},
		{"git", []treebuild.Change{treebuild.Upsert("a/.git/b", "")}, treebuild.ErrInvalidPath, "a/.git/b", ""},
		{"too long", []treebuild.Change{treebuild.Upsert(long, "")}, treebuild.ErrInvalidPath, long, ""},
		{"too large", []treebuild.Change{treebuild.Upsert("big", strings.Repeat("x", 1<<20+1))}, treebuild.ErrFileTooLarge, "big", "file too large: big"},
		{"invalid utf8", []treebuild.Change{treebuild.Upsert("u", "\xff")}, treebuild.ErrNotUTF8Text, "u", "file is not valid UTF-8 text: u"},
		{"nul", []treebuild.Change{treebuild.Upsert("u", "a\x00b")}, treebuild.ErrNotUTF8Text, "u", ""},
		{"duplicate", []treebuild.Change{treebuild.Upsert("d", "1"), treebuild.Upsert("d", "2")}, treebuild.ErrDuplicatePath, "d", "duplicate path: d"},
		{"duplicate upsert+delete", []treebuild.Change{treebuild.Upsert("d", "1"), treebuild.Remove("d")}, treebuild.ErrDuplicatePath, "d", ""},
		{"both", []treebuild.Change{{Path: "b", Content: str("x"), Delete: true}}, treebuild.ErrBadChange, "b", "change must have content or delete: b"},
		{"neither", []treebuild.Change{{Path: "b"}}, treebuild.ErrBadChange, "b", ""},
		{"file as dir", []treebuild.Change{treebuild.Upsert("file/x", "")}, treebuild.ErrPathConflict, "file/x", "path conflicts with existing file or directory: file/x"},
		{"dir as file", []treebuild.Change{treebuild.Upsert("dir", "")}, treebuild.ErrPathConflict, "dir", ""},
		{"in-call conflict", []treebuild.Change{treebuild.Upsert("n", ""), treebuild.Upsert("n/m", "")}, treebuild.ErrPathConflict, "n/m", ""},
		{"in-call conflict reversed", []treebuild.Change{treebuild.Upsert("n/m", ""), treebuild.Upsert("n", "")}, treebuild.ErrPathConflict, "n", ""},
		{"delete dir", []treebuild.Change{treebuild.Remove("dir")}, treebuild.ErrInvalidPath, "dir", ""},
		{"error after valid change", []treebuild.Change{treebuild.Upsert("ok", "1"), treebuild.Upsert("file/x", "")}, treebuild.ErrPathConflict, "file/x", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := treebuild.Apply(s, root, tc.cs)
			var te *treebuild.Error
			if !errors.Is(err, tc.kind) || !errors.As(err, &te) || te.Path != tc.path {
				t.Fatalf("err = %v, want kind %v path %q", err, tc.kind, tc.path)
			}
			if tc.msg != "" && err.Error() != tc.msg {
				t.Errorf("message %q, want %q", err.Error(), tc.msg)
			}
			if s.Len() != n {
				t.Error("objects written on error")
			}
		})
	}
}

func TestContentBoundaries(t *testing.T) {
	t.Parallel()
	s := treebuild.NewMemStore()
	apply(t, s, "", treebuild.Upsert("max", strings.Repeat("x", 1<<20)))
	apply(t, s, "", treebuild.Upsert("empty", ""))
	apply(t, s, "", treebuild.Upsert(strings.Repeat("a", 255), "x"))
	apply(t, s, "", treebuild.Upsert("ünï/文件", "héllo ✓"))
}

func TestMissingRootObject(t *testing.T) {
	t.Parallel()
	s := treebuild.NewMemStore()
	_, err := treebuild.Apply(s, strings.Repeat("a", 40), []treebuild.Change{treebuild.Upsert("a", "b")})
	if !errors.Is(err, treebuild.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
