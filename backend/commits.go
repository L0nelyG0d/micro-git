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

func writeTipMoved(w http.ResponseWriter, current string) {
	writeJSON(w, http.StatusConflict, map[string]string{
		"error": "branch tip has moved", "current_tip": current,
	})
}

// createCommit handles POST /repos/{owner}/{repo}/commits (behind requireAuth).
// Order of checks: 401, 404 (repo, branch), 403, 400, 409.
func (s *Server) createCommit(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFromPath(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCommitBody))
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

	rs := NewRepoStore(s.DB, repo.ID)
	// 404 for a missing branch comes before 403, so it needs the body.
	if jsonOK && validBranchName(req.Branch) && !repo.Empty {
		if _, err := rs.GetRef(r.Context(), req.Branch); errors.Is(err, ErrRefNotFound) {
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
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusNotFound, "repository not found")
			return
		}
		internalError(w, err)
	}
	// One transaction: objects and the ref move commit together or not at all.
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ts := NewRepoStore(tx, repo.ID)
	objs := ts.Objects(ctx)

	tip, err := ts.GetRef(ctx, req.Branch)
	if errors.Is(err, ErrRefNotFound) {
		tip, err = "", nil
	}
	if err != nil {
		internalError(w, err)
		return
	}
	root := ""
	if tip != "" {
		if root, err = commitTree(objs, tip); err != nil {
			internalError(w, err)
			return
		}
	}
	tree, err := treebuild.Apply(objs, root, changes)
	var verr *treebuild.Error
	if errors.As(err, &verr) {
		writeError(w, http.StatusBadRequest, verr.Error())
		return
	}
	if err != nil {
		fail(err)
		return
	}
	if req.ExpectedTip != tip {
		writeTipMoved(w, tip)
		return
	}

	sig := gitobj.Signature{Name: u.Username, Email: u.Username + "@" + commitEmailTLD, Unix: s.Now().Unix()}
	c := gitobj.Commit{Tree: tree, Author: sig, Committer: sig, Message: msg}
	if tip != "" {
		c.Parents = []string{tip}
	}
	body, err := gitobj.SerializeCommit(c)
	if err != nil {
		internalError(w, err)
		return
	}
	oid, err := objs.PutObject(gitobj.TypeCommit, body)
	if err != nil {
		fail(err)
		return
	}
	err = ts.UpdateRef(ctx, req.Branch, tip, oid)
	if errors.Is(err, ErrRefMoved) {
		_ = tx.Rollback(ctx)
		current, gerr := NewRepoStore(s.DB, repo.ID).GetRef(ctx, req.Branch)
		if gerr != nil && !errors.Is(gerr, ErrRefNotFound) {
			internalError(w, gerr)
			return
		}
		writeTipMoved(w, current)
		return
	}
	if err != nil {
		fail(err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		internalError(w, err)
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
