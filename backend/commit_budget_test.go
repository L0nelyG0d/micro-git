package main

import (
	"fmt"
	"net/http"
	"testing"
)

// TestCommitStatementBudget keeps POST commit at a handful of round trips: a
// commit of 10 files in 3 folders must not cost more than 8 statements
// (authentication, repository lookup, tip+commit read, begin, one tree read
// per directory depth, one write statement, commit).
func TestCommitStatementBudget(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"budget"}`)
	tip := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "init",
		up("README.md", "r"), up("a/x.txt", "x"), up("b/x.txt", "x"), up("c/x.txt", "x"))).OID
	buf := captureTimingLog(t)
	var ch []string
	for i := 0; i < 10; i++ {
		ch = append(ch, up(fmt.Sprintf("%c/f%d.txt", 'a'+i%3, i), "new"))
	}
	rec := do(s, http.MethodPost, "/repos/"+owner+"/budget/commits", commitJSON("main", tip, "ten", ch...), c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if _, n := lastTiming(t, buf); n > 8 {
		t.Errorf("POST commit used %d statements, want <= 8; log %q", n, buf)
	}
}
