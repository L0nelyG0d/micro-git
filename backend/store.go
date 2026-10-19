package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/linediff"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// querier is the part of *pgxpool.Pool and pgx.Tx the stores need, so a store
// can run on the pool or inside a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RepoStore gives access to one repository's objects and refs. Handlers
// pgxStore builds it per call with NewRepoStore(p.q, repoID), where p.q is the
// pool or the current pgx.Tx. It holds no state besides that querier and the
// repo id, so it is cheap to create per request.
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

var (
	_ treebuild.Store      = (*objectStore)(nil)
	_ treebuild.BatchStore = (*objectStore)(nil)
)

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
		return "", mapErr(err)
	}
	return oid, nil
}

// objectArrays splits objects into the column arrays the batch insert unnests.
func objectArrays(objs []treebuild.Object) (oids, types []string, data [][]byte) {
	oids = make([]string, len(objs))
	types = make([]string, len(objs))
	data = make([][]byte, len(objs))
	for i, ob := range objs {
		oids[i] = gitobj.Hash(ob.Type, ob.Body)
		types[i] = string(ob.Type)
		data[i] = ob.Body
		if data[i] == nil {
			data[i] = []byte{} // an empty blob is valid, not NULL
		}
	}
	return
}

const insertObjectsSQL = `INSERT INTO objects (repo_id, oid, type, data)
	SELECT $1, t.oid, t.type, t.data FROM unnest($2::text[], $3::text[], $4::bytea[]) AS t(oid, type, data)
	ON CONFLICT (repo_id, oid) DO NOTHING`

// PutObjects stores all objects (insert-if-absent) in one statement.
func (o *objectStore) PutObjects(objs []treebuild.Object) error {
	if len(objs) == 0 {
		return nil
	}
	oids, types, data := objectArrays(objs)
	_, err := o.rs.db.Exec(o.ctx, insertObjectsSQL, o.rs.repoID, oids, types, data)
	return mapErr(err)
}

// GetObjects returns the existing objects among oids in one statement.
func (o *objectStore) GetObjects(oids []string) (map[string]treebuild.Object, error) {
	for _, oid := range oids {
		if err := checkOID(oid); err != nil {
			return nil, err
		}
	}
	rows, err := o.rs.db.Query(o.ctx,
		`SELECT oid, type, data FROM objects WHERE repo_id = $1 AND oid = ANY($2::text[])`,
		o.rs.repoID, oids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]treebuild.Object, len(oids))
	for rows.Next() {
		var oid, t string
		var body []byte
		if err := rows.Scan(&oid, &t, &body); err != nil {
			return nil, err
		}
		out[oid] = treebuild.Object{Type: gitobj.Type(t), Body: body}
	}
	return out, rows.Err()
}

// GetRefCommit returns the oid a branch points at together with that commit
// object's body, in one statement. ErrNotFound if there is no such branch.
func (rs *RepoStore) GetRefCommit(ctx context.Context, name string) (string, []byte, error) {
	var oid string
	var body []byte
	err := rs.db.QueryRow(ctx,
		`SELECT r.oid, o.data FROM refs r
		 JOIN objects o ON o.repo_id = r.repo_id AND o.oid = r.oid
		 WHERE r.repo_id = $1 AND r.name = $2`, rs.repoID, name).Scan(&oid, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	return oid, body, err
}

// PutObjectsMoveRef stores objs and moves the branch like UpdateRef (a
// compare-and-swap from oldOID to newOID, empty oldOID = create) in one
// statement. It returns ErrConflict when the swap lost; the caller must then
// roll back its transaction so the objects are not kept.
func (rs *RepoStore) PutObjectsMoveRef(ctx context.Context, objs []treebuild.Object, name, oldOID, newOID string) error {
	oids, types, data := objectArrays(objs)
	var moved int
	var err error
	if oldOID == "" {
		err = rs.db.QueryRow(ctx,
			`WITH ins AS (`+insertObjectsSQL+`),
			 mv AS (INSERT INTO refs (repo_id, name, oid) VALUES ($1, $5, $6)
			        ON CONFLICT (repo_id, name) DO NOTHING RETURNING 1)
			 SELECT count(*) FROM mv`,
			rs.repoID, oids, types, data, name, newOID).Scan(&moved)
	} else {
		err = rs.db.QueryRow(ctx,
			`WITH ins AS (`+insertObjectsSQL+`),
			 mv AS (UPDATE refs SET oid = $6 WHERE repo_id = $1 AND name = $5 AND oid = $7 RETURNING 1)
			 SELECT count(*) FROM mv`,
			rs.repoID, oids, types, data, name, newOID, oldOID).Scan(&moved)
	}
	if err != nil {
		return err
	}
	if moved == 0 {
		return ErrConflict
	}
	return nil
}

// GetRef returns the oid of a branch, or ErrNotFound.
func (rs *RepoStore) GetRef(ctx context.Context, name string) (string, error) {
	var oid string
	err := rs.db.QueryRow(ctx,
		`SELECT oid FROM refs WHERE repo_id = $1 AND name = $2`, rs.repoID, name).Scan(&oid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
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

// CreateRef adds a new branch; ErrConflict if the name is taken.
func (rs *RepoStore) CreateRef(ctx context.Context, name, oid string) error {
	tag, err := rs.db.Exec(ctx,
		`INSERT INTO refs (repo_id, name, oid) VALUES ($1, $2, $3)
		 ON CONFLICT (repo_id, name) DO NOTHING`, rs.repoID, name, oid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// DeleteRef removes a branch; ErrNotFound if it does not exist.
func (rs *RepoStore) DeleteRef(ctx context.Context, name string) error {
	tag, err := rs.db.Exec(ctx,
		`DELETE FROM refs WHERE repo_id = $1 AND name = $2`, rs.repoID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRef is a compare-and-swap: it moves the branch to newOID only if it
// currently points at oldOID. An empty oldOID means "must not exist" (the first
// commit) and creates the branch. Otherwise it returns ErrConflict and leaves
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
		return ErrConflict
	}
	return nil
}

// ResolveCommit resolves a branch name or a full 40-char lowercase-hex commit
// oid of this repository to a commit oid. A branch with that name wins. An oid
// that is missing or not a commit gives ErrNotFound.
func (rs *RepoStore) ResolveCommit(ctx context.Context, ref string) (string, error) {
	oid, err := rs.GetRef(ctx, ref)
	if err == nil {
		return oid, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if !gitobj.ValidOID(ref) {
		return "", ErrNotFound
	}
	var t string
	err = rs.db.QueryRow(ctx,
		`SELECT type FROM objects WHERE repo_id = $1 AND oid = $2`, rs.repoID, ref).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && t != string(gitobj.TypeCommit) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return ref, nil
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
         substring(encode(substring(o.data from 1 for 100), 'escape') from $4), 1::bigint
    FROM objects o
   WHERE o.repo_id = $1 AND o.oid = $2 AND o.type = 'commit'
  UNION ALL
  SELECT o.oid, o.data,
         substring(encode(substring(o.data from 1 for 100), 'escape') from $4), w.depth + 1
    FROM walk w
    JOIN objects o ON o.repo_id = $1 AND o.oid = w.parent AND o.type = 'commit'
   WHERE w.depth < $3::bigint
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

// CommitReachability walks the parents of a and of b together, following every
// parent, in a single recursive statement. Only the commit header (up to the
// first blank line) is searched for `parent` lines, never the message. Rows
// are (walk, oid) pairs deduplicated by UNION, so each commit is visited once
// per walk and the recursion ends by itself on the finite commit graph: no
// per-commit round trip and no count of the repository's commits. A missing or
// non-commit oid just ends that branch of the walk.
func (rs *RepoStore) CommitReachability(ctx context.Context, a, b string) (Reachability, error) {
	var r Reachability
	err := rs.db.QueryRow(ctx, `
WITH RECURSIVE walk(root, oid) AS (
  SELECT s.root, o.oid
    FROM (VALUES (1, $2::text), (2, $3::text)) AS s(root, oid)
    JOIN objects o ON o.repo_id = $1 AND o.oid = s.oid AND o.type = 'commit'
  UNION
  SELECT w.root, p.oid
    FROM walk w
    JOIN objects c ON c.repo_id = $1 AND c.oid = w.oid
    CROSS JOIN LATERAL regexp_matches(
      encode(substring(c.data from 1 for COALESCE(NULLIF(position('\x0a0a'::bytea in c.data), 0), length(c.data) + 1)), 'escape'),
      '^parent ([0-9a-f]{40})$', 'gn') AS m(p)
    JOIN objects p ON p.repo_id = $1 AND p.oid = m.p[1] AND p.type = 'commit'
)
SELECT COALESCE(bool_or(root = 2 AND oid = $2), false),
       COALESCE(bool_or(root = 1 AND oid = $3), false)
  FROM walk`, rs.repoID, a, b).Scan(&r.AFromB, &r.BFromA)
	return r, err
}

// primedStore serves some objects from memory (ones a join already fetched)
// and everything else from the wrapped store, so code written against
// treebuild.Store keeps its behaviour and only skips the statements.
type primedStore struct {
	treebuild.Store
	objs map[string]linediff.Object
}

func (p *primedStore) GetObject(oid string) (gitobj.Type, []byte, error) {
	if o, ok := p.objs[oid]; ok {
		return o.Type, o.Body, nil
	}
	return p.Store.GetObject(oid)
}

// GetObjects implements linediff.Batcher: primed objects come from memory, the
// rest from the wrapped store's batch query when it has one.
func (p *primedStore) GetObjects(oids []string) (map[string]linediff.Object, error) {
	out := map[string]linediff.Object{}
	var rest []string
	for _, oid := range oids {
		if o, ok := p.objs[oid]; ok {
			out[oid] = o
		} else {
			rest = append(rest, oid)
		}
	}
	if b, ok := p.Store.(linediff.Batcher); ok && len(rest) > 0 {
		more, err := b.GetObjects(rest)
		if err != nil {
			return nil, err
		}
		for k, v := range more {
			out[k] = v
		}
	}
	return out, nil
}

// primed wraps a store with objects that are already in memory.
func primed(s treebuild.Store, objs map[string]linediff.Object) treebuild.Store {
	return &primedStore{Store: s, objs: objs}
}

// Header patterns: a commit body starts "tree <40 hex>\n".
const (
	treeHeaderRE   = `^tree ([0-9a-f]{40})\n`
	parentHeaderRE = `^tree [0-9a-f]{40}\nparent ([0-9a-f]{40})\n`
)

// FindRepoRef looks the repository up by owner and name (case-insensitive) and
// in the same statement the branch `ref` of it, the commit it points at and
// that commit's root tree. The repository is reported first: errRepoNotFound
// when it does not exist, otherwise HasRef tells whether the branch exists.
func FindRepoRef(ctx context.Context, db querier, owner, name, ref string) (ReadHead, error) {
	var h ReadHead
	var created time.Time
	var tip, treeOID *string
	var commitData, treeData []byte
	err := db.QueryRow(ctx, `
SELECT r.id, r.owner_id, u.username, r.name, r.description, r.default_branch, r.private, r.created_at,
       NOT EXISTS (SELECT 1 FROM refs WHERE repo_id = r.id),
       f.oid, c.data, t.oid, t.data
  FROM repos r
  JOIN users u ON u.id = r.owner_id
  LEFT JOIN refs f ON f.repo_id = r.id AND f.name = $3
  LEFT JOIN objects c ON c.repo_id = r.id AND c.oid = f.oid AND c.type = 'commit'
  LEFT JOIN objects t ON t.repo_id = r.id AND t.type = 'tree'
       AND t.oid = substring(encode(substring(c.data from 1 for 100), 'escape') from $4)
 WHERE lower(u.username) = lower($1) AND lower(r.name) = lower($2)`,
		owner, name, ref, treeHeaderRE).Scan(
		&h.Repo.ID, &h.Repo.OwnerID, &h.Repo.Owner, &h.Repo.Name, &h.Repo.Description,
		&h.Repo.DefaultBranch, &h.Repo.Private, &created, &h.Repo.Empty, &tip, &commitData, &treeOID, &treeData)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReadHead{}, ErrNotFound
	}
	if err != nil {
		return ReadHead{}, err
	}
	h.Repo.CreatedAt = created.UTC().Format(time.RFC3339)
	if tip != nil {
		h.HasRef, h.Tip = true, *tip
		h.Objects = map[string]linediff.Object{}
		if commitData != nil {
			h.Objects[*tip] = linediff.Object{Type: gitobj.TypeCommit, Body: commitData}
		}
		if treeOID != nil {
			h.Objects[*treeOID] = linediff.Object{Type: gitobj.TypeTree, Body: treeData}
		}
	}
	return h, nil
}

// CommitBundle loads a commit, its first parent and both root trees in one
// statement and returns them as objects keyed by oid (wrap a store with
// primed()). found is false when oid is not a commit of this repository.
// Anything the join could not match (a damaged parent, say) is simply missing
// from the map; the caller's normal GetObject fallback then reports it.
func (rs *RepoStore) CommitBundle(ctx context.Context, oid string) (objs map[string]linediff.Object, found bool, err error) {
	rows, err := rs.db.Query(ctx, `
WITH c AS (
  SELECT oid, data,
         substring(encode(substring(data from 1 for 100), 'escape') from $3) AS tree,
         substring(encode(substring(data from 1 for 100), 'escape') from $4) AS parent
    FROM objects WHERE repo_id = $1 AND oid = $2 AND type = 'commit'
), p AS (
  SELECT o.oid, o.data,
         substring(encode(substring(o.data from 1 for 100), 'escape') from $3) AS tree
    FROM objects o, c WHERE o.repo_id = $1 AND o.oid = c.parent AND o.type = 'commit'
)
SELECT 'commit', c.oid, c.data FROM c
UNION ALL SELECT 'commit', p.oid, p.data FROM p
UNION ALL SELECT 'tree', o.oid, o.data FROM objects o
  WHERE o.repo_id = $1 AND o.type = 'tree'
    AND o.oid IN (SELECT tree FROM c UNION SELECT tree FROM p)`,
		rs.repoID, oid, treeHeaderRE, parentHeaderRE)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	objs = map[string]linediff.Object{}
	for rows.Next() {
		var t, id string
		var body []byte
		if err := rows.Scan(&t, &id, &body); err != nil {
			return nil, false, err
		}
		objs[id] = linediff.Object{Type: gitobj.Type(t), Body: body}
		if id == oid && t == "commit" {
			found = true
		}
	}
	return objs, found, rows.Err()
}

// DeleteNonDefaultRef deletes a branch unless it is the repo's default, checked
// inside the DELETE so a concurrent set-default cannot slip in between.
// ErrNotFound if the branch does not exist, errDefaultBranch if it is the default.
func (rs *RepoStore) DeleteNonDefaultRef(ctx context.Context, name string) error {
	tag, err := rs.db.Exec(ctx,
		`DELETE FROM refs WHERE repo_id = $1 AND name = $2
		 AND NOT EXISTS (SELECT 1 FROM repos WHERE id = $1 AND default_branch = $2)`, rs.repoID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	if _, err := rs.GetRef(ctx, name); err != nil {
		return err // ErrNotFound, or a real error
	}
	return errDefaultBranch
}

const repoSelect = `SELECT r.id, r.owner_id, u.username, r.name, r.description, r.default_branch,
	r.private, r.created_at, NOT EXISTS (SELECT 1 FROM refs WHERE repo_id = r.id)
	FROM repos r JOIN users u ON u.id = r.owner_id`

// returningRepo wraps an INSERT/UPDATE (without RETURNING) so it yields the
// full repo row, joined like repoSelect, in one round trip.
func returningRepo(stmt string) string {
	return `WITH r AS (` + stmt + ` RETURNING *) ` +
		strings.Replace(repoSelect, "FROM repos r", "FROM r", 1)
}

func scanRepo(row pgx.Row) (Repo, error) {
	var r Repo
	var created time.Time
	err := row.Scan(&r.ID, &r.OwnerID, &r.Owner, &r.Name, &r.Description, &r.DefaultBranch, &r.Private, &created, &r.Empty)
	r.CreatedAt = created.UTC().Format(time.RFC3339)
	return r, err
}

// storeError carries a Store sentinel together with the driver error that
// caused it, so errors.Is matches the sentinel while logs keep the cause.
type storeError struct {
	sentinel error
	cause    error
}

func (e *storeError) Error() string        { return e.sentinel.Error() + ": " + e.cause.Error() }
func (e *storeError) Is(target error) bool { return target == e.sentinel }
func (e *storeError) Unwrap() error        { return e.cause }

// mapErr translates pgx errors into the Store sentinels: no row is
// ErrNotFound, a unique violation ErrConflict, a foreign-key violation
// ErrForeignKey. Other errors pass through unchanged.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return &storeError{ErrNotFound, err}
	case isUniqueViolation(err):
		return &storeError{ErrConflict, err}
	case isForeignKeyViolation(err):
		return &storeError{ErrForeignKey, err}
	}
	return err
}

// pgxStore is the Postgres implementation of Store, and the only one. It runs
// on the pool, or inside a transaction when made by InTx.
type pgxStore struct {
	pool *pgxpool.Pool // nil inside a transaction
	q    querier
}

var _ Store = (*pgxStore)(nil)

func newPgxStore(pool *pgxpool.Pool) *pgxStore { return &pgxStore{pool: pool, q: pool} }

func (p *pgxStore) rs(repoID int64) *RepoStore { return NewRepoStore(p.q, repoID) }

func (p *pgxStore) InTx(ctx context.Context, fn func(Store) error) error {
	if p.pool == nil { // already in a transaction: join it
		return fn(p)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(&pgxStore{q: tx}); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

func (p *pgxStore) CreateUser(ctx context.Context, username, passwordHash string) (int64, error) {
	var id int64
	err := p.q.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id`,
		username, passwordHash).Scan(&id)
	return id, mapErr(err)
}

func (p *pgxStore) UsernameByID(ctx context.Context, id int64) (string, error) {
	var name string
	err := p.q.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, id).Scan(&name)
	return name, mapErr(err)
}

func (p *pgxStore) UserForLogin(ctx context.Context, username string) (int64, string, error) {
	var id int64
	var hash string
	err := p.q.QueryRow(ctx,
		`SELECT id, password_hash FROM users WHERE lower(username) = $1`, username).Scan(&id, &hash)
	return id, hash, mapErr(err)
}

func (p *pgxStore) FindRepo(ctx context.Context, owner, name string) (Repo, error) {
	r, err := scanRepo(p.q.QueryRow(ctx,
		repoSelect+` WHERE lower(u.username) = lower($1) AND lower(r.name) = lower($2)`, owner, name))
	return r, mapErr(err)
}

func (p *pgxStore) FindRepoRef(ctx context.Context, owner, name, ref string) (ReadHead, error) {
	return FindRepoRef(ctx, p.q, owner, name, ref)
}

func (p *pgxStore) CreateRepo(ctx context.Context, ownerID int64, name, description string, private bool, createdAt time.Time) (Repo, error) {
	r, err := scanRepo(p.q.QueryRow(ctx, returningRepo(
		`INSERT INTO repos (owner_id, name, description, private, created_at) VALUES ($1, $2, $3, $4, $5)`),
		ownerID, name, description, private, createdAt.UTC()))
	return r, mapErr(err)
}

func (p *pgxStore) UpdateRepo(ctx context.Context, repoID int64, patch RepoPatch) (Repo, error) {
	var sets []string
	var args []any
	arg := func(col string, v any) int {
		args = append(args, v)
		sets = append(sets, col+" = $"+strconv.Itoa(len(args)))
		return len(args)
	}
	if patch.Name != nil {
		arg("name", *patch.Name)
	}
	if patch.Description != nil {
		arg("description", *patch.Description)
	}
	branchArg := 0
	if patch.DefaultBranch != nil {
		branchArg = arg("default_branch", *patch.DefaultBranch)
	}
	if patch.Private != nil {
		arg("private", *patch.Private)
	}
	if len(sets) == 0 {
		return Repo{}, errors.New("UpdateRepo: nothing to update")
	}
	args = append(args, repoID)
	where := "id = $" + strconv.Itoa(len(args))
	if branchArg > 0 { // the branch must still exist when the row is written
		where += " AND EXISTS (SELECT 1 FROM refs WHERE repo_id = repos.id AND name = $" + strconv.Itoa(branchArg) + ")"
	}
	r, err := scanRepo(p.q.QueryRow(ctx,
		returningRepo(`UPDATE repos SET `+strings.Join(sets, ", ")+` WHERE `+where), args...))
	return r, mapErr(err)
}

func (p *pgxStore) DeleteRepo(ctx context.Context, repoID int64) error {
	_, err := p.q.Exec(ctx, `DELETE FROM repos WHERE id = $1`, repoID)
	return mapErr(err)
}

func (p *pgxStore) RepoExists(ctx context.Context, repoID int64) (bool, error) {
	var one int
	err := p.q.QueryRow(ctx, `SELECT 1 FROM repos WHERE id = $1`, repoID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (p *pgxStore) ListRepos(ctx context.Context, viewerID int64, owner *string, limit, offset int) ([]Repo, error) {
	q := repoSelect + ` WHERE (NOT r.private OR r.owner_id = $1)`
	args := []any{viewerID}
	if owner != nil {
		q += ` AND lower(u.username) = lower($2)`
		args = append(args, *owner)
	}
	q += fmt.Sprintf(` ORDER BY r.created_at DESC, r.id DESC LIMIT %d OFFSET %d`, limit, offset)
	rows, err := p.q.Query(ctx, q, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	repos := []Repo{}
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		repos = append(repos, r)
	}
	return repos, mapErr(rows.Err())
}

func (p *pgxStore) Objects(ctx context.Context, repoID int64) treebuild.Store {
	return p.rs(repoID).Objects(ctx)
}

func (p *pgxStore) CommitBundle(ctx context.Context, repoID int64, oid string) (map[string]linediff.Object, bool, error) {
	if err := checkOID(oid); err != nil {
		return nil, false, err
	}
	objs, found, err := p.rs(repoID).CommitBundle(ctx, oid)
	return objs, found, mapErr(err)
}

func (p *pgxStore) GetRef(ctx context.Context, repoID int64, name string) (string, error) {
	oid, err := p.rs(repoID).GetRef(ctx, name)
	return oid, mapErr(err)
}

func (p *pgxStore) GetRefCommit(ctx context.Context, repoID int64, name string) (string, []byte, error) {
	oid, body, err := p.rs(repoID).GetRefCommit(ctx, name)
	return oid, body, mapErr(err)
}

func (p *pgxStore) ListRefs(ctx context.Context, repoID int64) ([]Ref, error) {
	refs, err := p.rs(repoID).ListRefs(ctx)
	return refs, mapErr(err)
}

func (p *pgxStore) CreateRef(ctx context.Context, repoID int64, name, oid string) error {
	return mapErr(p.rs(repoID).CreateRef(ctx, name, oid))
}

func (p *pgxStore) DeleteRef(ctx context.Context, repoID int64, name string) error {
	return mapErr(p.rs(repoID).DeleteRef(ctx, name))
}

func (p *pgxStore) DeleteNonDefaultRef(ctx context.Context, repoID int64, name string) error {
	return mapErr(p.rs(repoID).DeleteNonDefaultRef(ctx, name))
}

func (p *pgxStore) UpdateRef(ctx context.Context, repoID int64, name, oldOID, newOID string) error {
	if err := checkRefOIDs(oldOID, newOID); err != nil {
		return err
	}
	return mapErr(p.rs(repoID).UpdateRef(ctx, name, oldOID, newOID))
}

func (p *pgxStore) ResolveCommit(ctx context.Context, repoID int64, ref string) (string, error) {
	oid, err := p.rs(repoID).ResolveCommit(ctx, ref)
	return oid, mapErr(err)
}

func (p *pgxStore) PutObjectsMoveRef(ctx context.Context, repoID int64, objs []treebuild.Object, name, oldOID, newOID string) error {
	if err := checkRefOIDs(oldOID, newOID); err != nil {
		return err
	}
	return mapErr(p.rs(repoID).PutObjectsMoveRef(ctx, objs, name, oldOID, newOID))
}

func (p *pgxStore) WalkFirstParents(ctx context.Context, repoID int64, tip string, limit int) ([]CommitRow, error) {
	rows, err := p.rs(repoID).WalkFirstParents(ctx, tip, limit)
	return rows, mapErr(err)
}

func (p *pgxStore) CommitReachability(ctx context.Context, repoID int64, a, b string) (Reachability, error) {
	r, err := p.rs(repoID).CommitReachability(ctx, a, b)
	return r, mapErr(err)
}
