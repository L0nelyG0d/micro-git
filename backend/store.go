package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is the part of *pgxpool.Pool and pgx.Tx the stores need, so a store
// can run on the pool or inside a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Ref errors. Check with errors.Is.
var (
	ErrRefNotFound = errors.New("ref not found")
	ErrRefExists   = errors.New("ref already exists")
	// ErrRefMoved is returned by UpdateRef when the compare-and-swap lost: the
	// ref no longer has the expected oid (or already exists, for a first
	// commit). The ref is left unchanged.
	ErrRefMoved = errors.New("ref has moved")
)

// Ref is a branch: a name pointing at a commit oid.
type Ref struct {
	Name string
	OID  string
}

// RepoStore gives access to one repository's objects and refs. Issues #10-#12
// build it with NewRepoStore(s.DB, repo.ID) (or a pgx.Tx). It holds no state
// besides the connection and the repo id, so it is cheap to create per request.
type RepoStore struct {
	db     querier
	repoID int64
}

// NewRepoStore returns the store of the repository with the given id.
func NewRepoStore(db querier, repoID int64) *RepoStore {
	return &RepoStore{db: db, repoID: repoID}
}

// objectStore implements treebuild.Store for one repository on Postgres. The
// Store interface has no context, so the request context is bound at creation.
type objectStore struct {
	ctx context.Context
	rs  *RepoStore
}

var _ treebuild.Store = (*objectStore)(nil)

// Objects returns the repository's object store as a treebuild.Store. Objects
// of other repositories are never visible through it.
func (rs *RepoStore) Objects(ctx context.Context) treebuild.Store {
	return &objectStore{ctx: ctx, rs: rs}
}

// GetObject returns the type and body of an object, or an error wrapping
// treebuild.ErrNotFound.
func (o *objectStore) GetObject(oid string) (gitobj.Type, []byte, error) {
	var t string
	var body []byte
	err := o.rs.db.QueryRow(o.ctx,
		`SELECT type, data FROM objects WHERE repo_id = $1 AND oid = $2`,
		o.rs.repoID, oid).Scan(&t, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, fmt.Errorf("%w: %s", treebuild.ErrNotFound, oid)
	}
	if err != nil {
		return "", nil, err
	}
	return gitobj.Type(t), body, nil
}

// PutObject stores an object (insert-if-absent) and returns its oid.
func (o *objectStore) PutObject(t gitobj.Type, body []byte) (string, error) {
	oid := gitobj.Hash(t, body)
	if body == nil {
		body = []byte{} // pgx sends a nil slice as NULL; an empty blob is valid
	}
	_, err := o.rs.db.Exec(o.ctx,
		`INSERT INTO objects (repo_id, oid, type, data) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (repo_id, oid) DO NOTHING`,
		o.rs.repoID, oid, string(t), body)
	if err != nil {
		return "", err
	}
	return oid, nil
}

// GetRef returns the oid of a branch, or ErrRefNotFound.
func (rs *RepoStore) GetRef(ctx context.Context, name string) (string, error) {
	var oid string
	err := rs.db.QueryRow(ctx,
		`SELECT oid FROM refs WHERE repo_id = $1 AND name = $2`, rs.repoID, name).Scan(&oid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrRefNotFound
	}
	return oid, err
}

// ListRefs returns all branches sorted by name (bytewise); never nil.
func (rs *RepoStore) ListRefs(ctx context.Context) ([]Ref, error) {
	rows, err := rs.db.Query(ctx,
		`SELECT name, oid FROM refs WHERE repo_id = $1 ORDER BY name COLLATE "C"`, rs.repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []Ref{}
	for rows.Next() {
		var r Ref
		if err := rows.Scan(&r.Name, &r.OID); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// CreateRef adds a new branch; ErrRefExists if the name is taken.
func (rs *RepoStore) CreateRef(ctx context.Context, name, oid string) error {
	tag, err := rs.db.Exec(ctx,
		`INSERT INTO refs (repo_id, name, oid) VALUES ($1, $2, $3)
		 ON CONFLICT (repo_id, name) DO NOTHING`, rs.repoID, name, oid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRefExists
	}
	return nil
}

// DeleteRef removes a branch; ErrRefNotFound if it does not exist.
func (rs *RepoStore) DeleteRef(ctx context.Context, name string) error {
	tag, err := rs.db.Exec(ctx,
		`DELETE FROM refs WHERE repo_id = $1 AND name = $2`, rs.repoID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRefNotFound
	}
	return nil
}

// UpdateRef is a compare-and-swap: it moves the branch to newOID only if it
// currently points at oldOID. An empty oldOID means "must not exist" (the first
// commit) and creates the branch. Otherwise it returns ErrRefMoved and leaves
// the ref unchanged.
func (rs *RepoStore) UpdateRef(ctx context.Context, name, oldOID, newOID string) error {
	var tag pgconn.CommandTag
	var err error
	if oldOID == "" {
		tag, err = rs.db.Exec(ctx,
			`INSERT INTO refs (repo_id, name, oid) VALUES ($1, $2, $3)
			 ON CONFLICT (repo_id, name) DO NOTHING`, rs.repoID, name, newOID)
	} else {
		tag, err = rs.db.Exec(ctx,
			`UPDATE refs SET oid = $4 WHERE repo_id = $1 AND name = $2 AND oid = $3`,
			rs.repoID, name, oldOID, newOID)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRefMoved
	}
	return nil
}

// ResolveCommit resolves a branch name or a full 40-char lowercase-hex commit
// oid of this repository to a commit oid. A branch with that name wins. An oid
// that is missing or not a commit gives ErrRefNotFound.
func (rs *RepoStore) ResolveCommit(ctx context.Context, ref string) (string, error) {
	oid, err := rs.GetRef(ctx, ref)
	if err == nil {
		return oid, nil
	}
	if !errors.Is(err, ErrRefNotFound) {
		return "", err
	}
	if !gitobj.ValidOID(ref) {
		return "", ErrRefNotFound
	}
	var t string
	err = rs.db.QueryRow(ctx,
		`SELECT type FROM objects WHERE repo_id = $1 AND oid = $2`, rs.repoID, ref).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && t != string(gitobj.TypeCommit) {
		return "", ErrRefNotFound
	}
	if err != nil {
		return "", err
	}
	return ref, nil
}

// CommitRow is one commit of a first-parent walk: its oid, raw body and the
// first parent named in its header ("" for a root).
type CommitRow struct {
	OID    string
	Data   []byte
	Parent string
}

// WalkFirstParents returns the first-parent chain starting at tip, newest
// first, at most limit commits, in a single recursive statement. Only the
// commit header is searched for the parent, never the message. A tip or
// parent that is missing (or not a commit) gives an error wrapping
// treebuild.ErrNotFound rather than a shortened chain.
func (rs *RepoStore) WalkFirstParents(ctx context.Context, tip string, limit int) ([]CommitRow, error) {
	if limit < 1 {
		return []CommitRow{}, nil
	}
	// encode(..., 'escape') never fails on non-UTF-8 bytes; 100 bytes cover
	// "tree <40>\nparent <40>\n" (93 bytes), so the message is never searched.
	const parentRE = `^tree [0-9a-f]{40}\nparent ([0-9a-f]{40})\n`
	rows, err := rs.db.Query(ctx, `
WITH RECURSIVE walk(oid, data, parent, depth) AS (
  SELECT o.oid, o.data,
         substring(encode(substring(o.data from 1 for 100), 'escape') from $4), 1
    FROM objects o
   WHERE o.repo_id = $1 AND o.oid = $2 AND o.type = 'commit'
  UNION ALL
  SELECT o.oid, o.data,
         substring(encode(substring(o.data from 1 for 100), 'escape') from $4), w.depth + 1
    FROM walk w
    JOIN objects o ON o.repo_id = $1 AND o.oid = w.parent AND o.type = 'commit'
   WHERE w.depth < $3
)
SELECT oid, data, COALESCE(parent, '') FROM walk ORDER BY depth`,
		rs.repoID, tip, limit, parentRE)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommitRow{}
	for rows.Next() {
		var r CommitRow
		if err := rows.Scan(&r.OID, &r.Data, &r.Parent); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s", treebuild.ErrNotFound, tip)
	}
	if last := out[len(out)-1]; len(out) < limit && last.Parent != "" {
		return nil, fmt.Errorf("%w: %s", treebuild.ErrNotFound, last.Parent)
	}
	return out, nil
}
