// Command migrate-sqlite-to-pg copies every row of a micro git SQLite file
// (DB_DRIVER=sqlite, SQLITE_PATH) into a PostgreSQL database (DATABASE_URL).
// It is a one-shot tool for moving a local dev database to Postgres.
//
//	SQLITE_PATH=./data/mg.db DATABASE_URL=postgres://... \
//	  go run ./cmd/migrate-sqlite-to-pg [--force]
//
// The target must already have the Postgres schema: apply backend/db/schema.sql
// first (make schema). The whole copy runs in one transaction, so a failure
// leaves the target untouched. The target must be empty, unless --force is
// given, which truncates the four tables before copying. Ids are kept as they
// are in SQLite, and the identity sequences are moved past the largest id.
//
// Tables are copied in foreign-key order: users, repos, objects, refs. Row
// counts per table are printed for both sides; a mismatch rolls back.
//
// Connection strings are never printed: pgx errors embed them, so this
// command reports fixed messages instead of the driver text.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	// Registers the pure-Go SQLite driver as "sqlite" (no cgo).
	_ "github.com/glebarez/go-sqlite"
)

// batchRows is how many rows go into one multi-row INSERT. It keeps the
// statement count low when the database is remote (Neon).
const batchRows = 200

// tables lists the copied tables in foreign-key order.
var tables = []string{"users", "repos", "objects", "refs"}

// identityTables are the tables whose id is GENERATED ALWAYS AS IDENTITY.
var identityTables = []string{"users", "repos"}

func main() {
	force := flag.Bool("force", false, "truncate the target tables before copying")
	flag.Parse()
	if err := run(context.Background(), os.Getenv, *force, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migrate-sqlite-to-pg:", err)
		os.Exit(1)
	}
}

// run performs the migration. getenv is os.Getenv in production; out receives
// the progress and the row counts.
func run(ctx context.Context, getenv func(string) string, force bool, out io.Writer) error {
	sqlitePath := getenv("SQLITE_PATH")
	if sqlitePath == "" {
		return errors.New("SQLITE_PATH is not set")
	}
	dbURL := getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL is not set")
	}
	if fi, err := os.Stat(sqlitePath); err != nil {
		return fmt.Errorf("SQLITE_PATH %s: %w", sqlitePath, err)
	} else if fi.IsDir() {
		return fmt.Errorf("SQLITE_PATH %s is a directory", sqlitePath)
	}

	src, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer src.Close()
	src.SetMaxOpenConns(1)

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return errors.New("invalid DATABASE_URL (details hidden: they contain the connection string)")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return errors.New("cannot reach the database named by DATABASE_URL")
	}

	return migrate(ctx, src, pool, force, out)
}

// migrate copies all tables from src into pool in one transaction.
func migrate(ctx context.Context, src *sql.DB, pool *pgxpool.Pool, force bool, out io.Writer) error {
	srcCounts, err := sourceCounts(ctx, src)
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.New("begin transaction on the target database failed")
	}
	defer tx.Rollback(ctx) // no-op after Commit

	if err := requireSchema(ctx, tx); err != nil {
		return err
	}

	existing, err := targetCounts(ctx, tx)
	if err != nil {
		return err
	}
	total := 0
	for _, t := range tables {
		total += existing[t]
	}
	switch {
	case total > 0 && !force:
		return fmt.Errorf("target database is not empty (%s); rerun with --force to replace its data", nonEmptySummary(existing))
	case total > 0 && force:
		fmt.Fprintln(out, "--force: truncating target tables")
		if _, err := tx.Exec(ctx, "TRUNCATE "+strings.Join(tables, ", ")+" RESTART IDENTITY"); err != nil {
			return fmt.Errorf("truncate target: %w", err)
		}
	}

	start := time.Now()
	if err := copyUsers(ctx, src, tx); err != nil {
		return err
	}
	if err := copyRepos(ctx, src, tx); err != nil {
		return err
	}
	if err := copyObjects(ctx, src, tx); err != nil {
		return err
	}
	if err := copyRefs(ctx, src, tx); err != nil {
		return err
	}
	for _, t := range identityTables {
		// setval(seq, max, is_called): the next generated id is max+1; an empty table restarts at 1.
		stmt := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%[1]s', 'id'),
			GREATEST((SELECT COALESCE(MAX(id), 0) FROM %[1]s), 1),
			(SELECT COUNT(*) > 0 FROM %[1]s))`, t)
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("reset id sequence of %s: %w", t, err)
		}
	}

	dstCounts, err := targetCounts(ctx, tx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "row counts (source sqlite -> target postgres), copied in %s:\n", time.Since(start).Round(time.Millisecond))
	mismatch := false
	for _, t := range tables {
		ok := srcCounts[t] == dstCounts[t]
		mark := "ok"
		if !ok {
			mark = "MISMATCH"
			mismatch = true
		}
		fmt.Fprintf(out, "  %-8s %8d -> %8d  %s\n", t, srcCounts[t], dstCounts[t], mark)
	}
	if mismatch {
		return errors.New("row counts differ after the copy; nothing was committed")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit failed; nothing was committed")
	}
	fmt.Fprintln(out, "migration committed")
	return nil
}

// requireSchema checks that the four tables exist on the target.
func requireSchema(ctx context.Context, tx pgx.Tx) error {
	for _, t := range tables {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, t).Scan(&ok); err != nil {
			return fmt.Errorf("check target schema: %w", err)
		}
		if !ok {
			return fmt.Errorf("target has no table %q: apply backend/db/schema.sql first (make schema)", t)
		}
	}
	return nil
}

func targetCounts(ctx context.Context, tx pgx.Tx) (map[string]int, error) {
	counts := map[string]int{}
	for _, t := range tables {
		var n int
		if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n); err != nil {
			return nil, fmt.Errorf("count target %s: %w", t, err)
		}
		counts[t] = n
	}
	return counts, nil
}

func sourceCounts(ctx context.Context, src *sql.DB) (map[string]int, error) {
	counts := map[string]int{}
	for _, t := range tables {
		var n int
		if err := src.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n); err != nil {
			return nil, fmt.Errorf("count sqlite %s (is SQLITE_PATH a micro git database?): %w", t, err)
		}
		counts[t] = n
	}
	return counts, nil
}

func nonEmptySummary(counts map[string]int) string {
	var parts []string
	for _, t := range tables {
		if counts[t] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", t, counts[t]))
		}
	}
	return strings.Join(parts, ", ")
}

func copyUsers(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT id, username, password_hash, created_at FROM users ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read sqlite users: %w", err)
	}
	defer rows.Close()
	return copyBatched(ctx, tx, rows, "users",
		[]string{"id", "username", "password_hash", "created_at"}, 4,
		func(r *sql.Rows, dst []any) error {
			var id int64
			var username, hash string
			var created time.Time
			if err := r.Scan(&id, &username, &hash, &created); err != nil {
				return err
			}
			dst[0], dst[1], dst[2], dst[3] = id, username, hash, created
			return nil
		})
}

func copyRepos(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	// CAST: SQLite stores the boolean as 0/1 in a NUMERIC column; the cast
	// makes the scan independent of how the driver returns it.
	rows, err := src.QueryContext(ctx, `SELECT id, owner_id, name, description, default_branch,
		created_at, CAST(private AS INTEGER) FROM repos ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read sqlite repos: %w", err)
	}
	defer rows.Close()
	return copyBatched(ctx, tx, rows, "repos",
		[]string{"id", "owner_id", "name", "description", "default_branch", "created_at", "private"}, 7,
		func(r *sql.Rows, dst []any) error {
			var id, owner int64
			var name, desc, branch string
			var created time.Time
			var private int64
			if err := r.Scan(&id, &owner, &name, &desc, &branch, &created, &private); err != nil {
				return err
			}
			dst[0], dst[1], dst[2], dst[3], dst[4], dst[5], dst[6] = id, owner, name, desc, branch, created, private != 0
			return nil
		})
}

func copyObjects(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT repo_id, oid, type, data FROM objects ORDER BY repo_id, oid`)
	if err != nil {
		return fmt.Errorf("read sqlite objects: %w", err)
	}
	defer rows.Close()
	return copyBatched(ctx, tx, rows, "objects",
		[]string{"repo_id", "oid", "type", "data"}, 4,
		func(r *sql.Rows, dst []any) error {
			var repo int64
			var oid, typ string
			var data []byte
			if err := r.Scan(&repo, &oid, &typ, &data); err != nil {
				return err
			}
			if data == nil {
				data = []byte{} // an empty blob scans as nil; bytea NOT NULL needs a value
			}
			dst[0], dst[1], dst[2], dst[3] = repo, oid, typ, data
			return nil
		})
}

func copyRefs(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT repo_id, name, oid FROM refs ORDER BY repo_id, name`)
	if err != nil {
		return fmt.Errorf("read sqlite refs: %w", err)
	}
	defer rows.Close()
	return copyBatched(ctx, tx, rows, "refs",
		[]string{"repo_id", "name", "oid"}, 3,
		func(r *sql.Rows, dst []any) error {
			var repo int64
			var name, oid string
			if err := r.Scan(&repo, &name, &oid); err != nil {
				return err
			}
			dst[0], dst[1], dst[2] = repo, name, oid
			return nil
		})
}

// copyBatched reads rows from src, converts each with scan into ncols values,
// and inserts them into table in multi-row statements of batchRows rows. The
// OVERRIDING SYSTEM VALUE clause lets explicit ids reach identity columns.
func copyBatched(ctx context.Context, tx pgx.Tx, rows *sql.Rows, table string, cols []string, ncols int,
	scan func(*sql.Rows, []any) error) error {
	override := ""
	for _, t := range identityTables {
		if t == table {
			override = " OVERRIDING SYSTEM VALUE"
		}
	}
	var placeholders []string
	var batch []any
	flush := func() error {
		if len(placeholders) == 0 {
			return nil
		}
		stmt := fmt.Sprintf("INSERT INTO %s (%s)%s VALUES %s", table, strings.Join(cols, ", "), override, strings.Join(placeholders, ", "))
		if _, err := tx.Exec(ctx, stmt, batch...); err != nil {
			return fmt.Errorf("insert into %s: %w", table, err)
		}
		placeholders = placeholders[:0]
		batch = batch[:0]
		return nil
	}
	for rows.Next() {
		dst := make([]any, ncols)
		if err := scan(rows, dst); err != nil {
			return fmt.Errorf("read sqlite %s: %w", table, err)
		}
		ph := make([]string, ncols)
		for i := range ph {
			ph[i] = fmt.Sprintf("$%d", len(batch)+i+1)
		}
		placeholders = append(placeholders, "("+strings.Join(ph, ", ")+")")
		batch = append(batch, dst...)
		if len(placeholders) == batchRows {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read sqlite %s: %w", table, err)
	}
	return flush()
}
