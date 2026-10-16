package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/linediff"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

// commitsPerPage is the fixed page size of the commit log.
const commitsPerPage = 50

type commitSummary struct {
	OID     string   `json:"oid"`
	Message string   `json:"message"`
	Author  string   `json:"author"`
	Time    string   `json:"time"`
	Parents []string `json:"parents"`
}

type commitListResponse struct {
	Ref     string          `json:"ref"`
	Page    int             `json:"page"`
	PerPage int             `json:"per_page"`
	HasMore bool            `json:"has_more"`
	Commits []commitSummary `json:"commits"`
}

type commitDetailResponse struct {
	commitSummary
	Tree  string              `json:"tree"`
	Files []linediff.FileDiff `json:"files"`
}

// loadCommit reads and parses a commit object. A missing object, or one that
// is not a commit, gives an error wrapping treebuild.ErrNotFound.
func loadCommit(store treebuild.Store, oid string) (gitobj.Commit, error) {
	t, body, err := store.GetObject(oid)
	if err != nil {
		return gitobj.Commit{}, err
	}
	if t != gitobj.TypeCommit {
		return gitobj.Commit{}, treebuild.ErrNotFound
	}
	return gitobj.ParseCommit(body)
}

func summarize(oid string, c gitobj.Commit) commitSummary {
	parents := c.Parents
	if parents == nil {
		parents = []string{}
	}
	return commitSummary{
		OID:     oid,
		Message: strings.TrimSuffix(c.Message, "\n"),
		Author:  c.Author.Name,
		Time:    time.Unix(c.Author.Unix, 0).UTC().Format(time.RFC3339),
		Parents: parents,
	}
}

// parsePage reads the optional ?page= value: absent means 1; anything but a
// positive decimal integer is invalid (including an empty value).
func parsePage(r *http.Request) (int, bool) {
	q := r.URL.Query()
	if !q.Has("page") {
		return 1, true
	}
	v := q.Get("page")
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// listCommits handles GET /repos/{owner}/{repo}/commits?ref=&page=.
func (s *Server) listCommits(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFromPath(w, r)
	if !ok {
		return
	}
	page, ok := parsePage(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid page")
		return
	}
	ref := repo.DefaultBranch
	if r.URL.Query().Has("ref") {
		ref = r.URL.Query().Get("ref")
	}
	tip, ok := s.resolveRef(w, r, repo, ref)
	if !ok {
		return
	}
	resp := commitListResponse{Ref: ref, Page: page, PerPage: commitsPerPage, Commits: []commitSummary{}}
	if page > 1<<40 { // far past any history; avoids overflow below
		writeJSON(w, http.StatusOK, resp)
		return
	}
	skip := (page - 1) * commitsPerPage
	// First-parent walk in one statement; one extra commit past the page
	// tells has_more.
	rows, err := NewRepoStore(s.DB, repo.ID).WalkFirstParents(r.Context(), tip, skip+commitsPerPage+1)
	if err != nil {
		internalError(w, err)
		return
	}
	for i, row := range rows {
		c, err := gitobj.ParseCommit(row.Data)
		if err != nil {
			internalError(w, err)
			return
		}
		switch {
		case i >= skip+commitsPerPage:
			resp.HasMore = true
		case i >= skip:
			resp.Commits = append(resp.Commits, summarize(row.OID, c))
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// getCommit handles GET /repos/{owner}/{repo}/commits/{oid}: the commit and
// its per-file diff against the first parent (the empty tree for a root).
func (s *Server) getCommit(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFromPath(w, r)
	if !ok {
		return
	}
	oid := r.PathValue("oid")
	if !gitobj.ValidOID(oid) {
		writeError(w, http.StatusBadRequest, "invalid commit id")
		return
	}
	store := NewRepoStore(s.DB, repo.ID).Objects(r.Context())
	c, err := loadCommit(store, oid)
	if errors.Is(err, treebuild.ErrNotFound) {
		writeError(w, http.StatusNotFound, "commit not found")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	oldTree := ""
	if len(c.Parents) > 0 {
		p, err := loadCommit(store, c.Parents[0])
		if err != nil {
			internalError(w, err)
			return
		}
		oldTree = p.Tree
	}
	files, err := linediff.Trees(store, oldTree, c.Tree)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, commitDetailResponse{commitSummary: summarize(oid, c), Tree: c.Tree, Files: files})
}
