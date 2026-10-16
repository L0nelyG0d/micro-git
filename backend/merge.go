package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

type mergeRequest struct {
	Base string `json:"base"`
	Head string `json:"head"`
}

type mergeResponse struct {
	Branch      string `json:"branch"`
	OID         string `json:"oid"`
	PreviousOID string `json:"previous_oid"`
}

// beforeMergeSwap is a test hook run just before the compare-and-swap, so a
// test can move the base tip deterministically. Nil in production.
var beforeMergeSwap func()

// isAncestor reports whether ancestor is reachable from tip (or equal to it)
// through any parent. Each commit is visited once and the walk stops after
// limit commits, the number of commits in the repository.
func isAncestor(store treebuild.Store, ancestor, tip string, limit int) (bool, error) {
	seen := map[string]bool{}
	stack := []string{tip}
	for len(stack) > 0 && len(seen) <= limit {
		oid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if oid == ancestor {
			return true, nil
		}
		if seen[oid] {
			continue
		}
		seen[oid] = true
		c, err := loadCommit(store, oid)
		if err != nil {
			return false, err
		}
		stack = append(stack, c.Parents...)
	}
	return false, nil
}

func (rs *RepoStore) countCommits(ctx context.Context) (int, error) {
	var n int
	err := rs.db.QueryRow(ctx,
		`SELECT count(*) FROM objects WHERE repo_id = $1 AND type = 'commit'`, rs.repoID).Scan(&n)
	return n, err
}

// mergeBranches handles POST /repos/{owner}/{repo}/merge (behind requireAuth):
// fast-forward only. Order of checks: 401, 404 (repo, branches), 403, 400, 409.
func (s *Server) mergeBranches(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFromPath(w, r)
	if !ok {
		return
	}
	var req mergeRequest
	jsonOK := decodeJSONQuiet(w, r, &req)
	rs := NewRepoStore(s.DB, repo.ID)
	ctx := r.Context()
	var baseTip, headTip string
	if jsonOK && validBranchName(req.Base) && validBranchName(req.Head) {
		for _, p := range []struct {
			name string
			tip  *string
		}{{req.Base, &baseTip}, {req.Head, &headTip}} {
			tip, err := rs.GetRef(ctx, p.name)
			if errors.Is(err, ErrRefNotFound) {
				writeError(w, http.StatusNotFound, "branch not found")
				return
			}
			if err != nil {
				internalError(w, err)
				return
			}
			*p.tip = tip
		}
	}
	if u, _ := UserFromContext(ctx); u.ID != repo.OwnerID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if !jsonOK {
		badBody(w, r)
		return
	}
	if !validBranchName(req.Base) || !validBranchName(req.Head) {
		writeError(w, http.StatusBadRequest, "invalid branch name")
		return
	}
	if req.Base == req.Head {
		writeError(w, http.StatusBadRequest, "cannot merge a branch into itself")
		return
	}

	limit, err := rs.countCommits(ctx)
	if err != nil {
		internalError(w, err)
		return
	}
	store := rs.Objects(ctx)
	upToDate, err := isAncestor(store, headTip, baseTip, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	if upToDate {
		writeError(w, http.StatusBadRequest, "already up to date")
		return
	}
	ff, err := isAncestor(store, baseTip, headTip, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	if !ff {
		writeError(w, http.StatusConflict, "not a fast-forward")
		return
	}

	if beforeMergeSwap != nil {
		beforeMergeSwap()
	}
	err = rs.UpdateRef(ctx, req.Base, baseTip, headTip)
	if errors.Is(err, ErrRefMoved) {
		current, gerr := rs.GetRef(ctx, req.Base)
		if gerr != nil && !errors.Is(gerr, ErrRefNotFound) {
			internalError(w, gerr)
			return
		}
		writeTipMoved(w, current)
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mergeResponse{Branch: req.Base, OID: headTip, PreviousOID: baseTip})
}
