package main

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// GORM models for the SQLite and Postgres stores. They describe the same
// logical schema as db/schema.sql (the Postgres reference, guarded by
// TestMigrateMatchesSchemaSQL). The names carry a Model suffix because User,
// Repo and Ref are already the API types of this package.

// UserModel is a row of users.
type UserModel struct {
	ID           int64     `gorm:"primaryKey;autoIncrement"`
	Username     string    `gorm:"not null"` // unique on lower(username), see Migrate
	PasswordHash string    `gorm:"not null"`
	CreatedAt    time.Time `gorm:"not null;default:CURRENT_TIMESTAMP"`
}

func (UserModel) TableName() string { return "users" }

// RepoModel is a row of repos. owner_id references users without a cascade,
// as in schema.sql.
type RepoModel struct {
	ID            int64     `gorm:"primaryKey;autoIncrement"`
	OwnerID       int64     `gorm:"not null"`
	Name          string    `gorm:"not null"` // unique per owner on lower(name), see Migrate
	Description   string    `gorm:"not null;default:''"`
	DefaultBranch string    `gorm:"not null;default:main"`
	CreatedAt     time.Time `gorm:"not null;default:CURRENT_TIMESTAMP"`
	Private       bool      `gorm:"not null;default:false"`

	Owner UserModel `gorm:"foreignKey:OwnerID;references:ID"`
}

func (RepoModel) TableName() string { return "repos" }

// ObjectModel is a row of objects: an immutable git object body per repo.
type ObjectModel struct {
	RepoID int64  `gorm:"primaryKey;autoIncrement:false"`
	OID    string `gorm:"column:oid;primaryKey;type:char(40);not null"`
	Type   string `gorm:"not null;check:objects_type_check,type IN ('blob', 'tree', 'commit')"`
	Data   []byte `gorm:"not null"`

	Repo RepoModel `gorm:"foreignKey:RepoID;references:ID;constraint:OnDelete:CASCADE"`
	// Refs declares refs (repo_id, oid) -> objects (repo_id, oid), no cascade.
	// It sits on this side because GORM misreads the composite key as a
	// has-one when it is declared on RefModel.
	Refs []RefModel `gorm:"foreignKey:RepoID,OID;references:RepoID,OID"`
}

func (ObjectModel) TableName() string { return "objects" }

// RefModel is a row of refs: a branch pointing at a stored object.
type RefModel struct {
	RepoID int64  `gorm:"primaryKey;autoIncrement:false"`
	Name   string `gorm:"primaryKey"`
	OID    string `gorm:"column:oid;type:char(40);not null"`

	Repo RepoModel `gorm:"foreignKey:RepoID;references:ID;constraint:OnDelete:CASCADE"`
}

func (RefModel) TableName() string { return "refs" }

// migrateIndexes are the indexes GORM tags cannot express (expressions and
// descending order). The statement text is identical on SQLite and Postgres.
var migrateIndexes = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_idx ON users (lower(username))`,
	`CREATE UNIQUE INDEX IF NOT EXISTS repos_owner_name_lower_idx ON repos (owner_id, lower(name))`,
	`CREATE INDEX IF NOT EXISTS repos_created_idx ON repos (created_at DESC, id DESC)`,
}

// Migrate creates the tables and indexes on an empty database and is a no-op
// on an up-to-date one. It works on SQLite and Postgres.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(&UserModel{}, &RepoModel{}, &ObjectModel{}, &RefModel{}); err != nil {
		return fmt.Errorf("migrate tables: %w", err)
	}
	for _, stmt := range migrateIndexes {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("migrate index: %w", err)
		}
	}
	return nil
}
