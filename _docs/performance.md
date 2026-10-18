# Performance baseline

Taken on 2026-10-18 before any optimization (#48-#54). Each of those issues should re-run the measurement and show a before/after against this table.

## How it is measured

- Every request logs one line: `[slow: ]METHOD <route pattern> <status> <ms>ms stmts=<n>`, for example `GET /repos/{owner}/{repo}/tree/{ref}/{path...} 200 0.2ms stmts=7`. The pattern is logged, not the raw path; no body, cookie or query value is logged. Requests over 500 ms get the `slow: ` prefix. Code: `backend/timing.go`
- `stmts` counts the statements the pgx pool ran for the request (a pgx tracer on the pool, counting every traced query and batch query; transaction control statements are counted only if pgx reports them as queries)
- To reproduce: `cd backend && BASELINE=1 TEST_DATABASE_URL=<url> go test -run TestBaseline -v .` (the test builds a repo with a 50-commit `main`, a file 3 levels deep, and prints the table; median of 7 runs after a warm-up)

Fixture: `main` has 50 commits; the tree has `README.md`, `a/b/x.txt`, `a/b/c/deep.txt` and the commit files. Merge fast-forwards a branch sitting at the root commit to the 50th commit (merge only fast-forwards in this app).

## Backend

| Request | Statements | Statements after #48 | Local Postgres, median ms | Neon, estimated ms |
|---|---|---|---|---|
| GET tree (root) | 4 | | 0.1 | 800 |
| GET tree (3 levels deep) | 7 | | 0.2 | 1400 |
| GET blob | 8 | | 0.3 | 1600 |
| GET commits | 3 | | 4.0 | 600 |
| GET commit detail | 6 | | 0.4 | 1200 |
| POST commit (1 change) | 12 | 7 | 0.7 | 2400 (after: 1400) |
| POST commit (10 changes) | 21 | 7 | 1.0 | 4200 (after: 1400) |
| POST commit (10 changes, 3 folders) | 27 | 8 | | 5400 (after: 1600) |
| POST merge (50-commit history) | 56 | | 2.6 | 11200 |

#48: POST commit reads the tip and its commit in one statement, reads existing trees one statement per directory depth, and writes all new blobs, trees, the commit and the ref move in one statement inside the transaction. The "3 folders" row (10 new files spread over `a/`, `b/`, `c/`, which already exist) was added in #48; its "before" was measured on the pre-#48 code. A test (`TestCommitStatementBudget`) fails if that case exceeds 8 statements.

Local Postgres: Postgres 16 on localhost:54329, fsync off, in-process `httptest` (no network hop, no TLS). Times are therefore almost pure server work.

Neon column is an estimate, not a measurement: statements x 200 ms (the round-trip per statement noted in `_docs/AGENTS.md`). On Neon the time is dominated by round trips, so statement count is the number to reduce. No Neon credentials were used.

Decision (2026-10-18): the Neon column stays an estimate, labelled as such. The #47 criterion "time on Neon" is relaxed to "statement count (exact) plus estimated Neon time"; a real Neon run is not required.

## After #50 (merge ancestry in one recursive query)

| Request | Statements before | Statements after | Local ms before | Local ms after | Neon est. after |
|---|---|---|---|---|---|
| POST merge (50-commit history) | 56 | 6 | 2.6 | 11.4 | 1200 |

Both ancestry questions (already up to date, fast-forward possible) are answered by one `WITH RECURSIVE` statement (`RepoStore.CommitReachability`); the `count(*)` over commits is gone. The count is constant: a test merges a branch 200 commits ahead and asserts at most 10 statements. Local time rose because the parent lines are parsed in SQL with a regex, but on Neon the round trips dominate (56 -> 6). No index or generated column added: every step is a primary-key lookup on `objects (repo_id, oid)`, so there is no schema change and nothing to apply to Neon.

## Frontend

`npm run build` (`tsc -b && vite build`, vite 8.3.4):

| File | Raw | Gzip |
|---|---|---|
| `dist/assets/index-*.js` | 497.33 kB | 153.37 kB |
| `dist/assets/index-*.css` | 38.10 kB | 7.69 kB |
| `dist/index.html` | 0.39 kB | 0.26 kB |
| Total | 535.82 kB | 161.32 kB |

Vite reports a single JS chunk (no code splitting).
