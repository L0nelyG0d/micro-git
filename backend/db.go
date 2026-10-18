package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens a pgx pool and pings it, so a bad URL or an unreachable
// database fails at startup instead of on the first request.
//
// pgx errors embed the connection string (user, host, database), so they are
// never returned or wrapped: the caller gets a fixed message plus the error
// class from dbErrorClass.
func NewPool(ctx context.Context, url string, pc PoolConfig) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL: it could not be parsed (details hidden because they contain the connection string)")
	}
	pcfg.ConnConfig.Tracer = stmtTracer{} // counts statements per request, see timing.go
	pcfg.MaxConns = pc.MaxConns
	pcfg.MaxConnIdleTime = pc.MaxConnIdleTime
	pcfg.HealthCheckPeriod = pc.HealthCheckPeriod
	if pc.StatementTimeout > 0 {
		if pcfg.ConnConfig.RuntimeParams == nil {
			pcfg.ConnConfig.RuntimeParams = map[string]string{}
		}
		// a runaway query is cancelled by the server instead of holding a pooled connection
		pcfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(pc.StatementTimeout.Milliseconds(), 10)
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL: it could not be parsed (details hidden because they contain the connection string)")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("cannot reach the database named by DATABASE_URL (%s)", dbErrorClass(err))
	}
	return pool, nil
}

// dbErrorClass names the kind of a connection error without any of its text.
func dbErrorClass(err error) string {
	var pgErr *pgconn.PgError
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.As(err, &pgErr):
		return "server error, SQLSTATE " + pgErr.Code
	case errors.As(err, &dnsErr):
		return "host name lookup failed"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &netErr):
		return "network error"
	default:
		return "connection failed"
	}
}

// isUniqueViolation reports whether err is a Postgres unique-index violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isForeignKeyViolation reports whether err is a Postgres foreign-key violation.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
