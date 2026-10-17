# micro git

A small git-hosting site. Users register, create repositories, commit file
changes from the browser, and browse branches, history, files and diffs. The
backend (Go + PostgreSQL) stores blob / tree / commit objects in git's own
format; the frontend (React + Vite) looks like a GitHub repository page.
Clone, push and fetch are not supported. See `_docs/plan.md` for the full
feature list, endpoints, tables and rules.

## Quick start with make

With Go, Node.js and npm, `psql` and a PostgreSQL database available, a fresh
checkout needs no manual shell steps and no `source .env`:

```sh
cp /path/to/your/.env .env   # or create it as described under "Setup", step 2
make setup                   # go mod download + npm ci
make check-env               # DATABASE_URL and JWT_SECRET (32+ bytes) present?
make schema                  # apply backend/db/schema.sql (retries for Neon cold starts)
make dev                     # backend + frontend; open http://localhost:5173; Ctrl-C stops both
```

The backend port defaults to `8080`. If it is busy, `make` says so; pick
another one with `make dev PORT=9090` (also `make backend PORT=9090`); the
frontend proxy follows automatically. `PORT` in `.env` is not used by `make`.
`make` lists all targets (`backend`, `frontend`, `test`, `test-fast`, `build`,
`lint`, `clean`). The manual steps below do the same by hand.

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
   JWT_SECRET=replace-with-at-least-32-random-bytes
   COOKIE_SECURE=false
   PORT=8080
   ```

   | variable | required | purpose |
   |---|---|---|
   | `DATABASE_URL` | yes | PostgreSQL connection string for the running backend |
   | `TEST_DATABASE_URL` | for DB tests | Connection string of a separate database or branch used by `go test`; the database tests are skipped when it is unset |
   | `JWT_SECRET` | yes | Key used to sign the session JWT. At least 32 bytes (the backend refuses to start with a shorter one); use a long random string, for example `openssl rand -base64 48` |
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
   exits with an error if `DATABASE_URL` or `JWT_SECRET` is missing, if
   `JWT_SECRET` is shorter than 32 bytes or if `PORT` is not a number from 1
   to 65535. It shuts down gracefully on Ctrl+C / SIGTERM (10 seconds for
   requests in flight).

   ```sh
   cd backend && go run .
   ```

   Check it: `curl http://localhost:8080/health` prints `OK`.

6. Run the frontend (terminal 2) and open <http://localhost:5173>.

   ```sh
   cd frontend && npm run dev
   ```

The Vite dev server on port 5173 proxies `/api/*` to `http://localhost:8080`,
stripping the `/api` prefix (`/api/repos` reaches the backend as `/repos`). If
the backend listens elsewhere (a different `PORT`), start the frontend with the
`API_PROXY_TARGET` environment variable, for example
`API_PROXY_TARGET=http://localhost:9090 npm run dev`. It is read by
`frontend/vite.config.ts`, is optional and defaults to `http://localhost:8080`.

## Tests

```sh
cd backend && go test -p 1 -race -timeout 40m ./...   # whole backend suite
cd backend && go test ./gitobj/ ./treebuild/ ./linediff/  # pure packages, no database, seconds
cd frontend && npm test -- --run                       # frontend tests (Vitest), once
```

Export `.env` first (step 3) so the database tests run against
`TEST_DATABASE_URL`; without it they are skipped. Point it at a throwaway
database: tests write to it. On a small remote database (for example a
suspended-on-idle Neon branch) keep `-p 1` (one package at a time, so the
packages do not fight over the same database) and the long `-timeout`: the
full backend suite takes roughly 10 minutes there (about 200 ms per
statement). While developing, run one test: `go test -p 1 -run TestName .` in
`backend/`. The backend tests include concurrency tests (`concurrency_test.go`:
parallel commits, first commit, rename, branch create) and delete races
(`delete_race_test.go`: reads and commits while the repository is deleted).

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
  main.go, config.go       startup (server timeouts, graceful shutdown) and env parsing
  handler.go, httpx.go     routes, safe headers, JSON helpers, body limits, error shape
  auth.go, session.go      register/login/logout/me, JWT in an httpOnly cookie
  repos.go, rename.go      repo endpoints (list, create, get, delete) and rename (PATCH)
  branches.go, merge.go    branch endpoints; fast-forward merge
  commits.go               POST commits (compare-and-swap on the ref)
  reading.go, history.go   tree/blob reads, commit log and commit detail
  compare.go               diff between two branch tips or commits
  store.go, db.go          PostgreSQL access (pgx) for objects and refs
  db/schema.sql            users, repos, objects, refs
  *_test.go                tests next to the code, including concurrency_test.go and
                           delete_race_test.go (races on refs, unique indexes and deletes)
  gitobj/                  serialize, hash and parse blob/tree/commit (no DB, no HTTP)
  treebuild/               apply file changes to a tree, rebuilding touched trees
  linediff/                line diffs of texts and of two trees
frontend/                React + TypeScript + Vite + Tailwind + shadcn/ui
  src/pages/               Home, Login, Register, RepoPage, BlobPage, EditorPage,
                           HistoryPage, CommitPage, ComparePage, NotFound (+ AuthForm)
  src/components/          Nav, FileTree, BranchSwitcher, RepoTable, NewRepoForm,
                           MergeDialog, RenameRepoDialog, DeleteRepoDialog, ReadmeCard,
                           LatestCommitBar, DiffView, Breadcrumbs, OidLink, StateViews, ui/
  src/auth/                AuthContext (current user)
  src/lib/                 api client, fetch hook, formatting, git helpers
```

### API contract

`openapi.yaml` at the repo root describes every endpoint (OpenAPI 3.0.3): request and
response schemas, the `session` cookie auth, the `{"error": ...}` shape and every status
code the handlers return. The backend serves no `/api` prefix; the Vite dev server adds it
for the browser and strips it again. Lint it with

```
npx @redocly/cli lint openapi.yaml
```

(Redocly is run through `npx`, not a project dependency.) Two tests keep the spec in sync:
`backend/openapi_test.go` compares the routes in `handler.go` with the spec paths, and
`frontend/src/lib/openapi.test.ts` checks that every path the frontend requests exists in it.

## Demo script

Start the backend and frontend (Setup steps 5 and 6) and open
<http://localhost:5173>.

1. **Register.** Go to Register, enter a username (3 to 32 characters of
   `a-z`, `0-9`, `-`) and a password of at least 8 characters, submit.
   Expect: you are logged in and land on the home page without an error
   message (if the automatic login fails you land on the login page instead).
2. **Log in.** Click Log out in the nav bar, then Log in with the same
   credentials. Expect: the nav bar shows your username and a Log out button;
   the repository list is shown.
3. **Create a repo.** On the home page click New repository; a form appears.
   Enter the name `hello` and click Create repository. Expect: you stay on the
   home page, the form closes, a "created" toast appears and `you/hello` is in
   the list. Click it in the list. Expect: an empty repository (no branches,
   no files, a prompt to add the first file).
4. **First commit on the empty repo.** Click Add file, set path `README.md`,
   content `hello`, a commit message such as `first commit`, and click Commit.
   The branch is not a field to type here: the empty repo offers only its
   default branch `main`, and the commit creates it. Expect: the commit page
   opens (message, author, `README.md` as added). Go back to the repo by
   clicking it in the home page list. Expect: branch `main` exists, `README.md` is in the
   file list and the latest-commit bar shows your message.
5. **Edit a file.** Open `README.md`, click Edit, change the content, add a
   message, click Commit. Expect: the commit page opens with the new commit.
   Back on the repo page (click it in the home page list) the file list shows
   only names; the new content is visible in the rendered README card under
   the file tree, or by opening `README.md`. The latest commit message changes.
6. **History and diff.** On the repo page click History for `main`. Expect:
   two commits, newest first. Click the 7-character oid of the newest one (the
   message itself is not a link). Expect: `README.md` is
   marked modified with removed lines in `-` and added lines in `+`, with
   context.
7. **Create a branch.** With `main` selected in the branch switcher, click
   Create branch, name it `feature` and confirm. Expect: `feature` is listed
   and selected; its files and history match `main`.
8. **Delete a file and check ownership.** Switch to `main`, click Add file,
   add `notes.txt` with content `x` and commit. Open `notes.txt`, click Edit,
   enter a commit message and click Delete file, then confirm. Expect: the
   commit page opens and `notes.txt` is gone from the file list. Log out, then
   register and log in as a second user and open the same repo. Expect: no
   Edit, Add file, Create branch, Delete branch, Merge, Rename or Delete
   repository controls are offered; the API answers `403` to any write by a
   non-owner.
9. **The 409 case (two tabs).** As the owner, open the editor for `README.md`
   on `main` in tab A and again in tab B. Commit in tab A (succeeds). Then
   commit in tab B. Expect: tab B shows "Branch tip has moved" (HTTP `409`)
   and creates no commit; click Reload, redo the edit and it commits.
10. **Stretch: compare, merge, README, rename.** By now `feature` has fallen
    behind `main` (steps 8 and 9 added commits to `main`), so first create a
    branch that is ahead of `main`: switch to `main`, click Create branch,
    name it `topic`, then on `topic` click Add file, add `extra.txt` and
    commit. Back on the repo page: click Compare, choose Base `main` and
    Compare `topic` to see the file diff (`extra.txt` added). Select `main`
    again and click Merge, choose Merge from `topic` into `main`: `main` moves
    to `topic` (fast-forward, no new commit); merging again answers
    `already up to date` (HTTP `400`). The root `README.md` is shown rendered
    under the file tree. Click Rename, enter `hello2`, and confirm that the
    repo now opens at its new URL and the old URL shows not found.
11. **Delete the repo.** On the repo page click Delete repository and
    confirm. Expect: you return to the home page and the repo is gone from the
    list.
12. **Log out.** Click Log out. Expect: the nav bar shows Log in and Register
    again, and write actions are no longer offered.

## Known limits

- **Local only.** The app runs on your machine: the browser talks to the Vite
  dev server, which proxies `/api` to the backend and strips the prefix. The
  backend itself serves no `/api` prefix and no frontend files, and nothing
  here describes production hosting. Local-only is the accepted scope for this
  project (issue #22 closed as not planned).
- Text files only (UTF-8, at most 1 MiB), at most 50 changes per commit; no
  clone/push/fetch, tags, forks, pull requests or private repositories (see
  "Out of scope" in `_docs/plan.md`).
- README rendering supports a safe subset only (headings, paragraphs, lists,
  fenced and inline code, bold, italic); links, images, tables and HTML are
  shown as plain text.
- Renaming a repository does not redirect: the old name answers `404`.
- Merging is fast-forward only; there are no real merges or conflict handling.
- Logout only clears the cookie. The JWT is not revoked, so a stolen token
  stays valid until its 7 days run out.
- Objects are never garbage-collected: the blobs, trees and commits of a
  deleted branch stay in the database (they are removed only when the whole
  repository is deleted).
- No rate limiting on any endpoint (login included).
- `GET /repos` is not paginated.
- Request limits: JSON bodies over 1 MiB answer `413` (commits: 64 MiB); one
  diff response does at most 32 million line-pair cells, later files come back
  `truncated`; a file with more than 5000 lines on either side is not diffed.
