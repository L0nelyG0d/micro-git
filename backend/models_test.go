package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openSQLiteForTest(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mg.db")
	db, err := gorm.Open(sqlite.Open(path+"?_pragma=foreign_keys(1)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return db
}

// pgDSN returns TEST_DATABASE_URL with search_path set to schema.
func pgDSN(t *testing.T, schema string) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres migration test")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// openPostgresForTest opens a connection on a fresh, empty schema that is
// dropped on cleanup, so the test never touches the public schema.
func openPostgresForTest(t *testing.T, label string) (*gorm.DB, string) {
	t.Helper()
	schema := fmt.Sprintf("mig_%s_%d", label, time.Now().UnixNano())
	admin, err := gorm.Open(postgres.Open(pgDSN(t, "public")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := admin.Exec(`CREATE SCHEMA ` + schema).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	db, err := gorm.Open(postgres.Open(pgDSN(t, schema)), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() {
		admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		for _, d := range []*gorm.DB{db, admin} {
			if sqlDB, err := d.DB(); err == nil {
				sqlDB.Close()
			}
		}
	})
	return db, schema
}

type dialectCase struct {
	name string
	open func(t *testing.T) *gorm.DB
}

var dialects = []dialectCase{
	{"sqlite", openSQLiteForTest},
	{"postgres", func(t *testing.T) *gorm.DB { db, _ := openPostgresForTest(t, "m"); return db }},
}

// snapshot returns a sorted, comparable description of every table and index.
func snapshot(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var out []string
	if db.Dialector.Name() == "sqlite" {
		var rows []struct{ Type, Name, SQL string }
		if err := db.Raw(`SELECT type, name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name`).Scan(&rows).Error; err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		for _, r := range rows {
			out = append(out, r.Type+" "+r.Name+": "+r.SQL)
		}
		return out
	}
	return pgSnapshot(t, db)
}

func TestMigrateTwiceKeepsSchema(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			db := d.open(t)
			if err := Migrate(db); err != nil {
				t.Fatalf("first Migrate: %v", err)
			}
			before := snapshot(t, db)
			if len(before) == 0 {
				t.Fatal("Migrate created nothing")
			}
			if err := Migrate(db); err != nil {
				t.Fatalf("second Migrate: %v", err)
			}
			after := snapshot(t, db)
			if strings.Join(before, "\n") != strings.Join(after, "\n") {
				t.Errorf("schema changed on second Migrate\nbefore:\n%s\nafter:\n%s", strings.Join(before, "\n"), strings.Join(after, "\n"))
			}
		})
	}
}

// TestMigratedConstraints checks the behavior the schema promises, on both drivers.
func TestMigratedConstraints(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			db := d.open(t)
			if err := Migrate(db); err != nil {
				t.Fatal(err)
			}
			u := UserModel{Username: "Alice", PasswordHash: "x"}
			if err := db.Create(&u).Error; err != nil {
				t.Fatalf("create user: %v", err)
			}
			if u.ID == 0 || u.CreatedAt.IsZero() {
				t.Errorf("id/created_at not set: %+v", u)
			}
			if err := db.Create(&UserModel{Username: "aLICE", PasswordHash: "y"}).Error; err == nil {
				t.Error("username unique should be case-insensitive")
			}
			r := RepoModel{OwnerID: u.ID, Name: "Demo"}
			if err := db.Create(&r).Error; err != nil {
				t.Fatalf("create repo: %v", err)
			}
			var got RepoModel
			if err := db.First(&got, r.ID).Error; err != nil {
				t.Fatal(err)
			}
			if got.DefaultBranch != "main" || got.Private || got.Description != "" {
				t.Errorf("defaults wrong: %+v", got)
			}
			if err := db.Create(&RepoModel{OwnerID: u.ID, Name: "dEMO"}).Error; err == nil {
				t.Error("repo name unique per owner should be case-insensitive")
			}
			if err := db.Create(&RepoModel{OwnerID: u.ID + 999, Name: "orphan"}).Error; err == nil {
				t.Error("repos.owner_id must reference users")
			}
			oid := strings.Repeat("a", 40)
			if err := db.Create(&ObjectModel{RepoID: r.ID, OID: oid, Type: "bogus", Data: []byte("x")}).Error; err == nil {
				t.Error("objects.type CHECK should reject bogus")
			}
			if err := db.Create(&ObjectModel{RepoID: r.ID, OID: oid, Type: "commit", Data: []byte{0, 1, 2}}).Error; err != nil {
				t.Fatalf("create object: %v", err)
			}
			var obj ObjectModel
			if err := db.First(&obj, "repo_id = ? AND oid = ?", r.ID, oid).Error; err != nil || string(obj.Data) != "\x00\x01\x02" {
				t.Errorf("object round trip: %v %v", err, obj.Data)
			}
			if err := db.Create(&ObjectModel{RepoID: r.ID, OID: oid, Type: "blob", Data: []byte("x")}).Error; err == nil {
				t.Error("objects primary key (repo_id, oid) should reject a duplicate")
			}
			if err := db.Create(&RefModel{RepoID: r.ID, Name: "main", OID: strings.Repeat("b", 40)}).Error; err == nil {
				t.Error("refs (repo_id, oid) must reference objects")
			}
			if err := db.Create(&RefModel{RepoID: r.ID, Name: "main", OID: oid}).Error; err != nil {
				t.Fatalf("create ref: %v", err)
			}
			// Deleting the owner of a repo is refused (no cascade on owner_id).
			if err := db.Delete(&UserModel{}, u.ID).Error; err == nil {
				t.Error("deleting a user that owns repos should fail")
			}
			// Deleting the repo cascades to refs and objects.
			if err := db.Delete(&RepoModel{}, r.ID).Error; err != nil {
				t.Fatalf("delete repo: %v", err)
			}
			var n int64
			db.Model(&ObjectModel{}).Count(&n)
			var m int64
			db.Model(&RefModel{}).Count(&m)
			if n != 0 || m != 0 {
				t.Errorf("cascade left %d objects, %d refs", n, m)
			}
		})
	}
}

// pgSnapshot describes every table in the connection's search_path schema:
// columns (type, nullability), constraints (PK, FK, unique, check) and indexes.
// Names of constraints are left out so GORM's and schema.sql's naming schemes
// compare by definition; schema-qualified names are stripped.
func pgSnapshot(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var out []string
	var cols []struct {
		Tbl, Col, Typ string
		Notnull       bool
		Identity      string
		Def           *string
	}
	err := db.Raw(`
		SELECT c.relname AS tbl, a.attname AS col, format_type(a.atttypid, a.atttypmod) AS typ,
		       a.attnotnull AS notnull, a.attidentity AS identity, pg_get_expr(d.adbin, d.adrelid) AS def
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE c.relnamespace = current_schema()::regnamespace AND c.relkind = 'r'
		  AND a.attnum > 0 AND NOT a.attisdropped`).Scan(&cols).Error
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	for _, c := range cols {
		def := ""
		if c.Def != nil {
			def = *c.Def
		}
		out = append(out, fmt.Sprintf("column %s.%s %s notnull=%v identity=%q default=%s", c.Tbl, c.Col, c.Typ, c.Notnull, c.Identity, strings.ReplaceAll(def, "'::text", "'")))
	}
	var cons []struct{ Tbl, Def string }
	err = db.Raw(`
		SELECT c.relname AS tbl, pg_get_constraintdef(k.oid) AS def
		FROM pg_constraint k JOIN pg_class c ON c.oid = k.conrelid
		WHERE c.relnamespace = current_schema()::regnamespace`).Scan(&cons).Error
	if err != nil {
		t.Fatalf("constraints: %v", err)
	}
	for _, c := range cons {
		out = append(out, "constraint "+c.Tbl+" "+c.Def)
	}
	var idx []struct{ Def string }
	if err := db.Raw(`SELECT indexdef AS def FROM pg_indexes WHERE schemaname = current_schema()`).Scan(&idx).Error; err != nil {
		t.Fatalf("indexes: %v", err)
	}
	schema := ""
	db.Raw(`SELECT current_schema()`).Scan(&schema)
	for _, i := range idx {
		out = append(out, "index "+strings.ReplaceAll(i.Def, schema+".", ""))
	}
	sort.Strings(out)
	return out
}

// parityAllowlist lists every known difference between the schema Migrate
// builds and db/schema.sql. Keys are lines as printed by pgSnapshot for one
// side; the value is the reason. Both sides are listed when they differ.
var parityAllowlist = map[string]string{
	// GORM creates auto-increment keys as a serial (sequence default); schema.sql uses GENERATED ALWAYS AS IDENTITY.
	`column users.id bigint notnull=true identity="a" default=`:                                 "identity vs serial, same behavior for inserts that omit id",
	`column users.id bigint notnull=true identity="" default=nextval('users_id_seq'::regclass)`: "identity vs serial, same behavior for inserts that omit id",
	`column repos.id bigint notnull=true identity="a" default=`:                                 "identity vs serial, same behavior for inserts that omit id",
	`column repos.id bigint notnull=true identity="" default=nextval('repos_id_seq'::regclass)`: "identity vs serial, same behavior for inserts that omit id",
	// created_at defaults: now() does not exist on SQLite, CURRENT_TIMESTAMP is the portable spelling.
	`column users.created_at timestamp with time zone notnull=true identity="" default=now()`:             "now() vs CURRENT_TIMESTAMP, same value",
	`column users.created_at timestamp with time zone notnull=true identity="" default=CURRENT_TIMESTAMP`: "now() vs CURRENT_TIMESTAMP, same value",
	`column repos.created_at timestamp with time zone notnull=true identity="" default=now()`:             "now() vs CURRENT_TIMESTAMP, same value",
	`column repos.created_at timestamp with time zone notnull=true identity="" default=CURRENT_TIMESTAMP`: "now() vs CURRENT_TIMESTAMP, same value",
}

func TestMigrateMatchesSchemaSQL(t *testing.T) {
	migrated, _ := openPostgresForTest(t, "gorm")
	if err := Migrate(migrated); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	_, refSchema := openPostgresForTest(t, "ref")
	sqlText, err := os.ReadFile("db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(context.Background(), pgDSN(t, refSchema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	// schema.sql looks up refs_object_fk in pg_constraint across all schemas, so
	// a copy of the constraint in another schema would make it skip the ALTER.
	// Scope the lookup to the test schema.
	ddl := strings.Replace(string(sqlText), "conname = 'refs_object_fk'", "conname = 'refs_object_fk' AND connamespace = current_schema()::regnamespace", 1)
	if ddl == string(sqlText) {
		t.Fatal("schema.sql no longer has the refs_object_fk lookup this test patches")
	}
	if _, err := conn.Exec(context.Background(), ddl); err != nil {
		t.Fatalf("apply schema.sql: %v", err)
	}
	reference, err := gorm.Open(postgres.Open(pgDSN(t, refSchema)), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := reference.DB(); err == nil {
			sqlDB.Close()
		}
	})

	got := toSet(pgSnapshot(t, migrated))
	want := toSet(pgSnapshot(t, reference))
	for line := range want {
		if !got[line] {
			if _, ok := parityAllowlist[line]; !ok {
				t.Errorf("in schema.sql but not in Migrate output:\n  %s", line)
			}
		}
	}
	for line := range got {
		if !want[line] {
			if _, ok := parityAllowlist[line]; !ok {
				t.Errorf("in Migrate output but not in schema.sql:\n  %s", line)
			}
		}
	}
	for line := range parityAllowlist {
		if got[line] == want[line] {
			t.Errorf("stale allowlist entry (no longer a difference): %s", line)
		}
	}
}

func toSet(lines []string) map[string]bool {
	m := make(map[string]bool, len(lines))
	for _, l := range lines {
		m[l] = true
	}
	return m
}
