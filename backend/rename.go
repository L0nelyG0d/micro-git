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
}

// updateRepo handles PATCH /repos/{owner}/{repo} (behind requireAuth). Any of
// name, description and default_branch may be sent, at least one is required.
// A rename moves no data (objects and refs are keyed by repo_id); the unique
// index on (owner_id, lower(name)) turns a concurrent duplicate into 409.
// Check order: 401, 404, 403, 400, 404 (unknown default branch), 409.
func (s *Server) updateRepo(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.ownedRepoFromPath(w, r)
	if !ok {
		return
	}
	var req updateRepoRequest
	if !decodeJSONQuiet(w, r, &req) || (req.Name == nil && req.Description == nil && req.DefaultBranch == nil) {
		badBody(w, r)
		return
	}
	name := repo.Name
	if req.Name != nil {
		name = *req.Name
		if !repoNameRE.MatchString(name) || name == "." || name == ".." {
			writeError(w, http.StatusBadRequest, "invalid repository name")
			return
		}
	}
	description := repo.Description
	if req.Description != nil {
		description = *req.Description
		if utf8.RuneCountInString(description) > maxDescription {
			writeError(w, http.StatusBadRequest, "description must be at most 500 characters")
			return
		}
	}
	defaultBranch := repo.DefaultBranch
	if req.DefaultBranch != nil && *req.DefaultBranch != repo.DefaultBranch {
		defaultBranch = *req.DefaultBranch
		_, err := NewRepoStore(s.DB, repo.ID).GetRef(r.Context(), defaultBranch)
		if errors.Is(err, ErrRefNotFound) {
			writeError(w, http.StatusNotFound, "branch not found")
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
	}
	if name != repo.Name || description != repo.Description || defaultBranch != repo.DefaultBranch {
		_, err := s.DB.Exec(r.Context(),
			`UPDATE repos SET name = $1, description = $2, default_branch = $3 WHERE id = $4`,
			name, description, defaultBranch, repo.ID)
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "repository already exists")
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
	}
	updated, err := s.findRepo(r.Context(), repo.Owner, name)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
