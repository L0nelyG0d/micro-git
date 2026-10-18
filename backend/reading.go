package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
	"github.com/jackc/pgx/v5"
)

type treeEntryJSON struct {
	Name string `json:"name"`
	Type string `json:"type"`
	OID  string `json:"oid"`
}

type treeResponse struct {
	Ref     string          `json:"ref"`
	Commit  string          `json:"commit"`
	Path    string          `json:"path"`
	Entries []treeEntryJSON `json:"entries"`
}

type blobResponse struct {
	Ref     string `json:"ref"`
	Commit  string `json:"commit"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	OID     string `json:"oid"`
	Size    int    `json:"size"`
	Content string `json:"content"`
}

var errPathNotFound = errors.New("path not found")

// resolveRef resolves the {ref} path value (branch name first, else a full
// commit oid of this repository) to a commit oid. On failure it has written
// 404 "ref not found" (or 500) and returns false. Reused by the commit log.
func (s *Server) resolveRef(w http.ResponseWriter, r *http.Request, repo Repo, ref string) (string, bool) {
	oid, err := NewRepoStore(s.DB, repo.ID).ResolveCommit(r.Context(), ref)
	if errors.Is(err, ErrRefNotFound) {
		if s.repoGone(r, repo) { // deleted meanwhile: its refs went with it
			writeError(w, http.StatusNotFound, "repository not found")
			return "", false
		}
		writeError(w, http.StatusNotFound, "ref not found")
		return "", false
	}
	if err != nil {
		s.readError(w, r, repo, err)
		return "", false
	}
	s.testHook("ref-resolved")
	return oid, true
}

// cleanReadPath normalizes the {path...} value (one trailing slash allowed,
// empty means the root) and validates it. It returns false for an invalid
// path.
func cleanReadPath(p string) (string, bool) {
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return "", true
	}
	return p, treebuild.ValidatePath(p) == nil
}

// walkPath follows a validated path ("" = root) from a root tree and returns
// the oid of the entry and whether it is a directory. A missing path (or a
// path through a file) gives errPathNotFound.
func walkPath(store treebuild.Store, rootTree, path string) (oid string, isDir bool, err error) {
	oid, isDir = rootTree, true
	if path == "" {
		return oid, true, nil
	}
	for _, seg := range strings.Split(path, "/") {
		if !isDir {
			return "", false, errPathNotFound
		}
		entries, err := readTree(store, oid)
		if err != nil {
			return "", false, err
		}
		found := false
		for _, e := range entries {
			if e.Name == seg {
				oid, isDir, found = e.OID, e.IsDir(), true
				break
			}
		}
		if !found {
			return "", false, errPathNotFound
		}
	}
	return oid, isDir, nil
}

// readTree loads and parses a tree object.
func readTree(store treebuild.Store, oid string) ([]gitobj.TreeEntry, error) {
	t, body, err := store.GetObject(oid)
	if err != nil {
		return nil, err
	}
	if t != gitobj.TypeTree {
		return nil, errors.New("object " + oid + " is not a tree")
	}
	return gitobj.ParseTree(body)
}

// readTarget is the shared front half of the tree and blob handlers: repo,
// ref, path validation, then the lookup. Order: 404 repo, 404 ref, 400 path,
// 404 path. It returns the resolved commit, cleaned path and entry. The repo,
// the branch tip, the tip commit and its root tree come from one statement.
func (s *Server) readTarget(w http.ResponseWriter, r *http.Request) (repo Repo, commit, path string, store treebuild.Store, oid string, isDir bool, cache cacheHeaders, ok bool) {
	fail := func() (Repo, string, string, treebuild.Store, string, bool, cacheHeaders, bool) {
		return repo, "", "", nil, "", false, cacheHeaders{}, false
	}
	ref := r.PathValue("ref")
	head, err := FindRepoRef(r.Context(), s.DB, r.PathValue("owner"), r.PathValue("repo"), ref)
	if errors.Is(err, errRepoNotFound) {
		writeError(w, http.StatusNotFound, "repository not found")
		return fail()
	}
	if err != nil {
		internalError(w, err)
		return fail()
	}
	repo = head.Repo
	if !s.canView(w, r, repo) {
		return fail()
	}
	s.testHook("repo-loaded")
	objs := head.Objects
	if head.HasRef {
		commit = head.Tip
		s.testHook("ref-resolved")
	} else {
		// not a branch: maybe a full commit oid (one more statement)
		if commit, ok = s.resolveRef(w, r, repo, ref); !ok {
			return fail()
		}
		objs = nil
	}
	var valid bool
	if path, valid = cleanReadPath(r.PathValue("path")); !valid {
		writeError(w, http.StatusBadRequest, "invalid path")
		return fail()
	}
	// Caching: a branch-named read is revalidated by an ETag of the resolved
	// commit and path, and answered 304 here, after repo and ref resolution
	// (so 404s stay correct) but before any object is read. A read addressed
	// by a full commit oid is immutable.
	kind := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/repos/"), "/", 4)[2]
	if head.HasRef {
		cache = branchCache(kind, commit, path, repo.Private)
		if cache.notModified(w, r) {
			return fail()
		}
	} else {
		cache = immutableCache(repo.Private)
	}
	store = primed(NewRepoStore(s.DB, repo.ID).Objects(r.Context()), objs)
	root, err := commitTree(store, commit)
	if err == nil {
		oid, isDir, err = walkPath(store, root, path)
	}
	if errors.Is(err, errPathNotFound) {
		writeError(w, http.StatusNotFound, "path not found")
		return fail()
	}
	if err != nil {
		s.readError(w, r, repo, err)
		return fail()
	}
	return repo, commit, path, store, oid, isDir, cache, true
}

// getTree handles GET /repos/{owner}/{repo}/tree/{ref}/{path...}.
func (s *Server) getTree(w http.ResponseWriter, r *http.Request) {
	repo, commit, path, store, oid, isDir, cache, ok := s.readTarget(w, r)
	if !ok {
		return
	}
	if !isDir {
		writeError(w, http.StatusBadRequest, "not a directory")
		return
	}
	entries, err := readTree(store, oid)
	if err != nil {
		s.readError(w, r, repo, err)
		return
	}
	out := make([]treeEntryJSON, len(entries))
	for i, e := range entries {
		out[i] = treeEntryJSON{Name: e.Name, Type: "blob", OID: e.OID}
		if e.IsDir() {
			out[i].Type = "tree"
		}
	}
	// directories first, then files, each bytewise by name
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Type == "tree") != (out[j].Type == "tree") {
			return out[i].Type == "tree"
		}
		return out[i].Name < out[j].Name
	})
	cache.set(w)
	writeJSON(w, http.StatusOK, treeResponse{Ref: r.PathValue("ref"), Commit: commit, Path: path, Entries: out})
}

// getBlob handles GET /repos/{owner}/{repo}/blob/{ref}/{path...}.
func (s *Server) getBlob(w http.ResponseWriter, r *http.Request) {
	repo, commit, path, store, oid, isDir, cache, ok := s.readTarget(w, r)
	if !ok {
		return
	}
	if isDir || path == "" {
		writeError(w, http.StatusBadRequest, "not a file")
		return
	}
	_, body, err := store.GetObject(oid)
	if err != nil {
		s.readError(w, r, repo, err)
		return
	}
	name := path[strings.LastIndexByte(path, '/')+1:]
	cache.set(w)
	writeJSON(w, http.StatusOK, blobResponse{Ref: r.PathValue("ref"), Commit: commit, Path: path,
		Name: name, OID: oid, Size: len(body), Content: string(body)})
}

// repoGone reports whether the repository no longer exists. It ignores a
// canceled request context so the check still answers.
func (s *Server) repoGone(r *http.Request, repo Repo) bool {
	var one int
	err := s.DB.QueryRow(context.WithoutCancel(r.Context()),
		`SELECT 1 FROM repos WHERE id = $1`, repo.ID).Scan(&one)
	return errors.Is(err, pgx.ErrNoRows)
}

// readError is internalError for the read endpoints. A missing object or a
// canceled query is a 404 "repository not found" when the repository was
// deleted while the request ran; for an existing repository a missing object
// is real corruption and stays a 500. Client aborts are not logged as errors.
func (s *Server) readError(w http.ResponseWriter, r *http.Request, repo Repo, err error) {
	if (errors.Is(err, treebuild.ErrNotFound) || errors.Is(err, context.Canceled)) && s.repoGone(r, repo) {
		writeError(w, http.StatusNotFound, "repository not found")
		return
	}
	internalError(w, err)
}
