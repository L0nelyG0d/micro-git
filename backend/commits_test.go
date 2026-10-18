package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
)

func commitJSON(branch, tip, msg string, changes ...string) string {
	b, _ := json.Marshal(map[string]any{"branch": branch, "expected_tip": tip, "message": msg,
		"changes": rawList(changes)})
	return string(b)
}

func rawList(items []string) []json.RawMessage {
	out := make([]json.RawMessage, len(items))
	for i, it := range items {
		out[i] = json.RawMessage(it)
	}
	return out
}

func up(path, content string) string {
	b, _ := json.Marshal(map[string]any{"path": path, "content": content})
	return string(b)
}

func del(path string) string {
	b, _ := json.Marshal(map[string]any{"path": path, "delete": true})
	return string(b)
}

func commitsURL(owner string, r Repo) string {
	return "/repos/" + owner + "/" + r.Name + "/commits"
}

type commitResp struct {
	OID    string  `json:"oid"`
	Tree   string  `json:"tree"`
	Parent *string `json:"parent"`
	Branch string  `json:"branch"`
}

func mustCommit(t *testing.T, s *Server, owner string, c *http.Cookie, r Repo, body string) commitResp {
	t.Helper()
	rec := do(s, http.MethodPost, commitsURL(owner, r), body, c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body)
	}
	var out commitResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func countObjects(t *testing.T, s *Server, repoID int64) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM objects WHERE repo_id = $1`, repoID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCommitFirstAndSecond(t *testing.T) {
	s := testServer(t)
	s.Now = func() time.Time { return t0 }
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"r"}`)
	rs := NewRepoStore(s.DB, repo.ID)
	ctx := context.Background()

	first := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "  First  \n", up("a/b.txt", "hi\n")))
	if first.Parent != nil || first.Branch != "main" || !gitobj.ValidOID(first.OID) || !gitobj.ValidOID(first.Tree) {
		t.Errorf("first = %+v", first)
	}
	if tip, _ := rs.GetRef(ctx, "main"); tip != first.OID {
		t.Errorf("tip %s, want %s", tip, first.OID)
	}
	_, body, err := rs.Objects(ctx).GetObject(first.OID)
	if err != nil {
		t.Fatal(err)
	}
	sig := fmt.Sprintf("%s <%s@micro-git.local> %d +0000", owner, owner, t0.Unix())
	want := "tree " + first.Tree + "\nauthor " + sig + "\ncommitter " + sig + "\n\nFirst\n"
	if string(body) != want {
		t.Errorf("commit body\n%q\nwant\n%q", body, want)
	}

	second := mustCommit(t, s, owner, c, repo, commitJSON("main", first.OID, "Second", up("c.txt", "x"), del("a/b.txt")))
	if second.Parent == nil || *second.Parent != first.OID {
		t.Errorf("parent = %v", second.Parent)
	}
	if tip, _ := rs.GetRef(ctx, "main"); tip != second.OID {
		t.Errorf("tip %s, want %s", tip, second.OID)
	}
	// raw JSON has "parent": null for the first commit
	rec := do(s, http.MethodPost, commitsURL(owner, repo), commitJSON("main", second.OID, "Third", up("d", "")), c)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"parent":"`+second.OID+`"`) {
		t.Errorf("third: %d %s", rec.Code, rec.Body)
	}
}

func TestCommitFirstParentNull(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"r"}`)
	rec := do(s, http.MethodPost, commitsURL(owner, repo), commitJSON("main", "", "m", up("a", "b")), c)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"parent":null`) {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestCommitErrors(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	_, other := newUserCookie(t, s)
	empty := createRepo(t, s, c, `{"name":"empty"}`)
	full := createRepo(t, s, c, `{"name":"full"}`)
	base := mustCommit(t, s, owner, c, full, commitJSON("main", "", "base", up("keep.txt", "k")))
	stale := strings.Repeat("a", 40)
	many := make([]string, 51)
	for i := range many {
		many[i] = up(fmt.Sprintf("f%d", i), "x")
	}
	one := up("n.txt", "n")

	tests := []struct {
		name   string
		repo   Repo
		cookie *http.Cookie
		body   string
		status int
		msg    string
	}{
		{"no cookie", full, nil, commitJSON("main", base.OID, "m", one), 401, "authentication required"},
		{"not owner", full, other, commitJSON("main", base.OID, "m", one), 403, "forbidden"},
		{"missing branch beats 403", full, other, commitJSON("nope", base.OID, "m", one), 404, "branch not found"},
		{"bad json beats nothing but 403", full, other, `{`, 403, "forbidden"},
		{"403 beats 400", full, other, commitJSON("main", base.OID, "", one), 403, "forbidden"},
		{"missing branch", full, c, commitJSON("nope", "", "m", one), 404, "branch not found"},
		{"first commit other branch", empty, c, commitJSON("dev", "", "m", one), 400, "first commit must target the default branch"},
		{"bad json", full, c, `{`, 400, "invalid JSON body"},
		{"unknown field", full, c, `{"branch":"main","x":1}`, 400, "invalid JSON body"},
		{"wrong type", full, c, `{"branch":1}`, 400, "invalid JSON body"},
		{"invalid branch", full, c, commitJSON("a/b", base.OID, "m", one), 400, "invalid branch name"},
		{"oid-like branch", full, c, commitJSON(stale, base.OID, "m", one), 400, "invalid branch name"},
		{"dotdot branch", full, c, commitJSON("..", base.OID, "m", one), 400, "invalid branch name"},
		{"empty branch", full, c, commitJSON("", base.OID, "m", one), 400, "invalid branch name"},
		{"bad tip", full, c, commitJSON("main", "xyz", "m", one), 400, "invalid expected_tip"},
		{"upper tip", full, c, commitJSON("main", strings.Repeat("A", 40), "m", one), 400, "invalid expected_tip"},
		{"blank message", full, c, commitJSON("main", base.OID, " \n\t ", one), 400, "message must be 1-500 characters"},
		{"long message", full, c, commitJSON("main", base.OID, strings.Repeat("é", 501), one), 400, "message must be 1-500 characters"},
		{"zero changes", full, c, commitJSON("main", base.OID, "m"), 400, "changes must contain 1-50 entries"},
		{"51 changes", full, c, commitJSON("main", base.OID, "m", many...), 400, "changes must contain 1-50 entries"},
		{"invalid path", full, c, commitJSON("main", base.OID, "m", up("../x", "")), 400, "invalid path: ../x"},
		{"duplicate", full, c, commitJSON("main", base.OID, "m", one, up("n.txt", "z")), 400, "duplicate path: n.txt"},
		{"neither", full, c, commitJSON("main", base.OID, "m", `{"path":"q"}`), 400, "change must have content or delete: q"},
		{"too large", full, c, commitJSON("main", base.OID, "m", up("big", strings.Repeat("x", 1<<20+1))), 400, "file too large: big"},
		{"not utf8", full, c, commitJSON("main", base.OID, "m", up("nul", "a\x00b")), 400, "file is not valid UTF-8 text: nul"},
		{"conflict", full, c, commitJSON("main", base.OID, "m", up("keep.txt/x", "")), 400, "path conflicts with existing file or directory: keep.txt/x"},
		{"no changes", full, c, commitJSON("main", base.OID, "m", up("keep.txt", "k"), del("zzz")), 400, "no changes"},
		{"400 beats 409", full, c, commitJSON("main", stale, "", one), 400, "message must be 1-500 characters"},
		{"no changes beats 409", full, c, commitJSON("main", stale, "m", up("keep.txt", "k")), 400, "no changes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := countObjects(t, s, tc.repo.ID)
			rec := do(s, http.MethodPost, commitsURL(owner, tc.repo), tc.body, tc.cookie)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			var out map[string]string
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			if out["error"] != tc.msg {
				t.Errorf("error %q, want %q", out["error"], tc.msg)
			}
			if after := countObjects(t, s, tc.repo.ID); after != before {
				t.Errorf("objects %d -> %d", before, after)
			}
			if tip, _ := NewRepoStore(s.DB, full.ID).GetRef(context.Background(), "main"); tip != base.OID {
				t.Errorf("tip moved to %s", tip)
			}
		})
	}
	if rs := NewRepoStore(s.DB, empty.ID); true {
		if refs, _ := rs.ListRefs(context.Background()); len(refs) != 0 {
			t.Errorf("empty repo got refs %v", refs)
		}
	}
}

func TestCommitTipConflicts(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"r"}`)
	one := up("a", "1")
	stale := strings.Repeat("b", 40)

	check := func(name, body, wantTip string) {
		t.Helper()
		rec := do(s, http.MethodPost, commitsURL(owner, repo), body, c)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		tip, has := out["current_tip"]
		if rec.Code != 409 || out["error"] != "branch tip has moved" || !has || tip != wantTip {
			t.Errorf("%s: %d %s, want current_tip %q", name, rec.Code, rec.Body, wantTip)
		}
	}
	check("non-empty tip on empty repo", commitJSON("main", stale, "m", one), "")
	first := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "m", one))
	check("empty tip on existing branch", commitJSON("main", "", "m", up("a", "2")), first.OID)
	check("stale tip", commitJSON("main", stale, "m", up("a", "2")), first.OID)

	// another branch is untouched by commits on main
	if err := NewRepoStore(s.DB, repo.ID).CreateRef(context.Background(), "dev", first.OID); err != nil {
		t.Fatal(err)
	}
	second := mustCommit(t, s, owner, c, repo, commitJSON("main", first.OID, "m2", up("a", "2")))
	if tip, _ := NewRepoStore(s.DB, repo.ID).GetRef(context.Background(), "dev"); tip != first.OID {
		t.Errorf("dev moved to %s", tip)
	}
	// non-empty tip on a branch that does not exist is a 404 on a non-empty repo
	rec := do(s, http.MethodPost, commitsURL(owner, repo), commitJSON("new", second.OID, "m", one), c)
	if rec.Code != 404 {
		t.Errorf("new branch: %d %s", rec.Code, rec.Body)
	}
}

func TestCommitFiftyChanges(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"r"}`)
	items := make([]string, 50)
	for i := range items {
		items[i] = up(fmt.Sprintf("f%d", i), "x")
	}
	mustCommit(t, s, owner, c, repo, commitJSON("main", "", "fifty", items...))
}

func TestCommitTooLarge(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"r"}`)
	body := `{"message":"` + strings.Repeat("x", 64<<20+1) + `"}`
	rec := do(s, http.MethodPost, commitsURL(owner, repo), body, c)
	if rec.Code != 413 || !strings.Contains(rec.Body.String(), `"request too large"`) {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestCommitMissingRepoAndMethod(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	rec := do(s, http.MethodPost, "/repos/"+owner+"/nope/commits", commitJSON("main", "", "m", up("a", "")), c)
	if rec.Code != 404 {
		t.Errorf("missing repo: %d", rec.Code)
	}
	if rec := do(s, http.MethodPut, "/repos/"+owner+"/nope/commits", "", c); rec.Code != 405 {
		t.Errorf("PUT: %d", rec.Code)
	}
}

// race runs two commits with the same expected_tip at once; exactly one wins.
func race(t *testing.T, s *Server, owner string, c *http.Cookie, repo Repo, tip string) {
	t.Helper()
	bodies := []string{
		commitJSON("main", tip, "left", up("l.txt", "left")),
		commitJSON("main", tip, "right", up("r.txt", "right")),
	}
	codes := make([]int, 2)
	oids := make([]string, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := do(s, http.MethodPost, commitsURL(owner, repo), bodies[i], c)
			codes[i] = rec.Code
			var out commitResp
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			oids[i] = out.OID
		}()
	}
	close(start)
	wg.Wait()
	n201, n409 := 0, 0
	winner := ""
	for i, code := range codes {
		switch code {
		case 201:
			n201++
			winner = oids[i]
		case 409:
			n409++
		}
	}
	if n201 != 1 || n409 != 1 {
		t.Fatalf("codes = %v, want one 201 and one 409", codes)
	}
	if got, _ := NewRepoStore(s.DB, repo.ID).GetRef(context.Background(), "main"); got != winner {
		t.Errorf("tip %s, want winner %s", got, winner)
	}
}

func TestCommitRace(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	t.Run("first commit", func(t *testing.T) {
		repo := createRepo(t, s, c, `{"name":"first"}`)
		race(t, s, owner, c, repo, "")
	})
	t.Run("existing branch", func(t *testing.T) {
		repo := createRepo(t, s, c, `{"name":"later"}`)
		base := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "base", up("a", "1")))
		race(t, s, owner, c, repo, base.OID)
	})
}

// countingReader counts the bytes the server actually pulls from the body.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// paddedCommit is valid commit JSON followed by whitespace up to size bytes.
func paddedCommit(branch, tip string, size int) string {
	b := commitJSON(branch, tip, "m", up("pad.txt", "p"))
	return b + strings.Repeat(" ", size-len(b))
}

func TestCommitBodyLimitByRole(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	_, other := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"lim"}`)
	base := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "base", up("keep.txt", "k")))
	priv := createRepo(t, s, c, `{"name":"hidden","private":true}`)

	post := func(cookie *http.Cookie, r Repo, body string) (*httptest.ResponseRecorder, int64) {
		cr := &countingReader{r: strings.NewReader(body)}
		req := httptest.NewRequest(http.MethodPost, commitsURL(owner, r), cr)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec, cr.n
	}

	tests := []struct {
		name    string
		repo    Repo
		cookie  *http.Cookie
		body    string
		status  int
		msg     string
		maxRead int64 // 0: not checked
	}{
		{"non-owner 10 MiB", repo, other, paddedCommit("main", base.OID, 10<<20), 413, "request too large", maxJSONBody + 1},
		{"non-owner small valid", repo, other, commitJSON("main", base.OID, "m", up("n", "n")), 403, "forbidden", 0},
		{"non-owner missing branch", repo, other, commitJSON("nope", base.OID, "m", up("n", "n")), 404, "branch not found", 0},
		{"anonymous 10 MiB", repo, nil, paddedCommit("main", base.OID, 10<<20), 401, "authentication required", 0},
		{"private non-owner 10 MiB", priv, other, paddedCommit("main", "", 10<<20), 404, "repository not found", maxJSONBody + 1},
		{"owner 2 MiB reaches validation", repo, c, paddedCommit("main", "xyz", 2<<20), 400, "invalid expected_tip", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, n := post(tt.cookie, tt.repo, tt.body)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), `"`+tt.msg+`"`) {
				t.Errorf("got %d %s, want %d %q", rec.Code, rec.Body, tt.status, tt.msg)
			}
			if tt.maxRead > 0 && n > tt.maxRead {
				t.Errorf("server read %d bytes, want at most %d", n, tt.maxRead)
			}
		})
	}
}
