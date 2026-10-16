package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
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
	Empty         bool   `json:"empty"`
	CreatedAt     string `json:"created_at"`
	OwnerID       int64  `json:"-"`
}

var errRepoNotFound = errors.New("repository not found")

const repoSelect = `SELECT r.id, r.owner_id, u.username, r.name, r.description, r.default_branch,
	r.created_at, NOT EXISTS (SELECT 1 FROM refs WHERE repo_id = r.id)
	FROM repos r JOIN users u ON u.id = r.owner_id`

func scanRepo(row pgx.Row) (Repo, error) {
	var r Repo
	var created time.Time
	err := row.Scan(&r.ID, &r.OwnerID, &r.Owner, &r.Name, &r.Description, &r.DefaultBranch, &created, &r.Empty)
	r.CreatedAt = created.UTC().Format(time.RFC3339)
	return r, err
}

// findRepo looks a repository up by owner username and name, both
// case-insensitively. It returns errRepoNotFound when either is unknown.
func (s *Server) findRepo(ctx context.Context, owner, name string) (Repo, error) {
	r, err := scanRepo(s.DB.QueryRow(ctx,
		repoSelect+` WHERE lower(u.username) = lower($1) AND lower(r.name) = lower($2)`, owner, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return Repo{}, errRepoNotFound
	}
	return r, err
}

// repoFromPath loads the repository named by the {owner} and {repo} path
// values. On failure it has written 404 "repository not found" (or 500) and
// returns false. Shared by every /repos/{owner}/{repo}/... handler.
func (s *Server) repoFromPath(w http.ResponseWriter, r *http.Request) (Repo, bool) {
	repo, err := s.findRepo(r.Context(), r.PathValue("owner"), r.PathValue("repo"))
	if errors.Is(err, errRepoNotFound) {
		writeError(w, http.StatusNotFound, "repository not found")
		return Repo{}, false
	}
	if err != nil {
		internalError(w, err)
		return Repo{}, false
	}
	s.testHook("repo-loaded")
	return repo, true
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

// listRepos handles GET /repos.
func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	q := repoSelect
	var args []any
	if r.URL.Query().Has("owner") {
		q += ` WHERE lower(u.username) = lower($1)`
		args = append(args, r.URL.Query().Get("owner"))
	}
	rows, err := s.DB.Query(r.Context(), q+` ORDER BY r.created_at DESC, r.id DESC`, args...)
	if err != nil {
		internalError(w, err)
		return
	}
	defer rows.Close()
	repos := []Repo{}
	for rows.Next() {
		repo, err := scanRepo(rows)
		if err != nil {
			internalError(w, err)
			return
		}
		repos = append(repos, repo)
	}
	if err := rows.Err(); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]Repo{"repos": repos})
}

type createRepoRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
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
	var id int64
	err := s.DB.QueryRow(r.Context(),
		`INSERT INTO repos (owner_id, name, description, created_at) VALUES ($1, $2, $3, $4) RETURNING id`,
		u.ID, req.Name, req.Description, s.Now().UTC()).Scan(&id)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "repository already exists")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	repo, err := s.findRepo(r.Context(), u.Username, req.Name)
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
	if _, err := s.DB.Exec(r.Context(), `DELETE FROM repos WHERE id = $1`, repo.ID); err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
