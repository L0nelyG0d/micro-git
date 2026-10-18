package main

import (
	"context"
	"errors"
	"net/http"
)

// Branch is the branch JSON shape (plan.md "Shared shapes").
type Branch struct {
	Name      string `json:"name"`
	OID       string `json:"oid"`
	IsDefault bool   `json:"is_default"`
}

type createBranchRequest struct {
	Name string `json:"name"`
	From string `json:"from"`
}

// listBranches handles GET /repos/{owner}/{repo}/branches.
func (s *Server) listBranches(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFromPath(w, r)
	if !ok {
		return
	}
	refs, err := NewRepoStore(s.DB, repo.ID).ListRefs(r.Context())
	if err != nil {
		internalError(w, err)
		return
	}
	branches := make([]Branch, len(refs))
	for i, ref := range refs {
		branches[i] = Branch{Name: ref.Name, OID: ref.OID, IsDefault: ref.Name == repo.DefaultBranch}
	}
	writeJSON(w, http.StatusOK, map[string][]Branch{"branches": branches})
}

// createBranch handles POST /repos/{owner}/{repo}/branches (behind
// requireAuth). Order of checks: 401, 404 (repo, from), 403, 400, 409.
func (s *Server) createBranch(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFromPath(w, r)
	if !ok {
		return
	}
	var req createBranchRequest
	jsonOK := decodeJSONQuiet(w, r, &req)
	rs := NewRepoStore(s.DB, repo.ID)
	// A missing source branch is a 404, which comes before the 403.
	var tip string
	if jsonOK {
		var err error
		tip, err = rs.GetRef(r.Context(), req.From)
		if errors.Is(err, ErrRefNotFound) {
			writeError(w, http.StatusNotFound, "branch not found")
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
	}
	if u, _ := UserFromContext(r.Context()); u.ID != repo.OwnerID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if !jsonOK {
		badBody(w, r)
		return
	}
	if !validBranchName(req.Name) {
		writeError(w, http.StatusBadRequest, "invalid branch name")
		return
	}
	err := rs.CreateRef(r.Context(), req.Name, tip)
	if errors.Is(err, ErrRefExists) {
		writeError(w, http.StatusConflict, "branch already exists")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, Branch{Name: req.Name, OID: tip, IsDefault: req.Name == repo.DefaultBranch})
}

// deleteBranch handles DELETE /repos/{owner}/{repo}/branches/{branch} (behind
// requireAuth). Order of checks: 401, 404 (repo, branch), 403, 400. Objects
// are never deleted.
func (s *Server) deleteBranch(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFromPath(w, r)
	if !ok {
		return
	}
	name := r.PathValue("branch")
	rs := NewRepoStore(s.DB, repo.ID)
	if _, err := rs.GetRef(r.Context(), name); errors.Is(err, ErrRefNotFound) {
		writeError(w, http.StatusNotFound, "branch not found")
		return
	} else if err != nil {
		internalError(w, err)
		return
	}
	if u, _ := UserFromContext(r.Context()); u.ID != repo.OwnerID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if name == repo.DefaultBranch {
		writeError(w, http.StatusBadRequest, "cannot delete the default branch")
		return
	}
	err := deleteNonDefaultRef(r.Context(), rs, name)
	if errors.Is(err, errDefaultBranch) {
		writeError(w, http.StatusBadRequest, "cannot delete the default branch")
		return
	}
	if errors.Is(err, ErrRefNotFound) {
		writeError(w, http.StatusNotFound, "branch not found")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errDefaultBranch = errors.New("default branch")

// deleteNonDefaultRef deletes a branch unless it is the repo's default, checked
// inside the DELETE so a concurrent set-default cannot slip in between.
func deleteNonDefaultRef(ctx context.Context, rs *RepoStore, name string) error {
	tag, err := rs.db.Exec(ctx,
		`DELETE FROM refs WHERE repo_id = $1 AND name = $2
		 AND NOT EXISTS (SELECT 1 FROM repos WHERE id = $1 AND default_branch = $2)`, rs.repoID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	if _, err := rs.GetRef(ctx, name); err != nil {
		return err // ErrRefNotFound, or a real error
	}
	return errDefaultBranch
}
