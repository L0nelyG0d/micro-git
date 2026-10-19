# End-to-end browser check of the main user journey (issue #107)

A real browser (Playwright MCP browser tools, no repo dependency; adding Playwright to the repo is out of scope for #107) drives the real stack through the whole journey. The script is [`_docs/e2e/journey.js`](e2e/journey.js): paste it into `browser_run_code_unsafe`, set `BASE`, `STACK`, `SHOTS`, `LAYOUT` at its top. Screenshots go to a folder outside git; they are named below, not committed.

## Start the stacks

Free ports, never 5173/8080 (those may be someone else's vite/backend). `make dev-sqlite PORT=<free> VITE_PORT=<free>` is the one-command SQLite stack (data in `data/mg.db`, wiped with `make clean-data`). The recorded runs used the equivalent throwaway form (temp SQLite file, random `JWT_SECRET`, as `scripts/smoke-sqlite.sh` does):

| Stack | Backend | Frontend |
| --- | --- | --- |
| sqlite | `DB_DRIVER=sqlite SQLITE_PATH=<tmp>/e2e.db JWT_SECRET=<random 32+ bytes> PORT=<bp> go run .` (in `backend/`) | `API_PROXY_TARGET=http://127.0.0.1:<bp> npx vite --host 127.0.0.1 --port <vp> --strictPort` (in `frontend/`) |
| postgres | `DB_DRIVER=postgres DATABASE_URL=postgres://test@localhost:54329/mg?sslmode=disable JWT_SECRET=... PORT=<bp> go run .` (local throwaway Postgres from AGENTS.md) | same as above |
| mock | none | `VITE_USE_MOCK=true npx vite --host 127.0.0.1 --port <vp> --strictPort` |

The browser talks only to the Vite port (the `/api` proxy), exactly as in `make dev-sqlite`.

## Steps and expected results

Users are `alice<n>` and `bob<n>` (random suffix per run, so reruns on a used database do not collide). Viewport 1280 x 800, light, unless stated.

| # | Step | Expected |
| --- | --- | --- |
| 1 | Open `/`, click Register, submit username and password | Account created, signed in at once, lands on `/` with a "Log out" button |
| 2 | Log out, then log in at `/login` | Back on `/`, signed in, empty repository list with "New repository" |
| 3 | New repository `demo` (public, with a description) | Toast, `alice/demo` listed with a Public badge |
| 4 | Editor `/alice/demo/new/main`: `README.md` with two lines, message "first commit", Commit | Redirect to the commit page, diff `+2 -0`, "root commit" |
| 5 | Browse: repo page (tree row, rendered README), file page, history, commit page via the hash | Tree lists `README.md`, README rendered, blob shows content, history lists "first commit", diff `+2 -0` |
| 6 | Create branch `feature` from the repo page dialog | Lands on `/alice/demo/tree/feature` |
| 7 | Editor on `feature`: `docs/notes.txt`, message "feature commit" | Commit page for the new commit |
| 8 | Compare `main...feature` | Shows `docs/notes.txt` added `+1 -0` |
| 9 | Merge dialog (feature into main), confirm | `main` now has the `docs` folder (fast-forward) |
| L | Layout pass (sqlite run only): home, repo, file, history, compare, editor, settings, profile at 320, 768 and 1280 px, in light and dark | No horizontal overflow (`scrollWidth <= innerWidth`), no clipped controls on the screenshots |
| 10 | Settings, "Change visibility", Make private | Settings shows "currently private" |
| 11 | Log out | Nav shows Log in / Register |
| 12 | Anonymous opens `/alice/demo` | Not-found page (a private repo is a 404, never a 403) |
| 13 | Register and sign in as `bob<n>` | Signed in |
| 14 | Bob opens `/alice/demo` and the home list | Not-found page; the repo is not in the list |
| 15 | Bob logs out | Signed out |
| 16 | Alice logs in, Make public, logs out; bob opens `/alice/demo` | Bob sees the tree and README, and no Settings tab (read-only) |

Console check (step 17, "console clean"): every `console.error` and page error is collected and matched on status AND URL path. Only these are expected: `401` on `/api/auth/me` (anonymous visitor), `404` on `/favicon.ico`, and `404` on `/api/repos/<alice>/demo...` (the private repo read by the anonymous or second user). Anything else (another URL, another status, any other `console.error`, any `pageerror`) makes step 17 FAIL.

Layout step (L): for each page it also waits for a marker text inside `<main>` (Repositories, Files, hello, first commit, Compare branches, File path, Visibility, the username). A page that did not load or render (for example with the backend down) fails the step, as does any horizontal overflow.

Result: the function returns `{ verdict: 'PASS' | 'FAIL', passed, failed, failedSteps, ... }`; the verdict is FAIL if any step fails, including 17.

## Run recorded 2026-10-19 (branch issue-107)

Result table (step, pass/fail, issue number):

| Step | sqlite | postgres (:54329) | mock | Issue |
| --- | --- | --- | --- | --- |
| 1 register | pass | pass | pass | - |
| 2 logout + login | pass | pass | pass | - |
| 3 create repo | pass | pass | pass | - |
| 4 commit README in editor | pass | pass | pass | - |
| 5 browse tree/file/history/diff | pass | pass | pass | - |
| 6 create branch | pass | pass | pass | - |
| 7 commit on branch | pass | pass | pass | - |
| 8 compare | pass | pass | pass | - |
| 9 merge | pass | pass | pass | - |
| L layout 320/768/1280, light + dark | pass (48 screenshots, no overflow) | not run | not run | - |
| 10 make private | pass | pass | pass | - |
| 11 logout | pass | pass | pass | - |
| 12 anonymous sees 404 | pass | pass | pass | - |
| 13 second user | pass | pass | pass | - |
| 14 other user cannot see private repo | pass | pass | pass | - |
| 15 second user logout | pass | pass | pass | - |
| 16 make public, read-only for other user | pass | pass | pass | - |
| 17 console clean (exact allow-list) | pass | pass | pass | #125 |
| Verdict | PASS (18 steps) | PASS (17) | PASS (17) | - |

No failure remained, so no `user-test-bug` issue was created. Differences between the stacks: none in behaviour. Two properties of the mock are by design, not bugs: its data lives in memory (a full page load wipes it, README "Mock"), so the mock run navigates client-side only (`history.pushState`); and it has no network, so there is no 401/404 console noise (`ignored401_404: 0`).

The first mock run reported step 14 as a failure; it was the script's check (it read the home list before the route had rendered). The check now waits and looks for the list link; the rerun passed and a separate reproduction (public repo made private, owner logs out, second user signs in, home list read at 0 s and 1.5 s) showed an empty list.

Observations (not failures, no issue opened):

- At 320 px the logo wraps to two lines ("micro / git") and a long username is ellipsised in the header; nothing overflows.
- The history table links the commit hash, not the message.
- The favicon 404 and the anonymous `/auth/me` 401 still appear as red console lines (already #73).

Negative checks for #125 (scratch copy of the script, not committed): an injected `console.error('unexpected')` before step 17 gives `verdict: FAIL`, `17 console clean: unexpected console entries: unexpected (page /)`, all other 16 steps pass. With the sqlite backend killed, the run is FAIL and the run is `FAIL` (0 passed, 18 failed), the layout step L fails with `page did not render: repo@320/light: "Files" not rendered; blob@320/light: "hello" not rendered; ...` and step 17 names the `502` entries (`/api/auth/me`, `/api/repos`, `/api/auth/register`). The postgres run used an isolated database `mg_e107b` (created and dropped for the run).

Screenshots of this run: `/private/tmp/claude-501/-Users-akniyetislambekkyzy-Desktop-Assignnment2-webfront/d68891ea-aa89-44d0-9645-30cebc06486a/scratchpad/shots/{sqlite,postgres,mock}/` (`01-home-anon` ... `19-bob-public`, and `L-<page>-<width>-<light|dark>`).
