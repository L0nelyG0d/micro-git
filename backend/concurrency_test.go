package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const concurrentN = 8

// fireTogether runs n requests released at the same moment and returns the
// responses in goroutine order.
func fireTogether(n int, req func(i int) *httptest.ResponseRecorder) []*httptest.ResponseRecorder {
	recs := make([]*httptest.ResponseRecorder, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			recs[i] = req(i)
		}()
	}
	close(start)
	wg.Wait()
	return recs
}

// oneWinner checks that exactly one response has the status wantWin and every
// other one is a 409 with the error message lose. It returns the winner's index.
func oneWinner(t *testing.T, recs []*httptest.ResponseRecorder, wantWin int, lose string) int {
	t.Helper()
	winner := -1
	for i, rec := range recs {
		switch {
		case rec.Code == wantWin && winner < 0:
			winner = i
		case rec.Code == http.StatusConflict && errMsg(rec.Body) == lose:
		default:
			t.Errorf("request %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if winner < 0 {
		t.Fatalf("no request got %d", wantWin)
	}
	return winner
}

func checkCommitRace(t *testing.T, s *Server, owner string, c *http.Cookie, repo Repo, tip string) {
	t.Helper()
	recs := fireTogether(concurrentN, func(i int) *httptest.ResponseRecorder {
		body := commitJSON("main", tip, fmt.Sprintf("commit %d", i), up(fmt.Sprintf("f%d.txt", i), fmt.Sprint(i)))
		return do(s, http.MethodPost, commitsURL(owner, repo), body, c)
	})
	w := oneWinner(t, recs, http.StatusCreated, "branch tip has moved")
	var win commitResp
	if err := json.Unmarshal(recs[w].Body.Bytes(), &win); err != nil {
		t.Fatal(err)
	}
	for i, rec := range recs {
		if i == w {
			continue
		}
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out["current_tip"] != win.OID {
			t.Errorf("request %d: current_tip %q, want winner %s", i, out["current_tip"], win.OID)
		}
	}
	if got, _ := NewRepoStore(rawDB(s), repo.ID).GetRef(context.Background(), "main"); got != win.OID {
		t.Errorf("branch tip %s, want winner %s", got, win.OID)
	}
}

func TestConcurrentCommitsSameTip(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"cc-tip"}`)
	base := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "base", up("a.txt", "1")))
	checkCommitRace(t, s, owner, c, repo, base.OID)
}

func TestConcurrentFirstCommits(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"cc-first"}`)
	checkCommitRace(t, s, owner, c, repo, "")
	var refs int
	if err := rawDB(s).QueryRow(context.Background(), `SELECT count(*) FROM refs WHERE repo_id = $1`, repo.ID).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if refs != 1 {
		t.Errorf("%d refs, want exactly 1", refs)
	}
}

func TestConcurrentRenameSameName(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	names := []string{"cc-rename-a", "cc-rename-b"}
	for _, n := range names {
		createRepo(t, s, c, `{"name":"`+n+`"}`)
	}
	recs := fireTogether(2, func(i int) *httptest.ResponseRecorder {
		return do(s, http.MethodPatch, "/repos/"+owner+"/"+names[i], `{"name":"cc-renamed"}`, c)
	})
	oneWinner(t, recs, http.StatusOK, "repository already exists")
}

func TestConcurrentCreateSameBranch(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"cc-branch"}`)
	mustCommit(t, s, owner, c, repo, commitJSON("main", "", "base", up("a.txt", "1")))
	recs := fireTogether(concurrentN, func(int) *httptest.ResponseRecorder {
		return do(s, http.MethodPost, branchesURL(owner, repo), `{"name":"feature","from":"main"}`, c)
	})
	oneWinner(t, recs, http.StatusCreated, "branch already exists")
}

func TestConcurrentRepoDeletedMidCommitNonEmpty(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"cc-gone2"}`)
	tip := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "base", up("a/x.txt", "1"))).OID
	beforeCommitTx = func() {
		if _, err := rawDB(s).Exec(context.Background(), `DELETE FROM repos WHERE id = $1`, repo.ID); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { beforeCommitTx = nil })
	rec := do(s, http.MethodPost, commitsURL(owner, repo), commitJSON("main", tip, "next", up("a/y.txt", "2")), c)
	wantError(t, rec, http.StatusNotFound, "repository not found")
}

func TestConcurrentRepoDeletedMidCommit(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"cc-gone"}`)
	beforeCommitTx = func() {
		if _, err := rawDB(s).Exec(context.Background(), `DELETE FROM repos WHERE id = $1`, repo.ID); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { beforeCommitTx = nil })
	rec := do(s, http.MethodPost, commitsURL(owner, repo), commitJSON("main", "", "first", up("a.txt", "1")), c)
	wantError(t, rec, http.StatusNotFound, "repository not found")
}
