

- `go mod download` - install dependencies
- `go test ./...` - the whole suite
- `go test -race ./...` - the whole suite with the race detector
- `go test ./path/to/pkg` - one package
- `go test -run TestName ./...` - one test by name
- `npm test` (in `frontend/`) - frontend tests

Rules

- Dependencies are tracked in `go.mod` (backend) and `frontend/package.json` (frontend). 
- Pre-approved frontend packages: those listed under "Approved packages" in `_docs/design-system.md`
- Pre-approved Go modules: `github.com/jackc/pgx/v5`, `golang.org/x/crypto` (bcrypt), `github.com/golang-jwt/jwt/v5`. Everything else comes from the standard library: routing uses `net/http` patterns (no chi or other router)
- Never commit tokens, passwords, or connection strings, and never paste a secret into a doc, an issue, or a commit message. Keep `JWT_SECRET`, `DATABASE_URL`, and `TEST_DATABASE_URL` in environment variables or a git-ignored `.env`
- For design questions, consult the user before changing anything: give several options and show how they would look

Git

- Work directly on `main`; no branch or pull request per issue
- Small, regular commits that each leave the tests passing
- Short imperative messages that reference the issue (`Refs #4`); `Closes #4` on the last commit of the issue
- Push only when the user asks

Documents - look these up when relevant, don't load them all up front

- `_docs/process.md` - how work is organized: roles and the task lifecycle. Read before starting or closing any task
- `_docs/plan.md` - what the app does: features, endpoints, tables, rules
- `_docs/tasks.md` - the backlog, one task per GitHub issue
- `_docs/testing-guidelines.md` - read before writing tests
- `_docs/design-system.md` - read before anything touching the UI
