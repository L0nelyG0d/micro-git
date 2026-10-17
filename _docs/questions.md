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
