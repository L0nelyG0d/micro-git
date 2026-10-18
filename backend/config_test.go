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
