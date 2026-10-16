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

## 21. Stretch features (split)
Split into #24 fast-forward merge, #25 compare two branches, #26 render README.md, #27 rename repository. API contracts: `plan.md` > API contracts > "Stretch endpoints".
