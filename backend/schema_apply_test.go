package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
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
func TestApplySchemaIdempotent(t *testing.T) {
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
	schema := fmt.Sprintf("schema_apply_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")

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
	defer pool.Close()
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
