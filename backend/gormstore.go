package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/linediff"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// errNotPorted is returned by the gormStore methods that a later issue
// (#86 to #92) ports. It is never a sentinel the handlers match on.
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

// Not ported yet.

func (g *gormStore) FindRepo(ctx context.Context, owner, name string) (Repo, error) {
	return Repo{}, errNotPorted
}
func (g *gormStore) FindRepoRef(ctx context.Context, owner, name, ref string) (ReadHead, error) {
	return ReadHead{}, errNotPorted
}
func (g *gormStore) CreateRepo(ctx context.Context, ownerID int64, name, description string, private bool, createdAt time.Time) (Repo, error) {
	return Repo{}, errNotPorted
}
func (g *gormStore) UpdateRepo(ctx context.Context, repoID int64, p RepoPatch) (Repo, error) {
	return Repo{}, errNotPorted
}
func (g *gormStore) DeleteRepo(ctx context.Context, repoID int64) error { return errNotPorted }
func (g *gormStore) RepoExists(ctx context.Context, repoID int64) (bool, error) {
	return false, errNotPorted
}
func (g *gormStore) ListRepos(ctx context.Context, viewerID int64, owner *string, limit, offset int) ([]Repo, error) {
	return nil, errNotPorted
}

// Objects returns a treebuild.Store whose every call fails with errNotPorted,
// because the interface has no error return.
func (g *gormStore) Objects(ctx context.Context, repoID int64) treebuild.Store {
	return notPortedObjects{}
}
func (g *gormStore) CommitBundle(ctx context.Context, repoID int64, oid string) (map[string]linediff.Object, bool, error) {
	return nil, false, errNotPorted
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

// notPortedObjects is the treebuild.Store of an unported gormStore.
type notPortedObjects struct{}

func (notPortedObjects) GetObject(oid string) (gitobj.Type, []byte, error) {
	return "", nil, errNotPorted
}
func (notPortedObjects) PutObject(t gitobj.Type, body []byte) (string, error) {
	return "", errNotPorted
}
