package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRenameRepo(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	_, other := newUserCookie(t, s)
	r := createRepo(t, s, c, `{"name":"old","description":"keep me"}`)
	createRepo(t, s, c, `{"name":"Taken"}`)
	commit := mustCommit(t, s, owner, c, r, commitJSON("main", "", "first", up("a.txt", "hi\n")))
	base := "/repos/" + owner + "/"

	// check order: 401, 404, 403, 400, 409
	wantError(t, do(s, http.MethodPatch, base+"old", `{"name":"new"}`, nil), 401, "authentication required")
	wantError(t, do(s, http.MethodPatch, base+"nope", `{"name":"new"}`, c), 404, "repository not found")
	wantError(t, do(s, http.MethodPatch, "/repos/nobody-here/x", `{"name":"new"}`, c), 404, "repository not found")
	wantError(t, do(s, http.MethodPatch, base+"old", `{"name":"new"}`, other), 403, "forbidden")
	wantError(t, do(s, http.MethodPatch, base+"old", `{bad`, other), 403, "forbidden")

	for _, tc := range []struct{ name, body, msg string }{
		{"dot", `{"name":"."}`, "invalid repository name"},
		{"dotdot", `{"name":".."}`, "invalid repository name"},
		{"empty", `{"name":""}`, "invalid repository name"},
		{"space", `{"name":"a b"}`, "invalid repository name"},
		{"slash", `{"name":"a/b"}`, "invalid repository name"},
		{"too long", `{"name":"` + strings.Repeat("a", 101) + `"}`, "invalid repository name"},
		{"empty object", `{}`, "invalid JSON body"},
		{"null name", `{"name":null}`, "invalid JSON body"},
		{"unknown field", `{"name":"new","bogus":"x"}`, "invalid JSON body"},
		{"wrong type", `{"name":1}`, "invalid JSON body"},
		{"malformed", `{bad`, "invalid JSON body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantError(t, do(s, http.MethodPatch, base+"old", tc.body, c), 400, tc.msg)
		})
	}
	wantError(t, do(s, http.MethodPatch, base+"old", `{"name":"taken"}`, c), 409, "repository already exists")
	wantError(t, do(s, http.MethodPatch, base+"old", `{"name":"TAKEN"}`, c), 409, "repository already exists")

	rec := do(s, http.MethodPatch, base+"old", `{"name":"new.name_1-x"}`, c)
	if rec.Code != 200 {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	var got Repo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != r.ID || got.Name != "new.name_1-x" || got.Owner != owner || got.Description != "keep me" || got.Empty {
		t.Errorf("renamed = %+v (old %+v)", got, r)
	}

	// the new name and the nested routes resolve; the old name is gone
	for _, path := range []string{"", "/branches", "/tree/main", "/blob/main/a.txt", "/commits?ref=main", "/commits/" + commit.OID} {
		if rec := do(s, http.MethodGet, base+"new.name_1-x"+path, "", nil); rec.Code != 200 {
			t.Errorf("GET %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	wantError(t, do(s, http.MethodGet, base+"old", "", nil), 404, "repository not found")
	wantError(t, do(s, http.MethodGet, base+"old/branches", "", nil), 404, "repository not found")
	if n := countObjects(t, s, r.ID); n != 3 {
		t.Errorf("objects after rename = %d, want 3", n)
	}

	// the old name is free again
	createRepo(t, s, c, `{"name":"old"}`)
}

func TestRenameRepoCaseAndSame(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	r := createRepo(t, s, c, `{"name":"Hello"}`)
	base := "/repos/" + owner + "/"
	for _, name := range []string{"hello", "HELLO", "HELLO"} { // case-only changes, then the same name
		rec := do(s, http.MethodPatch, base+"Hello", `{"name":"`+name+`"}`, c)
		if rec.Code != 200 {
			t.Fatalf("rename to %s: %d %s", name, rec.Code, rec.Body)
		}
		var got Repo
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.ID != r.ID || got.Name != name {
			t.Errorf("after %s: %+v", name, got)
		}
	}
}

func TestUpdateRepoDescriptionAndDefaultBranch(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	_, other := newUserCookie(t, s)
	r := createRepo(t, s, c, `{"name":"proj","description":"old"}`)
	mustCommit(t, s, owner, c, r, commitJSON("main", "", "first", up("a.txt", "hi\n")))
	base := "/repos/" + owner + "/proj"
	if rec := do(s, http.MethodPost, base+"/branches", `{"name":"dev","from":"main"}`, c); rec.Code != 201 {
		t.Fatalf("create branch: %d %s", rec.Code, rec.Body)
	}

	wantError(t, do(s, http.MethodPatch, base, `{"description":"x"}`, other), 403, "forbidden")
	wantError(t, do(s, http.MethodPatch, base, `{}`, c), 400, "invalid JSON body")
	wantError(t, do(s, http.MethodPatch, base, `{"bogus":1}`, c), 400, "invalid JSON body")
	wantError(t, do(s, http.MethodPatch, base, `{"description":"`+strings.Repeat("a", 501)+`"}`, c), 400,
		"description must be at most 500 characters")
	wantError(t, do(s, http.MethodPatch, base, `{"default_branch":"nope"}`, c), 404, "branch not found")

	patch := func(body string) Repo {
		t.Helper()
		rec := do(s, http.MethodPatch, base, body, c)
		if rec.Code != 200 {
			t.Fatalf("PATCH %s: %d %s", body, rec.Code, rec.Body)
		}
		var got Repo
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := patch(`{"description":"new text"}`); got.Description != "new text" || got.Name != "proj" || got.DefaultBranch != "main" {
		t.Errorf("description only: %+v", got)
	}
	if got := patch(`{"description":""}`); got.Description != "" {
		t.Errorf("clearing the description: %+v", got)
	}
	if got := patch(`{"default_branch":"dev"}`); got.DefaultBranch != "dev" || got.Name != "proj" {
		t.Errorf("default branch: %+v", got)
	}
	if got := patch(`{"name":"proj2","description":"both","default_branch":"main"}`); got.Name != "proj2" || got.Description != "both" || got.DefaultBranch != "main" || got.ID != r.ID {
		t.Errorf("all three: %+v", got)
	}
}
