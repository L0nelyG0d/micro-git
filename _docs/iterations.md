# Iterations

## Connect: real frontend on real backend (SQLite) (#113)

The real frontend (`VITE_USE_MOCK` unset, `HttpApiService`) talks to the real backend on SQLite through the Vite `/api` proxy (the proxy strips `/api`; the browser sees one origin, so the `session` cookie needs no CORS).

Run it (two ways, same result):

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
