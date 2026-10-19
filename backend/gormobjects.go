package main

import (
	"context"
	"fmt"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/linediff"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Batch sizes. A statement may carry at most 32766 variables on SQLite (999
// on old builds); reads bind one per oid, inserts four per row.
const (
	getObjectsChunk = 2000
	putObjectsChunk = 500
)

// gormObjects is the treebuild.Store of one repository on GORM. The request
// context is bound at creation because the interface has no context.
type gormObjects struct {
	db     *gorm.DB
	ctx    context.Context
	repoID int64
}

var (
	_ treebuild.Store      = (*gormObjects)(nil)
	_ treebuild.BatchStore = (*gormObjects)(nil)
)

// Objects returns the object store of one repository; other repositories'
// objects are never visible through it.
func (g *gormStore) Objects(ctx context.Context, repoID int64) treebuild.Store {
	return &gormObjects{db: g.db.WithContext(ctx), ctx: ctx, repoID: repoID}
}

func (o *gormObjects) GetObject(oid string) (gitobj.Type, []byte, error) {
	var rows []ObjectModel
	err := o.db.Select("type", "data").Where("repo_id = ? AND oid = ?", o.repoID, oid).Limit(1).Find(&rows).Error
	if err != nil {
		return "", nil, mapGormErr(err)
	}
	if len(rows) == 0 {
		return "", nil, fmt.Errorf("%w: %s", treebuild.ErrNotFound, oid)
	}
	return gitobj.Type(rows[0].Type), bodyOf(rows[0].Data), nil
}

func (o *gormObjects) PutObject(t gitobj.Type, body []byte) (string, error) {
	oid := gitobj.Hash(t, body)
	if err := o.insert([]ObjectModel{o.row(oid, t, body)}); err != nil {
		return "", err
	}
	return oid, nil
}

func (o *gormObjects) PutObjects(objs []treebuild.Object) error {
	rows := make([]ObjectModel, len(objs))
	for i, ob := range objs {
		rows[i] = o.row(gitobj.Hash(ob.Type, ob.Body), ob.Type, ob.Body)
	}
	return o.insert(rows)
}

func (o *gormObjects) row(oid string, t gitobj.Type, body []byte) ObjectModel {
	return ObjectModel{RepoID: o.repoID, OID: OID(oid), Type: string(t), Data: bodyOf(body)}
}

// bodyOf copies a body into a non-nil slice: an empty blob is valid, not NULL.
func bodyOf(b []byte) []byte { return append([]byte{}, b...) }

// insert stores rows that are not stored yet (insert-if-absent), in chunks.
func (o *gormObjects) insert(rows []ObjectModel) error {
	if len(rows) == 0 {
		return nil
	}
	err := o.db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, putObjectsChunk).Error
	return mapGormErr(err)
}

// GetObjects returns the existing objects among oids; unknown oids are absent.
// ErrInvalidOID if any oid is not 40 lowercase hex characters.
func (o *gormObjects) GetObjects(oids []string) (map[string]treebuild.Object, error) {
	return getObjects(o.db, o.repoID, oids)
}

func getObjects(db *gorm.DB, repoID int64, oids []string) (map[string]treebuild.Object, error) {
	for _, oid := range oids {
		if err := checkOID(oid); err != nil {
			return nil, err
		}
	}
	out := make(map[string]treebuild.Object, len(oids))
	for start := 0; start < len(oids); start += getObjectsChunk {
		chunk := oids[start:min(start+getObjectsChunk, len(oids))]
		var rows []ObjectModel
		err := db.Select("oid", "type", "data").Where("repo_id = ? AND oid IN ?", repoID, chunk).Find(&rows).Error
		if err != nil {
			return nil, mapGormErr(err)
		}
		for _, r := range rows {
			out[string(r.OID)] = treebuild.Object{Type: gitobj.Type(r.Type), Body: bodyOf(r.Data)}
		}
	}
	return out, nil
}

// CommitBundle loads a commit, its first parent and both root trees and
// returns them keyed by oid; found is false when oid is not a commit of the
// repository. Anything it cannot match (a damaged parent, say) is simply
// missing, as in the other stores. At most three reads: the commit, then its
// parent with its tree, then the parent's tree.
func (g *gormStore) CommitBundle(ctx context.Context, repoID int64, oid string) (map[string]linediff.Object, bool, error) {
	if err := checkOID(oid); err != nil {
		return nil, false, err
	}
	db := g.db.WithContext(ctx)
	objs := map[string]linediff.Object{}
	got, err := getObjects(db, repoID, []string{oid})
	if err != nil {
		return nil, false, err
	}
	c, ok := got[oid]
	if !ok || c.Type != gitobj.TypeCommit {
		return objs, false, nil
	}
	objs[oid] = linediff.Object(c)

	want := map[string]gitobj.Type{} // oid -> required type
	var tree, parent string
	if mt := treeHeader.FindSubmatch(head(c.Body)); mt != nil {
		tree = string(mt[1])
		want[tree] = gitobj.TypeTree
	}
	if mt := parentHeader.FindSubmatch(head(c.Body)); mt != nil {
		parent = string(mt[1])
		want[parent] = gitobj.TypeCommit
	}
	if err := addTyped(db, repoID, objs, want); err != nil {
		return nil, false, err
	}
	if p, ok := objs[parent]; ok && parent != "" {
		if mt := treeHeader.FindSubmatch(head(p.Body)); mt != nil {
			if err := addTyped(db, repoID, objs, map[string]gitobj.Type{string(mt[1]): gitobj.TypeTree}); err != nil {
				return nil, false, err
			}
		}
	}
	return objs, true, nil
}

// addTyped reads the wanted oids and adds those whose type matches to objs.
func addTyped(db *gorm.DB, repoID int64, objs map[string]linediff.Object, want map[string]gitobj.Type) error {
	if len(want) == 0 {
		return nil
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	got, err := getObjects(db, repoID, ids)
	if err != nil {
		return err
	}
	for id, ob := range got {
		if ob.Type == want[id] {
			objs[id] = linediff.Object(ob)
		}
	}
	return nil
}
