package main

import (
	"context"
	"errors"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/linediff"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
	"gorm.io/gorm"
)

// sqliteDSNParams are the connection parameters the SQLite store needs for
// concurrent commits (#95 owns the full pragma set and the writer pool):
// foreign keys on, WAL so readers do not block the writer, a busy timeout so a
// second writer waits instead of failing with SQLITE_BUSY, and BEGIN IMMEDIATE
// so a transaction that reads before it writes takes the write lock up front
// (a deferred one cannot upgrade while another writer holds it).
const sqliteDSNParams = "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_txlock=immediate"

// InTx runs fn in one database transaction. The Store fn receives uses that
// transaction, so everything it does commits together, or rolls back when fn
// returns an error. Inside a transaction InTx joins it.
func (g *gormStore) InTx(ctx context.Context, fn func(Store) error) error {
	if g.inTx {
		return fn(g)
	}
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&gormStore{db: tx, inTx: true})
	})
	return mapGormErr(err)
}

func (g *gormStore) GetRef(ctx context.Context, repoID int64, name string) (string, error) {
	return getRef(g.db.WithContext(ctx), repoID, name)
}

func getRef(db *gorm.DB, repoID int64, name string) (string, error) {
	var rows []RefModel
	err := db.Select("oid").Where("repo_id = ? AND name = ?", repoID, name).Limit(1).Find(&rows).Error
	if err != nil {
		return "", mapGormErr(err)
	}
	if len(rows) == 0 {
		return "", ErrNotFound
	}
	return string(rows[0].OID), nil
}

func (g *gormStore) GetRefCommit(ctx context.Context, repoID int64, name string) (string, []byte, error) {
	var rows []struct {
		OID  string `gorm:"column:oid"`
		Data []byte
	}
	err := g.db.WithContext(ctx).Raw(`SELECT r.oid, o.data FROM refs r
		JOIN objects o ON o.repo_id = r.repo_id AND o.oid = r.oid
		WHERE r.repo_id = ? AND r.name = ?`, repoID, name).Scan(&rows).Error
	if err != nil {
		return "", nil, mapGormErr(err)
	}
	if len(rows) == 0 {
		return "", nil, ErrNotFound
	}
	return rows[0].OID, bodyOf(rows[0].Data), nil
}

// ResolveCommit resolves a branch name or a full commit oid; a branch wins.
func (g *gormStore) ResolveCommit(ctx context.Context, repoID int64, ref string) (string, error) {
	db := g.db.WithContext(ctx)
	oid, err := getRef(db, repoID, ref)
	if err == nil {
		return oid, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if !gitobj.ValidOID(ref) {
		return "", ErrNotFound
	}
	var rows []ObjectModel
	err = db.Select("type").Where("repo_id = ? AND oid = ?", repoID, ref).Limit(1).Find(&rows).Error
	if err != nil {
		return "", mapGormErr(err)
	}
	if len(rows) == 0 || rows[0].Type != string(gitobj.TypeCommit) {
		return "", ErrNotFound
	}
	return ref, nil
}

// casRef is the compare-and-swap of a branch as one conditional write, the
// same statement on SQLite and Postgres: an empty oldOID inserts the branch
// unless it exists, otherwise UPDATE ... WHERE oid = oldOID. Zero rows
// affected is a lost swap (ErrConflict). The (repo_id, oid) foreign key makes
// the database reject a ref that points at no object of its own repository
// (ErrForeignKey).
func casRef(db *gorm.DB, repoID int64, name, oldOID, newOID string) error {
	var res *gorm.DB
	if oldOID == "" {
		res = db.Exec(`INSERT INTO refs (repo_id, name, oid) VALUES (?, ?, ?)
			ON CONFLICT (repo_id, name) DO NOTHING`, repoID, name, newOID)
	} else {
		res = db.Exec(`UPDATE refs SET oid = ? WHERE repo_id = ? AND name = ? AND oid = ?`,
			newOID, repoID, name, oldOID)
	}
	if res.Error != nil {
		return mapGormErr(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrConflict
	}
	return nil
}

func (g *gormStore) UpdateRef(ctx context.Context, repoID int64, name, oldOID, newOID string) error {
	if err := checkRefOIDs(oldOID, newOID); err != nil {
		return err
	}
	return casRef(g.db.WithContext(ctx), repoID, name, oldOID, newOID)
}

// PutObjectsMoveRef stores objs and moves the branch in one transaction (the
// caller's, or a new one), so a lost swap leaves no objects behind.
func (g *gormStore) PutObjectsMoveRef(ctx context.Context, repoID int64, objs []treebuild.Object, name, oldOID, newOID string) error {
	if err := checkRefOIDs(oldOID, newOID); err != nil {
		return err
	}
	run := func(tx *gorm.DB) error {
		o := &gormObjects{db: tx, ctx: ctx, repoID: repoID}
		if err := o.PutObjects(objs); err != nil {
			return err
		}
		return casRef(tx, repoID, name, oldOID, newOID)
	}
	if g.inTx {
		return run(g.db)
	}
	return mapGormErr(g.db.WithContext(ctx).Transaction(run))
}

// FindRepoRef is FindRepo plus the branch, its commit and that commit's root
// tree: the repository, then one join for the ref and commit, then the tree.
func (g *gormStore) FindRepoRef(ctx context.Context, owner, name, ref string) (ReadHead, error) {
	repo, err := g.FindRepo(ctx, owner, name)
	if err != nil {
		return ReadHead{}, err
	}
	h := ReadHead{Repo: repo}
	db := g.db.WithContext(ctx)
	var rows []struct {
		OID  string `gorm:"column:oid"`
		Type *string
		Data []byte
	}
	err = db.Raw(`SELECT f.oid, c.type, c.data FROM refs f
		LEFT JOIN objects c ON c.repo_id = f.repo_id AND c.oid = f.oid
		WHERE f.repo_id = ? AND f.name = ?`, repo.ID, ref).Scan(&rows).Error
	if err != nil {
		return ReadHead{}, mapGormErr(err)
	}
	if len(rows) == 0 {
		return h, nil
	}
	h.HasRef, h.Tip = true, rows[0].OID
	h.Objects = map[string]linediff.Object{}
	if rows[0].Type == nil || *rows[0].Type != string(gitobj.TypeCommit) {
		return h, nil
	}
	body := bodyOf(rows[0].Data)
	h.Objects[h.Tip] = linediff.Object{Type: gitobj.TypeCommit, Body: body}
	if mt := treeHeader.FindSubmatch(head(body)); mt != nil {
		got, err := getObjects(db, repo.ID, []string{string(mt[1])})
		if err != nil {
			return ReadHead{}, err
		}
		if t, ok := got[string(mt[1])]; ok && t.Type == gitobj.TypeTree {
			h.Objects[string(mt[1])] = linediff.Object(t)
		}
	}
	return h, nil
}
