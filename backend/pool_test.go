package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLoadConfigPool(t *testing.T) {
	t.Parallel()
	base := map[string]string{"DATABASE_URL": "x", "JWT_SECRET": goodSecret}
	with := func(v string) func(string) string {
		return func(k string) string {
			if k == "DB_MAX_CONNS" {
				return v
			}
			return base[k]
		}
	}
	cfg, err := LoadConfig(with(""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pool != DefaultPoolConfig() || cfg.Pool.MaxConns != 10 {
		t.Errorf("default pool = %+v", cfg.Pool)
	}
	if cfg.Pool.MaxConnIdleTime <= 0 || cfg.Pool.HealthCheckPeriod <= 0 {
		t.Errorf("idle time and health check period must be set: %+v", cfg.Pool)
	}
	cfg, err = LoadConfig(with("25"))
	if err != nil || cfg.Pool.MaxConns != 25 {
		t.Errorf("DB_MAX_CONNS=25: cfg=%+v err=%v", cfg.Pool, err)
	}
	for _, bad := range []string{"0", "-1", "abc", "1001", "2.5"} {
		if _, err := LoadConfig(with(bad)); err == nil {
			t.Errorf("DB_MAX_CONNS=%q accepted", bad)
		}
	}
}

func TestNewPoolAppliesSettings(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pc := PoolConfig{MaxConns: 3, MaxConnIdleTime: time.Minute, HealthCheckPeriod: 7 * time.Second}
	pool, err := NewPool(context.Background(), url, pc)
	if err != nil {
		t.Fatal("NewPool failed (details hidden)")
	}
	defer pool.Close()
	got := pool.Config()
	if got.MaxConns != 3 || got.MaxConnIdleTime != time.Minute || got.HealthCheckPeriod != 7*time.Second {
		t.Errorf("pool settings not applied: %d %v %v", got.MaxConns, got.MaxConnIdleTime, got.HealthCheckPeriod)
	}
}
