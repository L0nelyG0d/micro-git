package main

import (
	"errors"
	"net/http"
	"unicode/utf8"
)

type updateRepoRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	DefaultBranch *string `json:"default_branch"`
	Private       *bool   `json:"private"`
}

// updateRepo handles PATCH /repos/{owner}/{repo} (behind requireAuth). Any of
// name, description, default_branch and private may be sent, at least one is required.
// A rename moves no data (objects and refs are keyed by repo_id); the unique
// index on (owner_id, lower(name)) turns a concurrent duplicate into 409.
// Check order: 401, 404, 403, 400, 404 (unknown default branch), 409.
func (s *Server) updateRepo(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.ownedRepoFromPath(w, r)
	if !ok {
		return
	}
	var req updateRepoRequest
	if !decodeJSONQuiet(w, r, &req) || (req.Name == nil && req.Description == nil && req.DefaultBranch == nil && req.Private == nil) {
		badBody(w, r)
		return
	}
	// Only the supplied fields are written, so a racing PATCH to another
	// field is never overwritten with this request's stale copy of it.
	var patch RepoPatch
	if req.Name != nil {
		if !repoNameRE.MatchString(*req.Name) || *req.Name == "." || *req.Name == ".." {
			writeError(w, http.StatusBadRequest, "invalid repository name")
			return
		}
		patch.Name = req.Name
	}
	if req.Description != nil {
		if utf8.RuneCountInString(*req.Description) > maxDescription {
			writeError(w, http.StatusBadRequest, "description must be at most 500 characters")
			return
		}
		patch.Description = req.Description
	}
	patch.DefaultBranch = req.DefaultBranch // never compared with the loaded copy: a racing PATCH may have changed it
	patch.Private = req.Private
	updated, err := s.Store.UpdateRepo(r.Context(), repo.ID, patch)
	if errors.Is(err, ErrNotFound) {
		// No row: the repository is gone, or the branch is.
		if _, ferr := s.findRepo(r.Context(), repo.Owner, repo.Name); ferr == nil {
			writeError(w, http.StatusNotFound, "branch not found")
		} else if errors.Is(ferr, ErrNotFound) {
			writeError(w, http.StatusNotFound, "repository not found")
		} else {
			internalError(w, ferr)
		}
		return
	}
	if errors.Is(err, ErrConflict) {
		writeError(w, http.StatusConflict, "repository already exists")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
