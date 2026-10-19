package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/treebuild"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// errNotPorted is returned by the gormStore methods that a later issue
// (#88 to #92) ports. It is never a sentinel the handlers match on.
var errNotPorted = errors.New("not implemented in gormStore yet")

// gormStore is the Store on GORM: one code path for SQLite and Postgres.
// Methods are ported group by group; until then they return errNotPorted.
type gormStore struct {
	db *gorm.DB
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

// Not ported yet.

func (g *gormStore) FindRepoRef(ctx context.Context, owner, name, ref string) (ReadHead, error) {
	return ReadHead{}, errNotPorted
}
func (g *gormStore) ListRepos(ctx context.Context, viewerID int64, owner *string, limit, offset int) ([]Repo, error) {
	return nil, errNotPorted
}

func (g *gormStore) GetRef(ctx context.Context, repoID int64, name string) (string, error) {
	return "", errNotPorted
}
func (g *gormStore) GetRefCommit(ctx context.Context, repoID int64, name string) (string, []byte, error) {
	return "", nil, errNotPorted
}
func (g *gormStore) ListRefs(ctx context.Context, repoID int64) ([]Ref, error) {
	return nil, errNotPorted
}
func (g *gormStore) CreateRef(ctx context.Context, repoID int64, name, oid string) error {
	return errNotPorted
}
func (g *gormStore) DeleteRef(ctx context.Context, repoID int64, name string) error {
	return errNotPorted
}
func (g *gormStore) DeleteNonDefaultRef(ctx context.Context, repoID int64, name string) error {
	return errNotPorted
}
func (g *gormStore) UpdateRef(ctx context.Context, repoID int64, name, oldOID, newOID string) error {
	return errNotPorted
}
func (g *gormStore) ResolveCommit(ctx context.Context, repoID int64, ref string) (string, error) {
	return "", errNotPorted
}
func (g *gormStore) PutObjectsMoveRef(ctx context.Context, repoID int64, objs []treebuild.Object, name, oldOID, newOID string) error {
	return errNotPorted
}
func (g *gormStore) WalkFirstParents(ctx context.Context, repoID int64, tip string, limit int) ([]CommitRow, error) {
	return nil, errNotPorted
}
func (g *gormStore) CommitReachability(ctx context.Context, repoID int64, a, b string) (Reachability, error) {
	return Reachability{}, errNotPorted
}
func (g *gormStore) InTx(ctx context.Context, fn func(Store) error) error { return errNotPorted }
