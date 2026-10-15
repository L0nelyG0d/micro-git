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
		{"default port", map[string]string{"DATABASE_URL": "x"}, ":8080", false},
		{"custom port", map[string]string{"DATABASE_URL": "x", "PORT": "9090"}, ":9090", false},
		{"bad port", map[string]string{"DATABASE_URL": "x", "PORT": "abc"}, "", true},
		{"port out of range", map[string]string{"DATABASE_URL": "x", "PORT": "70000"}, "", true},
		{"missing database url", map[string]string{}, "", true},
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
