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
Description: `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, and a middleware that reads the `session` cookie (JWT, 3 hours, `JWT_SECRET`) and puts the user id in the request context. Cookie is `SameSite=Lax`, `Secure` in production; the token is never in the body. Tests: valid login, wrong password, logout, `/auth/me`, protected route without a cookie.

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
Status: Done and merged: README, `plan.md` and `tasks.md` synced with the code, QA demo-script fixes from #28, stale comments fixed, duplicate fixture files `backend/gitobj/{commit0,commit1,tree}.bin` and `oids.txt` removed (the tests read `testdata/`).

## 37. Frontend: repo page overflowed at 390px, latest-commit bar squashed
Status: Done (bug from #28), merged.

## 38. Backend: reads concurrent with repo delete returned 500
Status: Done (bug from #28): such reads answer `404`. Merged.

## 39. Frontend: Merge dialog overhung its padding at 320px with long branch names
Status: Done (bug from #28), merged.

## Next round 2 (issues #40-#45)

Groomed by the PM on 2026-10-17 from the user's answers in `questions.md`. Details and acceptance criteria are in the issues. Keycloak was declined by the user (own JWT stays). The rate limiter (#44) and pagination (#45) were added after the user's answers of 2026-10-17.

- #40 Backend: session JWT and cookie last 3 hours instead of 7 days (A)
- #41 OpenAPI contract: `openapi.yaml` for every endpoint, with Go and frontend sync checks (B)
- #42 Makefile: set up, run, test and stop the project with make (C)
- #43 CI: mechanical gates on push to main and pull requests (D)
- #44 Backend: login/register rate limiter with known-device cookie, stdlib, in memory (E)
- #45 Pagination: `page`/`per_page` for GET /repos, `per_page` for commits, Previous/Next UI; schema index (F)

Conflict map:
- `README.md`: #40 owns the Known limits logout bullet, #41 owns a new "API contract" subsection, #42 owns "Quick start with make"; #43 does not edit it.
- `_docs/plan.md`: #40 cookie line and known limits; #41 API-contract sections and discrepancy fixes.
- `_docs/process.md`: #43 only (the "CI and QA" paragraph).
- `openapi.yaml` cookie `Max-Age`: #41 writes 10800 once #40 has merged.
- `frontend/package.json`: #41 only (optional script entry).

- `backend/session.go`, `session_test.go`: #40, then #44 (#44 starts after #40 merges).
- `openapi.yaml`: #41 creates it; #44 adds the `429` responses and #45 the pagination parameters (whichever of #41/#44 merges later updates the `429` rows).
- `_docs/plan.md`: #44 auth rows and "no rate limiting"; #45 repos/commits rows, schema section, "not paginated".
- `backend/db/schema.sql`: #45 only (idempotent index, applied to both Neon branches by the engineer).

Status: #40-#46 are all done, QA PASS and merged into `main`; all GitHub issues are closed.

## Optimization round (issues #47-#54)

Analysis on 2026-10-18. Main costs: one SQL statement per object on reads and writes (about 200 ms each on Neon), per-commit statements in merge ancestry, no HTTP caching or compression, one 497 KB JS bundle, no request cancel or cache in `useFetch`, default pool, slow test suite.

- #47 Request timing and statement counts, baseline in `_docs/performance.md` (do first)
- #48 Batch object writes in POST commit
- #49 Fewer round trips on tree, blob, commit-detail reads
- #50 Merge/compare ancestry in one recursive query
- #51 HTTP caching (immutable oids, ETag) and gzip
- #52 Frontend code-splitting
- #53 Frontend abort, cache, no waterfalls
- #54 Explicit DB pool settings, faster test suite

Status: #47-#55 are all done, QA PASS and merged into `main` (#55 = flaky tests after code-splitting). Decisions: Neon baseline stays an estimate (statements x 200 ms); gzipped bodies carry a weak ETag (RFC-correct); a forged ETag for a missing path on an existing branch gets 304 (documented limit in plan.md).

Original order: #47 first; then #48, #49, #50, #54 (backend, mostly separate files) and #51; #52 and #53 (frontend) in parallel. #51 and #53 both touch caching: #51 first.

## Testing and optimization round (issues #56-#78)

Created 2026-10-18 from three audits (QA layout/function test in the browser, backend + CLI audit, frontend best-practices audit). Worked in the working tree on `feature-private-repos-cli-github-ui`, nothing committed or merged yet (see `_docs/questions.md`, 2026-10-18 entries). Roles: PM and QA ran on Haiku, engineers and analysts on Sonnet.

Backend and CLI
- #62 mg clone/pull: reject unsafe server paths (incl. case-insensitive `.MG`/`.GIT`), never write through symlinks (os.OpenRoot) - QA PASS (round 2)
- #63 login: usernames over 32 bytes rejected before the rate limiter - QA PASS (exact 1 MiB body still 413 by the body cap)
- #64 commit body: non-owners capped at 1 MiB (413), owner check before the big read - QA PASS
- #65 repo PATCH and delete-branch made atomic - QA PASS; #75 stale default_branch skip fixed - QA PASS
- #66 statement_timeout (`DB_STATEMENT_TIMEOUT`, default 15s) and history depth cap 10000 - QA PASS (contract change logged)
- #67 `private, no-cache` on branch-addressed reads of private repos; #68 private-repo test gaps closed - QA PASS
- #69 go mod tidy, CI tidy-diff + govulncheck (advisory), COOKIE_SECURE startup warning - QA PASS
- #70 mg: 8 MiB response cap, concurrent blob fetch (6 workers) - QA PASS

Frontend
- #56 tests for the private-repo UI - QA PASS
- #57 editor unsaved-changes guard (beforeunload, in-app confirm dialog, draft kept across 401) - focus defect fixed; browser Back button not interceptable with BrowserRouter
- #58 dark-mode `color-scheme`/`theme-color`, no `transition-all` - NOT started (look-and-feel, needs the user)
- #59 Home/ProfilePage on useFetch - QA PASS; cross-tab stale list split off as #78
- #60 form focus/aria and busy label - QA PASS (round 2)
- #61 design-system.md vs DESIGN.md, shadows, stray files - NOT started (design decision for the user)
- #71 tab strips wrap below 640 px; #77 breadcrumb and page overflow at 320 px - implemented, unit tests pass; owner-side browser check blocked
- #72 hard-load spinner - closed: dev-server artifact (prod 67-135 ms)
- #73 favicon, no doomed README/commits requests - implemented; browser check blocked; README now waits for the tree (decision logged)
- #74 inline branch-name validation - implemented; browser check blocked
- #76 flaky tests (two races in RepoPage/EditorPage tests) - fixed, 60/60 under load
- #78 session revalidation on focus/visibilitychange - implemented; jsdom only

Open decisions are in `_docs/questions.md` under "Testing/optimization round".

## Teacher-assignment round (issues #79+)

Created 2026-10-19 from the teacher's assignment: close all mvp issues, persistent SQLite storage through an ORM (Postgres kept working), delivery in iterations (frontend + mock -> openapi.yaml -> backend in-memory -> backend SQLite -> connect), all .md in the repo, brainstorm history as .md, real running and testing with bugs filed as `user-test-bug` issues. The issue count is not padded: more issues come from real bugs found while testing. Issues #80, #83, #94, #96, #108 were merged into #79, #82, #93, #84, #107; #109, #110, #112, #114, #115 were closed as padding.

Backend storage (in order)
- #79 Store interface extracted from pgx code + sentinel errors (no dependency)
- #81 config DB_DRIVER / SQLITE_PATH / STORE (none)
- #82 in-memory Store + whole API suite on STORE=memory (#79, #81)
- #84 GORM models + portable migrations + Postgres schema parity test (#81)
- #85 GORM users/auth (#79, #84)
- #86 GORM repos (#85)
- #87 GORM objects (#86)
- #88 GORM refs and CAS commits (#87)
- #89 portable recursive CTE history (#88)
- #90 portable merge/compare ancestry (#89)
- #91 GORM branches (#88)
- #92 GORM visibility / private repos (#86, #91)
- #93 SQLite driver wired, data survives restart + restart test (#85-#92)
- #95 SQLite pragmas, busy handling (#93)
- #97 test matrix memory/sqlite/postgres (#82, #88, #93)
- #98 CI job for sqlite and memory (#97)
- #99 Makefile run-sqlite, run-memory, test-all, clean-data (#81, #93)

Frontend and contract
- #100 single services/API layer audit (none)
- #101 mock service + VITE_USE_MOCK (#100)
- #102 frontend tests against the mock (#101)
- #103 openapi.yaml updated + sync tests (#100)
- #111 backend-unreachable banner and error boundary (#100)

Docs, e2e, connect
- #104 _docs/brainstorm-history.md, secrets scrubbed (none)
- #105 README / plan / AGENTS / process updated (#97, #99)
- #106 _docs/iterations.md (#82, #93, #101, #103)
- #107 browser e2e journey on SQLite, then Postgres and mock (#93, #99, #101)
- #113 connect real frontend to backend on SQLite, smoke (#93, #99, #102, #103)

Suggested order: #79, #81 and #100, #104 in parallel; then #82, #84, #101, #103; GORM areas #85-#92 in sequence; #93, #95, #99; #97, #98, #102; #111, #105, #106; #113, #107.
