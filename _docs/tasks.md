# micro git — Task Backlog

Issue numbers match task numbers. Later tasks depend on earlier ones. Details of endpoints, tables and rules are in `_docs/plan.md`.

## 1. Go backend skeleton with a passing test
Goal: Scaffold a Go module under `backend/` that compiles and has one passing test.
Description: Module `github.com/L0nelyG0d/micro-git/backend`. Minimal `net/http` server on `:8080` with `GET /health` returning `200 OK`, and one `httptest` test for it. No database yet.

## 2. React frontend skeleton with a passing test
Goal: Scaffold a Vite React + TypeScript app under `frontend/` that renders and has one passing test.
Description: Tailwind CSS v4, `shadcn init` following `_docs/design-system.md` (light and dark tokens in `src/index.css`), a bare `<App />` heading, one Vitest + Testing Library test. Vite dev server proxies `/api` to `http://localhost:8080` and strips the prefix. No routing or API calls yet.

## 3. Neon database schema
Goal: Create the four tables from `plan.md` on Neon and commit the SQL.
Description: `backend/db/schema.sql` with `users`, `repos`, `objects`, `refs` exactly as in `_docs/plan.md` (case-insensitive unique username and per-owner repo name, `objects.type` CHECK, cascade deletes). Applied to the main Neon branch and the test branch. Connection strings only in the git-ignored `.env`.

## 4. Backend: user registration endpoint
Goal: `POST /auth/register` creates a user.
Description: Accepts `username` and `password`, bcrypt hash, `201` with id and username, `400` for a bad username or password shorter than 8, `409` if taken. Adds the pgx pool (`DATABASE_URL`) and the shared JSON `{"error": ...}` helper. Tests: success, duplicate, short password, bad username.

## 5. Backend: login, logout, current user and JWT middleware
Goal: Session handling with an httpOnly cookie.
Description: `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, and a middleware that reads the `session` cookie (JWT, 7 days, `JWT_SECRET`) and puts the user id in the request context. Cookie is `SameSite=Lax`, `Secure` in production; the token is never in the body. Tests: valid login, wrong password, logout, `/auth/me`, protected route without a cookie.

## 6. Backend: git object store (pure Go)
Goal: Encode, hash and parse blob, tree and commit objects in git's exact format.
Description: Package under `backend/` with no database. `sha1("<type> <len>\0"+body)`, tree entries sorted git-style, commit with parents, author and committer. Golden tests against real git values (empty blob `e69de29b…`, `"test content\n"` → `d670460b…`, plus a tree and a commit generated with real `git`).

## 7. Backend: tree builder (pure Go)
Goal: Apply a list of file changes to a root tree and return the new root tree.
Description: Works against a small in-memory object-store interface, no database. Handles nested paths, creating and deleting files, directories that disappear when emptied, file/directory conflicts, "no changes" detection, and the path and size rules in `plan.md`. Rebuilds only the touched trees.

## 8. Backend: line diff (pure Go)
Goal: Line-based diff between two texts.
Description: LCS diff producing hunks of `+`, `-` and context lines, plus per-file `added` / `modified` / `deleted` status for two trees. No database.

## 9. Backend: repository store and repo endpoints
Goal: Postgres-backed object and ref storage plus the repo endpoints.
Description: `GET /repos`, `POST /repos`, `GET /repos/{owner}/{repo}`, `DELETE /repos/{owner}/{repo}` per `plan.md`, with a store for `objects` (insert-if-absent, load) and `refs` (compare-and-swap). Tests include `401` without a cookie and `403` for a non-owner.

## 10. Backend: commit endpoint
Goal: `POST /repos/{owner}/{repo}/commits` writes a commit from file changes.
Description: Uses the object store, tree builder and ref compare-and-swap. First commit on an empty repo creates the branch. `409` when `expected_tip` is stale, `400` for "no changes" and rule violations, `401`/`403` for auth. Tests including two racing commits.

## 11. Backend: branches, tree and blob endpoints
Goal: Branch management and file browsing.
Description: List, create and delete branches (default branch cannot be deleted); `GET .../tree/{ref}/{path...}` and `GET .../blob/{ref}/{path...}` where `ref` is a branch name or a commit oid. Tests for missing ref, missing path, file vs directory, non-owner writes.

## 12. Backend: commit log and commit detail
Goal: History and diffs.
Description: `GET .../commits?ref=&page=` (first-parent walk, 50 per page) and `GET .../commits/{oid}` with changed files and diff hunks against the first parent (root commit diffs against the empty tree).

## 13. Frontend: app shell, routing, auth pages
Goal: Routing, nav, auth guard and working login and register forms.
Description: React Router routes from `plan.md`, placeholder pages, compact sticky nav with logout, `GET /api/auth/me` to know the session (`credentials: 'include'`, never `localStorage`), login and register forms with inline errors. Public pages stay reachable when logged out. Tests mock `fetch`.

## 14. Frontend: repository list and new repository
Goal: Home page lists repositories and lets a logged-in user create one.
Description: Table or cards from `GET /api/repos`, "New repository" form (name, description) with inline errors, loading, empty and error states, toast on success.

## 15. Frontend: repository page and file viewer
Goal: Browse a repo.
Description: `/:owner/:repo` with file tree, breadcrumbs, branch switcher, latest commit bar, empty-repo state; `/:owner/:repo/blob/:ref/*` file viewer. The "Edit" button shows only to the owner.

## 16. Frontend: history and commit diff
Goal: Commit list and commit detail pages.
Description: `/:owner/:repo/commits/:ref` with pagination, `/:owner/:repo/commit/:oid` with per-file diff hunks coloured with token colours and `+`/`-` markers (never colour alone).

## 17. Frontend: editor and commit form
Goal: Create, edit and delete files from the browser.
Description: Editor page with a textarea, path field for new files, commit message, branch choice, "Delete file". Handles `409` (tip moved) with a clear message and a way to reload. Redirects to the new commit.

## 18. README and end-to-end check
Goal: Someone can run the project from the README and follow a demo script.
Description: Root `README.md` with setup (env vars, schema, run backend and frontend), a short architecture note on the git object model, and a demo script. Run the whole flow once against the test branch and record results.

## 21. Stretch features (split, done)
Split into #24 fast-forward merge, #25 compare two branches, #26 render README.md, #27 rename repository. All four are merged and closed. Follow-ups outside the 18 MVP tasks: #19 (closed, duplicate), #20 delete branch UI (done), #22 production serving (closed, local-only), #23 `refs` foreign key to `objects` (done). API contracts: `plan.md` > API contracts > "Stretch endpoints".

## Next round (issues #29-#39)

Written by the system analyst after all MVP and stretch tasks were done. Details and acceptance criteria are in the issues. #28 (end-to-end demo, closed) added bug issues #37-#39. State: #29-#35 and #37-#39 are done and merged into main; #36 (this docs sync) is on branch `issue-36-docs-sync`, waiting for merge.

Conflict map (issues that touch the same files must run in order; everything else can run in parallel):
- `backend/history_test.go`: #29 and #30 (serialize).
- `README.md`: #31, #30 text and #36 (run #36 last). `_docs/plan.md`: #32 and #36. `backend/store.go`, `handler.go`, `config.go`: #29/#31 before #36.
- `frontend/src/pages/RepoPage.tsx` and `BranchSwitcher.tsx`: #35 only (serialize with any bug fix from #28 that touches them).
- Independent: #29, #30, #31, #32, #33, #34, #35.

## 29. Backend: commit log in one query instead of one per commit
Goal: `GET .../commits` walks first parents with a constant number of queries (today 51 for page 1, 50*N+1 for page N).
Description: A recursive-CTE store method used by `listCommits`; same response; a statement-count test and a test that a message containing a `parent <oid>` line is never read as a parent. Files: `history.go`, `store.go`, `history_test.go`.
Status: Done. `listCommits` uses one recursive-CTE query.

## 30. Backend tests: shared pool, cheap bcrypt, batched fixtures
Goal: The database tests (about 12 minutes remotely) run at least 40% faster with no test lost.
Description: One lazily created pool per test binary, a `bcryptCost` variable lowered in `TestMain`, `newHistFixture` inserts its 120 commits in one batch, other serial loops reviewed. Files: `auth_test.go`, `auth.go`, `session.go`, `history_test.go`.
Status: Done. Shared pool, cheap bcrypt in tests, bulk fixture; the full backend suite now takes roughly 10 minutes on Neon (was about 12).

## 31. Backend: server timeouts, graceful shutdown, secret length, safe headers
Goal: The process survives slow clients, shuts down cleanly, rejects a `JWT_SECRET` under 32 bytes and sends `nosniff` and `no-store` (auth) headers.
Description: `http.Server` with timeouts, signal handling, config check, small header middleware. Files: `main.go`, `config.go`, `handler.go`, tests, one README row.
Status: Done. Server timeouts, graceful shutdown, `JWT_SECRET` of at least 32 bytes, `nosniff` and `no-store` (on `/auth/*`).

## 32. Backend: cap JSON body size and total diff work
Goal: No endpoint reads an unbounded body (1 MiB, `413`; commits keep 64 MiB) and one diff response does at most 32 million LCS cells (later files `truncated`).
Description: Limits in `httpx.go` and `linediff`, tests, `plan.md` sentences. Files: `httpx.go`, `linediff/text.go`, `linediff/tree.go`, tests, `_docs/plan.md`.
Status: Done. JSON bodies over 1 MiB answer `413` (commits 64 MiB); a diff response does at most 32 million LCS cells.

## 33. Backend: concurrency tests and 404 for repo deleted mid-commit
Goal: Races on refs and unique indexes are tested (8 goroutines on commit, first commit, rename, branch create) and a repo deleted during a commit answers `404`, not `500`.
Description: New `concurrency_test.go`, a test hook in `commits.go`, FK violation mapping in `db.go`.
Status: Done. `concurrency_test.go`; a repo deleted mid-commit answers `404`.

## 34. Frontend: session-expired feedback and 401 tests for owner actions
Goal: A `401` from delete branch, create branch, merge, rename or delete repository logs the user out visibly (toast) and every path has a test.
Description: Toast in `expireSession`, tests in `RepoPage.test.tsx`, `MergeDialog.test.tsx`, `RenameRepo.test.tsx`. Files: `AuthContext.tsx` and those tests.
Status: Done. A `401` from an owner action shows a session-expired toast and logs out; tests added.

## 35. Frontend: page titles, skip link, focus, reduced motion, 320px layout
Goal: Keyboard and screen-reader basics from the design system, and the repository page fits a phone.
Description: `TitleSync` and skip link in `App.tsx`, reduced-motion block in `index.css`, wrapping in `BranchSwitcher.tsx`, `Nav.tsx` and the `RepoPage.tsx` header, manual 320 px check.
Status: Done. Page titles, skip link, focus, reduced motion, 320 px layout.

## 36. Docs: sync plan and README with the code, remove stale files
Goal: `plan.md`, `README.md` and comments match the code; four duplicate fixture files are removed.
Description: Missing routes, package layout, known limits, test duration, stale "later tasks" comments. Run last: it edits files also touched by #30, #31, #32 and #29.
Status: Done on branch `issue-36-docs-sync` (merge pending): README, `plan.md` and `tasks.md` synced with the code, QA demo-script fixes from #28, stale comments fixed, duplicate fixture files `backend/gitobj/{commit0,commit1,tree}.bin` and `oids.txt` removed (the tests read `testdata/`).

## 37. Frontend: repo page overflowed at 390px, latest-commit bar squashed
Status: Done (bug from #28), merged.

## 38. Backend: reads concurrent with repo delete returned 500
Status: Done (bug from #28): such reads answer `404`. Merged.

## 39. Frontend: Merge dialog overhung its padding at 320px with long branch names
Status: Done (bug from #28), merged.
