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
	d := differ{store: store, cache: map[string]Object{}}
	if err := d.run(oldTree, newTree); err != nil {
		return nil, err
	}
	// Files are diffed in path order, so the shared budget is spent
	// deterministically.
	sort.Slice(d.files, func(i, j int) bool { return d.files[i].path < d.files[j].path })
	out := make([]FileDiff, 0, len(d.files))
	budget := MaxTotalCells
	for i, f := range d.files {
		if i%blobBatch == 0 { // fetch the next chunk of blobs in one query
			var oids []string
			for _, g := range d.files[i:min(i+blobBatch, len(d.files))] {
				oids = append(oids, g.oldOID, g.newOID)
			}
			if err := d.prefetch(oids); err != nil {
				return nil, err
			}
		}
		fd, err := d.diffFile(f, &budget)
		if err != nil {
			return nil, err
		}
		out = append(out, fd)
	}
	return out, nil
}

// blobBatch is how many changed files have their blobs fetched per query; it
// bounds memory for huge commits.
const blobBatch = 50

// Object is a stored object as a Batcher returns it.
type Object struct {
	Type gitobj.Type
	Body []byte
}

// Batcher is an optional extension of treebuild.Store: it fetches several
// objects in one round trip. Oids that do not exist are left out of the result
// (Trees then asks GetObject, which gives the proper not-found error). Stores
// without it are read one object at a time.
type Batcher interface {
	GetObjects(oids []string) (map[string]Object, error)
}

// fileJob is a changed file found by walk; an empty oid means absent.
type fileJob struct{ path, oldOID, newOID string }

type differ struct {
	store treebuild.Store
	files []fileJob
	cache map[string]Object // objects fetched by prefetch
}

// get returns an object from the prefetch cache, else from the store.
func (d *differ) get(oid string) (gitobj.Type, []byte, error) {
	if o, ok := d.cache[oid]; ok {
		return o.Type, o.Body, nil
	}
	return d.store.GetObject(oid)
}

// prefetch loads the not yet cached objects among oids in one query when the
// store can batch. Empty oids and the empty tree are skipped.
func (d *differ) prefetch(oids []string) error {
	b, ok := d.store.(Batcher)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var need []string
	for _, oid := range oids {
		if oid == "" || oid == EmptyTreeOID || seen[oid] {
			continue
		}
		seen[oid] = true
		if _, ok := d.cache[oid]; !ok {
			need = append(need, oid)
		}
	}
	if len(need) == 0 {
		return nil
	}
	got, err := b.GetObjects(need)
	if err != nil {
		return err
	}
	for oid, o := range got {
		d.cache[oid] = o
	}
	return nil
}

func (d *differ) entries(oid string) (map[string]gitobj.TreeEntry, error) {
	m := map[string]gitobj.TreeEntry{}
	if oid == "" || oid == EmptyTreeOID {
		return m, nil
	}
	t, body, err := d.get(oid)
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
	t, body, err := d.get(oid)
	if err != nil {
		return "", err
	}
	if t != gitobj.TypeBlob {
		return "", fmt.Errorf("object %s is a %s, not a blob", oid, t)
	}
	return string(body), nil
}

// dirPair is a pair of directories (old and new side, "" = absent) to compare.
type dirPair struct{ prefix, oldOID, newOID string }

// run walks the two trees one level at a time: all directories of a level are
// fetched in one query, then compared, which yields the next level.
func (d *differ) run(oldTree, newTree string) error {
	level := []dirPair{{"", oldTree, newTree}}
	for len(level) > 0 {
		var oids []string
		for _, p := range level {
			if p.oldOID != p.newOID {
				oids = append(oids, p.oldOID, p.newOID)
			}
		}
		if err := d.prefetch(oids); err != nil {
			return err
		}
		var next []dirPair
		for _, p := range level {
			var err error
			if next, err = d.step(p, next); err != nil {
				return err
			}
		}
		level = next
	}
	return nil
}

// step compares one directory pair; subdirectories to compare are appended to next.
func (d *differ) step(p dirPair, next []dirPair) ([]dirPair, error) {
	prefix, oldOID, newOID := p.prefix, p.oldOID, p.newOID
	if oldOID == newOID {
		return next, nil
	}
	oldE, err := d.entries(oldOID)
	if err != nil {
		return nil, err
	}
	newE, err := d.entries(newOID)
	if err != nil {
		return nil, err
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
			next = append(next, dirPair{p + "/", o.OID, n.OID})
		case hasO && hasN && !o.IsDir() && !n.IsDir():
			err = d.file(p, o.OID, n.OID)
		default:
			if hasO {
				if o.IsDir() {
					next = append(next, dirPair{p + "/", o.OID, ""})
				} else {
					err = d.file(p, o.OID, "")
				}
			}
			if err == nil && hasN {
				if n.IsDir() {
					next = append(next, dirPair{p + "/", "", n.OID})
				} else {
					err = d.file(p, "", n.OID)
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	return next, nil
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
