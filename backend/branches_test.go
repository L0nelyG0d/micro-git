package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
)

func branchesURL(owner string, r Repo) string {
	return "/repos/" + owner + "/" + r.Name + "/branches"
}

func errMsg(rec interface{ String() string }) string {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal([]byte(rec.String()), &e)
	return e.Error
}

type branchList struct {
	Branches []Branch `json:"branches"`
}

func listBranchesT(t *testing.T, s *Server, owner string, r Repo) []Branch {
	t.Helper()
	rec := do(s, http.MethodGet, branchesURL(owner, r), "", nil)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var out branchList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Branches
}

func TestBranchesLifecycle(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"r"}`)

	// empty repo
	rec := do(s, http.MethodGet, branchesURL(owner, repo), "", nil)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"branches":[]}` {
		t.Fatalf("empty list: %d %s", rec.Code, rec.Body)
	}
	// any from on an empty repo is 404
	rec = do(s, http.MethodPost, branchesURL(owner, repo), `{"name":"x","from":"main"}`, c)
	if rec.Code != 404 || errMsg(rec.Body) != "branch not found" {
		t.Errorf("from on empty: %d %s", rec.Code, rec.Body)
	}

	first := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "m", up("a.txt", "a")))
	rec = do(s, http.MethodPost, branchesURL(owner, repo), `{"name":"zeta","from":"main"}`, c)
	var b Branch
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != 201 || b.Name != "zeta" || b.OID != first.OID || b.IsDefault {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if rec := do(s, http.MethodPost, branchesURL(owner, repo), `{"name":"alpha","from":"zeta"}`, c); rec.Code != 201 {
		t.Fatalf("create alpha: %d %s", rec.Code, rec.Body)
	}
	got := listBranchesT(t, s, owner, repo)
	if len(got) != 3 || got[0].Name != "alpha" || got[1].Name != "main" || !got[1].IsDefault || got[2].Name != "zeta" {
		t.Errorf("list = %+v", got)
	}

	// 409 and 404 from
	rec = do(s, http.MethodPost, branchesURL(owner, repo), `{"name":"zeta","from":"main"}`, c)
	if rec.Code != 409 || errMsg(rec.Body) != "branch already exists" {
		t.Errorf("dup: %d %s", rec.Code, rec.Body)
	}
	rec = do(s, http.MethodPost, branchesURL(owner, repo), `{"name":"n","from":"nope"}`, c)
	if rec.Code != 404 || errMsg(rec.Body) != "branch not found" {
		t.Errorf("missing from: %d %s", rec.Code, rec.Body)
	}

	// delete keeps objects
	n := countObjects(t, s, repo.ID)
	rec = do(s, http.MethodDelete, branchesURL(owner, repo)+"/zeta", "", c)
	if rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	for _, b := range listBranchesT(t, s, owner, repo) {
		if b.Name == "zeta" {
			t.Error("zeta still listed")
		}
	}
	if countObjects(t, s, repo.ID) != n {
		t.Error("objects changed by delete")
	}
	rec = do(s, http.MethodGet, "/repos/"+owner+"/r/blob/"+first.OID+"/a.txt", "", nil)
	if rec.Code != 200 {
		t.Errorf("commit unreadable after delete: %d %s", rec.Code, rec.Body)
	}
	rec = do(s, http.MethodDelete, branchesURL(owner, repo)+"/zeta", "", c)
	if rec.Code != 404 || errMsg(rec.Body) != "branch not found" {
		t.Errorf("delete missing: %d %s", rec.Code, rec.Body)
	}
	rec = do(s, http.MethodDelete, branchesURL(owner, repo)+"/main", "", c)
	if rec.Code != 400 || errMsg(rec.Body) != "cannot delete the default branch" {
		t.Errorf("delete default: %d %s", rec.Code, rec.Body)
	}
}

func TestBranchInvalidNames(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"r"}`)
	mustCommit(t, s, owner, c, repo, commitJSON("main", "", "m", up("a", "a")))
	for _, name := range []string{"", ".", "..", "a/b", "a b", "é", strings.Repeat("a", 101), strings.Repeat("a", 40)} {
		body, _ := json.Marshal(map[string]string{"name": name, "from": "main"})
		rec := do(s, http.MethodPost, branchesURL(owner, repo), string(body), c)
		if rec.Code != 400 || errMsg(rec.Body) != "invalid branch name" {
			t.Errorf("name %q: %d %s", name, rec.Code, rec.Body)
		}
	}
	rec := do(s, http.MethodPost, branchesURL(owner, repo), `{"name":"x","from":"main","extra":1}`, c)
	if rec.Code != 400 || errMsg(rec.Body) != "invalid JSON body" {
		t.Errorf("unknown field: %d %s", rec.Code, rec.Body)
	}
	// 100 chars is fine
	body, _ := json.Marshal(map[string]string{"name": strings.Repeat("a", 100), "from": "main"})
	if rec := do(s, http.MethodPost, branchesURL(owner, repo), string(body), c); rec.Code != 201 {
		t.Errorf("100 chars: %d %s", rec.Code, rec.Body)
	}
}

func TestBranchWriteAuth(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	_, other := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"r"}`)
	mustCommit(t, s, owner, c, repo, commitJSON("main", "", "m", up("a", "a")))
	do(s, http.MethodPost, branchesURL(owner, repo), `{"name":"f","from":"main"}`, c)
	u := branchesURL(owner, repo)

	tests := []struct {
		name, method, url, body string
		cookie                  *http.Cookie
		code                    int
	}{
		{"create no cookie", "POST", u, `{"name":"x","from":"main"}`, nil, 401},
		{"delete no cookie", "DELETE", u + "/f", "", nil, 401},
		{"create non-owner", "POST", u, `{"name":"x","from":"main"}`, other, 403},
		{"create non-owner bad name", "POST", u, `{"name":"a/b","from":"main"}`, other, 403},
		{"create non-owner bad json", "POST", u, `nope`, other, 403},
		{"create non-owner missing from", "POST", u, `{"name":"x","from":"zz"}`, other, 404},
		{"delete non-owner", "DELETE", u + "/f", "", other, 403},
		{"delete non-owner default", "DELETE", u + "/main", "", other, 403},
		{"delete non-owner missing", "DELETE", u + "/zz", "", other, 404},
		{"create bad json owner", "POST", u, `nope`, c, 400},
		{"create unknown repo", "POST", "/repos/" + owner + "/zz/branches", `{}`, c, 404},
		{"delete unknown repo", "DELETE", "/repos/" + owner + "/zz/branches/a", "", c, 404},
		{"list unknown repo", "GET", "/repos/" + owner + "/zz/branches", "", nil, 404},
	}
	for _, tc := range tests {
		rec := do(s, tc.method, tc.url, tc.body, tc.cookie)
		if rec.Code != tc.code {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body, tc.code)
		}
	}
	if len(listBranchesT(t, s, owner, repo)) != 2 {
		t.Error("non-owner writes changed branches")
	}
}

// TestStoreBranchOps drives the branch methods of the Store directly, on
// whichever store TEST_STORE selects.
func TestStoreBranchOps(t *testing.T) {
	s := testServer(t)
	st := s.Store
	ctx := t.Context()
	name := uniqueName(t)
	uid, err := st.CreateUser(ctx, name, "hash")
	if err != nil {
		t.Fatal(err)
	}
	cleanupUser(t, s, name)
	repo, err := st.CreateRepo(ctx, uid, "r", "", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.DeleteRepo(context.Background(), repo.ID) })
	var oids []string
	for _, x := range []string{"a", "b"} {
		oid, err := st.Objects(ctx, repo.ID).PutObject(gitobj.TypeBlob, []byte(x))
		if err != nil {
			t.Fatal(err)
		}
		oids = append(oids, oid)
	}
	a, b := oids[0], oids[1]

	if refs, err := st.ListRefs(ctx, repo.ID); err != nil || refs == nil || len(refs) != 0 {
		t.Errorf("ListRefs empty = %#v, %v; want [] and nil error", refs, err)
	}
	if err := st.DeleteRef(ctx, repo.ID, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteRef missing: %v, want ErrNotFound", err)
	}
	if err := st.DeleteNonDefaultRef(ctx, repo.ID, "main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteNonDefaultRef missing: %v, want ErrNotFound", err)
	}
	// Only gormStore checks the oid format up front (#91); the other stores
	// leave it to their foreign key.
	badOIDs := []string{}
	if _, ok := st.(*gormStore); ok {
		badOIDs = []string{"", "abc", strings.Repeat("a", 50), strings.Repeat("A", 40), strings.Repeat("g", 40)}
	}
	for _, bad := range badOIDs {
		if err := st.CreateRef(ctx, repo.ID, "bad", bad); !errors.Is(err, ErrInvalidOID) {
			t.Errorf("CreateRef(%q): %v, want ErrInvalidOID", bad, err)
		}
	}
	// Bytewise order: "Z" < "a" < "b" < U+00E9 < U+4E2D. A locale-aware
	// collation would put "é" next to "e" and "Z" after "a".
	names := []string{"é", "b", "中", "a", "Z", "main"}
	for _, n := range names {
		if err := st.CreateRef(ctx, repo.ID, n, a); err != nil {
			t.Fatalf("CreateRef(%q): %v", n, err)
		}
	}
	if err := st.CreateRef(ctx, repo.ID, "a", b); !errors.Is(err, ErrConflict) {
		t.Errorf("CreateRef duplicate: %v, want ErrConflict", err)
	}
	refs, err := st.ListRefs(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range refs {
		got = append(got, r.Name)
		if r.OID != a {
			t.Errorf("ref %q oid = %s, want %s (duplicate create must not overwrite)", r.Name, r.OID, a)
		}
	}
	want := []string{"Z", "a", "b", "main", "é", "中"}
	if !slices.Equal(got, want) {
		t.Errorf("ListRefs order = %q, want %q", got, want)
	}

	// The default branch is protected; the check is part of the delete.
	mainName := "main"
	if _, err := st.UpdateRepo(ctx, repo.ID, RepoPatch{DefaultBranch: &mainName}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteNonDefaultRef(ctx, repo.ID, "main"); !errors.Is(err, errDefaultBranch) {
		t.Errorf("delete default: %v, want errDefaultBranch", err)
	}
	if got, err := st.GetRef(ctx, repo.ID, "main"); err != nil || got != a {
		t.Errorf("default branch after refused delete = %q, %v", got, err)
	}
	// Move the default away; the old default is now deletable and the new
	// one is protected.
	if _, err := st.UpdateRepo(ctx, repo.ID, RepoPatch{DefaultBranch: &[]string{"b"}[0]}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteNonDefaultRef(ctx, repo.ID, "b"); !errors.Is(err, errDefaultBranch) {
		t.Errorf("delete new default: %v, want errDefaultBranch", err)
	}
	if err := st.DeleteNonDefaultRef(ctx, repo.ID, "main"); err != nil {
		t.Errorf("delete old default: %v", err)
	}
	if err := st.DeleteRef(ctx, repo.ID, "Z"); err != nil {
		t.Errorf("DeleteRef: %v", err)
	}
	if err := st.DeleteRef(ctx, repo.ID, "Z"); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteRef twice: %v, want ErrNotFound", err)
	}
}
