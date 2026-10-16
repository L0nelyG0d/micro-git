package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// readFixture is one repo shared by the whole reading test group.
type readFixture struct {
	s            *Server
	owner        string
	repo         Repo
	c1, c2       commitResp // c1: first commit, c2: second (main tip)
	base         string
	emptyRepo    Repo
	emptyBaseURL string
}

func newReadFixture(t *testing.T) *readFixture {
	t.Helper()
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"r"}`)
	f := &readFixture{s: s, owner: owner, repo: repo, base: "/repos/" + owner + "/r"}
	f.c1 = mustCommit(t, s, owner, c, repo, commitJSON("main", "", "first",
		up("README.md", "hello\n"),
		up("empty.txt", ""),
		up("src/main.go", "package main\n"),
		up("src/lib/util.go", "package lib\n"),
		up("src/lib/Z.txt", "z"),
		up("src/a.txt", "a"),
		up("src/b dir/f.txt", "spaced"),
	))
	if rec := do(s, http.MethodPost, f.base+"/branches", `{"name":"dev","from":"main"}`, c); rec.Code != 201 {
		t.Fatalf("branch: %d %s", rec.Code, rec.Body)
	}
	f.c2 = mustCommit(t, s, owner, c, repo, commitJSON("main", f.c1.OID, "second", up("README.md", "hello world\n")))
	f.emptyRepo = createRepo(t, s, c, `{"name":"empty"}`)
	f.emptyBaseURL = "/repos/" + owner + "/empty"
	return f
}

func (f *readFixture) get(path string) (int, map[string]any) {
	rec := do(f.s, http.MethodGet, f.base+path, "", nil)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func entryNames(m map[string]any) []string {
	var out []string
	for _, e := range m["entries"].([]any) {
		e := e.(map[string]any)
		out = append(out, e["type"].(string)+":"+e["name"].(string))
	}
	return out
}

func TestReadingTree(t *testing.T) {
	f := newReadFixture(t)

	t.Run("root by branch", func(t *testing.T) {
		for _, p := range []string{"/tree/main", "/tree/main/"} {
			code, m := f.get(p)
			if code != 200 {
				t.Fatalf("%s: %d %v", p, code, m)
			}
			if m["ref"] != "main" || m["commit"] != f.c2.OID || m["path"] != "" {
				t.Errorf("%s: header = %v", p, m)
			}
			got := strings.Join(entryNames(m), ",")
			if want := "tree:src,blob:README.md,blob:empty.txt"; got != want {
				t.Errorf("%s: entries %s, want %s", p, got, want)
			}
		}
	})
	t.Run("entry oids", func(t *testing.T) {
		_, m := f.get("/tree/main")
		for _, e := range m["entries"].([]any) {
			e := e.(map[string]any)
			if len(e["oid"].(string)) != 40 {
				t.Errorf("bad oid %v", e)
			}
			if e["name"] == "empty.txt" && e["oid"] != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
				t.Errorf("empty blob oid = %v", e["oid"])
			}
		}
	})
	t.Run("nested and sorting", func(t *testing.T) {
		code, m := f.get("/tree/main/src")
		if code != 200 || m["path"] != "src" {
			t.Fatalf("%d %v", code, m)
		}
		got := strings.Join(entryNames(m), ",")
		if want := "tree:b dir,tree:lib,blob:a.txt,blob:main.go"; got != want {
			t.Errorf("entries %s, want %s", got, want)
		}
		_, m = f.get("/tree/main/src/lib")
		got = strings.Join(entryNames(m), ",")
		if want := "blob:Z.txt,blob:util.go"; got != want { // bytewise: uppercase first
			t.Errorf("entries %s, want %s", got, want)
		}
	})
	t.Run("trailing slash", func(t *testing.T) {
		code, m := f.get("/tree/main/src/lib/")
		if code != 200 || m["path"] != "src/lib" {
			t.Errorf("%d %v", code, m)
		}
	})
	t.Run("percent encoded segment", func(t *testing.T) {
		code, m := f.get("/tree/main/src/" + url.PathEscape("b dir"))
		if code != 200 || m["path"] != "src/b dir" {
			t.Errorf("%d %v", code, m)
		}
	})
	t.Run("other branch and commit oid", func(t *testing.T) {
		_, m := f.get("/tree/dev")
		if m["commit"] != f.c1.OID || m["ref"] != "dev" {
			t.Errorf("dev: %v", m)
		}
		_, m = f.get("/tree/" + f.c1.OID)
		if m["commit"] != f.c1.OID || m["ref"] != f.c1.OID {
			t.Errorf("oid: %v", m)
		}
		_, m = f.get("/tree/" + f.c2.OID)
		if m["commit"] != f.c2.OID {
			t.Errorf("oid2: %v", m)
		}
	})
	t.Run("errors", func(t *testing.T) {
		tests := []struct {
			name, path string
			code       int
			msg        string
		}{
			{"file is not a directory", "/tree/main/README.md", 400, "not a directory"},
			{"unknown ref", "/tree/nope", 404, "ref not found"},
			{"abbreviated oid", "/tree/" + f.c1.OID[:7], 404, "ref not found"},
			{"unknown full oid", "/tree/" + strings.Repeat("a", 40), 404, "ref not found"},
			{"tree oid is not a commit", "/tree/" + f.c1.Tree, 404, "ref not found"},
			{"missing path", "/tree/main/nope", 404, "path not found"},
			{"through a file", "/tree/main/README.md/x", 404, "path not found"},
			{"git segment", "/tree/main/.git", 400, "invalid path"},
			{"path too long", "/tree/main/" + strings.Repeat("a", 256), 400, "invalid path"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				code, m := f.get(tc.path)
				if code != tc.code || m["error"] != tc.msg {
					t.Errorf("%s: %d %v, want %d %q", tc.path, code, m, tc.code, tc.msg)
				}
			})
		}
	})
	t.Run("repo not found and method", func(t *testing.T) {
		rec := do(f.s, http.MethodGet, "/repos/"+f.owner+"/zz/tree/main", "", nil)
		if rec.Code != 404 || errMsg(rec.Body) != "repository not found" {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
		rec = do(f.s, http.MethodPost, f.base+"/tree/main", "", nil)
		if rec.Code != 405 {
			t.Errorf("post: %d", rec.Code)
		}
	})
	t.Run("empty repo", func(t *testing.T) {
		for _, p := range []string{"/tree/main", "/tree/main/", "/tree/main/a"} {
			rec := do(f.s, http.MethodGet, f.emptyBaseURL+p, "", nil)
			if rec.Code != 404 || errMsg(rec.Body) != "ref not found" {
				t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
			}
		}
	})
}

func TestReadingBlob(t *testing.T) {
	f := newReadFixture(t)

	t.Run("nested file", func(t *testing.T) {
		code, m := f.get("/blob/main/src/lib/util.go")
		if code != 200 {
			t.Fatalf("%d %v", code, m)
		}
		if m["ref"] != "main" || m["commit"] != f.c2.OID || m["path"] != "src/lib/util.go" ||
			m["name"] != "util.go" || m["size"] != float64(12) || m["content"] != "package lib\n" || len(m["oid"].(string)) != 40 {
			t.Errorf("blob = %v", m)
		}
	})
	t.Run("root file and trailing slash", func(t *testing.T) {
		code, m := f.get("/blob/main/README.md")
		if code != 200 || m["content"] != "hello world\n" || m["name"] != "README.md" {
			t.Errorf("%d %v", code, m)
		}
		code, m = f.get("/blob/main/README.md/")
		if code != 200 || m["path"] != "README.md" {
			t.Errorf("trailing slash: %d %v", code, m)
		}
	})
	t.Run("empty file", func(t *testing.T) {
		code, m := f.get("/blob/main/empty.txt")
		if code != 200 || m["size"] != float64(0) || m["content"] != "" ||
			m["oid"] != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
			t.Errorf("%d %v", code, m)
		}
	})
	t.Run("ref resolution", func(t *testing.T) {
		_, m := f.get("/blob/dev/README.md")
		if m["content"] != "hello\n" || m["commit"] != f.c1.OID {
			t.Errorf("dev: %v", m)
		}
		_, m = f.get("/blob/" + f.c1.OID + "/README.md")
		if m["content"] != "hello\n" || m["ref"] != f.c1.OID {
			t.Errorf("old oid: %v", m)
		}
		_, m = f.get("/blob/" + f.c2.OID + "/README.md")
		if m["content"] != "hello world\n" {
			t.Errorf("new oid: %v", m)
		}
	})
	t.Run("percent encoded", func(t *testing.T) {
		code, m := f.get("/blob/main/src/" + url.PathEscape("b dir") + "/f.txt")
		if code != 200 || m["content"] != "spaced" {
			t.Errorf("%d %v", code, m)
		}
	})
	t.Run("errors", func(t *testing.T) {
		tests := []struct {
			name, path string
			code       int
			msg        string
		}{
			{"directory", "/blob/main/src", 400, "not a file"},
			{"directory trailing slash", "/blob/main/src/", 400, "not a file"},
			{"root", "/blob/main", 400, "not a file"},
			{"root slash", "/blob/main/", 400, "not a file"},
			{"unknown ref", "/blob/nope/README.md", 404, "ref not found"},
			{"abbreviated oid", "/blob/" + f.c1.OID[:7] + "/README.md", 404, "ref not found"},
			{"missing path", "/blob/main/nope.txt", 404, "path not found"},
			{"through a file", "/blob/main/README.md/x", 404, "path not found"},
			{"git segment", "/blob/main/src/.git/x", 400, "invalid path"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				code, m := f.get(tc.path)
				if code != tc.code || m["error"] != tc.msg {
					t.Errorf("%s: %d %v, want %d %q", tc.path, code, m, tc.code, tc.msg)
				}
			})
		}
	})
	t.Run("repo not found", func(t *testing.T) {
		rec := do(f.s, http.MethodGet, "/repos/"+f.owner+"/zz/blob/main/a", "", nil)
		if rec.Code != 404 || errMsg(rec.Body) != "repository not found" {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	})
	t.Run("empty repo", func(t *testing.T) {
		for _, p := range []string{"/blob/main", "/blob/main/a.txt"} {
			rec := do(f.s, http.MethodGet, f.emptyBaseURL+p, "", nil)
			if rec.Code != 404 || errMsg(rec.Body) != "ref not found" {
				t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
			}
		}
	})
}

// A ref is never resolved against another repository's objects.
func TestReadingOtherReposCommit(t *testing.T) {
	f := newReadFixture(t)
	g := newReadFixture(t)
	rec := do(g.s, http.MethodGet, g.base+"/tree/"+f.c1.OID, "", nil)
	if rec.Code != 404 || errMsg(rec.Body) != "ref not found" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// net/http's ServeMux cleans "..", "." and empty segments with a redirect
// before the handlers run, so they never reach the path validation.
func TestReadingUncleanPathRedirects(t *testing.T) {
	f := newReadFixture(t)
	for _, p := range []string{"/tree/main/src/../x", "/tree/main/./src", "/tree/main/src//lib", "/blob/main/../README.md"} {
		rec := do(f.s, http.MethodGet, f.base+p, "", nil)
		if rec.Code != http.StatusTemporaryRedirect {
			t.Errorf("%s: %d, want 307", p, rec.Code)
		}
	}
}
