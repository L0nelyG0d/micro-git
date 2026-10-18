package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestLocalChanges(t *testing.T) {
	base := map[string]string{"keep": "1", "edit": "1", "gone": "1"}
	now := map[string]string{"keep": "1", "edit": "2", "new": "1"}
	a, m, d := localChanges(base, now)
	if !reflect.DeepEqual(a, []string{"new"}) || !reflect.DeepEqual(m, []string{"edit"}) || !reflect.DeepEqual(d, []string{"gone"}) {
		t.Fatalf("got added=%v modified=%v deleted=%v", a, m, d)
	}
}

func TestScanLocalSkipsBinaryAndMetadata(t *testing.T) {
	dir := t.TempDir()
	write := func(p string, b []byte) {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", []byte("hi\n"))
	write("src/b.go", []byte("package b\n"))
	write("img.bin", []byte{0xff, 0x00, 0xfe})
	write(".git/config", []byte("x"))
	write(".mg/state.json", []byte("{}"))
	got, err := scanLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["a.txt"] != hashOf([]byte("hi\n")) || got["src/b.go"] == "" {
		t.Fatalf("scanLocal = %v", got)
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadState(dir); err == nil {
		t.Fatal("expected an error for a directory that is not a working copy")
	}
	want := state{Repo: "alice/notes", Branch: "main", Tip: "abc", Files: map[string]string{"a": "h"}}
	if err := want.save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(dir)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if o, n := got.owner(); o != "alice" || n != "notes" {
		t.Fatalf("owner() = %s, %s", o, n)
	}
}

func TestEscPath(t *testing.T) {
	if got := escPath("src/b c#.go"); got != "src/b%20c%23.go" {
		t.Fatalf("escPath = %q", got)
	}
}

func TestSafeRel(t *testing.T) {
	bad := []struct{ name, in string }{
		{"absolute", "/etc/x"},
		{"empty", ""},
		{"dot", "."},
		{"dotdot", ".."},
		{"leading dotdot", "../x"},
		{"inner dotdot", "a/../b"},
		{"git", ".git/config"},
		{"nested git", "a/.git/hooks"},
		{"mg", ".mg/state.json"},
		{"nested mg", "a/.mg/x"},
		{"upper MG", ".MG/state.json"},
		{"mixed Mg", ".Mg"},
		{"upper GIT", ".GIT/config"},
		{"nested upper GIT", "a/.GIT/x"},
		{"git trailing space", ".git /x"},
		{"mg trailing dot", ".mg./x"},
		{"segment trailing dot", "a./b"},
		{"segment trailing space", "a /b"},
		{"backslash", `a\b`},
		{"nul", "a\x00b"},
		{"empty segment", "a//b"},
		{"trailing slash", "a/"},
	}
	for _, tc := range bad {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			if got, err := safeRel(tc.in); err == nil {
				t.Fatalf("safeRel(%q) = %q, want an error", tc.in, got)
			}
		})
	}
	good := []string{"a.txt", "a/b/c.txt", "dir/.gitignore", "my.mgfile"}
	for _, in := range good {
		t.Run("accepts "+in, func(t *testing.T) {
			got, err := safeRel(in)
			if err != nil || got != in {
				t.Fatalf("safeRel(%q) = %q, %v", in, got, err)
			}
		})
	}
}

// fakeRemote serves just enough of the API for clone and pull. rootNames are
// the entries of the root tree (all blobs); changes is the compare result.
func fakeRemote(t *testing.T, rootNames []string, changes []map[string]string) config {
	t.Helper()
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("/repos/o/n", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"default_branch": "main"})
	})
	mux.HandleFunc("/repos/o/n/branches", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"branches": []map[string]string{{"name": "main", "oid": "newtip1234"}}})
	})
	mux.HandleFunc("/repos/o/n/tree/", func(w http.ResponseWriter, r *http.Request) {
		var es []map[string]string
		for _, n := range rootNames {
			es = append(es, map[string]string{"name": n, "type": "blob"})
		}
		writeJSON(w, map[string]any{"entries": es})
	})
	mux.HandleFunc("/repos/o/n/blob/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"content": "PWNED\n"})
	})
	mux.HandleFunc("/repos/o/n/compare", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"files": changes})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return config{Server: srv.URL}
}

// snapshot lists every path under root (symlinks are not followed) with its
// content or link target, so two snapshots can be compared.
func snapshot(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		entry := rel
		switch {
		case d.Type()&os.ModeSymlink != 0:
			tgt, _ := os.Readlink(p)
			entry += " -> " + tgt
		case d.Type().IsRegular():
			b, _ := os.ReadFile(p)
			entry += " = " + string(b)
		}
		out = append(out, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func assertUnchanged(t *testing.T, label, root string, before []string) {
	t.Helper()
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s changed:\nbefore %q\nafter  %q", label, before, after)
	}
}

var unsafeNames = []string{"../x", ".mg/state.json", ".MG/state.json", ".Mg", ".GIT/config", "a/.GIT/x", "/abs", `back\slash`, "nul\x00byte"}

func TestCloneRejectsUnsafePaths(t *testing.T) {
	for _, bad := range unsafeNames {
		t.Run(bad, func(t *testing.T) {
			// A good file comes first: it must not be written either.
			cfg := fakeRemote(t, []string{"good.txt", bad}, nil)
			base := t.TempDir()
			dir := filepath.Join(base, "target")
			_, err := doClone(cfg, "o/n", dir)
			if err == nil {
				t.Fatal("expected an error")
			}
			if _, statErr := os.Lstat(dir); statErr == nil {
				t.Fatal("rejected clone left the target dir behind")
			}
			if _, statErr := os.Stat(filepath.Join(dir, "good.txt")); statErr == nil {
				t.Fatal("good.txt was written before the bad path was rejected")
			}
			if _, statErr := os.Stat(filepath.Join(dir, ".mg")); statErr == nil {
				t.Fatal(".mg was created")
			}
			if entries, _ := os.ReadDir(base); len(entries) > 1 {
				t.Fatalf("files escaped into %s: %v", base, entries)
			}
		})
	}
}

func newWorkingCopy(t *testing.T) (dir, outside string) {
	t.Helper()
	base := t.TempDir()
	dir = filepath.Join(base, "wc")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{dir, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st := state{Repo: "o/n", Branch: "main", Tip: "oldtip1234", Files: map[string]string{}}
	if err := st.save(dir); err != nil {
		t.Fatal(err)
	}
	return dir, outside
}

func TestPullRejectsUnsafePaths(t *testing.T) {
	for _, bad := range unsafeNames {
		for _, status := range []string{"added", "deleted"} {
			t.Run(status+" "+bad, func(t *testing.T) {
				cfg := fakeRemote(t, nil, []map[string]string{
					{"path": "good.txt", "status": "added"},
					{"path": bad, "status": status},
				})
				dir, outside := newWorkingCopy(t)
				if err := os.WriteFile(filepath.Join(outside, "x"), []byte("keep"), 0o644); err != nil {
					t.Fatal(err)
				}
				beforeDir, beforeOut := snapshot(t, dir), snapshot(t, filepath.Dir(dir))
				if _, err := doPull(cfg, dir, true); err == nil {
					t.Fatal("expected an error")
				}
				assertUnchanged(t, "working copy", dir, beforeDir)
				assertUnchanged(t, "parent of working copy", filepath.Dir(dir), beforeOut)
			})
		}
	}
}

func TestPullDoesNotWriteThroughSymlink(t *testing.T) {
	t.Run("symlinked file", func(t *testing.T) {
		dir, outside := newWorkingCopy(t)
		target := filepath.Join(outside, "secret.txt")
		if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		cfg := fakeRemote(t, nil, []map[string]string{{"path": "link", "status": "modified"}})
		before := snapshot(t, filepath.Dir(dir))
		_, err := doPull(cfg, dir, true)
		if err == nil || !strings.Contains(err.Error(), "link") {
			t.Fatalf("want an error naming link, got %v", err)
		}
		assertUnchanged(t, "tree", filepath.Dir(dir), before)
	})
	t.Run("symlinked directory", func(t *testing.T) {
		dir, outside := newWorkingCopy(t)
		if err := os.Symlink(outside, filepath.Join(dir, "sub")); err != nil {
			t.Fatal(err)
		}
		cfg := fakeRemote(t, nil, []map[string]string{{"path": "sub/new.txt", "status": "added"}})
		before := snapshot(t, filepath.Dir(dir))
		_, err := doPull(cfg, dir, true)
		if err == nil || !strings.Contains(err.Error(), "sub/new.txt") {
			t.Fatalf("want an error naming sub/new.txt, got %v", err)
		}
		assertUnchanged(t, "tree", filepath.Dir(dir), before)
	})
	t.Run("delete through symlinked directory", func(t *testing.T) {
		dir, outside := newWorkingCopy(t)
		if err := os.WriteFile(filepath.Join(outside, "victim"), []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "sub")); err != nil {
			t.Fatal(err)
		}
		cfg := fakeRemote(t, nil, []map[string]string{{"path": "sub/victim", "status": "deleted"}})
		before := snapshot(t, filepath.Dir(dir))
		if _, err := doPull(cfg, dir, true); err == nil {
			t.Fatal("expected an error")
		}
		assertUnchanged(t, "tree", filepath.Dir(dir), before)
	})
}

func TestPullAndCloneAcceptNestedPaths(t *testing.T) {
	cfg := fakeRemote(t, []string{"a.txt"}, nil)
	dir := filepath.Join(t.TempDir(), "c")
	if _, err := doClone(cfg, "o/n", dir); err != nil {
		t.Fatal(err)
	}
	cfg = fakeRemote(t, nil, []map[string]string{{"path": "a/b/c.txt", "status": "added"}})
	st, _ := loadState(dir)
	st.Tip = "oldtip1234"
	if err := st.save(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := doPull(cfg, dir, false); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "a", "b", "c.txt")); err != nil || string(b) != "PWNED\n" {
		t.Fatalf("nested file: %q, %v", b, err)
	}
}
