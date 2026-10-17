package main

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"
)

// Fake credentials only: none of these may appear in an error or a log line.
const (
	fakePass = "FAKEPASS123"
	fakeUser = "fakeuser"
	fakeHost = "nohost.invalid"
)

// Not parallel: it redirects the global logger.
func TestNewPoolDoesNotLeakDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
	}{
		{"bad port", "postgres://" + fakeUser + ":" + fakePass + "@" + fakeHost + ":notaport/db"},
		{"bad escape in password", "postgres://" + fakeUser + ":FAKE%ZZPASS123@" + fakeHost + "/db"},
		{"keyword form, bad sslmode", "host=" + fakeHost + " user=" + fakeUser + " password=" + fakePass + " dbname=db sslmode=bogus"},
		{"unresolvable host", "postgres://" + fakeUser + ":" + fakePass + "@" + fakeHost + "/db"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logged bytes.Buffer
			orig := log.Writer()
			log.SetOutput(&logged)
			t.Cleanup(func() { log.SetOutput(orig) })

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			pool, err := NewPool(ctx, tc.dsn)
			if err == nil {
				pool.Close()
				t.Fatal("NewPool succeeded on a bad DSN")
			}
			log.Printf("database: %v", err) // what main does before exiting
			for _, out := range []string{err.Error(), logged.String()} {
				for _, secret := range []string{fakePass, "FAKE%ZZ", fakeUser, fakeHost} {
					if strings.Contains(out, secret) {
						t.Errorf("output leaks %q: %s", secret, out)
					}
				}
				if !strings.Contains(out, "DATABASE_URL") {
					t.Errorf("output does not name DATABASE_URL: %s", out)
				}
			}
		})
	}
}

func TestLoadConfigDoesNotLeakSecrets(t *testing.T) {
	t.Parallel()
	jwt := "FAKEJWTSECRET" // shorter than 32 bytes, so LoadConfig errors
	_, err := LoadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://" + fakeUser + ":" + fakePass + "@" + fakeHost + "/db", "JWT_SECRET": jwt}[k]
	})
	if err == nil {
		t.Fatal("want an error for a short JWT_SECRET")
	}
	for _, secret := range []string{jwt, fakePass, fakeUser, fakeHost} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks %q: %v", secret, err)
		}
	}
}
