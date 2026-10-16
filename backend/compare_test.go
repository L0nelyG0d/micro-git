package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type cmpResp struct {
	Base       string     `json:"base"`
	Head       string     `json:"head"`
	BaseCommit string     `json:"base_commit"`
	HeadCommit string     `json:"head_commit"`
	Files      []histFile `json:"files"`
}

func TestCompare(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"cmp"}`)
	base := "/repos/" + owner + "/cmp/compare"
	createRepo(t, s, c, `{"name":"void"}`)

	root := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "root",
		up("a.txt", "1\n2\n3\n"), up("gone.txt", "bye\n"), up("same.txt", "s\n")))
	if rec := do(s, http.MethodPost, "/repos/"+owner+"/cmp/branches", `{"name":"feat.x","from":"main"}`, c); rec.Code != http.StatusCreated {
		t.Fatalf("branch: %d %s", rec.Code, rec.Body)
	}
	head := mustCommit(t, s, owner, c, repo, commitJSON("feat.x", root.OID, "feature",
		up("a.txt", "1\nTWO\n3\n"), up("dir/new.txt", "n\n"), del("gone.txt")))

	get := func(q string, out any) (int, string) {
		t.Helper()
		rec := do(s, http.MethodGet, base+q, "", nil) // no cookie: read endpoint
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				t.Fatalf("decode %s: %v", rec.Body, err)
			}
			return rec.Code, ""
		}
		return rec.Code, errMsg(rec.Body)
	}
	query := func(b, h string) string { return "?base=" + url.QueryEscape(b) + "&head=" + url.QueryEscape(h) }

	t.Run("added modified deleted", func(t *testing.T) {
		var r cmpResp
		if code, msg := get(query("main", "feat.x"), &r); code != 200 {
			t.Fatalf("%d %s", code, msg)
		}
		if r.Base != "main" || r.Head != "feat.x" || r.BaseCommit != root.OID || r.HeadCommit != head.OID {
			t.Errorf("header: %+v", r)
		}
		var got []string
		for _, f := range r.Files {
			got = append(got, f.Path+":"+f.Status)
		}
		want := "a.txt:modified dir/new.txt:added gone.txt:deleted"
		if strings.Join(got, " ") != want {
			t.Fatalf("files %v, want %s", got, want)
		}
		a := r.Files[0]
		if a.Additions != 1 || a.Deletions != 1 || len(a.Hunks) != 1 {
			t.Errorf("a.txt: %+v", a)
		}
	})

	t.Run("matches commit detail", func(t *testing.T) {
		var r cmpResp
		var d histDetail
		get(query(root.OID, "feat.x"), &r)
		rec := do(s, http.MethodGet, "/repos/"+owner+"/cmp/commits/"+head.OID, "", nil)
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(r.Files)
		b, _ := json.Marshal(d.Files)
		if string(a) != string(b) {
			t.Errorf("compare %s\ncommit %s", a, b)
		}
	})

	t.Run("reversed is two tips", func(t *testing.T) {
		var r cmpResp
		get(query("feat.x", "main"), &r)
		if len(r.Files) != 3 || r.Files[1].Status != "deleted" || r.Files[2].Status != "added" {
			t.Errorf("%+v", r.Files)
		}
	})

	t.Run("oid as ref", func(t *testing.T) {
		var r cmpResp
		if code, msg := get(query(root.OID, head.OID), &r); code != 200 || len(r.Files) != 3 || r.Base != root.OID {
			t.Errorf("%d %q %+v", code, msg, r)
		}
	})

	t.Run("identical tips", func(t *testing.T) {
		rec := do(s, http.MethodGet, base+query("main", "main"), "", nil)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"files":[]`) {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	})

	t.Run("errors", func(t *testing.T) {
		cases := []struct {
			name, path string
			code       int
			msg        string
		}{
			{"missing both", base, 400, "base and head are required"},
			{"missing head", base + "?base=main", 400, "base and head are required"},
			{"empty base", base + "?base=&head=main", 400, "base and head are required"},
			{"unknown base", base + query("nope", "main"), 404, "ref not found"},
			{"unknown head", base + query("main", "nope"), 404, "ref not found"},
			{"unknown oid", base + query("main", strings.Repeat("a", 40)), 404, "ref not found"},
			{"empty repo", "/repos/" + owner + "/void/compare" + query("main", "main"), 404, "ref not found"},
			{"no repo", "/repos/" + owner + "/zzz/compare" + query("main", "main"), 404, "repository not found"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec := do(s, http.MethodGet, tc.path, "", nil)
				if rec.Code != tc.code || errMsg(rec.Body) != tc.msg {
					t.Errorf("got %d %q", rec.Code, errMsg(rec.Body))
				}
			})
		}
	})
}
