package main

import "net/http"

type renameRepoRequest struct {
	Name *string `json:"name"`
}

// renameRepo handles PATCH /repos/{owner}/{repo} (behind requireAuth). Only the
// name changes; objects and refs are keyed by repo_id, so no data moves. The
// unique index on (owner_id, lower(name)) turns a concurrent duplicate into 409.
// Check order: 401, 404, 403, 400, 409.
func (s *Server) renameRepo(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.ownedRepoFromPath(w, r)
	if !ok {
		return
	}
	var req renameRepoRequest
	if !decodeJSONQuiet(w, r, &req) || req.Name == nil {
		badBody(w, r)
		return
	}
	name := *req.Name
	if !repoNameRE.MatchString(name) || name == "." || name == ".." {
		writeError(w, http.StatusBadRequest, "invalid repository name")
		return
	}
	if name != repo.Name {
		_, err := s.DB.Exec(r.Context(), `UPDATE repos SET name = $1 WHERE id = $2`, name, repo.ID)
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
