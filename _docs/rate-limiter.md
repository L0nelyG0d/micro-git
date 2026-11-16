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
7. **Register** is limited to 10 per IP per window; `REGISTER_MAX_PER_IP` overrides the number (the compose stack and the E2E workflow raise it for the test suite).

## Details
- The check and the count are atomic (reserve at check time, undo on success), and cookie / no-cookie requests have separate pair counters, so bursts cannot bypass the limit or eat the real user's cookie budget.
- Client identity: see "Client address behind a proxy" below.
- State lives in memory and resets on restart. Memory is bounded: at most 10,000 entries, oldest evicted.
- The clock is injectable so tests need no sleeping.
- All numbers above are defaults, kept as constants so they are easy to change.
- Documented in `_docs/openapi.yaml` (429 response, `Retry-After` header) and `plan.md`.

## Client address behind a proxy (issue #194)
Setting `TRUSTED_PROXY_HOPS` (integer 0-5, default 0, read once in `LoadConfig`; an invalid value falls back to 0 with a startup warning). The function is `clientAddr` in `backend/ratelimit.go`; the register cap, the per-pair login counters and the per-IP login counter all use it (`Server.limiterIP`). Nothing else trusts a forwarded header, and the access log records no client address.
- **0 (default):** the host part of `RemoteAddr`; `X-Forwarded-For` is ignored. Right when the app is reachable directly.
- **N > 0:** all `X-Forwarded-For` lines are joined in order and split on commas; the client is the Nth entry counted from the **right**. Each trusted proxy appends the address it received the request from, so the last entry is what the nearest proxy saw and is not controlled by the client. Entries to the left may be written by the client (`X-Forwarded-For: 1.2.3.4` sent by an attacker arrives as `1.2.3.4, <real address>`), so they are never used. Counting from the left would let an attacker pick a fresh bucket per request.
- **Fallback (fail safe):** fewer entries than N, no header, an entry that is not an IP (a port and IPv6 brackets are stripped; zones and empty values are rejected), a header over 4 KB or with more than 32 entries: the request uses the host of `RemoteAddr`, i.e. the proxy bucket, never a client-chosen value.
- **Normalisation:** IPv4-mapped IPv6 (`::ffff:a.b.c.d`) becomes IPv4. IPv6 is kept whole: there is no grouping per /64, so one IPv6 client can use many addresses to get many buckets.
- **Values per environment:** AWS 1 (Caddy is the only proxy: the app container is started without a published port and the security group opens only 80, 443 and SSH; `remote-deploy.sh` sets it, effective after the next promotion). Render stays 0 until someone measures how many proxies sit in front of a service. Local and compose 0 (`docker-compose.yml` passes `TRUSTED_PROXY_HOPS`, default 0).
- **Measuring (acceptance criterion 1, not done yet):** the owner can check without logging client IPs. Deploy, then from two different networks (for example a laptop and a phone on mobile data) call `POST /api/auth/register` with an invalid body more than `registerMaxPerIP` times from the first network. If the second network is also answered `429`, both share one bucket: the app sees the proxy (hops too low or header missing). If the second network still gets `400`, the limiter separates clients. On Render run the same check with `TRUSTED_PROXY_HOPS` unset first: a shared `429` shows the proxy address is what `RemoteAddr` holds, and a value of 1 can then be tested the same way (spoofing check: send an extra `X-Forwarded-For: 1.2.3.4` from the first network, it must stay `429`).

## Where the related instructions live
- Workflow rules for agents (branch per issue, controller, QA, waiting for the user): `_docs/AGENTS.md`, `_docs/process.md`, `_docs/team/*.md`.
- Questions waiting for the user and their answers: `_docs/questions.md`.
- Backlog and order of work: `_docs/tasks.md`.
