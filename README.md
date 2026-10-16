# micro git

A small git-hosting site. Users register, create repositories, commit file
changes from the browser, and browse branches, history, files and diffs. The
backend (Go + PostgreSQL) stores blob / tree / commit objects in git's own
format; the frontend (React + Vite) looks like a GitHub repository page.
Clone, push and fetch are not supported. See `_docs/plan.md` for the full
feature list, endpoints, tables and rules.

## Prerequisites

- Go (the version in `backend/go.mod`)
- Node.js and npm
- A PostgreSQL database you can connect to (for example a Neon branch), plus a
  second, separate one for tests
- `psql` to apply the schema

## Setup

Run these steps in order.

1. Install dependencies.

   ```sh
   cd backend && go mod download
   cd ../frontend && npm install
   ```

2. Create a git-ignored `.env` in the repository root with the variables below.
   The backend does not read `.env` itself; export it into your shell.

   ```sh
   # .env  (placeholders only; put your own values here, never commit this file)
   DATABASE_URL=postgres://USER:PASSWORD@HOST/DBNAME?sslmode=require
   TEST_DATABASE_URL=postgres://USER:PASSWORD@HOST/TESTDBNAME?sslmode=require
   JWT_SECRET=a-long-random-string
   COOKIE_SECURE=false
   PORT=8080
   ```

   | variable | required | purpose |
   |---|---|---|
   | `DATABASE_URL` | yes | PostgreSQL connection string for the running backend |
   | `TEST_DATABASE_URL` | for DB tests | Connection string of a separate database or branch used by `go test`; the database tests are skipped when it is unset |
   | `JWT_SECRET` | yes | Key used to sign the session JWT. Use a long random string |
   | `COOKIE_SECURE` | no | `true` adds the `Secure` attribute to the session cookie (needs HTTPS). Anything else, including unset, means off. Keep it off for local HTTP |
   | `PORT` | no | Backend listen port, 1 to 65535. Defaults to `8080` |

3. Export the variables into your shell (repeat in every new terminal).

   ```sh
   set -a; source .env; set +a
   ```

4. Apply the schema. It is idempotent, so applying it twice is safe. Apply it
   to both `DATABASE_URL` and `TEST_DATABASE_URL`.

   ```sh
   psql "$DATABASE_URL" -f backend/db/schema.sql
   psql "$TEST_DATABASE_URL" -f backend/db/schema.sql
   ```

5. Run the backend (terminal 1). It listens on `:$PORT` (default `8080`) and
   exits with an error if `DATABASE_URL` or `JWT_SECRET` is missing.

   ```sh
   cd backend && go run .
   ```

   Check it: `curl http://localhost:8080/health` prints `OK`.

6. Run the frontend (terminal 2) and open <http://localhost:5173>.

   ```sh
   cd frontend && npm run dev
   ```

The Vite dev server on port 5173 proxies `/api/*` to `http://localhost:8080`,
stripping the `/api` prefix (`/api/repos` reaches the backend as `/repos`). The
proxy target is hard-coded in `frontend/vite.config.ts`, so if you change
`PORT`, change the target there too.

## Tests

```sh
cd backend && go test ./...          # whole backend suite
cd backend && go test -race ./...    # with the race detector
cd frontend && npm test              # frontend tests (Vitest)
```

Export `.env` first (step 3) so the database tests run against
`TEST_DATABASE_URL`; without it they are skipped. Point it at a throwaway
database: tests write to it.

## Architecture

### Git object model

Storage mimics git's content-addressed model, using git's exact serialization
and SHA-1, so an object's oid equals what `git hash-object` would print.

- **blob**: the file bytes.
- **tree**: a directory; entries sorted by name, each `<mode> <name>\0<20-byte oid>`
  (`100644` file, `40000` directory).
- **commit**: `tree <oid>`, zero or more `parent <oid>`, `author`, `committer`,
  a blank line, then the message.
- **oid** = `sha1("<type> <len>\0" + body)`. Objects are immutable and stored
  once per repo (`INSERT ... ON CONFLICT DO NOTHING`).
- **refs**: a branch is a row mapping a name to a commit oid. A repo with no
  refs is empty.
- **Commit flow**: write the blobs, rebuild only the trees along changed paths,
  bottom-up (unchanged subtrees are reused by oid), write the commit, then move
  the branch ref with a compare-and-swap on the `expected_tip` the client saw.
  If the tip moved meanwhile the API answers `409` and nothing changes. The
  first commit on an empty repo has no parent and creates the branch.
- **History and diffs**: the log walks first parents, 50 commits per page; a
  commit's diff is a line-based (LCS) diff against its first parent.

Details: `_docs/plan.md` ("Core model", "Tables", "Rules").

### Package layout

```
backend/                 package main: HTTP server (net/http patterns, no router lib)
  main.go, config.go       startup and env parsing
  handler.go, httpx.go     routes, JSON helpers and error shape
  auth.go, session.go      register/login/logout/me, JWT in an httpOnly cookie
  repos.go, branches.go    repo and branch endpoints
  commits.go               POST commits (compare-and-swap on the ref)
  reading.go, history.go   tree/blob reads, commit log and commit detail
  store.go, db.go          PostgreSQL access (pgx) for objects and refs
  db/schema.sql            users, repos, objects, refs
  gitobj/                  serialize, hash and parse blob/tree/commit (no DB, no HTTP)
  treebuild/               apply file changes to a tree, rebuilding touched trees
  linediff/                line diffs of texts and of two trees
frontend/                React + TypeScript + Vite + Tailwind + shadcn/ui
  src/pages/               Home, Login, Register, RepoPage, BlobPage, EditorPage,
                           HistoryPage, CommitPage, NotFound
  src/components/          Nav, file tree, branch switcher, repo table, dialogs, ui/
  src/auth/                AuthContext (current user)
  src/lib/                 api client, fetch hook, formatting, git helpers
```

## Demo script

Start the backend and frontend (Setup steps 5 and 6) and open
<http://localhost:5173>.

1. **Register.** Go to Register, enter a username (3 to 32 characters of
   `a-z`, `0-9`, `-`) and a password of at least 8 characters, submit.
   Expect: you land on the login page or the home page without an error message.
2. **Log in.** Log in with the same credentials. Expect: the nav bar shows your
   username and a logout control; the repository list is shown.
3. **Create a repo.** Use the new-repository form with a name such as `hello`.
   Expect: the repo appears in the list and opens as an empty repository
   (no branches, no files, a prompt to add the first file).
4. **First commit on the empty repo.** Click add file, set path `README.md`,
   content `hello`, a message such as `first commit`, branch `main`, commit.
   Expect: you return to the repo page, branch `main` exists and `README.md` is
   in the file list; the latest-commit bar shows your message.
5. **Edit a file.** Open `README.md`, choose edit, change the content, add a
   message, commit. Expect: the file shows the new content and the latest
   commit message changes.
6. **History and diff.** Open the commit history for `main`. Expect: two
   commits, newest first. Open the newest one. Expect: `README.md` is marked
   modified with removed lines in `-` and added lines in `+`, with context.
7. **Create a branch.** In the branch switcher create `feature` from `main`.
   Expect: `feature` is listed and can be selected; its files and history match
   `main`.
8. **The 409 case (two tabs).** Open the editor for `README.md` on `main` in
   tab A and again in tab B. Commit in tab A (succeeds). Then commit in tab B.
   Expect: tab B shows a conflict error saying the branch changed (HTTP `409`)
   and creates no commit; reload tab B, redo the edit and it commits.
9. **Delete the repo.** On the repo page choose delete and confirm. Expect:
   you return to the home page and the repo is gone from the list.
10. **Log out.** Use the logout control. Expect: the nav bar shows login and
    register again, and write actions are no longer offered.

## Known limits

- **Local only.** The app runs on your machine: the browser talks to the Vite
  dev server, which proxies `/api` to the backend and strips the prefix. The
  backend itself serves no `/api` prefix and no frontend files, and nothing
  here describes production hosting. How `/api` should be served in production
  is an open decision: see issue #22
  (<https://github.com/L0nelyG0d/micro-git/issues/22>).
- Text files only (UTF-8, at most 1 MiB), at most 50 changes per commit; no
  clone/push/fetch, tags, forks, pull requests or private repositories (see
  "Out of scope" in `_docs/plan.md`).
- Stretch features (merge, compare, README rendering, repo rename) are not
  implemented (issue #21).
