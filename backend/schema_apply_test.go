package main

import (
	"context"
	"os"
	"strings"
	"testing"
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

// ApplySchema must be safe to run twice and must leave the tables usable.
func TestApplySchemaIdempotent(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := NewPool(ctx, url, DefaultPoolConfig())
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
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name IN ('users','repos','objects','refs')`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("tables = %d, err %v", n, err)
	}
}
