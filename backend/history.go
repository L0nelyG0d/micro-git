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

// defaultPerPage is the default page size of the commit log and the repo
// list; maxPerPage is the largest ?per_page= accepted.
const (
	defaultPerPage = 50
	maxPerPage     = 100
)

// maxHistoryCommits caps how deep ?page= can reach: page*per_page may not
// exceed it, which bounds the commits one request loads.
const maxHistoryCommits = 10000

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

// parsePerPage reads the optional ?per_page= value: absent means
// defaultPerPage; otherwise a decimal integer from 1 to maxPerPage.
func parsePerPage(r *http.Request) (int, bool) {
	q := r.URL.Query()
	if !q.Has("per_page") {
		return defaultPerPage, true
	}
	v := q.Get("per_page")
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxPerPage {
		return 0, false
	}
	return n, true
}

// listCommits handles GET /repos/{owner}/{repo}/commits?ref=&page=&per_page=.
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
	perPage, ok := parsePerPage(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid per_page")
		return
	}
	if page > maxHistoryCommits || page*perPage > maxHistoryCommits {
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
	resp := commitListResponse{Ref: ref, Page: page, PerPage: perPage, Commits: []commitSummary{}}
	skip := (page - 1) * perPage
	// First-parent walk in one statement; one extra commit past the page
	// tells has_more.
	rows, err := s.Store.WalkFirstParents(r.Context(), repo.ID, tip, skip+perPage+1)
	if err != nil {
		s.readError(w, r, repo, err)
		return
	}
	for i, row := range rows {
		c, err := gitobj.ParseCommit(row.Data)
		if err != nil {
			s.readError(w, r, repo, err)
			return
		}
		switch {
		case i >= skip+perPage:
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
	// commit, first parent and both root trees in one statement; anything it
	// misses is read on demand through the same store
	objs, found, err := s.Store.CommitBundle(r.Context(), repo.ID, oid)
	if err != nil {
		s.readError(w, r, repo, err)
		return
	}
	store := primed(s.Store.Objects(r.Context(), repo.ID), objs)
	var c gitobj.Commit
	if found {
		c, err = loadCommit(store, oid)
	} else {
		err = treebuild.ErrNotFound
	}
	if errors.Is(err, treebuild.ErrNotFound) {
		if s.repoGone(r, repo) {
			writeError(w, http.StatusNotFound, "repository not found")
			return
		}
		writeError(w, http.StatusNotFound, "commit not found")
		return
	}
	if err != nil {
		s.readError(w, r, repo, err)
		return
	}
	oldTree := ""
	if len(c.Parents) > 0 {
		p, err := loadCommit(store, c.Parents[0])
		if err != nil {
			s.readError(w, r, repo, err)
			return
		}
		oldTree = p.Tree
	}
	files, err := linediff.Trees(store, oldTree, c.Tree)
	if err != nil {
		s.readError(w, r, repo, err)
		return
	}
	immutableCache(repo.Private).set(w)
	writeJSON(w, http.StatusOK, commitDetailResponse{commitSummary: summarize(oid, c), Tree: c.Tree, Files: files})
}
