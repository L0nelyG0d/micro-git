// Package treebuild applies file changes (upserts and deletes) to a git root
// tree and returns the new root tree oid, rebuilding only the trees along
// touched paths. It is pure Go over the Store interface: no database, HTTP,
// clock or global state. Objects are written only after every change has
// been validated, so on error nothing is written.
package treebuild

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
)

// Limits from plan.md.
const (
	MaxPathLen     = 255
	MaxContentSize = 1 << 20 // 1 MiB
)

// Change is one file change. Exactly one of Content (upsert, may point to an
// empty string) or Delete must be set.
type Change struct {
	Path    string
	Content *string
	Delete  bool
}

// Upsert returns a Change that creates or replaces a file.
func Upsert(path, content string) Change { return Change{Path: path, Content: &content} }

// Remove returns a Change that deletes a file.
func Remove(path string) Change { return Change{Path: path, Delete: true} }

// ValidatePath checks the path rules; it returns an *Error of kind
// ErrInvalidPath or nil.
func ValidatePath(p string) error {
	if p == "" || utf8.RuneCountInString(p) > MaxPathLen || !utf8.ValidString(p) || strings.ContainsRune(p, 0) {
		return newErr(ErrInvalidPath, p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." || seg == ".git" {
			return newErr(ErrInvalidPath, p)
		}
	}
	return nil
}

type node struct {
	children map[string]*node
	change   *Change // set on leaves
}

type objectGetter interface {
	GetObject(oid string) (gitobj.Type, []byte, error)
}

type pending struct {
	t    gitobj.Type
	body []byte
}

// Apply applies changes to the tree root ("" or the empty tree for an empty
// repository) and returns the new root tree oid. All new objects are written
// to store only if every change is valid and at least one changes something;
// otherwise a typed *Error is returned (see errors.go) and nothing is
// written. A delete of a missing path is ignored; an upsert with identical
// content is a no-op; if all changes are no-ops the error is ErrNoChanges.
// The result does not depend on the order of changes. Store failures are
// returned as-is.
func Apply(store Store, root string, changes []Change) (string, error) {
	oid, objs, err := Build(store, root, changes)
	if err != nil {
		return "", err
	}
	if bs, ok := store.(BatchStore); ok {
		if err := bs.PutObjects(objs); err != nil {
			return "", err
		}
		return oid, nil
	}
	for _, o := range objs {
		if _, err := store.PutObject(o.Type, o.Body); err != nil {
			return "", err
		}
	}
	return oid, nil
}

// Build is Apply without the writes: it returns the new root tree oid and the
// new objects (blobs and trees) the caller must store. Existing trees are read
// level by level with one GetObjects call per level when store is a
// BatchStore, one GetObject per tree otherwise.
func Build(store Store, root string, changes []Change) (string, []Object, error) {
	top, err := plan(changes)
	if err != nil {
		return "", nil, err
	}
	var writes []pending
	g := &getter{store: store, cache: map[string]Object{}}
	if bs, ok := store.(BatchStore); ok && root != "" {
		if err := prefetch(bs, g, root, top); err != nil {
			return "", nil, err
		}
	}
	oid, _, changed, err := rebuild(g, root, top, true, &writes)
	if err != nil {
		return "", nil, err
	}
	if !changed {
		return "", nil, newErr(ErrNoChanges, "")
	}
	objs := make([]Object, len(writes))
	for i, w := range writes {
		objs[i] = Object{Type: w.t, Body: w.body}
	}
	return oid, objs, nil
}

// getter serves GetObject from a prefetched cache, falling back to the store.
type getter struct {
	store Store
	cache map[string]Object
}

func (g *getter) GetObject(oid string) (gitobj.Type, []byte, error) {
	if o, ok := g.cache[oid]; ok {
		return o.Type, o.Body, nil
	}
	return g.store.GetObject(oid)
}

type frontier struct {
	oid string
	n   *node
}

// prefetch loads the existing trees the changes will touch, one GetObjects
// call per directory depth. Anything it misses is read lazily by rebuild, so
// errors surface exactly as before.
func prefetch(bs BatchStore, g *getter, root string, top *node) error {
	level := []frontier{{root, top}}
	for len(level) > 0 {
		oids := make([]string, 0, len(level))
		for _, f := range level {
			oids = append(oids, f.oid)
		}
		got, err := bs.GetObjects(oids)
		if err != nil {
			return err
		}
		var next []frontier
		for _, f := range level {
			o, ok := got[f.oid]
			if !ok || o.Type != gitobj.TypeTree {
				continue
			}
			g.cache[f.oid] = o
			entries, err := gitobj.ParseTree(o.Body)
			if err != nil {
				continue // rebuild reports it
			}
			for _, e := range entries {
				if child, ok := f.n.children[e.Name]; ok && child.change == nil && e.IsDir() {
					next = append(next, frontier{e.OID, child})
				}
			}
		}
		level = next
	}
	return nil
}

// plan validates the changes themselves and builds a path trie.
func plan(changes []Change) (*node, error) {
	top := &node{children: map[string]*node{}}
	for i := range changes {
		c := &changes[i]
		if err := ValidatePath(c.Path); err != nil {
			return nil, err
		}
		if (c.Content == nil) == !c.Delete { // both or neither
			return nil, newErr(ErrBadChange, c.Path)
		}
		if c.Content != nil {
			if len(*c.Content) > MaxContentSize {
				return nil, newErr(ErrFileTooLarge, c.Path)
			}
			if !utf8.ValidString(*c.Content) || strings.ContainsRune(*c.Content, 0) {
				return nil, newErr(ErrNotUTF8Text, c.Path)
			}
		}
		n := top
		segs := strings.Split(c.Path, "/")
		for j, seg := range segs {
			if n.change != nil { // an earlier change is a prefix of this path
				return nil, newErr(ErrPathConflict, c.Path)
			}
			next, ok := n.children[seg]
			if !ok {
				next = &node{children: map[string]*node{}}
				n.children[seg] = next
			}
			n = next
			if j == len(segs)-1 {
				if n.change != nil {
					return nil, newErr(ErrDuplicatePath, c.Path)
				}
				if len(n.children) > 0 { // a later... earlier change lies below this path
					return nil, newErr(ErrPathConflict, c.Path)
				}
				n.change = c
			}
		}
	}
	return top, nil
}

// firstUpsert returns the path of some upsert below n, or "".
func firstUpsert(n *node) string {
	if n.change != nil {
		if n.change.Content != nil {
			return n.change.Path
		}
		return ""
	}
	names := sortedNames(n)
	for _, name := range names {
		if p := firstUpsert(n.children[name]); p != "" {
			return p
		}
	}
	return ""
}

func sortedNames(n *node) []string {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// rebuild applies the trie n to the tree treeOID ("" = empty). It returns the
// new tree oid, whether the tree is empty, and whether anything changed.
// New objects are queued in writes, not stored.
func rebuild(store objectGetter, treeOID string, n *node, isRoot bool, writes *[]pending) (oid string, empty, changed bool, err error) {
	var entries []gitobj.TreeEntry
	if treeOID != "" {
		t, body, err := store.GetObject(treeOID)
		if err != nil {
			return "", false, false, err
		}
		if t != gitobj.TypeTree {
			return "", false, false, fmt.Errorf("treebuild: object %s is a %s, not a tree", treeOID, t)
		}
		if entries, err = gitobj.ParseTree(body); err != nil {
			return "", false, false, err
		}
	}
	index := map[string]int{}
	for i, e := range entries {
		index[e.Name] = i
	}
	removed := map[string]bool{}

	for _, name := range sortedNames(n) {
		child := n.children[name]
		i, exists := index[name]
		var cur gitobj.TreeEntry
		if exists {
			cur = entries[i]
		}
		if child.change != nil {
			c := child.change
			if exists && cur.IsDir() {
				if c.Content != nil {
					return "", false, false, newErr(ErrPathConflict, c.Path)
				}
				return "", false, false, newErr(ErrInvalidPath, c.Path) // deleting a directory
			}
			if c.Content == nil { // delete
				if exists {
					removed[name] = true
					changed = true
				}
				continue
			}
			blob := []byte(*c.Content)
			boid := gitobj.HashBlob(blob)
			if exists && cur.OID == boid {
				continue
			}
			*writes = append(*writes, pending{gitobj.TypeBlob, blob})
			changed = true
			ne := gitobj.TreeEntry{Mode: gitobj.ModeFile, Name: name, OID: boid}
			if exists {
				entries[i] = ne
			} else {
				entries = append(entries, ne)
			}
			continue
		}
		// intermediate directory
		sub := ""
		if exists {
			if !cur.IsDir() {
				if p := firstUpsert(child); p != "" {
					return "", false, false, newErr(ErrPathConflict, p)
				}
				continue // only deletes below a file: missing paths, ignored
			}
			sub = cur.OID
		}
		soid, sempty, schanged, err := rebuild(store, sub, child, false, writes)
		if err != nil {
			return "", false, false, err
		}
		if !schanged {
			continue
		}
		changed = true
		switch {
		case sempty:
			removed[name] = true
		case exists:
			entries[i].OID = soid
		default:
			entries = append(entries, gitobj.TreeEntry{Mode: gitobj.ModeDir, Name: name, OID: soid})
		}
	}
	if !changed {
		return treeOID, false, false, nil
	}
	kept := entries[:0:0]
	for _, e := range entries {
		if !removed[e.Name] {
			kept = append(kept, e)
		}
	}
	body, err := gitobj.SerializeTree(kept)
	if err != nil {
		return "", false, false, err
	}
	oid = gitobj.Hash(gitobj.TypeTree, body)
	empty = len(kept) == 0
	if !empty || isRoot {
		*writes = append(*writes, pending{gitobj.TypeTree, body})
	}
	return oid, empty, true, nil
}
