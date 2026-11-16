package main

import (
	"errors"
	"fmt"
	"net"
	"regexp"
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
	// MetricsAddr is METRICS_ADDR: a separate listener for /metrics ("" = none).
	// Its port never equals PORT.
	MetricsAddr string
	// ApplySchema (APPLY_SCHEMA=true) applies the embedded db/schema.sql to the
	// Postgres database at startup. The script is idempotent.
	ApplySchema bool
	// AppEnv is APP_ENV, the environment label (dev, prod, ...): [a-z0-9-]{1,16},
	// default "local". An invalid value falls back to "local" and adds a warning.
	AppEnv string
	Pool   PoolConfig
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
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
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
	metricsAddr := getenv("METRICS_ADDR")
	if metricsAddr != "" {
		_, mport, err := net.SplitHostPort(metricsAddr)
		if err != nil {
			return Config{}, fmt.Errorf("METRICS_ADDR must be host:port such as \"127.0.0.1:9100\", got %q", metricsAddr)
		}
		// Compare resolved numbers, not strings: "08080", "+8080" and named
		// ports such as "http" all name a port that may equal PORT.
		mnum, err := net.LookupPort("tcp", mport)
		if err != nil || mnum < 1 || mnum > 65535 {
			// Port 0 or an empty port would bind a random port silently.
			return Config{}, fmt.Errorf("METRICS_ADDR port must be a service name or a number from 1 to 65535, got %q", mport)
		}
		if mnum == portNum {
			// BSD/macOS accepts a loopback bind on the public port and would shadow the API.
			return Config{}, fmt.Errorf("METRICS_ADDR port %s must differ from PORT", mport)
		}
	}
	secure, cookieWarn := parseCookieSecure(getenv("COOKIE_SECURE"))
	var warnings []string
	if cookieWarn != "" {
		warnings = append(warnings, cookieWarn)
	}
	if store == StoreMemory {
		warnings = append(warnings, "STORE=memory: all data is lost when the process exits")
	}
	appEnv, envWarn := parseAppEnv(getenv("APP_ENV"))
	if envWarn != "" {
		warnings = append(warnings, envWarn)
	}
	return Config{
		AppEnv:       appEnv,
		Store:        store,
		DBDriver:     driver,
		SQLitePath:   sqlitePath,
		Warnings:     warnings,
		Pool:         pool,
		Addr:         ":" + strconv.Itoa(portNum),
		DatabaseURL:  dbURL,
		JWTSecret:    secret,
		CookieSecure: secure,
		MetricsAddr:  metricsAddr,
		ApplySchema:  getenv("APPLY_SCHEMA") == "true",
	}, nil
}

// parseCookieSecure is the single reading of COOKIE_SECURE (also used by the
// observer): only the exact value "true" turns it on. It returns a warning for
// an empty value and for anything other than true/false.
func parseCookieSecure(v string) (bool, string) {
	switch v {
	case "true":
		return true, ""
	case "false":
		return false, ""
	case "":
		return false, "COOKIE_SECURE is not set: the session cookie is sent without the Secure attribute. Set COOKIE_SECURE=true when serving over HTTPS"
	}
	return false, "COOKIE_SECURE must be \"true\" or \"false\"; other values are treated as false and the session cookie is sent without the Secure attribute"
}

// defaultAppEnv is the environment label when APP_ENV is unset or invalid.
const defaultAppEnv = "local"

var appEnvRE = regexp.MustCompile(`^[a-z0-9-]{1,16}$`)

// parseAppEnv is the single reading of APP_ENV: it returns the label and, for
// an invalid value, a warning (the process still starts, with "local").
func parseAppEnv(v string) (string, string) {
	if v == "" {
		return defaultAppEnv, ""
	}
	if !appEnvRE.MatchString(v) {
		return defaultAppEnv, "APP_ENV must match [a-z0-9-]{1,16}; using \"local\""
	}
	return v, ""
}
