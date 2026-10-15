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

## Frontend tests
- Vitest and Testing Library; test what the user sees (text, roles, labels),
  not component internals

## Running
- `go test ./...` must pass before committing
- `TEST_DATABASE_URL=<neon test branch url> go test ./...` to include database tests
- `go test -race ./...` before closing a task
