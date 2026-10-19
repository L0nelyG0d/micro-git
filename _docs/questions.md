# Open questions for the user

Decisions that need the user and were not made while they were away. Work continues around them with the simplest reversible default, noted per item. The user will ask about these on return; answer from this file.

Format: `- [ ] (#issue) question - default used - date`

- [x] (#3/#9) Should `refs(repo_id, oid)` get a foreign key to `objects`? Data-model change. RESOLVED 2026-10-16: user says use the FK if it fits; done in issue #23. - 2026-10-15
- [x] (#18/#22) How is `/api` served outside the Vite dev proxy (deployment)? Stack decision. RESOLVED 2026-10-16: local-only is fine; #22 closed as not planned. - 2026-10-15
- [x] (#12) A commit that only adds or removes a final newline: `modified` with no hunks acceptable? Default: yes. - 2026-10-15
- [x] (#15/#19/#20) Branch create and repo delete UI placed inside #15; branch delete UI deferred to #20; #19 overlaps #15. Default: as groomed. - 2026-10-15
- [x] (#21) Stretch features (merge, compare, README render, rename) have an issue but are not part of the 18 tasks. Default: skipped unless time remains. - 2026-10-15
- [x] (#13) `react-hook-form` (shadcn `Form`) is not in the approved packages list. Default: plain controlled inputs with `Label`/`Input`; swap to `Form` if approved. - 2026-10-15
- [x] (infra) Neon test branch compute is 0.25 CU (max) and auto-suspends; parallel test suites stall and cold starts fail. Raising max CU or disabling suspend costs more. Default: unchanged, suites run serially (`go test -p 1`). - 2026-10-16

RESOLVED 2026-10-16: questions 3, 4, 5, 6, 7 confirmed as defaults. #19 closed as duplicate of #15/#20.

## Stretch (#24-#27), stashed 2026-10-16
- [x] (#27) Should the old repo name redirect to the new one after rename? Default: no, old name 404s (no extra table).
- [x] (#24) Merge when already up to date: 400 or 200 no-op? Default: 400 `already up to date`.
- [x] (#25) Compare route: query params or `a...b` in path? Default: query params (branch names may contain dots).
- [x] (#26) Real markdown renderer with links/images (needs a package, none approved)? Default: safe in-repo subset renderer, no links/images.

RESOLVED 2026-10-16: user approved all four stretch defaults (no rename redirect, 400 already up to date, query-param compare, safe-subset README).

## Next-round analysis (#29-#36), stashed 2026-10-16 - all default "no, keep as is"
- [ ] Logout does not revoke the JWT (valid 7 days). Revoking needs a token_version column or sessions table. Default: no; documented as a known limit (#36). - for this dont we need a keycloak authorization with a signature that  to not pass the credentials every single time. And lets make the jwt valid only for 3 hours
- [ ] Rate limiting login/register. Default: no (bcrypt makes floods costly); add an in-memory limiter only if the demo is public. - yes lets make a limiter based on if the user is known and lets make a signature check. Explain to me in details how that would work
- [ ] Denormalize parents into a commits table/column. Default: no; the recursive CTE in #29 is enough. - ok
- [ ] Paginate GET /repos or add per_page to the commit list (contract change). Default: no. - lets make it per page paginating makes no sense and indexing as I am not planning on making it a global project
- [ ] Garbage-collect objects after branch/repo delete. Default: no; documented.
- [ ] Add CI (GitHub Actions) for go test / npm test (needs a DB secret). Default: no. - yes lets make it on merge accepted but other conditions so that the QA s role and CI wont just repeat the same work
- [ ] Default COOKIE_SECURE to true / require TLS. Default: no (local-only). - its fine
- [ ] Real merges, tags, abbreviated-oid refs. Default: stay out of scope. - yep
and add OpenApi.yaml so that code knows what frontend expects from the backend and create a makefile that will run all the programs so that the project can start without any issues

## Controller notes on the answers above (2026-10-17)
- JWT: lifetime goes from 7 days to 3 hours (decided, issue below). Keycloak: NOT adopted yet. It is an architectural change (external identity server) and the current httpOnly-cookie JWT already avoids sending credentials on every request. Open question for the user, with a recommendation (keep our own JWT) - see the chat reply.
- Rate limiter: user said yes and asked for a detailed explanation of how "known user" tiers and a "signature check" would work. Design explained in chat; waiting for the user to confirm the design and what "signature check" means before an issue is created.
- Denormalize parents: ok (no). Garbage collection: no answer, default stays (no). COOKIE_SECURE: fine (no). Real merges/tags/abbreviated oids: out of scope.
- Pagination: user text reads as "do not paginate / no per_page, we are not building a global project" (default kept: no). If they meant the opposite, tell the controller.
- CI: yes, on merge to main, scoped so it does not repeat QA's work (CI = mechanical gates on an ephemeral Postgres; QA = acceptance criteria, exploratory and browser checks).
- New asks: openapi.yaml as the API contract, and a Makefile to run everything.

## User answers, 2026-10-17 (second round)
- Keycloak: user agreed to keep our own JWT (3h lifetime). Closed.
- Rate limiter: user's concern - a bot making a few requests must not lock the real user out. Revised design (controller default, user may veto before merge): count failures per (client IP + username) pair, not per username alone, so a bot on another IP cannot lock the victim; a separate looser per-IP cap across usernames; throttle with growing delay and 429 + Retry-After instead of a hard lockout; a successful login sets an HMAC-signed "known device" cookie (signed with JWT_SECRET-derived key) and requests carrying a valid one for that username get the lenient tier, so the real user keeps working during an attack; unknown usernames behave identically (no account enumeration); in-memory, resets on restart.
- Pagination: user confirmed "make it per page": add `page` + `per_page` (default 50, max 100) to GET /repos and `per_page` to the commit list; contract change, updates plan.md and openapi.yaml.

## Optimization round (#47-#54), stashed 2026-10-18
- [ ] (#47) Baseline on Neon is estimated as statements x 200 ms, not measured (engineer has no Neon credentials; secrets stay out of agents). Default: accept the estimate; statement counts are exact. Say if you want a real Neon run.

## Testing/optimization round (#56+), stashed 2026-10-18
- [ ] (#56+) The private-repo feature, the new UI and the `mg` CLI are uncommitted on `feature-private-repos-cli-github-ui`, not on `main`. Issue branches are normally cut from `main`, which would not contain that work. Default: audit and test the working tree as is; engineers cut `issue-<N>-<slug>` branches from the feature branch HEAD only after the pending work is committed (needs your OK to commit it), otherwise issues stay groomed and queued. - 2026-10-18 - we have to merge the branch to the main after it was tested on its own branch
- [ ] (infra) `graphify-out/` is not in `.gitignore`; it was generated into the working tree by /graphify. Default: left untracked, add to `.gitignore` if you do not want to commit it. - 2026-10-18 - yes
- [ ] (#64) A non-owner posting an oversized commit body: answer 413 (before the 403 owner check) or 403? Slight change to the documented order (commits.go:66 / plan.md:109). Default used: 413 for bodies over 1 MiB from non-owners, 403 otherwise; plan.md line to be added when merged. Concurrency semaphore for commits left out of scope (no issue filed). - 2026-10-18 - file the issue
- [ ] (process) Round #56-#70 is implemented straight in the working tree (no issue branches, no commits) because the base feature work is uncommitted; QA checks the working tree. Default: leave everything uncommitted for the user to review. - 2026-10-18 - if the tests and everything else passed then commit and push the changes.
- [ ] (#66) Contract change: a commit-history page where page*per_page exceeds 10000 now answers 400 `invalid page` instead of 200 with an empty list; three tests of the old behavior were replaced (TestCommitLogHugePage and two pagination cases). Default used: 10000 cap, documented in openapi.yaml and plan.md. Revert if you want the old behavior. - 2026-10-18 - it is fine
- [ ] (#67) Immutable oid responses stay `public, max-age=31536000, immutable`, so after a repo flips public -> private, shared caches that stored them earlier may keep serving them for up to a year. Default used: contract unchanged (documented in `_docs/performance.md`); want a shorter max-age or no `public` instead? - 2026-10-18 - no we good we can use shorter age like a week max for now
- [ ] (#69) `govulncheck` on `go 1.26.0` (backend/go.mod) reports 30 Go standard-library vulnerabilities; the fix is raising the `go` line to a patched 1.26.x release (changes the required toolchain, so it is your call). Default used: the new CI govulncheck step is `continue-on-error: true` (advisory) until you bump. - 2026-10-18 - yeah we are good
- [ ] (#73 vs #53) To keep the console free of 404s, the repo page now requests README.md only after the root tree lists it, and skips the commits request on an empty repo. Cost: README render waits one extra round trip (it used to start in parallel with the tree, per #53's no-waterfall design). Default used: no 404s, serial README. Alternative: start README in parallel again and accept one logged 404 for repos without a README. - 2026-10-18 -we good
- [ ] (#71, #74 QA) Browser verification of the owner's Settings tab / settings nav (#71) and the create-branch dialog (#74) needs a QA-owned repo with one commit, in the throwaway local DB. The QA agent's commit through the web editor was denied by the permission classifier and it then (wrongly) tried clicking the disabled Commit button via JS (no effect). Default used: #71 and #74 stay at "unit tests pass, browser check pending"; nobody works around the denial. To finish: allow one commit of a small file in a QA-owned repo (local DB only), or say to skip browser verification for these two. Also: ~10 QA registrations in a row trip the login/register rate limiter (that is the limiter working). - 2026-10-18
- [ ] (#73, #57 QA) The permission classifier denied the Playwright `browser_wait_for` and `browser_snapshot` tools to a QA agent mid-run. Not verified in the browser as a result: #73 (console 4xx on empty / no-README / README repos, README render timing, logged-out 401), and #57 (expired-session draft restore, SPA Back button). Default used: #73 stays at "unit tests pass, /favicon.svg 200 by curl"; nobody works around the denial. To finish: allow the Playwright tools for QA in this project (permission settings are yours to change), or say to skip browser verification. - 2026-10-18 - change the permissions I allow it
- [ ] (#57) The browser's own Back/Forward buttons cannot be intercepted by the editor's unsaved-changes guard: the app uses `BrowserRouter`, where the URL has already changed when `popstate` fires and undoing it is racy. Reload, tab close, in-app links, Cancel/Back/Up and breadcrumbs are guarded. Proper fix: migrate to a data router (`createBrowserRouter`) so `useBlocker` works - a routing change across the app. Default used: not migrated; limitation documented on #57. - 2026-10-18 - yes do the fix and start closing the issues. 

Now that we discussed the questions start closing the issues with the qa commiting and pushing the changes and merging them into main

## UI polish/cleanup (#58, #61), stashed 2026-10-19
- [ ] (#61) `_docs/design-system.md` is marked superseded by `DESIGN.md` + `frontend/src/index.css`; its primary/ring token rows and the card-shadow sentence were updated to the Primer values, other historical values (backgrounds, radius, p-3 density) are left with the banner. Default: simplest reversible edit, no look change. Want the whole file rewritten to match DESIGN.md instead? 
- [ ] (#61) Stray transcript files (`frontend/*local-command-caveat*.txt`) do not exist; `resolving the issues.txt` is already git-ignored and untracked. Default: nothing deleted.
- [ ] (#61) shadcn form controls (`select.tsx`, `textarea.tsx`) and dialogs keep their own `shadow-xs`/`shadow-lg`; the issue names only surfaces/cards. Default: left unchanged.

## #103 openapi checks: YAML parser?
Asked 2026-10-19 (PM). No YAML parser or JSON-schema validator is approved. Default used: extend the existing line scanner in `frontend/src/lib/openapi.test.ts`. Say if a parser dependency is wanted.

## #119 nav overflow at 320px (stashed 2026-10-19)
- [ ] (#119) How should "Session unavailable" look below 360px, and does the username stay visible at 320px? Default used: username stays visible (already truncates); below 360px the label shows "Offline" next to the warning icon (the full phrase stays in the `title` and at 360px and up); no hamburger menu. Reversible: one span pair in `Nav.tsx`. Alternatives: wrap the label, or a banner under the nav.

## Issue #120 focus rings, stashed 2026-10-19
- [ ] (#120) Focus ring fix: nav/page controls use `ring-ring/50` (about 2:1 contrast), so the ring is too faint to see. Default used: change to the full-opacity existing `--ring` token (about 4.9:1 light, 4.4:1 dark), and full `--destructive` for destructive buttons. No new look. Want a different option (for example a wider ring at 50%)?

## SQLite round (#95), stashed 2026-10-19
- [ ] (#95) `_docs/plan.md` said SQLite uses "a single writer connection". The pool is not capped at one: a write on the outer store while a transaction is open would then deadlock instead of waiting. Default used: document the SQLite write lock (one writer at a time) plus the 10 s busy timeout, no pool cap (plan.md Storage updated). Alternative: cap the pool at one connection and forbid outer-store use inside `InTx`.
