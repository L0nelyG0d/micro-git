package main

import (
	"net/http"

	"github.com/L0nelyG0d/micro-git/backend/linediff"
)

type compareResponse struct {
	Base       string              `json:"base"`
	Head       string              `json:"head"`
	BaseCommit string              `json:"base_commit"`
	HeadCommit string              `json:"head_commit"`
	Files      []linediff.FileDiff `json:"files"`
}

// compare handles GET /repos/{owner}/{repo}/compare?base=&head=: the diff of
// the base tip tree against the head tip tree (two tips, no merge base),
// using the same tree diff as the commit detail.
func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFromPath(w, r)
	if !ok {
		return
	}
	base, head := r.URL.Query().Get("base"), r.URL.Query().Get("head")
	if base == "" || head == "" {
		writeError(w, http.StatusBadRequest, "base and head are required")
		return
	}
	baseOID, ok := s.resolveRef(w, r, repo, base)
	if !ok {
		return
	}
	headOID, ok := s.resolveRef(w, r, repo, head)
	if !ok {
		return
	}
	store := s.Store.Objects(r.Context(), repo.ID)
	bc, err := loadCommit(store, baseOID)
	if err != nil {
		s.readError(w, r, repo, err)
		return
	}
	hc, err := loadCommit(store, headOID)
	if err != nil {
		s.readError(w, r, repo, err)
		return
	}
	files, err := linediff.Trees(store, bc.Tree, hc.Tree)
	if err != nil {
		s.readError(w, r, repo, err)
		return
	}
	if files == nil {
		files = []linediff.FileDiff{}
	}
	writeJSON(w, http.StatusOK, compareResponse{Base: base, Head: head, BaseCommit: baseOID, HeadCommit: headOID, Files: files})
}
