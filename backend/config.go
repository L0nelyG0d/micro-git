package main

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// minJWTSecretBytes is the shortest HMAC signing key accepted.
const minJWTSecretBytes = 32

// Config holds process settings read from the environment.
type Config struct {
	Addr        string // listen address, ":" + PORT
	DatabaseURL string
	JWTSecret   string
	// CookieSecure adds the Secure attribute to the session cookie (COOKIE_SECURE=true).
	CookieSecure bool
	Pool         PoolConfig
}

// PoolConfig sizes the pgx connection pool.
type PoolConfig struct {
	MaxConns          int32         // DB_MAX_CONNS, default 10
	MaxConnIdleTime   time.Duration // idle connections are closed after this
	HealthCheckPeriod time.Duration // how often idle connections are checked
}

const (
	defaultMaxConns          = 10
	maxMaxConns              = 1000
	defaultMaxConnIdleTime   = 5 * time.Minute
	defaultHealthCheckPeriod = 30 * time.Second
)

// DefaultPoolConfig is the pool sizing used when the environment sets nothing.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxConns:          defaultMaxConns,
		MaxConnIdleTime:   defaultMaxConnIdleTime,
		HealthCheckPeriod: defaultHealthCheckPeriod,
	}
}

func loadPoolConfig(getenv func(string) string) (PoolConfig, error) {
	pc := DefaultPoolConfig()
	if v := getenv("DB_MAX_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxMaxConns {
			return PoolConfig{}, fmt.Errorf("DB_MAX_CONNS must be a number from 1 to %d, got %q", maxMaxConns, v)
		}
		pc.MaxConns = int32(n)
	}
	return pc, nil
}

// LoadConfig reads settings through getenv (os.Getenv in production) so tests
// can pass a fake.
func LoadConfig(getenv func(string) string) (Config, error) {
	port := getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return Config{}, fmt.Errorf("PORT must be a number from 1 to 65535, got %q", port)
	}
	dbURL := getenv("DATABASE_URL")
	if dbURL == "" {
		return Config{}, errors.New("DATABASE_URL is not set")
	}
	secret := getenv("JWT_SECRET")
	if secret == "" {
		return Config{}, errors.New("JWT_SECRET is not set")
	}
	if len(secret) < minJWTSecretBytes {
		return Config{}, errors.New("JWT_SECRET must be at least 32 bytes")
	}
	pool, err := loadPoolConfig(getenv)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Pool:         pool,
		Addr:         ":" + port,
		DatabaseURL:  dbURL,
		JWTSecret:    secret,
		CookieSecure: getenv("COOKIE_SECURE") == "true",
	}, nil
}
