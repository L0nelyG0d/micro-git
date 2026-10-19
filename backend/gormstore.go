package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/treebuild"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// errNotPorted is returned by the gormStore methods that a later issue
// (#89 to #92) ports. It is never a sentinel the handlers match on.
var errNotPorted = errors.New("not implemented in gormStore yet")

// gormStore is the Store on GORM: one code path for SQLite and Postgres.
// Methods are ported group by group; until then they return errNotPorted.
type gormStore struct {
	db   *gorm.DB
	inTx bool // db is a transaction made by InTx
}

var _ Store = (*gormStore)(nil)

func newGormStore(db *gorm.DB) *gormStore { return &gormStore{db: db} }

// mapGormErr turns driver errors into the Store sentinels. It recognises
// gorm.ErrDuplicatedKey (set when the DB was opened with TranslateError) and
// the raw driver errors, so it works whether or not translation is on.
// Anything else passes through unchanged.
func mapGormErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return ErrNotFound
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return ErrConflict
	case errors.Is(err, gorm.ErrForeignKeyViolated):
		return ErrForeignKey
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23503":
			return ErrForeignKey
		}
		return err
	}
	// glebarez/sqlite reports constraint failures as text.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed"):
		return ErrConflict
	case strings.Contains(msg, "FOREIGN KEY constraint failed"):
		return ErrForeignKey
	}
	return err
}

// Users.

func (g *gormStore) CreateUser(ctx context.Context, username, passwordHash string) (int64, error) {
	u := UserModel{Username: username, PasswordHash: passwordHash}
	if err := g.db.WithContext(ctx).Create(&u).Error; err != nil {
		return 0, mapGormErr(err)
	}
	return u.ID, nil
}

func (g *gormStore) UsernameByID(ctx context.Context, id int64) (string, error) {
	var u UserModel
	if err := g.db.WithContext(ctx).Select("username").Where("id = ?", id).Take(&u).Error; err != nil {
		return "", mapGormErr(err)
	}
	return u.Username, nil
}

func (g *gormStore) UserForLogin(ctx context.Context, username string) (int64, string, error) {
	var u UserModel
	if err := g.db.WithContext(ctx).Select("id", "password_hash").
		Where("lower(username) = ?", username).Take(&u).Error; err != nil {
		return 0, "", mapGormErr(err)
	}
	return u.ID, u.PasswordHash, nil
}

// Repos.

// repoQuery selects a repository joined with its owner, plus the Empty flag
// (no refs). Same shape as repoSelect in store.go, with column aliases that
// match repoRow.
const repoQuery = `SELECT r.id, r.owner_id, u.username AS owner, r.name, r.description,
	r.default_branch, r.private, r.created_at, NOT EXISTS (SELECT 1 FROM refs WHERE repo_id = r.id) AS empty
	FROM repos r JOIN users u ON u.id = r.owner_id`

type repoRow struct {
	ID            int64
	OwnerID       int64
	Owner         string
	Name          string
	Description   string
	DefaultBranch string
	Private       bool
	CreatedAt     time.Time
	Empty         bool
}

func (r repoRow) repo() Repo {
	return Repo{ID: r.ID, OwnerID: r.OwnerID, Owner: r.Owner, Name: r.Name, Description: r.Description,
		DefaultBranch: r.DefaultBranch, Private: r.Private, Empty: r.Empty,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339)}
}

// loadRepo reads one repository by id on db (the store or a transaction).
func loadRepo(db *gorm.DB, id int64) (Repo, error) {
	var rows []repoRow
	if err := db.Raw(repoQuery+` WHERE r.id = ?`, id).Scan(&rows).Error; err != nil {
		return Repo{}, mapGormErr(err)
	}
	if len(rows) == 0 {
		return Repo{}, ErrNotFound
	}
	return rows[0].repo(), nil
}

func (g *gormStore) FindRepo(ctx context.Context, owner, name string) (Repo, error) {
	var rows []repoRow
	err := g.db.WithContext(ctx).Raw(repoQuery+` WHERE lower(u.username) = lower(?) AND lower(r.name) = lower(?)`,
		owner, name).Scan(&rows).Error
	if err != nil {
		return Repo{}, mapGormErr(err)
	}
	if len(rows) == 0 {
		return Repo{}, ErrNotFound
	}
	return rows[0].repo(), nil
}

func (g *gormStore) CreateRepo(ctx context.Context, ownerID int64, name, description string, private bool, createdAt time.Time) (Repo, error) {
	var out Repo
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		m := RepoModel{OwnerID: ownerID, Name: name, Description: description, Private: private, CreatedAt: createdAt.UTC()}
		// Select the columns so Private=false is written, not defaulted.
		if err := tx.Select("OwnerID", "Name", "Description", "Private", "CreatedAt").Create(&m).Error; err != nil {
			return err
		}
		r, err := loadRepo(tx, m.ID)
		out = r
		return err
	})
	return out, mapGormErr(err)
}

// UpdateRepo writes only the fields set in the patch in one UPDATE; when the
// default branch is set, the same statement requires the ref to exist.
func (g *gormStore) UpdateRepo(ctx context.Context, repoID int64, p RepoPatch) (Repo, error) {
	set := map[string]any{}
	if p.Name != nil {
		set["name"] = *p.Name
	}
	if p.Description != nil {
		set["description"] = *p.Description
	}
	if p.Private != nil {
		set["private"] = *p.Private
	}
	where := "id = ?"
	args := []any{repoID}
	if p.DefaultBranch != nil {
		set["default_branch"] = *p.DefaultBranch
		where += " AND EXISTS (SELECT 1 FROM refs WHERE repo_id = repos.id AND name = ?)"
		args = append(args, *p.DefaultBranch)
	}
	if len(set) == 0 {
		return Repo{}, errors.New("UpdateRepo: nothing to update")
	}
	var out Repo
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if p.DefaultBranch != nil {
			if err := lockRepoRow(tx, repoID); err != nil {
				return err
			}
		}
		res := tx.Model(&RepoModel{}).Where(where, args...).Updates(set)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		r, err := loadRepo(tx, repoID)
		out = r
		return err
	})
	return out, mapGormErr(err)
}

// DeleteRepo relies on ON DELETE CASCADE for objects and refs.
func (g *gormStore) DeleteRepo(ctx context.Context, repoID int64) error {
	return mapGormErr(g.db.WithContext(ctx).Delete(&RepoModel{}, repoID).Error)
}

func (g *gormStore) RepoExists(ctx context.Context, repoID int64) (bool, error) {
	var n int64
	err := g.db.WithContext(ctx).Model(&RepoModel{}).Where("id = ?", repoID).Count(&n).Error
	return n > 0, mapGormErr(err)
}

// ListRepos lists the repositories the viewer may see, newest first (id breaks
// ties). Private ones are listed for their owner only (viewerID 0 is anonymous
// and owns nothing); limit and offset apply after that filter. owner, when
// set, matches the username case-insensitively.
func (g *gormStore) ListRepos(ctx context.Context, viewerID int64, owner *string, limit, offset int) ([]Repo, error) {
	q := repoQuery + ` WHERE (NOT r.private OR r.owner_id = ?)`
	args := []any{viewerID}
	if owner != nil {
		q += ` AND lower(u.username) = lower(?)`
		args = append(args, *owner)
	}
	q += ` ORDER BY r.created_at DESC, r.id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	var rows []repoRow
	if err := g.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, mapGormErr(err)
	}
	repos := make([]Repo, 0, len(rows))
	for _, r := range rows {
		repos = append(repos, r.repo())
	}
	return repos, nil
}

// Branches.

// ListRefs returns the branches sorted bytewise by name, never nil. SQLite's
// default BINARY collation is bytewise; Postgres needs COLLATE "C".
func (g *gormStore) ListRefs(ctx context.Context, repoID int64) ([]Ref, error) {
	order := "name"
	if g.db.Dialector.Name() == "postgres" {
		order = `name COLLATE "C"`
	}
	var rows []RefModel
	err := g.db.WithContext(ctx).Select("name", "oid").Where("repo_id = ?", repoID).Order(order).Find(&rows).Error
	if err != nil {
		return nil, mapGormErr(err)
	}
	refs := make([]Ref, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, Ref{Name: r.Name, OID: string(r.OID)})
	}
	return refs, nil
}

// CreateRef adds a branch: ErrInvalidOID for a bad oid (before any write),
// ErrConflict if the name is taken.
func (g *gormStore) CreateRef(ctx context.Context, repoID int64, name, oid string) error {
	if err := checkOID(oid); err != nil {
		return err
	}
	res := g.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&RefModel{RepoID: repoID, Name: name, OID: OID(oid)})
	if res.Error != nil {
		return mapGormErr(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrConflict
	}
	return nil
}

// DeleteRef removes a branch; ErrNotFound if it does not exist.
func (g *gormStore) DeleteRef(ctx context.Context, repoID int64, name string) error {
	res := g.db.WithContext(ctx).Where("repo_id = ? AND name = ?", repoID, name).Delete(&RefModel{})
	if res.Error != nil {
		return mapGormErr(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteNonDefaultRef deletes a branch unless it is the default, decided inside
// the DELETE so a concurrent set-default cannot slip in between. ErrNotFound if
// the branch is missing, errDefaultBranch if it is the default.
func (g *gormStore) DeleteNonDefaultRef(ctx context.Context, repoID int64, name string) error {
	return mapGormErr(g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockRepoRow(tx, repoID); err != nil {
			return err
		}
		return deleteNonDefaultRef(tx, repoID, name)
	}))
}

// lockRepoRow locks the repos row FOR UPDATE on Postgres (SQLite has a single
// writer, so it needs nothing). Setting the default branch and deleting a branch
// both take it, so under READ COMMITTED they run one after the other and the
// later statement sees the earlier one's commit (#122). ErrNotFound if the
// repository is gone.
func lockRepoRow(tx *gorm.DB, repoID int64) error {
	if tx.Dialector.Name() == "sqlite" {
		return nil
	}
	var ids []int64
	if err := tx.Raw(`SELECT id FROM repos WHERE id = ? FOR UPDATE`, repoID).Scan(&ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return ErrNotFound
	}
	return nil
}

func deleteNonDefaultRef(db *gorm.DB, repoID int64, name string) error {
	res := db.Exec(`DELETE FROM refs WHERE repo_id = ? AND name = ?
		AND NOT EXISTS (SELECT 1 FROM repos WHERE id = ? AND default_branch = ?)`, repoID, name, repoID, name)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	var n int64
	if err := db.Model(&RefModel{}).Where("repo_id = ? AND name = ?", repoID, name).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return errDefaultBranch
}

// Not ported yet.

func (g *gormStore) CommitReachability(ctx context.Context, repoID int64, a, b string) (Reachability, error) {
	return Reachability{}, errNotPorted
}

// firstParentSQL returns, per dialect, the expression that reads the first
// parent oid out of the commit body column o.data, or NULL when the header
// has none. This is the only place the dialects differ (the one variant of the
// statement), and #90 reuses it. Both read only the first 100 bytes, which
// cover "tree <40>\nparent <40>\n" (93 bytes), so the message is never
// searched; this is the same header-only rule as the pgx version (store.go).
//
//   - postgres: bytea has no text cast that survives non-UTF-8 bytes, so the
//     window goes through encode(..., 'escape') and a regular expression.
//   - sqlite: no regular expressions; the window is cast to TEXT and matched
//     with GLOB, then the oid is cut out at its fixed offset (bytes 54..93).
//
// The returned arguments go with the expression's "?" placeholders.
func firstParentSQL(g *gorm.DB) (string, []any) {
	if g.Dialector.Name() == "sqlite" {
		hex40 := strings.Repeat("[0-9a-f]", 40)
		pat := "tree " + hex40 + "\nparent " + hex40 + "\n*"
		return `CASE WHEN CAST(substr(o.data, 1, 100) AS TEXT) GLOB ? THEN substr(CAST(substr(o.data, 1, 100) AS TEXT), 54, 40) END`, []any{pat}
	}
	// objects.oid is char(40): the cast keeps oid = parent a bpchar comparison
	// so the primary key is used (text would turn it into a scan per step).
	return `CAST(substring(encode(substring(o.data from 1 for 100), 'escape') from ?) AS char(40))`,
		[]any{`^tree [0-9a-f]{40}\nparent ([0-9a-f]{40})\n`}
}

// WalkFirstParents returns the first-parent chain from tip, newest first, at
// most limit commits, in one recursive statement (no query per commit). A
// missing tip or a missing/non-commit parent in the middle of the chain gives
// an error wrapping treebuild.ErrNotFound, not a shortened chain.
func (g *gormStore) WalkFirstParents(ctx context.Context, repoID int64, tip string, limit int) ([]CommitRow, error) {
	if limit < 1 {
		return []CommitRow{}, nil
	}
	parent, parentArgs := firstParentSQL(g.db)
	// Placeholders appear in this order: parent expr (anchor), repo, tip,
	// parent expr (step), repo, limit, repo
	// (the final join that fetches the bodies).
	args := append([]any{}, parentArgs...)
	args = append(args, repoID, tip)
	args = append(args, parentArgs...)
	args = append(args, repoID, limit, repoID)
	// CAST(? AS bigint): Postgres types a bare parameter here as int4, which
	// cannot hold a limit of 2^31 or more.
	query := `
WITH RECURSIVE walk(oid, parent, depth) AS (
  SELECT o.oid, ` + parent + `, 1
    FROM objects o
   WHERE o.repo_id = ? AND o.oid = ? AND o.type = 'commit'
  UNION ALL
  SELECT o.oid, ` + parent + `, w.depth + 1
    FROM walk w
    JOIN objects o ON o.repo_id = ? AND o.oid = w.parent AND o.type = 'commit'
   WHERE w.depth < CAST(? AS bigint)
)
SELECT w.oid, o.data, COALESCE(w.parent, '') AS parent
  FROM walk w JOIN objects o ON o.repo_id = ? AND o.oid = w.oid ORDER BY w.depth`
	var rows []struct {
		OID    string `gorm:"column:oid"`
		Data   []byte
		Parent string
	}
	if err := g.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, mapGormErr(err)
	}
	out := make([]CommitRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, CommitRow{OID: r.OID, Data: r.Data, Parent: r.Parent})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s", treebuild.ErrNotFound, tip)
	}
	if last := out[len(out)-1]; len(out) < limit && last.Parent != "" {
		return nil, fmt.Errorf("%w: %s", treebuild.ErrNotFound, last.Parent)
	}
	return out, nil
}
