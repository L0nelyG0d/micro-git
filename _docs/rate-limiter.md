# Login / register rate limiter (issue #44)

Status: implemented (`backend/ratelimit.go`), merged for issue #44.

## What it is
An in-memory limiter on `POST /auth/login` and `POST /auth/register`. It slows down password guessing and account spam. It must never let a bot lock the real user out, which is the user's main requirement.

## How it works
1. **Failures are counted per (client IP + username) pair**, not per username. A bot on another IP cannot burn the victim's allowance.
2. **Free failures first.** The first 5 failed logins for a pair are answered normally.
3. **Then growing delay, not a lockout.** After that the server answers `429` with `Retry-After`. The wait doubles from 1 s up to 15 min. The window is 15 min and resets after a success.
4. **Per-IP cap across usernames** (30 failures) stops one IP from spraying many usernames.
5. **Known-device cookie.** A successful login sets an HMAC-signed cookie (key derived from `JWT_SECRET`) that names the username. A request carrying a valid cookie for that username gets the lenient tier (20 free failures). So the real user keeps working while a bot attacks from elsewhere. The signature check stops forged cookies.
6. **No account enumeration.** Unknown usernames are counted and answered exactly like known ones.
7. **Register** is limited to 10 per IP per window.

## Details
- The check and the count are atomic (reserve at check time, undo on success), and cookie / no-cookie requests have separate pair counters, so bursts cannot bypass the limit or eat the real user's cookie budget.
- Client identity is `RemoteAddr` only. `X-Forwarded-For` is ignored because it is spoofable and the app is local-only.
- State lives in memory and resets on restart. Memory is bounded: at most 10,000 entries, oldest evicted.
- The clock is injectable so tests need no sleeping.
- All numbers above are defaults, kept as constants so they are easy to change.
- Documented in `openapi.yaml` (429 response, `Retry-After` header) and `plan.md`.

## Where the related instructions live
- Workflow rules for agents (branch per issue, controller, QA, waiting for the user): `_docs/AGENTS.md`, `_docs/process.md`, `_docs/team/*.md`.
- Questions waiting for the user and their answers: `_docs/questions.md`.
- Backlog and order of work: `_docs/tasks.md`.
