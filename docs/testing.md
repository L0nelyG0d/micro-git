# Testing

How the project is tested locally and in CI. Rules for writing tests live in [`_docs/testing-guidelines.md`](../_docs/testing-guidelines.md); the Playwright suite and the older manual browser journey are described in [`_docs/e2e.md`](../_docs/e2e.md); setup in [`docs/developer-guide.md`](developer-guide.md).

## Test levels

| Level | Where | Tooling |
| --- | --- | --- |
| Unit | `backend/**/*_test.go` (next to the code), `frontend/src/**/*.test.ts(x)` | `go test`, Vitest + Testing Library |
| Integration | Backend API suite (`httptest` against a real store); `make smoke-sqlite` (real Vite proxy + real backend on a temp SQLite file) | `go test`, `scripts/smoke-sqlite.sh` |
| Contract | `backend/openapi_test.go` (routes in `handler.go` vs `_docs/openapi.yaml`), `frontend/src/lib/openapi.test.ts` and `openapi.mock.test.ts` (frontend paths and mock answers vs the spec) | `go test`, Vitest |
| End-to-end | `e2e/` Playwright suite (Chromium) against the built Docker image on Postgres 16 | Playwright; base URL from `E2E_BASE_URL` (default `http://localhost:8080`); no `webServer`, the caller starts the app |

OpenAPI is linted with Redocly (`_docs/openapi.yaml`).

## Stores matrix

The API suite runs against one store, chosen by `TEST_STORE`:

| Store | Needs | Selected by |
| --- | --- | --- |
| `postgres` | `TEST_DATABASE_URL` | default when `TEST_DATABASE_URL` is set |
| `sqlite` | nothing (temp file) | default when it is unset |
| `memory` | nothing | `TEST_STORE=memory` |

Postgres-only tests (pgx store, statement counts, pool settings, migration checks) skip on the other stores.

`make test-all` runs `scripts/test-matrix.sh` and then the frontend tests. The script runs `go test -p 1 -race ./...` on memory, sqlite and postgres in turn and prints one `pass`/`fail`/`skip` line per store; `scripts/test-matrix.sh sqlite` runs only the named stores.

- Postgres uses `TEST_DATABASE_URL`, else the local server on `:54329` (below). If the server is unreachable the matrix fails; it never skips the Postgres pass. The URL may have or lack a database path and query string and may carry user:password (shapes checked by `scripts/test-pgurl.sh`, which needs no database); a value that is not a `postgres://` URL fails the matrix.
- The script always creates its own database `mg_matrix_<pid>` on that server, applies `backend/db/schema.sql` to it and drops it at the end (also on TERM or Ctrl-C), so parallel runs and the shared `mg` database are untouched.
- Skip accounting: every skipped test must be listed for its store in `backend/known_skips.txt` (`<store> <test name>`); an unlisted skip fails the matrix. After adding a skip on purpose run `scripts/test-matrix.sh --update-skips` and review the diff. The script sets `TEST_MATRIX_STRICT=1`, which makes `testServer` fail instead of skip if `TEST_STORE=postgres` runs without `TEST_DATABASE_URL`. Which skips are expected per store: [`_docs/testing-guidelines.md`](../_docs/testing-guidelines.md).

CI does not run `scripts/test-matrix.sh`, so the skip accounting is checked only locally (tracked in #198).

## Local Postgres on :54329

Throwaway server, role `test`, no password (setup in `_docs/AGENTS.md`):

```
TEST_DATABASE_URL=postgres://test@localhost:54329/mg?sslmode=disable
```

`make test` and `scripts/test-matrix.sh` fall back to it when `TEST_DATABASE_URL` is unset.

## Make targets

| Target | What it does |
| --- | --- |
| `make setup` | `go mod download`, `npm ci` |
| `make test` | `go test -p 1 -race ./...` (DB: `TEST_DATABASE_URL`, else :54329, else `.env`), then frontend tests |
| `make test-fast` | No database: `gitobj`, `treebuild`, `linediff` packages plus frontend |
| `make test-all` | `scripts/test-matrix.sh` (memory, sqlite, postgres) then frontend |
| `make smoke-sqlite` | Integration smoke on a temp SQLite file |
| `make lint` | `gofmt -l` (must list nothing), `go vet`, `tsc -b`, `npm run lint` if defined (the frontend has no lint script today) |
| `make build` | Backend binary and frontend build |

`scripts/test-matrix.sh [memory|sqlite|postgres ...] [--update-skips]` runs selected stores. `JWT_SECRET` (32+ bytes) is required by the backend suites; `make` reads it from `.env` without printing it.

Run the Playwright suite by hand: start the stack (for example `docker compose up -d --wait`, see [deployment.md](deployment.md)), then `cd e2e && npm ci && npx playwright install chromium && npx playwright test`. Set `REGISTER_MAX_PER_IP` high on the stack, because the suite registers one user per test. Suite layout and conventions: [`_docs/e2e.md`](../_docs/e2e.md#playwright-suite-layout).

## CI jobs

| Workflow / job | Trigger | What it checks |
| --- | --- | --- |
| CI / `backend` | push to `main`, PRs, manual | gofmt, vet, `go mod tidy -diff`, govulncheck (advisory, `continue-on-error`), schema applied to a `postgres:16` service, `go test -p 1 -race` |
| CI / `backend-store` | same | Same suite with `TEST_STORE=sqlite` and `memory`, no database; calls `go test` directly |
| CI / `frontend` | same | `npm ci`, `npm test -- --run`, `npm run build` (Node 22) |
| CI / `openapi` | same | `npx @redocly/cli lint _docs/openapi.yaml` |
| CI / `secret-scan` | same | `git grep` over tracked files for credentialed `postgres://` URLs and `npg_...` tokens; prints file:line only. Excludes `.github/workflows/ci.yml` and lines with `USER:PASSWORD@HOST`, `${` or `%s@` |
| E2E / `e2e` | same | Builds the Dockerfile image, `docker compose up -d --wait` (compose builds the app and starts its own Postgres, with `REGISTER_MAX_PER_IP=1000`), runs Playwright; uploads `playwright-report` (14 days) |
| E2E / `integration-sqlite` | same | `make setup`, `make smoke-sqlite` |

CI needs no repository secrets; the JWT secret is generated per run and masked. Deploy ([deployment.md](deployment.md)) starts only after CI and E2E both succeed on `main`.
