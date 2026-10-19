# Iterations

The teacher-assignment round (issues #79 and up, listed in `_docs/tasks.md`) delivered the app in five steps: mock, openapi, memory, sqlite, connect. Each step can be run on its own. The API contract is [`openapi.yaml`](../openapi.yaml); the backlog is `_docs/tasks.md`, issue numbers are `L0nelyG0d/micro-git` issues.

## Layers

| Layer | What it is | Where |
| --- | --- | --- |
| UI | React pages and components | `frontend/src/pages`, `components` |
| ApiService | One interface, one method per openapi `operationId`; the only code the UI calls (#100) | `frontend/src/services/ApiService.ts`, `index.ts` |
| HTTP | `HttpApiService` over `fetch` (`services/http.ts`, the only `fetch(`), the Vite `/api` proxy, Go handlers | `frontend/src/services/`, `backend/*.go` handlers |
| Store | Go `Store` interface (#79) with three implementations: `pgxStore`, `gormStore`, `memStore` | `backend/storage.go`, `store.go`, `gormstore.go`, `memstore.go` |
| DB | Postgres, a SQLite file, or nothing (memory) | `backend/db/schema.sql`, `models.go` migrations, `data/mg.db` |

| Iteration | UI | ApiService | HTTP | Store | DB |
| --- | --- | --- | --- | --- | --- |
| 1 Mock | real | `MockApiService` (fake) | none | none | none (in browser memory) |
| 2 OpenAPI | real | both services checked against the contract | contract only | none | none |
| 3 Memory | real | `HttpApiService` | real | `memStore` (fake) | none |
| 4 SQLite | real | `HttpApiService` | real | `gormStore` | real SQLite file |
| 5 Connect | real | `HttpApiService` | real, through the Vite proxy | `gormStore` | real SQLite file |

## 1. Mock: frontend on an in-browser backend

Purpose: build and test the UI with no server. Issues: #100 (single ApiService layer, merged `64a3854`), #101 (`MockApiService` behind `VITE_USE_MOCK`, merged `8593f6e`), #102 (screen tests run on the mock, merged `be34c02`), #111 (backend-unreachable state and error boundary, merged `cd5d57d`).

Run: `cd frontend && npm ci && VITE_USE_MOCK=true npm run dev` (http://localhost:5173). Tests: `cd frontend && npm test`.

Faked: the whole backend. `MockApiService` keeps users, repositories, git objects and sessions in memory and computes real git object ids and diffs in the browser (`mockGit.ts`). Data is lost on reload. Without the flag the build contains no mock code (dynamic import in `services/index.ts`).

Real: all UI code, routing, forms, error handling, and the `ApiService` seam.

## 2. OpenAPI: the contract both sides must keep

Purpose: one written contract so frontend, mock and backend cannot drift. Issues: #103 (update `openapi.yaml`, add sync tests, merged `d349197`); the contract itself came from #41 earlier. #100 gave the interface one method per `operationId` (23).

Run: `cd backend && go test -run TestOpenAPIMatchesRoutes ./...` (every route in `handler.go` is in `openapi.yaml` and the reverse; no database needed), `cd frontend && npm test` (`openapi.test.ts` and `openapi.mock.test.ts`; the latter checks that `MockApiService` answers carry the required keys and types of the contract), and `npx --yes @redocly/cli@2.64.1 lint openapi.yaml` (the CI `OpenAPI lint` job).

Faked: nothing new; the mock is still the only backend the UI sees. Real: the contract file and the tests that tie it to the Go routes and to the mock. The checks are line scanners (no YAML parser is approved, see `_docs/questions.md`, "#103 openapi checks").

## 3. Memory: real HTTP backend, data in a Go map

Purpose: run the real handlers, auth and rules without any database. Issues: #79 (`Store` interface and sentinel errors `ErrNotFound`, `ErrConflict`, `ErrForeignKey`; no behaviour change; #80 was merged into it), #81 (config `STORE`, `DB_DRIVER`, `SQLITE_PATH`), #82 (`memStore`, all 23 `Store` methods plus `InTx`; #83, the whole API suite on `STORE=memory`, was merged into it).

Run: `make run-memory` (backend on :8080, `STORE=memory`, random `JWT_SECRET` in the git-ignored `.env`), then `make frontend` in a second terminal. Test: `cd backend && TEST_STORE=memory go test -p 1 -race ./...`.

Faked: persistence. `memStore` holds everything behind one RWMutex; data is lost when the process exits (the backend prints a warning). Real: handlers, JWT cookie sessions, bcrypt, rate limiter, compare-and-swap commits, case-insensitive uniqueness, cascade delete, visibility rules.

## 4. SQLite: persistent store through GORM

Purpose: data that survives a restart, with no Postgres, while Postgres keeps working. Issues: #84 (GORM models and migrations portable across SQLite and Postgres; schema parity test, #96 merged into it), #85 (users and auth), #86 (repositories), #87 (git objects), #88 (refs and compare-and-swap commits, `InTx`), #89 (commit history as one recursive CTE), #90 (merge and compare ancestry as one recursive statement), #91 (branches), #92 (private repositories and `ListRepos`), #93 (SQLite driver wired, data survives restart; #94 merged into it), #95 (pragmas and busy handling), #97 (test matrix), #98 (CI job), #99 (Makefile targets). Driver: `github.com/glebarez/sqlite`, no cgo.

Run: `make run-sqlite` (backend on :8080, data in `data/mg.db`), pair it with `make frontend`; `make clean-data` deletes the SQLite files. Without make: `cd backend && DB_DRIVER=sqlite SQLITE_PATH=/tmp/mg.db JWT_SECRET=<32+ bytes> go run .`. Tests: `make test-all` (memory, sqlite and an isolated Postgres database on :54329, then the frontend) or `cd backend && TEST_STORE=sqlite go test -p 1 -race ./...`.

Faked: nothing in the backend. Real: the file database with `foreign_keys=ON`, WAL, `synchronous=NORMAL`, a 10 s busy timeout and `_txlock=immediate` on every connection; one writer at a time through SQLite's write lock (the pool is not capped to one connection, see the #95 entry below). `backend/known_skips.txt` lists the tests that are skipped per store.

## 5. Connect: real frontend on real backend (SQLite) (#113)

Purpose: prove the real UI works on the real backend and the real SQLite file. Issue: #113 (merged `3f25652`). Faked: nothing but the dev-server proxy that makes the browser see one origin. Real: `HttpApiService`, the Go handlers, `gormStore`, the SQLite file.

The real frontend (`VITE_USE_MOCK` unset, `HttpApiService`) talks to the real backend on SQLite through the Vite `/api` proxy (the proxy strips `/api`; the browser sees one origin, so the `session` cookie needs no CORS).

Run it (two ways, same result). If 5173 is taken add `VITE_PORT=<other>` to the make command (`make frontend VITE_PORT=5174`, `make dev-sqlite VITE_PORT=5174`); if 8080 is taken add `PORT=<other>` to both:

- One command: `make dev-sqlite`. Backend on `:8080` (`PORT=` to change) with `DB_DRIVER=sqlite` and `data/mg.db`; Vite on `http://localhost:5173`. A random `JWT_SECRET` goes into the git-ignored `.env` if missing. No Postgres. Ctrl-C stops both. `make clean-data` wipes the SQLite files.
- Two terminals: `make run-sqlite` and `make frontend` (use the same `PORT=` for both if 8080 is taken).

Smoke: `make smoke-sqlite` (`scripts/smoke-sqlite.sh`). It picks two free ports, builds the backend, starts it on a temp SQLite file and a Vite dev server with the proxy pointed at it, then through the Vite port only:

- SPA: `/` and a deep link return `index.html`.
- Journey: register (no cookie), login (`session` cookie HttpOnly, Path=/, not Secure over http), create repo, first commit, second commit, stale tip gives 409 with `current_tip`, history, tree, blob, commit detail, repo list.
- Reload: a new request with the same cookie jar is still logged in; logout, 401, login again.
- Persistence: the backend is restarted on the same file; session and history survive.
- Parity: `frontend/src/services/parity.live.test.ts` runs one journey through `MockApiService` and the real `HttpApiService` and compares the JSON shapes (keys and value types) and error statuses/messages of every step. It is skipped unless `SMOKE_BASE_URL` is set (the script sets it).

Needs `curl`, `jq`, `python3`, Go and `make setup` done. Everything is removed on exit; no secrets are printed.

Not covered here: the browser E2E (#107) and pixel-level UI comparison; the parity check compares response shapes, which is what makes the UI identical.

## Iterations log (what happened)

Written 2026-10-19 from issue comments, `git log origin/main` and `_docs/questions.md`. Every issue passed the same loop: engineer branch, QA PASS or FAIL, reviewer APPROVE or CHANGES REQUESTED, merge by the reviewer, QA confirms on `main`. Only the findings that changed something are listed.

Backlog shape: on 2026-10-18 #80, #83, #94, #96 and #108 were closed as merged into #79, #82, #93, #84 and #107, and #109, #110, #112, #114 and #115 were closed as padding (not needed for the assignment).

### Order of delivery (merge commits on `main`)

- Mock and contract: #100 `64a3854`, #101 `8593f6e`, #111 `cd5d57d`, #103 `d349197`, #102 `be34c02`.
- Memory: #79 `e093a6d`, #81 `21b7969`, #82 `666f70c`.
- SQLite: #84 `a808158`, #85 `e7e09d4`, #86 `6c66d33`, #87 `2d483ef`, #88 `090cd3e`, #89 `3976e7f`, #91 `27cf63f`, #92 `f9ee90d`, #90 `99bc255`, #93 `249a028`, #99 `5eadd9b`, #95 `0636b06`, #98 `5351a5b`, #97 `1caae19` plus the fix `2426f0e`.
- Connect: #113 `3f25652`.
- Docs: #104 `df38075`, #105 `8068b06` (QA confirmed on `main`, issue closed), #124 `dc71775` (merged; issue still open when this was written).

### What QA found and what was fixed

- #84: QA FAIL, SQLite created `oid char(40)` while the criterion says TEXT, and the identity-versus-serial allowlist reason understated a difference. Fixed in `e50a0ec` (oid TEXT on SQLite, identity ids on Postgres). Reviewer asked to reword a test skip reason that named validation that did not exist yet.
- #85: QA FAIL, `TestLoginOversizedUsername` was skipped on SQLite because the test group was picked from the wrong caller. Fixed in `35e1fe8` (the group is found from the running Test function; `4ab2ded` was the commit QA failed). The reviewer then asked for a stale comment (`store.go`) to be reworded (`068f8b5`).
- #86: QA FAIL twice on the per-test SQLite skip list (tests that could run were skipped, some reasons were wrong). Fixed by gating per test (`sqlitePending`) with correct reasons.
- #87: QA FAIL, `TestObjectStore` still ran on Postgres only. Fixed in `81ce7b0` (runs on every store).
- #88: QA found a stale pgx skip, filed as #121 (merged).
- #89: QA FAIL, a limit above int4 failed to encode on Postgres through GORM. Fixed in `3538497` (cast to bigint, test for huge limits).
- #90: QA FAIL, the first version followed only 16 parents per commit, so an octopus merge with 17 or more parents gave a different answer than the pgx store. Reworked to follow every parent in one statement (`09d79ce`); QA PASS with octopus merges of 17 to 40 parents.
- #91: Found by QA on #91: deleting a branch racing a change of the default branch could leave the default pointing at a deleted branch on Postgres (170 of 200 rounds on the pgx store, 5 of 200 on GORM). Filed as #122, fixed with a per-repo advisory lock (`835bd79`, merged `1156828`).
- #123: `TestListRepos` depended on how many rows the shared Postgres already held. Fixed in #123 (the test pages through the full list).
- #93: QA FAIL, the documented run command (`go run ./backend` from the repo root) does not work because the module is in `backend/`. Fixed in `02d916a`. The restart test (kill -9 then restart, same data, old session cookie still valid) passed.
- #95: QA PASS. Decision stashed in `_docs/questions.md`: `_docs/plan.md` had said "a single writer connection"; the pool is not capped at one because a write on the outer store while a transaction is open would deadlock. Default used: document one writer at a time through the SQLite write lock plus the 10 s busy timeout. Alternative: cap the pool at one and forbid outer-store use inside `InTx`. Pending the user's answer.
- #97: QA FAIL, the docs claimed an unset `TEST_DATABASE_URL` fails the matrix but the script fell back to the local :54329 server. Decision stashed in `_docs/questions.md`: keep the fallback, an unreachable server fails with "not reachable"; docs changed to match. After the merge, CI on `main` failed the secret scan because a fake password URL in `scripts/test-pgurl.sh` matched the scan pattern. Fixed in `10d3117` by building the URL from parts at run time, scan unchanged; CI run on `2426f0e` was green. Related earlier case: #116 (`config_test.go` fixture, same cause, fixed before #84).
- #101: reviewer CHANGES REQUESTED, `VITE_USE_MOCK` was documented only in `.env.example`; README section added. QA also had the mock stop starting a session on register (the backend does not), and tree and branch ordering changed to UTF-8 byte order like the backend.
- #102: QA FAIL three times. The tests still stubbed `fetch` in all 16 files, some "kept on fetch" reasons were false, and the mock's merge had no tip check. Reworked: only `HttpApiService.test.ts` and `useFetch.test.tsx` keep a fetch stub, the mock got the merge compare-and-swap, and both 409 "branch tip has moved" answers now carry `current_tip` as `openapi.yaml` says (`db67fa8`). QA PASS.
- #111: QA FAIL, Retry on a failed lazy page chunk replayed the cached rejection. Fixed in `354aa98` (the chunk import is retried, reload if that fails too).
- UI findings from QA in the same rounds: #117 editor 3 px overflow at 320 px (found on #57), #119 nav overflow at 320 px (decision stashed: below 360 px the label reads "Offline"), #120 invisible keyboard focus rings (QA FAIL on the logo and plain links, fixed in `7f5f55d`; reviewer asked for a rebase because `_docs/questions.md` conflicted; decision stashed: full-opacity `--ring`). #118 (Playwright layout check in CI) was closed as not planned.
- #105 (docs for the new storage): two QA FAIL comments on `b7d68b7`, both listing stale claims in the first docs pass: private repositories called out of scope in README and `plan.md`, `plan.md` repos table without the `private` column, `RepoTable` instead of `RepoRows.tsx`, pointers to the superseded `design-system.md`, and a `-timeout 30m` that disagreed with 40m in README and Makefile. The final FAIL added two more: the `plan.md` frontend route table without the `/:owner` and `/:owner/:repo/settings` rows (`frontend/src/routes.tsx` lines 25 and 33), and the README "Package layout" section without ProfilePage, SettingsPage and several components. Fixed in `823513f` and `23eb463`; QA PASS, merged `8068b06`.
- #121: removed a stale pgx skip so `TestStoreObjectsRepoGone` runs on every store.

### Decisions logged in `_docs/questions.md` this round

Checkbox state of `_docs/questions.md` when this was written:

- #95 single-writer wording (above): unchecked, waiting for the user.
- #97 unset-URL fallback (above): unchecked, waiting for the user.
- #119 nav label below 360 px: unchecked, waiting for the user.
- #120 focus ring: unchecked, waiting for the user.
- #103 no YAML parser approved, a line scanner extends the existing test: written as a plain section without a checkbox, no answer recorded.

### Still open when this was written

Open issues per `gh issue list --state open`:

- #106 this document (branch `issue-106-iterations-log`).
- #107 browser E2E of the main journey on SQLite.
- #124 docs fix (README port note, api client path, Settings route row): merged as `dc71775`, issue still open.
- #125 E2E journey: console errors and page errors never fail the run.
