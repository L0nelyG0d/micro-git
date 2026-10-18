package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

const (
	maxCommitBody  = 64 << 20
	maxChanges     = 50
	maxMessageLen  = 500
	maxBranchLen   = 100
	commitEmailTLD = "micro-git.local"
)

// beforeCommitTx is a test hook run after the repository lookup and before
// the transaction opens, so a test can delete the repository deterministically.
// Nil in production.
var beforeCommitTx func()

var branchNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validBranchName: [A-Za-z0-9._-]+, not "." or "..", at most 100 characters,
// and not shaped like an oid, so a ref is never ambiguous.
func validBranchName(n string) bool {
	return branchNameRE.MatchString(n) && n != "." && n != ".." &&
		len(n) <= maxBranchLen && !gitobj.ValidOID(n)
}

type changeRequest struct {
	Path    string  `json:"path"`
	Content *string `json:"content"`
	Delete  bool    `json:"delete"`
}

type commitRequest struct {
	Branch      string          `json:"branch"`
	ExpectedTip string          `json:"expected_tip"`
	Message     string          `json:"message"`
	Changes     []changeRequest `json:"changes"`
}

type commitResponse struct {
	OID    string  `json:"oid"`
	Tree   string  `json:"tree"`
	Parent *string `json:"parent"`
	Branch string  `json:"branch"`
}

// tipMovedError is returned from the commit transaction when the branch tip
// is no longer the one the client expected.
type tipMovedError struct{ current string }

func (e *tipMovedError) Error() string { return "branch tip has moved" }

func writeTipMoved(w http.ResponseWriter, current string) {
	writeJSON(w, http.StatusConflict, map[string]string{
		"error": "branch tip has moved", "current_tip": current,
	})
}

// createCommit handles POST /repos/{owner}/{repo}/commits (behind requireAuth).
// Order of checks: 401, 404 (repo, branch), 413 (non-owner, body over 1 MiB),
// 403, 400, 409. Only the owner may send up to maxCommitBody; anyone else is
// cut off at maxJSONBody before the server buffers more.
func (s *Server) createCommit(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFromPath(w, r)
	if !ok {
		return
	}
	limit := int64(maxJSONBody)
	if u, _ := UserFromContext(r.Context()); u.ID == repo.OwnerID {
		limit = maxCommitBody
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeError(w, http.StatusRequestEntityTooLarge, "request too large")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var req commitRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	jsonOK := dec.Decode(&req) == nil && dec.Decode(&struct{}{}) == io.EOF

	// 404 for a missing branch comes before 403, so it needs the body. The
	// same statement reads the tip and its commit, which the transaction below
	// reuses: a tip that moves in between is caught by the compare-and-swap.
	var tip string
	var tipBody []byte
	if jsonOK && validBranchName(req.Branch) && !repo.Empty {
		var err error
		if tip, tipBody, err = s.Store.GetRefCommit(r.Context(), repo.ID, req.Branch); errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "branch not found")
			return
		} else if err != nil {
			internalError(w, err)
			return
		}
	}
	if u, _ := UserFromContext(r.Context()); u.ID != repo.OwnerID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if !jsonOK {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !validBranchName(req.Branch) {
		writeError(w, http.StatusBadRequest, "invalid branch name")
		return
	}
	if req.ExpectedTip != "" && !gitobj.ValidOID(req.ExpectedTip) {
		writeError(w, http.StatusBadRequest, "invalid expected_tip")
		return
	}
	msg := strings.TrimSpace(req.Message)
	if n := utf8.RuneCountInString(msg); n < 1 || n > maxMessageLen {
		writeError(w, http.StatusBadRequest, "message must be 1-500 characters")
		return
	}
	if len(req.Changes) < 1 || len(req.Changes) > maxChanges {
		writeError(w, http.StatusBadRequest, "changes must contain 1-50 entries")
		return
	}
	if repo.Empty && req.Branch != repo.DefaultBranch {
		writeError(w, http.StatusBadRequest, "first commit must target the default branch")
		return
	}
	changes := make([]treebuild.Change, len(req.Changes))
	for i, c := range req.Changes {
		changes[i] = treebuild.Change{Path: c.Path, Content: c.Content, Delete: c.Delete}
	}

	u, _ := UserFromContext(r.Context())
	ctx := r.Context()
	if beforeCommitTx != nil {
		beforeCommitTx()
	}
	// A repository deleted since the lookup shows up as a foreign-key violation
	// on the objects/refs inserts: answer 404 instead of 500.
	fail := func(err error) {
		// The tip was read before the transaction, so a repository deleted
		// since then shows up as missing trees, not only as a foreign key.
		if errors.Is(err, ErrForeignKey) ||
			errors.Is(err, treebuild.ErrNotFound) && s.repoGone(r, repo) {
			writeError(w, http.StatusNotFound, "repository not found")
			return
		}
		internalError(w, err)
	}
	// One transaction: objects and the ref move commit together or not at all.
	var oid, tree string
	err = s.Store.InTx(ctx, func(ts Store) error {
		objs := ts.Objects(ctx, repo.ID)
		root := ""
		if tip != "" {
			pc, err := gitobj.ParseCommit(tipBody)
			if err != nil {
				return err
			}
			root = pc.Tree
		}
		var newObjs []treebuild.Object
		var err error
		tree, newObjs, err = treebuild.Build(objs, root, changes)
		if err != nil {
			return err
		}
		if req.ExpectedTip != tip {
			return &tipMovedError{current: tip}
		}

		sig := gitobj.Signature{Name: u.Username, Email: u.Username + "@" + commitEmailTLD, Unix: s.Now().Unix()}
		c := gitobj.Commit{Tree: tree, Author: sig, Committer: sig, Message: msg}
		if tip != "" {
			c.Parents = []string{tip}
		}
		body, err := gitobj.SerializeCommit(c)
		if err != nil {
			return err
		}
		// Blobs, trees and the commit go in with the ref move in one statement.
		oid = gitobj.Hash(gitobj.TypeCommit, body)
		newObjs = append(newObjs, treebuild.Object{Type: gitobj.TypeCommit, Body: body})
		return ts.PutObjectsMoveRef(ctx, repo.ID, newObjs, req.Branch, tip, oid)
	})
	var verr *treebuild.Error
	var moved *tipMovedError
	switch {
	case errors.As(err, &verr):
		writeError(w, http.StatusBadRequest, verr.Error())
		return
	case errors.As(err, &moved):
		writeTipMoved(w, moved.current)
		return
	case errors.Is(err, ErrConflict):
		// The swap lost and the transaction rolled back with it.
		current, gerr := s.Store.GetRef(ctx, repo.ID, req.Branch)
		if gerr != nil && !errors.Is(gerr, ErrNotFound) {
			internalError(w, gerr)
			return
		}
		writeTipMoved(w, current)
		return
	case err != nil:
		fail(err)
		return
	}
	resp := commitResponse{OID: oid, Tree: tree, Branch: req.Branch}
	if tip != "" {
		resp.Parent = &tip
	}
	writeJSON(w, http.StatusCreated, resp)
}

// commitTree returns the root tree oid of a commit.
func commitTree(store treebuild.Store, oid string) (string, error) {
	_, body, err := store.GetObject(oid)
	if err != nil {
		return "", err
	}
	c, err := gitobj.ParseCommit(body)
	if err != nil {
		return "", err
	}
	return c.Tree, nil
}
