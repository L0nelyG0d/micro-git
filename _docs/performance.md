# Performance baseline

Taken on 2026-10-18 before any optimization (#48-#54). Each of those issues should re-run the measurement and show a before/after against this table.

## How it is measured

- Every request logs one line: `[slow: ]METHOD <route pattern> <status> <ms>ms stmts=<n>`, for example `GET /repos/{owner}/{repo}/tree/{ref}/{path...} 200 0.2ms stmts=7`. The pattern is logged, not the raw path; no body, cookie or query value is logged. Requests over 500 ms get the `slow: ` prefix. Code: `backend/timing.go`
- `stmts` counts the statements the pgx pool ran for the request (a pgx tracer on the pool, counting every traced query and batch query; transaction control statements are counted only if pgx reports them as queries)
- To reproduce: `cd backend && BASELINE=1 TEST_DATABASE_URL=<url> go test -run TestBaseline -v .` (the test builds a repo with a 50-commit `main`, a file 3 levels deep, and prints the table; median of 7 runs after a warm-up)

Fixture: `main` has 50 commits; the tree has `README.md`, `a/b/x.txt`, `a/b/c/deep.txt` and the commit files. Merge fast-forwards a branch sitting at the root commit to the 50th commit (merge only fast-forwards in this app).

## Backend

| Request | Statements | Local Postgres, median ms | Neon, estimated ms |
|---|---|---|---|
| GET tree (root) | 4 | 0.1 | 800 |
| GET tree (3 levels deep) | 7 | 0.2 | 1400 |
| GET blob | 8 | 0.3 | 1600 |
| GET commits | 3 | 4.0 | 600 |
| GET commit detail | 6 | 0.4 | 1200 |
| POST commit (1 change) | 12 | 0.7 | 2400 |
| POST commit (10 changes) | 21 | 1.0 | 4200 |
| POST merge (50-commit history) | 56 | 2.6 | 11200 |

Local Postgres: Postgres 16 on localhost:54329, fsync off, in-process `httptest` (no network hop, no TLS). Times are therefore almost pure server work.

Neon column is an estimate, not a measurement: statements x 200 ms (the round-trip per statement noted in `_docs/AGENTS.md`). On Neon the time is dominated by round trips, so statement count is the number to reduce. No Neon credentials were used.

Decision (2026-10-18): the Neon column stays an estimate, labelled as such. The #47 criterion "time on Neon" is relaxed to "statement count (exact) plus estimated Neon time"; a real Neon run is not required.

## Frontend

`npm run build` (`tsc -b && vite build`, vite 8.3.4):

| File | Raw | Gzip |
|---|---|---|
| `dist/assets/index-*.js` | 497.33 kB | 153.37 kB |
| `dist/assets/index-*.css` | 38.10 kB | 7.69 kB |
| `dist/index.html` | 0.39 kB | 0.26 kB |
| Total | 535.82 kB | 161.32 kB |

Vite reports a single JS chunk (no code splitting).
