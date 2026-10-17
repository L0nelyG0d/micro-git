package main

import (
	"errors"
	"net/http"
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

	// One statement answers both questions: head already in base's history
	// (up to date) and base in head's history (fast-forward possible).
	reach, err := rs.CommitReachability(ctx, headTip, baseTip)
	if err != nil {
		internalError(w, err)
		return
	}
	if reach.AFromB {
		writeError(w, http.StatusBadRequest, "already up to date")
		return
	}
	if !reach.BFromA {
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
