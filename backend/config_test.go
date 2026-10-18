package main

import (
	"strings"
	"testing"
)

// goodSecret is the shortest JWT_SECRET LoadConfig accepts.
var goodSecret = strings.Repeat("s", 32)

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		env      map[string]string
		wantAddr string
		wantErr  bool
	}{
		{"default port", map[string]string{"DATABASE_URL": "x", "JWT_SECRET": goodSecret}, ":8080", false},
		{"custom port", map[string]string{"DATABASE_URL": "x", "JWT_SECRET": goodSecret, "PORT": "9090"}, ":9090", false},
		{"bad port", map[string]string{"DATABASE_URL": "x", "JWT_SECRET": goodSecret, "PORT": "abc"}, "", true},
		{"port out of range", map[string]string{"DATABASE_URL": "x", "JWT_SECRET": goodSecret, "PORT": "70000"}, "", true},
		{"missing database url", map[string]string{"JWT_SECRET": goodSecret}, "", true},
		{"missing jwt secret", map[string]string{"DATABASE_URL": "x"}, "", true},
		{"jwt secret 31 bytes", map[string]string{"DATABASE_URL": "x", "JWT_SECRET": strings.Repeat("s", 31)}, "", true},
		{"jwt secret 32 bytes", map[string]string{"DATABASE_URL": "x", "JWT_SECRET": strings.Repeat("s", 32)}, ":8080", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := LoadConfig(func(k string) string { return tc.env[k] })
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if cfg.Addr != tc.wantAddr {
				t.Errorf("addr = %q, want %q", cfg.Addr, tc.wantAddr)
			}
		})
	}
}

func TestLoadConfigCookieSecure(t *testing.T) {
	t.Parallel()
	for val, want := range map[string]bool{"": false, "true": true, "false": false, "1": false} {
		env := map[string]string{"DATABASE_URL": "x", "JWT_SECRET": goodSecret, "COOKIE_SECURE": val}
		cfg, err := LoadConfig(func(k string) string { return env[k] })
		if err != nil || cfg.CookieSecure != want || cfg.JWTSecret != goodSecret {
			t.Errorf("COOKIE_SECURE=%q: cfg %+v err %v, want secure=%v", val, cfg, err, want)
		}
	}
}

func TestLoadConfigCookieSecureWarning(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		val      string
		wantWarn bool
	}{{"", true}, {"true", false}, {"false", false}} {
		env := map[string]string{"DATABASE_URL": "x", "JWT_SECRET": goodSecret}
		if tc.val != "" {
			env["COOKIE_SECURE"] = tc.val
		}
		cfg, err := LoadConfig(func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("COOKIE_SECURE=%q: %v", tc.val, err)
		}
		if got := len(cfg.Warnings) > 0; got != tc.wantWarn {
			t.Errorf("COOKIE_SECURE=%q: warnings %q, want warning=%v", tc.val, cfg.Warnings, tc.wantWarn)
		}
		for _, w := range cfg.Warnings {
			if !strings.Contains(w, "COOKIE_SECURE") {
				t.Errorf("warning %q does not name COOKIE_SECURE", w)
			}
			if strings.Contains(w, goodSecret) || strings.Contains(w, "postgres") {
				t.Errorf("warning leaks a secret: %q", w)
			}
		}
	}
}

func TestLoadConfigStorage(t *testing.T) {
	t.Parallel()
	const pg = "postgres://u:p@h/db"
	tests := []struct {
		name       string
		env        map[string]string
		wantErr    string
		wantStore  string
		wantDriver string
		wantURL    string
		wantPath   string
		wantWarn   bool
	}{
		{"default without url", map[string]string{"JWT_SECRET": goodSecret}, "DATABASE_URL is not set", "", "", "", "", false},
		{"postgres with url", map[string]string{"JWT_SECRET": goodSecret, "DATABASE_URL": pg}, "", "db", "postgres", pg, "", false},
		{"explicit db and postgres", map[string]string{"JWT_SECRET": goodSecret, "DATABASE_URL": pg, "STORE": "db", "DB_DRIVER": "postgres"}, "", "db", "postgres", pg, "", false},
		{"sqlite without url", map[string]string{"JWT_SECRET": goodSecret, "DB_DRIVER": "sqlite"}, "", "db", "sqlite", "", "./data/mg.db", false},
		{"sqlite empty path", map[string]string{"JWT_SECRET": goodSecret, "DB_DRIVER": "sqlite", "SQLITE_PATH": ""}, "", "db", "sqlite", "", "./data/mg.db", false},
		{"sqlite custom path", map[string]string{"JWT_SECRET": goodSecret, "DB_DRIVER": "sqlite", "SQLITE_PATH": "/tmp/x.db"}, "", "db", "sqlite", "", "/tmp/x.db", false},
		{"memory with nothing set", map[string]string{"JWT_SECRET": goodSecret, "STORE": "memory"}, "", "memory", "postgres", "", "", true},
		{"bad store", map[string]string{"JWT_SECRET": goodSecret, "DATABASE_URL": pg, "STORE": "bogus"}, `STORE must be one of: db, memory, got "bogus"`, "", "", "", "", false},
		{"bad driver", map[string]string{"JWT_SECRET": goodSecret, "DATABASE_URL": pg, "DB_DRIVER": "mysql"}, `DB_DRIVER must be one of: postgres, sqlite, got "mysql"`, "", "", "", "", false},
		{"bad driver with memory", map[string]string{"JWT_SECRET": goodSecret, "STORE": "memory", "DB_DRIVER": "bogus"}, `DB_DRIVER must be one of: postgres, sqlite, got "bogus"`, "", "", "", "", false},
		{"memory still needs jwt", map[string]string{"STORE": "memory"}, "JWT_SECRET is not set", "", "", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := LoadConfig(func(k string) string { return tc.env[k] })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				if strings.Contains(err.Error(), pg) || strings.Contains(err.Error(), goodSecret) {
					t.Errorf("error leaks a secret: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Store != tc.wantStore || cfg.DBDriver != tc.wantDriver || cfg.DatabaseURL != tc.wantURL || cfg.SQLitePath != tc.wantPath {
				t.Errorf("cfg = %+v", cfg)
			}
			lost := false
			for _, w := range cfg.Warnings {
				if strings.Contains(w, "lost when the process exits") {
					lost = true
				}
				if strings.Contains(w, pg) || strings.Contains(w, goodSecret) {
					t.Errorf("warning leaks a secret: %q", w)
				}
			}
			if lost != tc.wantWarn {
				t.Errorf("memory warning = %v, want %v (%q)", lost, tc.wantWarn, cfg.Warnings)
			}
		})
	}
}

func TestCheckStoreAvailable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"postgres", Config{Store: StoreDB, DBDriver: DriverPostgres}, false},
		{"memory", Config{Store: StoreMemory, DBDriver: DriverPostgres}, false},
		{"memory ignores the db driver", Config{Store: StoreMemory, DBDriver: DriverSQLite}, false},
		{"sqlite", Config{Store: StoreDB, DBDriver: DriverSQLite}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkStoreAvailable(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "not available in this build yet") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

// STORE=memory builds a memStore without a database URL or a pool.
func TestOpenStoreMemory(t *testing.T) {
	t.Parallel()
	st, closeStore, err := openStore(t.Context(), Config{Store: StoreMemory, DBDriver: DriverPostgres})
	if err != nil {
		t.Fatal(err)
	}
	defer closeStore()
	if _, ok := st.(*memStore); !ok {
		t.Errorf("store is %T, want *memStore", st)
	}
}
