package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLoadConfigApplySchema(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]bool{"": false, "false": false, "true": true} {
		cfg, err := LoadConfig(func(k string) string {
			return map[string]string{"DATABASE_URL": "x", "JWT_SECRET": goodSecret, "APPLY_SCHEMA": v}[k]
		})
		if err != nil || cfg.ApplySchema != want {
			t.Errorf("APPLY_SCHEMA=%q: got %v, %v", v, cfg.ApplySchema, err)
		}
	}
}

func TestSchemaEmbedded(t *testing.T) {
	t.Parallel()
	if !strings.Contains(schemaSQL, "CREATE TABLE IF NOT EXISTS users") {
		t.Fatal("embedded schema missing the users table")
	}
}

// ApplySchema must work on a fresh schema, be safe to run twice, and produce
// the late additions (private column, refs_object_fk). It runs in a temporary
// schema selected through search_path, so it never touches real tables.
// tempSchemaPool returns a pool whose search_path is a fresh, empty schema that
// is dropped when the test ends.
func tempSchemaPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := NewPool(ctx, raw, DefaultPoolConfig())
	if err != nil {
		t.Fatal("NewPool failed (details hidden)")
	}
	schema := fmt.Sprintf("schema_apply_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal("bad TEST_DATABASE_URL (details hidden)")
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	pool, err := NewPool(ctx, u.String(), DefaultPoolConfig())
	if err != nil {
		t.Fatal("NewPool failed (details hidden)")
	}
	t.Cleanup(pool.Close)
	return pool, schema
}

func TestApplySchemaIdempotent(t *testing.T) {
	ctx := context.Background()
	pool, schema := tempSchemaPool(t)
	for i := 0; i < 2; i++ {
		if err := ApplySchema(ctx, pool); err != nil {
			t.Fatalf("apply #%d: %v", i+1, err)
		}
	}
	var tables, priv, fk int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name IN ('users','repos','objects','refs')`, schema).Scan(&tables); err != nil || tables != 4 {
		t.Fatalf("tables = %d, err %v", tables, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'repos' AND column_name = 'private'`, schema).Scan(&priv); err != nil || priv != 1 {
		t.Fatalf("private column = %d, err %v", priv, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conname = 'refs_object_fk' AND conrelid = 'refs'::regclass`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("refs_object_fk = %d, err %v", fk, err)
	}
}

// Two instances starting together both succeed (the advisory lock serialises them).
func TestApplySchemaConcurrent(t *testing.T) {
	pool, _ := tempSchemaPool(t)
	ctx := context.Background()
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- ApplySchema(ctx, pool) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent apply: %v", err)
		}
	}
}

// Re-applying on a live database must not wait behind ordinary traffic: an open
// INSERT into users and an open SELECT on repos must not block it.
func TestApplySchemaDoesNotBlockOnTraffic(t *testing.T) {
	pool, _ := tempSchemaPool(t)
	ctx := context.Background()
	if err := ApplySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ins, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ins.Rollback(ctx)
	if _, err := ins.Exec(ctx, `INSERT INTO users (username, password_hash) VALUES ('u', 'h')`); err != nil {
		t.Fatal(err)
	}
	sel, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sel.Rollback(ctx)
	if _, err := sel.Exec(ctx, `SELECT * FROM repos`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := ApplySchema(ctx, pool); err != nil {
		t.Fatalf("apply with open transactions: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("apply took %v with open transactions", d)
	}
}

// The index guards must look at the table's own indexes: a same-named index in
// another schema on the search_path must not stop ours from being created.
func TestApplySchemaIndexGuardScopedToTable(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := NewPool(ctx, raw, DefaultPoolConfig())
	if err != nil {
		t.Fatal("NewPool failed (details hidden)")
	}
	defer admin.Close()
	schema := fmt.Sprintf("schema_guard_%d", time.Now().UnixNano())
	other := schema + "_pub"
	for _, s := range []string{schema, other} {
		if _, err := admin.Exec(ctx, "CREATE SCHEMA "+s); err != nil {
			t.Fatalf("create schema: %v", err)
		}
		defer admin.Exec(ctx, "DROP SCHEMA "+s+" CASCADE")
	}
	// Decoys with the guarded index names in the later schema on the path.
	for _, q := range []string{
		"CREATE TABLE " + other + ".decoy (a text, b text, c timestamptz)",
		"CREATE INDEX users_username_lower_idx ON " + other + ".decoy (a)",
		"CREATE INDEX repos_owner_name_lower_idx ON " + other + ".decoy (b)",
		"CREATE INDEX repos_created_idx ON " + other + ".decoy (c)",
	} {
		if _, err := admin.Exec(ctx, q); err != nil {
			t.Fatalf("decoy: %v", err)
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal("bad TEST_DATABASE_URL (details hidden)")
	}
	q := u.Query()
	q.Set("search_path", schema+","+other)
	u.RawQuery = q.Encode()
	pool, err := NewPool(ctx, u.String(), DefaultPoolConfig())
	if err != nil {
		t.Fatal("NewPool failed (details hidden)")
	}
	defer pool.Close()
	for i := 0; i < 2; i++ {
		if err := ApplySchema(ctx, pool); err != nil {
			t.Fatalf("apply #%d: %v", i+1, err)
		}
	}
	var n int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = $1 AND indexname IN ('users_username_lower_idx','repos_owner_name_lower_idx','repos_created_idx')`, schema).Scan(&n)
	if err != nil || n != 3 {
		t.Fatalf("indexes in %s = %d, err %v (want 3)", schema, n, err)
	}
}

// Lock order: the advisory lock is taken before lock_timeout and the statement
// timeout is raised, so waiting longer than lock_timeout (3s) behind another
// instance must still succeed.
func TestApplySchemaWaitsForAdvisoryLock(t *testing.T) {
	pool, _ := tempSchemaPool(t)
	ctx := context.Background()
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(schemaLockID)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- ApplySchema(ctx, pool) }()
	time.Sleep(4 * time.Second)
	holder.Rollback(ctx)
	if err := <-done; err != nil {
		t.Fatalf("apply after waiting on the advisory lock: %v", err)
	}
	if d := time.Since(start); d < 3*time.Second {
		t.Fatalf("apply finished in %v, did not wait for the lock", d)
	}
}
