package main

import (
	"net/http"
	"strings"
	"testing"
)

// A PATCH that loaded the repo, then lost a race to another PATCH, must not
// write its stale copy of the fields it did not send.
func TestPatchDoesNotRevertRacingRename(t *testing.T) {
	s := testServer(t)
	owner, c := newUserCookie(t, s)
	createRepo(t, s, c, `{"name":"before"}`)
	base := "/repos/" + owner + "/"
	fired := false
	s.hook = func(p string) {
		if p == "repo-loaded" && !fired {
			fired = true
			if rec := do(s, http.MethodPatch, base+"before", `{"name":"after"}`, c); rec.Code != 200 {
				t.Errorf("racing rename: %d %s", rec.Code, rec.Body)
			}
		}
	}
	rec := do(s, http.MethodPatch, base+"before", `{"private":true}`, c)
	if rec.Code != 200 {
		t.Fatalf("make private: %d %s", rec.Code, rec.Body)
	}
	s.hook = nil
	got, err := s.findRepo(t.Context(), owner, "after")
	if err != nil {
		t.Fatalf("renamed repo lost: %v", err)
	}
	if !got.Private {
		t.Errorf("private flag lost: %+v", got)
	}
}

// delete-branch that passed the "not the default" check must not delete a
// branch another request just made the default.
func TestDeleteBranchRacingSetDefault(t *testing.T) {
	f := newRaceFixture(t)
	owner := f.repo.Owner
	c := login(t, f.s, f.repo.Owner)
	if rec := do(f.s, http.MethodPost, f.base+"/branches", `{"name":"dev","from":"main"}`, c); rec.Code != 201 {
		t.Fatalf("create branch: %d %s", rec.Code, rec.Body)
	}
	fired := false
	f.s.hook = func(p string) {
		if p == "repo-loaded" && !fired {
			fired = true
			if rec := do(f.s, http.MethodPatch, "/repos/"+owner+"/race", `{"default_branch":"dev"}`, c); rec.Code != 200 {
				t.Errorf("set default: %d %s", rec.Code, rec.Body)
			}
		}
	}
	rec := do(f.s, http.MethodDelete, f.base+"/branches/dev", "", c)
	f.s.hook = nil
	wantError(t, rec, 400, "cannot delete the default branch")
	if _, err := NewRepoStore(rawDB(f.s), f.repo.ID).GetRef(t.Context(), "dev"); err != nil {
		t.Errorf("default branch was deleted: %v", err)
	}
}

func TestPatchDefaultBranchMissingAndRepoGone(t *testing.T) {
	f := newRaceFixture(t)
	c := login(t, f.s, f.repo.Owner)
	wantError(t, do(f.s, http.MethodPatch, f.base, `{"default_branch":"nope"}`, c), 404, "branch not found")
	fired := false
	f.s.hook = func(p string) {
		if p == "repo-loaded" && !fired {
			fired = true
			f.deleteNow(t)
		}
	}
	wantError(t, do(f.s, http.MethodPatch, f.base, `{"description":"x"}`, c), 404, "repository not found")
}

// A PATCH whose default_branch equals the default it loaded must still write
// it when a racing PATCH changed the default in between: answering 200 from
// the stale snapshot would report (and leave) the wrong default.
func TestPatchDefaultBranchNotSkippedByStaleSnapshot(t *testing.T) {
	f := newRaceFixture(t)
	c := login(t, f.s, f.repo.Owner)
	if rec := do(f.s, http.MethodPost, f.base+"/branches", `{"name":"dev","from":"main"}`, c); rec.Code != 201 {
		t.Fatalf("create branch: %d %s", rec.Code, rec.Body)
	}
	fired := false
	f.s.hook = func(p string) {
		if p == "repo-loaded" && !fired {
			fired = true
			if rec := do(f.s, http.MethodPatch, f.base, `{"default_branch":"dev"}`, c); rec.Code != 200 {
				t.Errorf("racing set default: %d %s", rec.Code, rec.Body)
			}
		}
	}
	rec := do(f.s, http.MethodPatch, f.base, `{"default_branch":"main"}`, c)
	f.s.hook = nil
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"default_branch":"main"`) {
		t.Errorf("response has wrong default: %s", rec.Body)
	}
	got, err := f.s.findRepo(t.Context(), f.repo.Owner, f.repo.Name)
	if err != nil || got.DefaultBranch != "main" {
		t.Errorf("stored default = %q (%v), want main", got.DefaultBranch, err)
	}
}
