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
- [ ] Logout does not revoke the JWT (valid 7 days). Revoking needs a token_version column or sessions table. Default: no; documented as a known limit (#36).
- [ ] Rate limiting login/register. Default: no (bcrypt makes floods costly); add an in-memory limiter only if the demo is public.
- [ ] Denormalize parents into a commits table/column. Default: no; the recursive CTE in #29 is enough.
- [ ] Paginate GET /repos or add per_page to the commit list (contract change). Default: no.
- [ ] Garbage-collect objects after branch/repo delete. Default: no; documented.
- [ ] Add CI (GitHub Actions) for go test / npm test (needs a DB secret). Default: no.
- [ ] Default COOKIE_SECURE to true / require TLS. Default: no (local-only).
- [ ] Real merges, tags, abbreviated-oid refs. Default: stay out of scope.
