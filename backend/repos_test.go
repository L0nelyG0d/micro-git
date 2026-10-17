package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// login returns a session cookie for a registered user.
func login(t *testing.T, s *Server, name string) *http.Cookie {
	t.Helper()
	rec := do(s, http.MethodPost, "/auth/login", loginBody(name, "correct horse"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	return sessionCookie(t, rec)
}

// newUserCookie registers and logs in a fresh user.
func newUserCookie(t *testing.T, s *Server) (string, *http.Cookie) {
	t.Helper()
	name := uniqueName(t)
	uid := newUser(t, s, name, "correct horse")
	t.Cleanup(func() { // runs before the user cleanup (LIFO); repos cascade
		if _, err := s.DB.Exec(context.Background(), `DELETE FROM repos WHERE owner_id = $1`, uid); err != nil {
			t.Errorf("cleanup repos: %v", err)
		}
	})
	return name, login(t, s, name)
}

func createRepo(t *testing.T, s *Server, c *http.Cookie, body string) Repo {
	t.Helper()
	rec := do(s, http.MethodPost, "/repos", body, c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create repo: %d %s", rec.Code, rec.Body)
	}
	var r Repo
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func decodeRepos(t *testing.T, rec interface{ String() string }) []Repo {
	t.Helper()
	var out struct {
		Repos []Repo `json:"repos"`
	}
	if err := json.Unmarshal([]byte(rec.String()), &out); err != nil {
		t.Fatal(err)
	}
	return out.Repos
}

func TestCreateRepo(t *testing.T) {
	s := testServer(t)
	s.Now = func() time.Time { return t0 }
	owner, c := newUserCookie(t, s)

	rec := do(s, http.MethodPost, "/repos", `{"name":"Hello.world_1-x","description":"d"}`, c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type %q", ct)
	}
	var raw map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if len(raw) != 7 {
		t.Errorf("fields = %v", raw)
	}
	var r Repo
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	if r.Owner != owner || r.Name != "Hello.world_1-x" || r.Description != "d" || !r.Empty ||
		r.DefaultBranch != "main" || r.CreatedAt != "2026-01-02T15:04:05Z" || r.ID == 0 {
		t.Errorf("repo = %+v", r)
	}
	var n int
	_ = s.DB.QueryRow(context.Background(), `SELECT count(*) FROM refs WHERE repo_id = $1`, r.ID).Scan(&n)
	if n != 0 {
		t.Errorf("refs created: %d", n)
	}
	// description may be omitted
	if r2 := createRepo(t, s, c, `{"name":"two"}`); r2.Description != "" {
		t.Errorf("description = %q", r2.Description)
	}
}

func TestCreateRepoErrors(t *testing.T) {
	s := testServer(t)
	_, c := newUserCookie(t, s)
	createRepo(t, s, c, `{"name":"taken"}`)
	desc := func(n int) string { return `{"name":"ok","description":"` + strings.Repeat("é", n) + `"}` }

	tests := []struct {
		name, body string
		cookie     *http.Cookie
		status     int
		msg        string
	}{
		{"no cookie", `{"name":"x"}`, nil, 401, "authentication required"},
		{"no cookie beats bad body", `nope`, nil, 401, "authentication required"},
		{"empty name", `{"name":""}`, c, 400, "invalid repository name"},
		{"dot", `{"name":"."}`, c, 400, "invalid repository name"},
		{"dotdot", `{"name":".."}`, c, 400, "invalid repository name"},
		{"space", `{"name":"a b"}`, c, 400, "invalid repository name"},
		{"slash", `{"name":"a/b"}`, c, 400, "invalid repository name"},
		{"101 chars", `{"name":"` + strings.Repeat("a", 101) + `"}`, c, 400, "invalid repository name"},
		{"description 501", desc(501), c, 400, "description must be at most 500 characters"},
		{"duplicate", `{"name":"taken"}`, c, 409, "repository already exists"},
		{"duplicate other case", `{"name":"TAKEN"}`, c, 409, "repository already exists"},
		{"bad json", `{"name":1}`, c, 400, "invalid JSON body"},
		{"unknown field", `{"name":"x","private":true}`, c, 400, "invalid JSON body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantError(t, do(s, http.MethodPost, "/repos", tc.body, tc.cookie), tc.status, tc.msg)
		})
	}
	for _, n := range []int{500} { // boundaries that are accepted
		if rec := do(s, http.MethodPost, "/repos", desc(n), c); rec.Code != http.StatusCreated {
			t.Errorf("description %d chars: %d %s", n, rec.Code, rec.Body)
		}
	}
	if rec := do(s, http.MethodPost, "/repos", `{"name":"`+strings.Repeat("a", 100)+`"}`, c); rec.Code != http.StatusCreated {
		t.Errorf("100 char name: %d", rec.Code)
	}
}

func TestSameNameDifferentOwner(t *testing.T) {
	s := testServer(t)
	_, c1 := newUserCookie(t, s)
	_, c2 := newUserCookie(t, s)
	createRepo(t, s, c1, `{"name":"shared"}`)
	createRepo(t, s, c2, `{"name":"Shared"}`)
}

func TestListRepos(t *testing.T) {
	s := testServer(t)
	ownerA, cA := newUserCookie(t, s)
	ownerB, cB := newUserCookie(t, s)
	clock := t0
	s.Now = func() time.Time { return clock }
	a1 := createRepo(t, s, cA, `{"name":"first"}`)
	clock = clock.Add(time.Second)
	b1 := createRepo(t, s, cB, `{"name":"second"}`)
	clock = clock.Add(time.Second)
	a2 := createRepo(t, s, cA, `{"name":"third"}`)

	rec := do(s, http.MethodGet, "/repos?owner="+strings.ToUpper(ownerA), "", nil)
	got := decodeRepos(t, rec.Body)
	if rec.Code != 200 || len(got) != 2 || got[0].ID != a2.ID || got[1].ID != a1.ID {
		t.Errorf("owner filter: %d %s", rec.Code, rec.Body)
	}
	all := decodeRepos(t, do(s, http.MethodGet, "/repos", "", nil).Body)
	pos := map[int64]int{}
	for i, r := range all {
		pos[r.ID] = i
	}
	if !(pos[a2.ID] < pos[b1.ID] && pos[b1.ID] < pos[a1.ID]) {
		t.Errorf("not newest first: %v", pos)
	}
	_ = ownerB

	rec = do(s, http.MethodGet, "/repos?owner=nobody-here", "", nil)
	if rec.Code != 200 || rec.Body.String() != `{"repos":[],"page":1,"per_page":50,"has_more":false}` {
		t.Errorf("unknown owner: %d %s", rec.Code, rec.Body)
	}
	if rec := do(s, http.MethodGet, "/repos?owner=", "", nil); rec.Body.String() != `{"repos":[],"page":1,"per_page":50,"has_more":false}` {
		t.Errorf("empty owner: %s", rec.Body)
	}
}

func TestGetRepo(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	r := createRepo(t, s, c, `{"name":"MixedCase"}`)

	for _, path := range []string{"/repos/" + owner + "/MixedCase", "/repos/" + strings.ToUpper(owner) + "/mixedcase"} {
		rec := do(s, http.MethodGet, path, "", nil)
		var got Repo
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if rec.Code != 200 || got != r {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{"/repos/" + owner + "/missing", "/repos/nobody-here/MixedCase"} {
		wantError(t, do(s, http.MethodGet, path, "", nil), 404, "repository not found")
	}
	if rec := do(s, http.MethodPut, "/repos/"+owner+"/MixedCase", "", c); rec.Code != 405 || rec.Header().Get("Allow") != "DELETE, GET, PATCH" {
		t.Errorf("PUT: %d allow %q", rec.Code, rec.Header().Get("Allow"))
	}
	if rec := do(s, http.MethodDelete, "/repos", "", c); rec.Code != 405 {
		t.Errorf("DELETE /repos: %d", rec.Code)
	}
}

func TestEmptyFlag(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	r := createRepo(t, s, c, `{"name":"e"}`)
	rs := NewRepoStore(s.DB, r.ID)
	oid, err := rs.Objects(context.Background()).PutObject("blob", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}

	if err := rs.UpdateRef(context.Background(), "main", "", oid); err != nil {
		t.Fatal(err)
	}
	var got Repo
	_ = json.Unmarshal(do(s, http.MethodGet, "/repos/"+owner+"/e", "", nil).Body.Bytes(), &got)
	if got.Empty {
		t.Error("empty = true after a ref was inserted")
	}
	if l := decodeRepos(t, do(s, http.MethodGet, "/repos?owner="+owner, "", nil).Body); len(l) != 1 || l[0].Empty {
		t.Errorf("list empty flag: %+v", l)
	}
	if err := rs.DeleteRef(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(do(s, http.MethodGet, "/repos/"+owner+"/e", "", nil).Body.Bytes(), &got)
	if !got.Empty {
		t.Error("empty = false with no refs")
	}
}

func TestDeleteRepo(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	_, other := newUserCookie(t, s)
	r := createRepo(t, s, c, `{"name":"doomed"}`)
	rs := NewRepoStore(s.DB, r.ID)
	ctx := context.Background()
	oid, err := rs.Objects(ctx).PutObject("blob", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.CreateRef(ctx, "main", oid); err != nil {
		t.Fatal(err)
	}
	path := "/repos/" + owner + "/doomed"

	wantError(t, do(s, http.MethodDelete, path, "", nil), 401, "authentication required")
	wantError(t, do(s, http.MethodDelete, path, "", other), 403, "forbidden")
	// check order: 401, then 404, then 403
	missing := "/repos/" + owner + "/missing"
	wantError(t, do(s, http.MethodDelete, missing, "", nil), 401, "authentication required")
	wantError(t, do(s, http.MethodDelete, missing, "", other), 404, "repository not found")
	wantError(t, do(s, http.MethodDelete, "/repos/nobody-here/x", "", c), 404, "repository not found")

	rec := do(s, http.MethodDelete, path, "", c)
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	wantError(t, do(s, http.MethodGet, path, "", nil), 404, "repository not found")
	wantError(t, do(s, http.MethodDelete, path, "", c), 404, "repository not found")
	for _, table := range []string{"objects", "refs"} {
		var n int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE repo_id = $1`, r.ID).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s rows after delete = %d, err %v", table, n, err)
		}
	}
}

type repoPage struct {
	Repos   []Repo `json:"repos"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
	HasMore bool   `json:"has_more"`
}

func getRepoPage(t *testing.T, s *Server, q string) repoPage {
	t.Helper()
	rec := do(s, http.MethodGet, "/repos"+q, "", nil)
	if rec.Code != 200 {
		t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
	}
	var p repoPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListReposPagination(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	clock := t0
	s.Now = func() time.Time { clock = clock.Add(time.Second); return clock }
	var ids []int64 // newest first
	for _, n := range []string{"p1", "p2", "p3"} {
		ids = append([]int64{createRepo(t, s, c, `{"name":"`+n+`"}`).ID}, ids...)
	}
	base := "?owner=" + owner
	p1 := getRepoPage(t, s, base+"&per_page=2")
	p2 := getRepoPage(t, s, base+"&per_page=2&page=2")
	if len(p1.Repos) != 2 || !p1.HasMore || p1.Page != 1 || p1.PerPage != 2 {
		t.Errorf("page 1: %+v", p1)
	}
	if len(p2.Repos) != 1 || p2.HasMore || p2.Page != 2 {
		t.Errorf("page 2: %+v", p2)
	}
	got := []int64{p1.Repos[0].ID, p1.Repos[1].ID, p2.Repos[0].ID}
	for i := range ids {
		if got[i] != ids[i] {
			t.Errorf("order: got %v want %v", got, ids)
		}
	}
	past := getRepoPage(t, s, base+"&per_page=2&page=3")
	if past.Repos == nil || len(past.Repos) != 0 || past.HasMore || past.Page != 3 {
		t.Errorf("past the end: %+v", past)
	}
	rec := do(s, http.MethodGet, "/repos?owner="+owner+"&page=3", "", nil)
	if !strings.Contains(rec.Body.String(), `"repos":[]`) {
		t.Errorf("past the end body: %s", rec.Body)
	}
	for _, pg := range []string{"99999999999999", "1099511627777", "9223372036854775807"} {
		huge := getRepoPage(t, s, "?page="+pg)
		if huge.Repos == nil || len(huge.Repos) != 0 || huge.HasMore {
			t.Errorf("huge page %s: %+v", pg, huge)
		}
	}
	// Bounds.
	for _, pp := range []string{"1", "100"} {
		if p := getRepoPage(t, s, base+"&per_page="+pp); p.PerPage != mustAtoi(t, pp) {
			t.Errorf("per_page=%s: %+v", pp, p)
		}
	}
	for _, q := range []string{"page=0", "page=-1", "page=abc", "page=", "page=1.5"} {
		rec := do(s, http.MethodGet, "/repos?"+q, "", nil)
		if rec.Code != 400 || errMsg(rec.Body) != "invalid page" {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
	for _, q := range []string{"per_page=0", "per_page=101", "per_page=-1", "per_page=abc", "per_page=", "per_page=2.5"} {
		rec := do(s, http.MethodGet, "/repos?"+q, "", nil)
		if rec.Code != 400 || errMsg(rec.Body) != "invalid per_page" {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestListReposDefaultPageBoundary(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	createRepo(t, s, c, `{"name":"r0"}`)
	var ownerID int64
	if err := s.DB.QueryRow(t.Context(), `SELECT id FROM users WHERE username = $1`, owner).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	_, err := s.DB.Exec(t.Context(),
		`INSERT INTO repos (owner_id, name, created_at)
		 SELECT $1, 'r' || g, $2::timestamptz + g * interval '1 second' FROM generate_series(1, 49) g`,
		ownerID, t0)
	if err != nil {
		t.Fatal(err)
	}
	p := getRepoPage(t, s, "?owner="+owner)
	if len(p.Repos) != 50 || p.HasMore || p.PerPage != 50 {
		t.Fatalf("50 repos: len %d has_more %v per_page %d", len(p.Repos), p.HasMore, p.PerPage)
	}
	if _, err := s.DB.Exec(t.Context(),
		`INSERT INTO repos (owner_id, name, created_at) VALUES ($1, 'r50', $2)`, ownerID, t0); err != nil {
		t.Fatal(err)
	}
	p = getRepoPage(t, s, "?owner="+owner)
	if len(p.Repos) != 50 || !p.HasMore {
		t.Fatalf("51 repos: len %d has_more %v", len(p.Repos), p.HasMore)
	}
	if p2 := getRepoPage(t, s, "?owner="+owner+"&page=2"); len(p2.Repos) != 1 || p2.HasMore {
		t.Fatalf("page 2: %+v", p2)
	}
}
