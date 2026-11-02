package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// sqliteDDL is the schema GORM creates on SQLite (backend Migrate), copied
// from a real database. Only the tables and columns matter to the copy.
var sqliteDDL = []string{
	"CREATE TABLE `users` (`id` integer PRIMARY KEY AUTOINCREMENT,`username` text NOT NULL,`password_hash` text NOT NULL,`created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP)",
	"CREATE UNIQUE INDEX users_username_lower_idx ON users (lower(username))",
	"CREATE TABLE `repos` (`id` integer PRIMARY KEY AUTOINCREMENT,`owner_id` integer NOT NULL,`name` text NOT NULL,`description` text NOT NULL DEFAULT \"\",`default_branch` text NOT NULL DEFAULT \"main\",`created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,`private` numeric NOT NULL DEFAULT false,CONSTRAINT `fk_repos_owner` FOREIGN KEY (`owner_id`) REFERENCES `users`(`id`))",
	"CREATE UNIQUE INDEX repos_owner_name_lower_idx ON repos (owner_id, lower(name))",
	"CREATE INDEX repos_created_idx ON repos (created_at DESC, id DESC)",
	"CREATE TABLE `objects` (`repo_id` integer,`oid` text NOT NULL,`type` text NOT NULL,`data` blob NOT NULL,PRIMARY KEY (`repo_id`,`oid`),CONSTRAINT `fk_objects_repo` FOREIGN KEY (`repo_id`) REFERENCES `repos`(`id`) ON DELETE CASCADE,CONSTRAINT `objects_type_check` CHECK (type IN ('blob', 'tree', 'commit')))",
	"CREATE TABLE `refs` (`repo_id` integer,`name` text,`oid` text NOT NULL,PRIMARY KEY (`repo_id`,`name`),CONSTRAINT `fk_refs_repo` FOREIGN KEY (`repo_id`) REFERENCES `repos`(`id`) ON DELETE CASCADE,CONSTRAINT `fk_objects_refs` FOREIGN KEY (`repo_id`,`oid`) REFERENCES `objects`(`repo_id`,`oid`))",
}

// zeroOID is a well-formed 40-character object id for the fixture.
var zeroOID = strings.Repeat("0", 39) + "1"

// fixture writes a SQLite file with two users, two repos (one private), three
// objects and two refs, and returns its path.
func fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mg.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range sqliteDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	created := "2026-10-01 12:00:00.123456789+00:00"
	for _, stmt := range []string{
		"INSERT INTO users (id, username, password_hash, created_at) VALUES (1, 'alice', 'hash-a', '" + created + "'), (2, 'bob', 'hash-b', '2026-10-02 08:30:00+00:00')",
		"INSERT INTO repos (id, owner_id, name, description, default_branch, created_at, private) VALUES (1, 1, 'notes', 'my notes', 'main', '" + created + "', 0), (2, 2, 'secret', '', 'dev', '2026-10-03 09:00:00+00:00', 1)",
		"INSERT INTO objects (repo_id, oid, type, data) VALUES (1, 'e69de29bb2d1d6434b8b29ae775ad8c2e48c5391', 'blob', x''), (1, 'd670460b4b4aece5915caf5c68d12f560a9fe3e4', 'blob', x'7465737420636f6e74656e740a'), (2, '" + zeroOID + "', 'commit', x'74726565')",
		"INSERT INTO refs (repo_id, name, oid) VALUES (1, 'main', 'd670460b4b4aece5915caf5c68d12f560a9fe3e4'), (2, 'dev', '" + zeroOID + "')",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("insert %q: %v", stmt, err)
		}
	}
	return path
}

// pgTarget returns a URL of a fresh, schema-applied database. It is skipped
// when TEST_DATABASE_URL is unset.
func pgTarget(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set; the migration test needs a Postgres")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	defer admin.Close()
	name := fmt.Sprintf("mg_migrate_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgxpool.New(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close()
		c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	target := withDatabase(t, base, name)
	pool, err := pgxpool.New(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	schema, err := os.ReadFile("../../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(schema)); err != nil {
		t.Fatalf("apply schema.sql: %v", err)
	}
	return target
}

// withDatabase returns base with its database name replaced by name.
func withDatabase(t *testing.T, base, name string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestMigrateSQLiteToPostgres(t *testing.T) {
	src := fixture(t)
	dst := pgTarget(t)
	ctx := context.Background()

	var out bytes.Buffer
	if err := run(ctx, env(map[string]string{"SQLITE_PATH": src, "DATABASE_URL": dst}), false, &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	for _, want := range []string{"users", "repos", "objects", "refs", "migration committed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), dst) {
		t.Error("output contains the connection string")
	}

	pool, err := pgxpool.New(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Row counts and values survive the copy.
	want := map[string]int{"users": 2, "repos": 2, "objects": 3, "refs": 2}
	for tb, n := range want {
		var got int
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+tb).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != n {
			t.Errorf("%s: %d rows, want %d", tb, got, n)
		}
	}
	var private bool
	var owner int64
	var created time.Time
	if err := pool.QueryRow(ctx, "SELECT owner_id, private, created_at FROM repos WHERE id = 2").Scan(&owner, &private, &created); err != nil {
		t.Fatal(err)
	}
	if owner != 2 || !private {
		t.Errorf("repo 2: owner %d private %v, want owner 2 private true", owner, private)
	}
	if !created.Equal(time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("repo 2 created_at = %v", created)
	}
	var data []byte
	if err := pool.QueryRow(ctx, "SELECT data FROM objects WHERE repo_id = 1 AND oid = 'd670460b4b4aece5915caf5c68d12f560a9fe3e4'").Scan(&data); err != nil {
		t.Fatal(err)
	}
	if string(data) != "test content\n" {
		t.Errorf("blob data = %q", data)
	}

	// The identity sequences continue after the copied ids.
	var id int64
	if err := pool.QueryRow(ctx, "INSERT INTO users (username, password_hash) VALUES ('carol', 'h') RETURNING id").Scan(&id); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}
	if id != 3 {
		t.Errorf("next user id = %d, want 3", id)
	}

	// The copied refs satisfy the composite foreign key (checked by the schema).
	if _, err := pool.Exec(ctx, "INSERT INTO refs (repo_id, name, oid) VALUES (1, 'bad', repeat('f', 40))"); err == nil {
		t.Error("ref to a missing object was accepted")
	}
}

func TestMigrateRefusesNonEmptyTargetUnlessForced(t *testing.T) {
	src := fixture(t)
	dst := pgTarget(t)
	ctx := context.Background()
	e := env(map[string]string{"SQLITE_PATH": src, "DATABASE_URL": dst})

	if err := run(ctx, e, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	err := run(ctx, e, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("second run without --force: err = %v, want refusal", err)
	}

	// --force replaces the data: counts equal the source, not doubled.
	var out bytes.Buffer
	if err := run(ctx, e, true, &out); err != nil {
		t.Fatalf("forced run: %v\n%s", err, out.String())
	}
	pool, err := pgxpool.New(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var users int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 2 {
		t.Errorf("users after --force = %d, want 2", users)
	}
}

func TestMigrateRequiresTargetSchema(t *testing.T) {
	src := fixture(t)
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set; the migration test needs a Postgres")
	}
	ctx := context.Background()
	// A fresh database with no tables: the migration must refuse before writing anything.
	name := fmt.Sprintf("mg_migrate_empty_%d_%d", os.Getpid(), time.Now().UnixNano())
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	admin.Close()
	t.Cleanup(func() {
		c, err := pgxpool.New(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close()
		c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	err = run(ctx, env(map[string]string{"SQLITE_PATH": src, "DATABASE_URL": withDatabase(t, base, name)}), false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "schema.sql") {
		t.Fatalf("err = %v, want a schema.sql refusal", err)
	}
}

func TestMigrateMissingSQLiteFile(t *testing.T) {
	err := run(context.Background(), env(map[string]string{
		"SQLITE_PATH":  filepath.Join(t.TempDir(), "absent.db"),
		"DATABASE_URL": "postgres://nobody@127.0.0.1:1/none?sslmode=disable",
	}), false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "SQLITE_PATH") {
		t.Fatalf("err = %v, want a missing-file error before any connection", err)
	}
}
