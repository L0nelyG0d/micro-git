package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/L0nelyG0d/micro-git/backend/gitobj"
	"github.com/L0nelyG0d/micro-git/backend/linediff"
	"github.com/L0nelyG0d/micro-git/backend/treebuild"
)

// Store errors. Every Store implementation translates its driver's errors into
// these sentinels, so handlers compare only against them (errors.Is) and never
// against driver types.
var (
	// ErrNotFound: the row (user, repository, branch, ...) does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict: a duplicate (unique name) or a lost compare-and-swap.
	ErrConflict = errors.New("conflict")
	// ErrForeignKey: the write points at a row that no longer exists, for
	// example objects of a repository that was deleted meanwhile.
	ErrForeignKey = errors.New("foreign key violation")
	// ErrInvalidOID: an object id that is not 40 lowercase hex characters.
	// Postgres (char(40)) would reject a longer value while SQLite's text
	// column accepts anything, so every store checks it in Go.
	ErrInvalidOID = errors.New("invalid object id")
)

// checkOID returns ErrInvalidOID (wrapped with the offending value, cut to 64
// bytes) unless oid is 40 lowercase hex characters.
func checkOID(oid string) error {
	if gitobj.ValidOID(oid) {
		return nil
	}
	if len(oid) > 64 {
		oid = oid[:64] + "..."
	}
	return fmt.Errorf("%w: %q", ErrInvalidOID, oid)
}

// errDefaultBranch is returned by DeleteNonDefaultRef when the branch is the
// repository's default branch.
var errDefaultBranch = errors.New("default branch")

// Ref is a branch: a name pointing at a commit oid.
type Ref struct {
	Name string
	OID  string
}

// CommitRow is one commit of a first-parent walk: its oid, raw body and the
// first parent named in its header ("" for a root).
type CommitRow struct {
	OID    string
	Data   []byte
	Parent string
}

// Reachability tells, for two commits a and b, whether each is reachable from
// the other (a commit is reachable from itself).
type Reachability struct {
	AFromB bool // a is reachable from b
	BFromA bool // b is reachable from a
}

// ReadHead is what the tree and blob endpoints need up front, fetched in one
// call: the repository, the branch tip named by the ref (HasRef false if there
// is no such branch), and the tip commit with its root tree.
type ReadHead struct {
	Repo    Repo
	HasRef  bool
	Tip     string
	Objects map[string]linediff.Object // tip commit and its root tree, when found
}

// RepoPatch holds the repository fields a PATCH changes; nil means "leave as
// is". Only the supplied fields are written.
type RepoPatch struct {
	Name          *string
	Description   *string
	DefaultBranch *string
	Private       *bool
}

// Store is everything the handlers need from persistence. Groups: users,
// repos, visibility, objects, refs (compare-and-swap commit), history walk,
// ancestry and branches. Every method takes the request context first.
// Errors are the sentinels above (or an unexpected driver error); a method
// that can miss says so.
type Store interface {
	// Users.

	// CreateUser inserts a user and returns its id; ErrConflict if the
	// username is taken.
	CreateUser(ctx context.Context, username, passwordHash string) (int64, error)
	// UsernameByID returns the username; ErrNotFound for an unknown id.
	UsernameByID(ctx context.Context, id int64) (string, error)
	// UserForLogin finds a user by case-insensitive username (pass it
	// lower-cased) and returns the id and password hash; ErrNotFound if none.
	UserForLogin(ctx context.Context, username string) (id int64, passwordHash string, err error)

	// Repos.

	// FindRepo looks a repository up by owner username and name, both
	// case-insensitively; ErrNotFound if either is unknown.
	FindRepo(ctx context.Context, owner, name string) (Repo, error)
	// FindRepoRef is FindRepo plus, in the same call, the branch ref of it,
	// the commit it points at and that commit's root tree. ErrNotFound only
	// for the repository; HasRef tells whether the branch exists.
	FindRepoRef(ctx context.Context, owner, name, ref string) (ReadHead, error)
	// CreateRepo inserts a repository and returns it; ErrConflict if the owner
	// already has one with that name (case-insensitive).
	CreateRepo(ctx context.Context, ownerID int64, name, description string, private bool, createdAt time.Time) (Repo, error)
	// UpdateRepo writes the supplied fields of a repository and returns it.
	// ErrNotFound when the repository is gone or, if DefaultBranch is set,
	// that branch does not exist (checked in the same write); ErrConflict on a
	// duplicate name.
	UpdateRepo(ctx context.Context, repoID int64, p RepoPatch) (Repo, error)
	// DeleteRepo removes a repository with its objects and refs.
	DeleteRepo(ctx context.Context, repoID int64) error
	// RepoExists reports whether the repository still exists.
	RepoExists(ctx context.Context, repoID int64) (bool, error)

	// Visibility. A repository's Private flag is written by CreateRepo and
	// UpdateRepo and read back in Repo. ListRepos applies the rule "private
	// repositories are listed for their owner only".

	// ListRepos returns repositories newest first that viewerID may see
	// (viewerID 0 = anonymous), optionally only those of one owner
	// (case-insensitive), with limit and offset.
	ListRepos(ctx context.Context, viewerID int64, owner *string, limit, offset int) ([]Repo, error)

	// Objects.

	// Objects returns the object store of one repository; objects of other
	// repositories are never visible through it. Its Get methods wrap
	// treebuild.ErrNotFound for a missing object.
	Objects(ctx context.Context, repoID int64) treebuild.Store
	// CommitBundle loads a commit, its first parent and both root trees in
	// one call; found is false when oid is not a commit of the repository.
	CommitBundle(ctx context.Context, repoID int64, oid string) (objs map[string]linediff.Object, found bool, err error)

	// Refs (branches).

	// GetRef returns the oid a branch points at; ErrNotFound.
	GetRef(ctx context.Context, repoID int64, name string) (string, error)
	// GetRefCommit returns the oid a branch points at with that commit's
	// body; ErrNotFound.
	GetRefCommit(ctx context.Context, repoID int64, name string) (string, []byte, error)
	// ListRefs returns all branches sorted by name (bytewise); never nil.
	ListRefs(ctx context.Context, repoID int64) ([]Ref, error)
	// CreateRef adds a branch; ErrConflict if the name is taken.
	CreateRef(ctx context.Context, repoID int64, name, oid string) error
	// DeleteRef removes a branch; ErrNotFound.
	DeleteRef(ctx context.Context, repoID int64, name string) error
	// DeleteNonDefaultRef removes a branch unless it is the repository's
	// default (decided in the same write): ErrNotFound if missing,
	// errDefaultBranch if it is the default.
	DeleteNonDefaultRef(ctx context.Context, repoID int64, name string) error
	// UpdateRef is a compare-and-swap of a branch from oldOID to newOID (empty
	// oldOID = must not exist, creates it); ErrConflict if it lost.
	UpdateRef(ctx context.Context, repoID int64, name, oldOID, newOID string) error
	// ResolveCommit resolves a branch name or a full commit oid to a commit
	// oid (a branch wins); ErrNotFound otherwise.
	ResolveCommit(ctx context.Context, repoID int64, ref string) (string, error)
	// PutObjectsMoveRef stores objs and moves the branch like UpdateRef in one
	// write; ErrConflict if the swap lost (inside InTx the caller must then
	// return the error so the objects are rolled back); ErrForeignKey if the
	// repository is gone.
	PutObjectsMoveRef(ctx context.Context, repoID int64, objs []treebuild.Object, name, oldOID, newOID string) error

	// History walk and ancestry.

	// WalkFirstParents returns the first-parent chain from tip, newest first,
	// at most limit commits; a missing commit wraps treebuild.ErrNotFound.
	WalkFirstParents(ctx context.Context, repoID int64, tip string, limit int) ([]CommitRow, error)
	// CommitReachability tells whether a and b are each reachable from the other.
	CommitReachability(ctx context.Context, repoID int64, a, b string) (Reachability, error)

	// InTx runs fn atomically: everything fn does through the Store it is
	// given commits together, or not at all when fn returns an error.
	InTx(ctx context.Context, fn func(Store) error) error
}
