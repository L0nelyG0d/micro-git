package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func mergeBody(base, head string) string {
	b, _ := json.Marshal(map[string]string{"base": base, "head": head})
	return string(b)
}

func mergeURL(owner string, r Repo) string { return "/repos/" + owner + "/" + r.Name + "/merge" }

// mergeFix: main at a; feature at b (child of a); side at cc (child of a).
type mergeFix struct {
	s        *Server
	owner    string
	c, other *http.Cookie
	repo     Repo
	a, b, cc string
	rs       *RepoStore
}

func newMergeFix(t *testing.T) mergeFix {
	t.Helper()
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	_, other := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"r"}`)
	a := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "a", up("a.txt", "a")))
	rs := NewRepoStore(rawDB(s), repo.ID)
	ctx := context.Background()
	for _, n := range []string{"feature", "side"} {
		if err := rs.CreateRef(ctx, n, a.OID); err != nil {
			t.Fatal(err)
		}
	}
	b := mustCommit(t, s, owner, c, repo, commitJSON("feature", a.OID, "b", up("b.txt", "b")))
	cm := mustCommit(t, s, owner, c, repo, commitJSON("side", a.OID, "c", up("c.txt", "c")))
	return mergeFix{s, owner, c, other, repo, a.OID, b.OID, cm.OID, rs}
}

func (f mergeFix) tip(t *testing.T, name string) string {
	t.Helper()
	oid, err := f.rs.GetRef(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func TestMergeFastForward(t *testing.T) {
	f := newMergeFix(t)
	n := countObjects(t, f.s, f.repo.ID)
	rec := do(f.s, "POST", mergeURL(f.owner, f.repo), mergeBody("main", "feature"), f.c)
	var out mergeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out.Branch != "main" || out.OID != f.b || out.PreviousOID != f.a {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if f.tip(t, "main") != f.b || f.tip(t, "feature") != f.b || f.tip(t, "side") != f.cc {
		t.Error("refs wrong after merge")
	}
	if countObjects(t, f.s, f.repo.ID) != n {
		t.Error("objects written by merge")
	}
	// now main equals feature: up to date
	rec = do(f.s, "POST", mergeURL(f.owner, f.repo), mergeBody("main", "feature"), f.c)
	if rec.Code != 400 || errMsg(rec.Body) != "already up to date" {
		t.Errorf("repeat: %d %s", rec.Code, rec.Body)
	}
}

func TestMergeErrors(t *testing.T) {
	f := newMergeFix(t)
	u := mergeURL(f.owner, f.repo)
	tests := []struct {
		name, body string
		cookie     *http.Cookie
		code       int
		msg        string
	}{
		{"no cookie", mergeBody("main", "feature"), nil, 401, ""},
		{"non-owner", mergeBody("main", "feature"), f.other, 403, "forbidden"},
		{"non-owner bad json", `nope`, f.other, 403, "forbidden"},
		{"unknown head", mergeBody("main", "zz"), f.c, 404, "branch not found"},
		{"unknown base", mergeBody("zz", "feature"), f.c, 404, "branch not found"},
		{"unknown head non-owner", mergeBody("main", "zz"), f.other, 404, "branch not found"},
		{"same branch", mergeBody("main", "main"), f.c, 400, "cannot merge a branch into itself"},
		{"bad json", `nope`, f.c, 400, "invalid JSON body"},
		{"unknown field", `{"base":"main","head":"feature","x":1}`, f.c, 400, "invalid JSON body"},
		{"invalid name", mergeBody("main", "a/b"), f.c, 400, "invalid branch name"},
		{"oid not accepted", mergeBody("main", f.b), f.c, 400, "invalid branch name"},
		{"diverged", mergeBody("feature", "side"), f.c, 409, "not a fast-forward"},
		{"head behind base", mergeBody("feature", "main"), f.c, 400, "already up to date"},
	}
	for _, tc := range tests {
		rec := do(f.s, "POST", u, tc.body, tc.cookie)
		if rec.Code != tc.code || tc.msg != "" && errMsg(rec.Body) != tc.msg {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body)
		}
	}
	if rec := do(f.s, "GET", u, "", f.c); rec.Code != 405 {
		t.Errorf("GET: %d", rec.Code)
	}
	if f.tip(t, "main") != f.a || f.tip(t, "feature") != f.b || f.tip(t, "side") != f.cc {
		t.Error("refs changed by rejected merges")
	}
}

func TestMergeEqualTipsUpToDate(t *testing.T) {
	f := newMergeFix(t)
	if err := f.rs.CreateRef(context.Background(), "twin", f.a); err != nil {
		t.Fatal(err)
	}
	rec := do(f.s, "POST", mergeURL(f.owner, f.repo), mergeBody("main", "twin"), f.c)
	if rec.Code != 400 || errMsg(rec.Body) != "already up to date" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestMergeTipMoved(t *testing.T) {
	f := newMergeFix(t)
	t.Cleanup(func() { beforeMergeSwap = nil })
	var moved string
	beforeMergeSwap = func() {
		m := mustCommit(t, f.s, f.owner, f.c, f.repo, commitJSON("main", f.a, "race", up("m.txt", "m")))
		moved = m.OID
	}
	rec := do(f.s, "POST", mergeURL(f.owner, f.repo), mergeBody("main", "feature"), f.c)
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 409 || out["error"] != "branch tip has moved" || out["current_tip"] != moved || moved == "" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if f.tip(t, "main") != moved {
		t.Error("main overwritten")
	}
}
