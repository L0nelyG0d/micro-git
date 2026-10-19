

- `go mod download` - install dependencies
- `go test ./...` - the whole suite
- `go test -race ./...` - the whole suite with the race detector
- `make test-all` - backend on memory, sqlite and postgres (`scripts/test-matrix.sh`, own database on the local :54329 server or `TEST_DATABASE_URL`), then the frontend tests. Run it before closing a task that touches a store
- `TEST_STORE=memory|sqlite|postgres go test -p 1 -race ./...` - the API suite on one store (`TEST_STORE` unset: postgres if `TEST_DATABASE_URL` is set, else sqlite). `memory` and `sqlite` need no database
- Against a remote Neon test branch (~200ms per statement, only if the user asks) use `go test -p 1 -race -timeout 40m ./...` (the root package takes ~12 min; 40m is the value `make test` and `make test-all` use); on the local :54329 server it takes minutes
- `go test ./path/to/pkg` - one package
- `go test -run TestName ./...` - one test by name
- `npm test` (in `frontend/`) - frontend tests
- `make run-sqlite` - backend on SQLite (`data/mg.db`, git-ignored), no Postgres; `make dev-sqlite` adds the frontend; `make run-memory` for the in-memory store; `make clean-data` deletes the SQLite files; `make smoke-sqlite` is the integration smoke (README "Quick start")

Rules

- Dependencies are tracked in `go.mod` (backend) and `frontend/package.json` (frontend). 
- Pre-approved frontend packages: those listed under "Approved packages" in `_docs/design-system.md`
- Pre-approved Go modules: `github.com/jackc/pgx/v5`, `golang.org/x/crypto` (bcrypt), `github.com/golang-jwt/jwt/v5`. Also approved for the SQLite/ORM iteration (teacher's requirement, user decision 2026-10-19): `gorm.io/gorm`, `gorm.io/driver/postgres`, `github.com/glebarez/sqlite` (pure Go, no cgo). Everything else comes from the standard library: routing uses `net/http` patterns (no chi or other router)
- Never commit tokens, passwords, or connection strings, and never paste a secret into a doc, an issue, or a commit message. Keep `JWT_SECRET`, `DATABASE_URL`, and `TEST_DATABASE_URL` in environment variables or a git-ignored `.env`
- For design questions, consult the user before changing anything: give several options and show how they would look

Git

- One branch per issue: `issue-<N>-<short-slug>`, created from an up-to-date `main`. The engineer commits only on that branch; QA checks that same branch; the code reviewer reviews it after QA PASS and is the only role that merges it into `main` (see `_docs/process.md`). Nobody commits to `main` directly except the code reviewer's merges and doc-only housekeeping
- Small, regular commits that each leave the tests passing
- Short imperative messages that reference the issue (`Refs #4`); `Closes #4` on the last commit of the issue
- Only the code reviewer pushes, and only `main` right after merging an approved issue (the user delegated this on 2026-10-19, replacing "push only when the user asks"). No other role pushes; never force-push

Documents - look these up when relevant, don't load them all up front

- `_docs/process.md` - how work is organized: roles and the task lifecycle. Read before starting or closing any task
- `_docs/plan.md` - what the app does: features, endpoints, tables, rules
- `_docs/tasks.md` - the backlog, one task per GitHub issue
- `_docs/testing-guidelines.md` - read before writing tests
- `_docs/process.md` sections "Testing-phase issues" (which skills the `ui` issues use: `web-design-guidelines`, `design-taste-frontend`, `image-to-code`) and "Dogfooding with mg" (committing our work with `bin/mg` and storing the hash) - read when QA files an issue or an issue is about to merge
- `DESIGN.md` and `frontend/src/index.css` - the source of truth for colors, radius, density; read before anything touching the UI. `_docs/design-system.md` is superseded for visual values (it still holds the non-visual rules and the approved packages)

Fast local test database (use this instead of Neon when time matters)

- A throwaway local Postgres 16 runs on port 54329 (no password, fsync off): `TEST_DATABASE_URL=postgres://test@localhost:54329/mg?sslmode=disable`. Schema is already applied. Tests there run in minutes instead of ~12-18 min.
- Start it if it is down: `LC_ALL=en_US.UTF-8 /opt/homebrew/opt/postgresql@16/bin/pg_ctl -D <scratchpad>/pgtest -o "-p 54329 -k /tmp" start`. Re-apply schema with `psql -h localhost -p 54329 -U test mg -f backend/db/schema.sql`.
- Use unique usernames anyway; Neon remains the final check only if the user asks.
