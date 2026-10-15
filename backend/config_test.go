package main

import "testing"

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		env      map[string]string
		wantAddr string
		wantErr  bool
	}{
		{"default port", map[string]string{"DATABASE_URL": "x", "JWT_SECRET": "s"}, ":8080", false},
		{"custom port", map[string]string{"DATABASE_URL": "x", "JWT_SECRET": "s", "PORT": "9090"}, ":9090", false},
		{"bad port", map[string]string{"DATABASE_URL": "x", "JWT_SECRET": "s", "PORT": "abc"}, "", true},
		{"port out of range", map[string]string{"DATABASE_URL": "x", "JWT_SECRET": "s", "PORT": "70000"}, "", true},
		{"missing database url", map[string]string{"JWT_SECRET": "s"}, "", true},
		{"missing jwt secret", map[string]string{"DATABASE_URL": "x"}, "", true},
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
		env := map[string]string{"DATABASE_URL": "x", "JWT_SECRET": "s", "COOKIE_SECURE": val}
		cfg, err := LoadConfig(func(k string) string { return env[k] })
		if err != nil || cfg.CookieSecure != want || cfg.JWTSecret != "s" {
			t.Errorf("COOKIE_SECURE=%q: cfg %+v err %v, want secure=%v", val, cfg, err, want)
		}
	}
}
