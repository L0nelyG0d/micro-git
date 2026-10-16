package linediff

import (
	"fmt"
	"sort"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

// File statuses.
const (
	StatusAdded    = "added"
	StatusModified = "modified"
	StatusDeleted  = "deleted"
)

// EmptyTreeOID is the oid of the empty tree. It is treated like "".
const EmptyTreeOID = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// FileDiff is the diff of one file. For a truncated file Hunks is empty and
// Additions/Deletions are zero.
type FileDiff struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Truncated bool   `json:"truncated"`
	Hunks     []Hunk `json:"hunks"`
}

// Trees compares two root trees read from store and returns the changed files
// sorted by path. An empty oldTree ("" or the empty tree oid, as for a root
// commit) is the empty tree, and so is newTree. Subtrees with equal oids are
// skipped without being read. A file replaced by a directory (or the reverse)
// is reported as the file deleted plus the new files added. No rename
// detection is done.
func Trees(store treebuild.Store, oldTree, newTree string) ([]FileDiff, error) {
	d := differ{store: store}
	if err := d.walk("", oldTree, newTree); err != nil {
		return nil, err
	}
	// Files are diffed in path order, so the shared budget is spent
	// deterministically.
	sort.Slice(d.files, func(i, j int) bool { return d.files[i].path < d.files[j].path })
	out := make([]FileDiff, 0, len(d.files))
	budget := MaxTotalCells
	for _, f := range d.files {
		fd, err := d.diffFile(f, &budget)
		if err != nil {
			return nil, err
		}
		out = append(out, fd)
	}
	return out, nil
}

// fileJob is a changed file found by walk; an empty oid means absent.
type fileJob struct{ path, oldOID, newOID string }

type differ struct {
	store treebuild.Store
	files []fileJob
}

func (d *differ) entries(oid string) (map[string]gitobj.TreeEntry, error) {
	m := map[string]gitobj.TreeEntry{}
	if oid == "" || oid == EmptyTreeOID {
		return m, nil
	}
	t, body, err := d.store.GetObject(oid)
	if err != nil {
		return nil, err
	}
	if t != gitobj.TypeTree {
		return nil, fmt.Errorf("object %s is a %s, not a tree", oid, t)
	}
	es, err := gitobj.ParseTree(body)
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		m[e.Name] = e
	}
	return m, nil
}

func (d *differ) blob(oid string) (string, error) {
	if oid == "" {
		return "", nil
	}
	t, body, err := d.store.GetObject(oid)
	if err != nil {
		return "", err
	}
	if t != gitobj.TypeBlob {
		return "", fmt.Errorf("object %s is a %s, not a blob", oid, t)
	}
	return string(body), nil
}

func (d *differ) walk(prefix, oldOID, newOID string) error {
	if oldOID == newOID {
		return nil
	}
	oldE, err := d.entries(oldOID)
	if err != nil {
		return err
	}
	newE, err := d.entries(newOID)
	if err != nil {
		return err
	}
	names := map[string]struct{}{}
	for n := range oldE {
		names[n] = struct{}{}
	}
	for n := range newE {
		names[n] = struct{}{}
	}
	for name := range names {
		o, hasO := oldE[name]
		n, hasN := newE[name]
		p := prefix + name
		if hasO && hasN && o.OID == n.OID && o.IsDir() == n.IsDir() {
			continue
		}
		// Old side: directory contents are all deleted unless the new side
		// is a directory too.
		switch {
		case hasO && hasN && o.IsDir() && n.IsDir():
			err = d.walk(p+"/", o.OID, n.OID)
		case hasO && hasN && !o.IsDir() && !n.IsDir():
			err = d.file(p, o.OID, n.OID)
		default:
			if hasO {
				if o.IsDir() {
					err = d.walk(p+"/", o.OID, "")
				} else {
					err = d.file(p, o.OID, "")
				}
			}
			if err == nil && hasN {
				if n.IsDir() {
					err = d.walk(p+"/", "", n.OID)
				} else {
					err = d.file(p, "", n.OID)
				}
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// file records one changed file to diff later.
func (d *differ) file(path, oldOID, newOID string) error {
	d.files = append(d.files, fileJob{path, oldOID, newOID})
	return nil
}

func (d *differ) diffFile(f fileJob, budget *int) (FileDiff, error) {
	oldText, err := d.blob(f.oldOID)
	if err != nil {
		return FileDiff{}, err
	}
	newText, err := d.blob(f.newOID)
	if err != nil {
		return FileDiff{}, err
	}
	status := StatusModified
	switch {
	case f.oldOID == "":
		status = StatusAdded
	case f.newOID == "":
		status = StatusDeleted
	}
	r := diffBudget(oldText, newText, budget)
	return FileDiff{
		Path: f.path, Status: status,
		Additions: r.Additions, Deletions: r.Deletions,
		Truncated: r.Truncated, Hunks: r.Hunks,
	}, nil
}
