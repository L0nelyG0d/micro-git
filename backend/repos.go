package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"unicode/utf8"
)

var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

const maxDescription = 500

// Repo is the repository JSON shape (plan.md "Shared shapes") plus the owner id,
// which is not serialized.
type Repo struct {
	ID            int64  `json:"id"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Empty         bool   `json:"empty"`
	CreatedAt     string `json:"created_at"`
	OwnerID       int64  `json:"-"`
}

// findRepo looks a repository up by owner username and name, both
// case-insensitively. It returns ErrNotFound when either is unknown.
func (s *Server) findRepo(ctx context.Context, owner, name string) (Repo, error) {
	return s.Store.FindRepo(ctx, owner, name)
}

// repoFromPath loads the repository named by the {owner} and {repo} path
// values. On failure it has written 404 "repository not found" (or 500) and
// returns false. Shared by every /repos/{owner}/{repo}/... handler.
func (s *Server) repoFromPath(w http.ResponseWriter, r *http.Request) (Repo, bool) {
	repo, err := s.findRepo(r.Context(), r.PathValue("owner"), r.PathValue("repo"))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "repository not found")
		return Repo{}, false
	}
	if err != nil {
		internalError(w, err)
		return Repo{}, false
	}
	if !s.canView(w, r, repo) {
		return Repo{}, false
	}
	s.testHook("repo-loaded")
	return repo, true
}

// canView reports whether the requester may see the repository. A private
// repository is visible to its owner only; everyone else gets the same 404 as
// for a missing one, so its existence is not revealed. On false it has already
// written the response.
func (s *Server) canView(w http.ResponseWriter, r *http.Request, repo Repo) bool {
	if !repo.Private {
		return true
	}
	u, ok := UserFromContext(r.Context())
	if !ok {
		var err error
		if u, ok, err = s.authenticate(r); err != nil {
			internalError(w, err)
			return false
		}
	}
	if !ok || u.ID != repo.OwnerID {
		writeError(w, http.StatusNotFound, "repository not found")
		return false
	}
	return true
}

// ownedRepoFromPath is repoFromPath plus the owner check, for write endpoints
// behind requireAuth: 404 before 403. Check order is 401, 404, 403.
func (s *Server) ownedRepoFromPath(w http.ResponseWriter, r *http.Request) (Repo, bool) {
	repo, ok := s.repoFromPath(w, r)
	if !ok {
		return Repo{}, false
	}
	if u, _ := UserFromContext(r.Context()); u.ID != repo.OwnerID {
		writeError(w, http.StatusForbidden, "forbidden")
		return Repo{}, false
	}
	return repo, true
}

type repoListResponse struct {
	Repos   []Repo `json:"repos"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
	HasMore bool   `json:"has_more"`
}

// listRepos handles GET /repos?owner=&page=&per_page=.
func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	page, ok := parsePage(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid page")
		return
	}
	perPage, ok := parsePerPage(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid per_page")
		return
	}
	resp := repoListResponse{Repos: []Repo{}, Page: page, PerPage: perPage}
	if page > 1<<40 { // far past any list; avoids overflow below
		writeJSON(w, http.StatusOK, resp)
		return
	}
	// Private repositories are listed for their owner only.
	viewer, _, err := s.authenticate(r)
	if err != nil {
		internalError(w, err)
		return
	}
	var owner *string
	if r.URL.Query().Has("owner") {
		o := r.URL.Query().Get("owner")
		owner = &o
	}
	// One extra row past the page tells has_more.
	repos, err := s.Store.ListRepos(r.Context(), viewer.ID, owner, perPage+1, (page-1)*perPage)
	if err != nil {
		internalError(w, err)
		return
	}
	resp.Repos = repos
	if len(resp.Repos) > perPage {
		resp.Repos = resp.Repos[:perPage]
		resp.HasMore = true
	}
	writeJSON(w, http.StatusOK, resp)
}

type createRepoRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Private     bool   `json:"private"`
}

// createRepo handles POST /repos (behind requireAuth).
func (s *Server) createRepo(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	var req createRepoRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !repoNameRE.MatchString(req.Name) || req.Name == "." || req.Name == ".." {
		writeError(w, http.StatusBadRequest, "invalid repository name")
		return
	}
	if utf8.RuneCountInString(req.Description) > maxDescription {
		writeError(w, http.StatusBadRequest, "description must be at most 500 characters")
		return
	}
	repo, err := s.Store.CreateRepo(r.Context(), u.ID, req.Name, req.Description, req.Private, s.Now())
	if errors.Is(err, ErrConflict) {
		writeError(w, http.StatusConflict, "repository already exists")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}

// getRepo handles GET /repos/{owner}/{repo}.
func (s *Server) getRepo(w http.ResponseWriter, r *http.Request) {
	if repo, ok := s.repoFromPath(w, r); ok {
		writeJSON(w, http.StatusOK, repo)
	}
}

// deleteRepo handles DELETE /repos/{owner}/{repo} (behind requireAuth); the
// foreign keys cascade to the repository's objects and refs.
func (s *Server) deleteRepo(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.ownedRepoFromPath(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteRepo(r.Context(), repo.ID); err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
