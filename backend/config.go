package main

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// minJWTSecretBytes is the shortest HMAC signing key accepted.
const minJWTSecretBytes = 32

const (
	StoreDB     = "db"
	StoreMemory = "memory"

	DriverPostgres = "postgres"
	DriverSQLite   = "sqlite"

	defaultSQLitePath = "./data/mg.db"
)

// Config holds process settings read from the environment.
type Config struct {
	Addr        string // listen address, ":" + PORT
	DatabaseURL string // only set when Store is "db" and DBDriver is "postgres"
	// Store is STORE: "db" (default) or "memory".
	Store string
	// DBDriver is DB_DRIVER: "postgres" (default) or "sqlite". Validated even
	// when Store is "memory".
	DBDriver string
	// SQLitePath is SQLITE_PATH, only set when DBDriver is "sqlite".
	SQLitePath string
	JWTSecret  string
	// CookieSecure adds the Secure attribute to the session cookie (COOKIE_SECURE=true).
	CookieSecure bool
	Pool         PoolConfig
	// Warnings are startup notices about risky defaults. They never contain
	// secret values; main logs them.
	Warnings []string
}

// PoolConfig sizes the pgx connection pool.
type PoolConfig struct {
	MaxConns          int32         // DB_MAX_CONNS, default 10
	MaxConnIdleTime   time.Duration // idle connections are closed after this
	HealthCheckPeriod time.Duration // how often idle connections are checked
	// StatementTimeout is the Postgres statement_timeout set on every pooled
	// connection (DB_STATEMENT_TIMEOUT, a Go duration such as "15s").
	StatementTimeout time.Duration
}

const (
	defaultMaxConns          = 10
	maxMaxConns              = 1000
	defaultMaxConnIdleTime   = 5 * time.Minute
	defaultHealthCheckPeriod = 30 * time.Second
	defaultStatementTimeout  = 15 * time.Second
	minStatementTimeout      = time.Millisecond
	maxStatementTimeout      = time.Hour
)

// DefaultPoolConfig is the pool sizing used when the environment sets nothing.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxConns:          defaultMaxConns,
		MaxConnIdleTime:   defaultMaxConnIdleTime,
		HealthCheckPeriod: defaultHealthCheckPeriod,
		StatementTimeout:  defaultStatementTimeout,
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
	if v := getenv("DB_STATEMENT_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < minStatementTimeout || d > maxStatementTimeout {
			return PoolConfig{}, fmt.Errorf("DB_STATEMENT_TIMEOUT must be a duration from 1ms to 1h such as \"15s\", got %q", v)
		}
		pc.StatementTimeout = d
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
	store := getenv("STORE")
	if store == "" {
		store = StoreDB
	}
	if store != StoreDB && store != StoreMemory {
		return Config{}, fmt.Errorf("STORE must be one of: db, memory, got %q", store)
	}
	driver := getenv("DB_DRIVER")
	if driver == "" {
		driver = DriverPostgres
	}
	if driver != DriverPostgres && driver != DriverSQLite {
		return Config{}, fmt.Errorf("DB_DRIVER must be one of: postgres, sqlite, got %q", driver)
	}
	var dbURL, sqlitePath string
	if store == StoreDB {
		switch driver {
		case DriverPostgres:
			dbURL = getenv("DATABASE_URL")
			if dbURL == "" {
				return Config{}, errors.New("DATABASE_URL is not set")
			}
		case DriverSQLite:
			sqlitePath = getenv("SQLITE_PATH")
			if sqlitePath == "" {
				sqlitePath = defaultSQLitePath
			}
		}
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
	var warnings []string
	if getenv("COOKIE_SECURE") == "" {
		warnings = append(warnings, "COOKIE_SECURE is not set: the session cookie is sent without the Secure attribute. Set COOKIE_SECURE=true when serving over HTTPS")
	}
	if store == StoreMemory {
		warnings = append(warnings, "STORE=memory: all data is lost when the process exits")
	}
	return Config{
		Store:        store,
		DBDriver:     driver,
		SQLitePath:   sqlitePath,
		Warnings:     warnings,
		Pool:         pool,
		Addr:         ":" + port,
		DatabaseURL:  dbURL,
		JWTSecret:    secret,
		CookieSecure: getenv("COOKIE_SECURE") == "true",
	}, nil
}
