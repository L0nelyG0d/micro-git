package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// raceFixture is a repo with one commit on main; deleteNow removes it.
type raceFixture struct {
	s    *Server
	base string
	repo Repo
	c    commitResp
}

func newRaceFixture(t *testing.T) *raceFixture {
	t.Helper()
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	repo := createRepo(t, s, c, `{"name":"race"}`)
	cr := mustCommit(t, s, owner, c, repo, commitJSON("main", "", "first", up("README.md", "hello\n"), up("a/b.txt", "b")))
	return &raceFixture{s: s, base: "/repos/" + owner + "/race", repo: repo, c: cr}
}

func (f *raceFixture) deleteNow(t *testing.T) {
	t.Helper()
	deleteRepo(t, f.s, f.repo.ID)
}

// captureLog redirects the standard logger for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestReadsRacingRepoDeleteAre404(t *testing.T) {
	// "repo-loaded": gone before the ref lookup; "ref-resolved": gone before
	// any object is read (not reached by the commit detail, which has no ref).
	cases := []struct{ point, path string }{
		{"repo-loaded", "/blob/main/README.md"},
		{"repo-loaded", "/tree/main/a"},
		{"repo-loaded", "/commits?ref=main"},
		{"repo-loaded", "/commits/{oid}"},
		{"repo-loaded", "/compare?base=main&head=main"},
		{"ref-resolved", "/blob/main/README.md"},
		{"ref-resolved", "/tree/main/a"},
		{"ref-resolved", "/commits?ref=main"},
		{"ref-resolved", "/compare?base=main&head=main"},
	}
	for _, tc := range cases {
		t.Run(tc.point+tc.path, func(t *testing.T) {
			f := newRaceFixture(t)
			fired := false
			f.s.hook = func(got string) {
				if got == tc.point && !fired {
					fired = true
					f.deleteNow(t)
				}
			}
			p := strings.ReplaceAll(tc.path, "{oid}", f.c.OID)
			rec := do(f.s, http.MethodGet, f.base+p, "", nil)
			if !fired {
				t.Fatalf("hook %q never fired", tc.point)
			}
			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "repository not found") {
				t.Errorf("got %d %s, want 404 repository not found", rec.Code, rec.Body)
			}
		})
	}
}

func TestMissingObjectInExistingRepoStays500(t *testing.T) {
	f := newRaceFixture(t)
	dropBlobs(t, f.s, f.repo.ID)
	logs := captureLog(t)
	rec := do(f.s, http.MethodGet, f.base+"/blob/main/README.md", "", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d %s, want 500", rec.Code, rec.Body)
	}
	if !strings.Contains(logs.String(), "error:") {
		t.Errorf("corruption not logged: %q", logs.String())
	}
}

func TestClientAbortIsNotLoggedAsError(t *testing.T) {
	logs := captureLog(t)
	rec := httptest.NewRecorder()
	internalError(rec, fmt.Errorf("query: %w", context.Canceled))
	if logs.Len() != 0 {
		t.Errorf("client abort logged: %q", logs.String())
	}
	internalError(rec, errors.New("boom"))
	if !strings.Contains(logs.String(), "error: boom") {
		t.Errorf("real error not logged: %q", logs.String())
	}
}
