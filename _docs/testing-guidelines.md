# Testing guidelines

## Layout
- Tests live next to the code: `foo.go` → `foo_test.go`
- Use the external test package (`package foo_test`) for public API tests,
  the internal package only when testing unexported helpers

## Style
- Table-driven tests with `t.Run(tc.name, ...)` for anything with 2+ cases
- Use `t.Helper()` in helpers, `t.Cleanup()` instead of `defer` in helpers
- Use `t.Parallel()` unless the test touches shared state (the database)
- Assertions: standard library only (`if got != want { t.Errorf("got %v, want %v", got, want) }`).
  Do not add `testify` or any other assertion library

## What to test
- Behavior through the public API (HTTP handlers via `httptest`), not implementation details
- Every bug fix gets a regression test that fails without the fix
- Error paths, not just the happy path
- Every endpoint behind auth: a request without a token, and a request for
  another user's data, must both be rejected

## What not to do
- No mocking of things we own; use a real implementation or a small fake
- No `time.Sleep` in tests; use channels, contexts, or an injected clock
- No network calls to external services; use `httptest.NewServer`

## Database tests
- Tests that need PostgreSQL run against a separate Neon branch, never the
  main one. The connection string comes from the `TEST_DATABASE_URL`
  environment variable
- If `TEST_DATABASE_URL` is not set, the test calls `t.Skip` with a clear message
- Each test creates its own data (unique emails, its own user) and removes it
  with `t.Cleanup()`, so tests do not depend on each other or on leftover rows
- Do not use `t.Parallel()` in tests that share the same database rows

## Choosing the store (`TEST_STORE`)
- The API suite runs against one store, chosen by `TEST_STORE`: `postgres` (needs `TEST_DATABASE_URL`), `memory` (no database, a fresh `memStore` per test), `sqlite` (`gormStore` on a temp file). With `TEST_STORE` unset the store is `postgres` when `TEST_DATABASE_URL` is set, else `sqlite`
- `TEST_STORE=memory go test -p 1 -race ./...` runs with `TEST_DATABASE_URL` unset
- Local Postgres for tests (throwaway, no password, no secret): `TEST_DATABASE_URL=postgres://test@localhost:54329/mg?sslmode=disable` (setup in `_docs/AGENTS.md`)

### The matrix: `make test-all`
- `make test-all` (= `scripts/test-matrix.sh`) runs `go test -p 1 -race ./...` on memory, sqlite and postgres in turn and prints one line per store: `pass`, `fail` and `skip` counts. `scripts/test-matrix.sh sqlite` runs only the named stores
- Postgres uses `TEST_DATABASE_URL`, else the :54329 server above. The script creates its own database `mg_matrix_<pid>`, applies `backend/db/schema.sql` to it and drops it at the end, so parallel runs and the shared `mg` database are untouched
- No silent skips: the script sets `TEST_MATRIX_STRICT=1` (an unset `TEST_DATABASE_URL` on the postgres pass is a failure, and an unreachable server fails the matrix), and every skipped test must be listed for its store in `backend/known_skips.txt` (`<store> <test name>`, one per line). A skip that is not listed fails the matrix; after adding a skip on purpose run `scripts/test-matrix.sh --update-skips` and review the diff
- Known skips, all in `backend/known_skips.txt`: on memory and sqlite the Postgres-only tests (pgx store, statement counts, pool settings, migration checks, the `/postgres`, `/pgx`, `/gorm-postgres` subtests) skip because `TEST_DATABASE_URL` is unset for those passes; on postgres only `TestBaseline` (needs `BASELINE=1`) and the 10005-commit walk skip. `sqlitePending` (auth_test.go) is empty
- Reach the store through `Server.Store`. For what the interface cannot do (count rows, drop objects, delete a user) use the helpers in `backend/storetest_test.go` (`countObjects`, `dropBlobs`, `deleteRepo`, `deleteUser`, `viewOf`), never `rawDB`
- A test that is about Postgres itself (statement counts, the pgx store, the pool) starts with `requirePostgres(t)`, which skips it on the other stores

## Frontend tests
- Vitest and Testing Library; test what the user sees (text, roles, labels),
  not component internals

## Running
- `go test ./...` must pass before committing; `make test-all` before closing a task that touches a store
- `TEST_DATABASE_URL=<neon test branch url> go test ./...` to include database tests
- `go test -race ./...` before closing a task
