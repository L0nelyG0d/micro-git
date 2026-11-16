# Responder capability table (attack surface inventory)

The responder is the automated agent that reacts to an alert. Each row is something it can do. Anything not listed is denied. Credentials are named, never stored here.

Status: inventory derived from the compose/config files. The allowlist and webhook auth were exercised in a local live drill (2026-11-12); nothing is verified as deployed to production.

| Capability | Credential needed | Scope | Provenance | Approver | Denied |
|---|---|---|---|---|---|
| Read Prometheus | none or read-only token (TBD) | query API only | metrics from app `/metrics` via collector | none (read-only) | admin/write APIs, rule or config changes |
| Read Loki | none or read-only token (TBD) | query API, incident time window | app logs | none | delete API, push |
| Read Tempo | none or read-only token (TBD) | trace search/fetch | OTLP from app | none | write/ingest |
| Read repo | checkout token, read-only | this repo | git history (commit/tag = deployed version) | none | reading `.env`, `_docs` credential files, secrets paths |
| Worktree write | none (local fs) | a disposable git worktree only | agent-authored patch | reviewed via PR | main checkout, `.env`, force-push |
| Run allowlisted docker compose actions | local docker access | the model selects an `action_id` (`restart_app` or `fault_off`) and cannot supply a command: `policy-check.py` validates the id and params against the allowlist and builds the argv from a fixed template, with `-p`/`-f` from `COMPOSE_PROJECT_NAME`/`COMPOSE_FILES` (rollback removed; neither action takes params) | `incident-response/autonomy-policy.yaml` (per-action types and templates, strict param rules, confidence >= 0.7, one action per incident); a response with a `command` field escalates; runs only with `AUTO_EXECUTE=1` | policy engine auto for allowlist; human for anything else | `down -v`, volume removal, exec into DB, arbitrary `docker run`, editing compose files |
| Create PR | scoped GitHub token (PR write only) | this repo | agent branch | human merges | merge, push to main, workflow edits, secrets access |
| Model API call | model API key | one configured model, prompt carries redacted evidence | provider | key owner | sending raw logs containing secrets/PII; other providers |

## Other components (attack surface inventory)

Derived from `docker-compose.yml`, `observability/compose.yaml`, `observability/collector.yaml`, `observability/alertmanager.yml`, `deploy/render.yaml` (names only; no `.env` read). Not verified as deployed.

| Component | Can read | Can write / call | Credentials (named) | Network exposure | Who approves changes |
|---|---|---|---|---|---|
| Backend (Go `mg-server`, also serves the built frontend via `static.go`) | Postgres (`DATABASE_URL`), request data, its own env | Writes Postgres (users, repos, sessions); sends OTLP to collector; sets session cookies (HttpOnly, SameSite=Lax, Secure from `COOKIE_SECURE`) | `DATABASE_URL`, `JWT_SECRET`, `COOKIE_SECURE` (must be `true` in production; `deploy/render.yaml` sets it) | Public: `${APP_PORT:-8080}:8080` on all interfaces; `/metrics` is served by the same port (confirm it is not publicly reachable in production) | Human via PR review + CI |
| Frontend (SPA bundle, no own runtime) | Browser state; calls backend `/api` same origin | Browser-side only; no server credentials | none (session is an HttpOnly cookie, not readable by JS) | Public via backend port | Human via PR review + CI |
| Postgres (`db`) | All app data | Own volume `pgdata` | `POSTGRES_USER`, `POSTGRES_DB`, password from `.env` | No host port published in compose (internal network only) | Human |
| OTel collector (`otel/opentelemetry-collector-contrib:0.111.0`) | App OTLP, scrape of app `/metrics`, docker json-file logs (filelog; logs may contain PII) | Writes to Prometheus (remote write), Tempo, Loki | none configured | OTLP/HTTP `127.0.0.1:4318` (loopback only; 4317 inside the network) | Human |
| Prometheus | Metrics store, scrape targets | Receives remote write; evaluates `alerts.yaml`; sends alerts to Alertmanager | none | `127.0.0.1:9090` only (loopback); no auth | Human |
| Loki | Log store | Accepts OTLP push from collector | none | `127.0.0.1:3100` only | Human |
| Tempo | Trace store | Accepts OTLP from collector | none | `127.0.0.1:3200` only | Human |
| Grafana | Prometheus/Loki/Tempo datasources | Dashboards, users | admin password `GRAFANA_ADMIN_PASSWORD` (required, no default); sign-up disabled | `127.0.0.1:3000` only | Human |
| Alertmanager | Alerts from Prometheus | Webhook to the local responder at `host.docker.internal:9099/alert` | bearer token file `incident-response/.alert-token` (mounted read-only; listener compares in constant time) | Internal network; webhook target is the host listener | Human; the responder rows above apply to what the webhook may trigger |

Gaps to review: Prometheus and OTLP have no authentication (loopback only); the listener bind address must be limited to the docker bridge/host-reachable interface; token file permissions inside the Alertmanager container. Tracked here, not yet filed as findings.

Escalation: any action outside the allowlist produces an escalation packet for a human instead of executing.

## Agent extension supply chain

Inventory names only (no config or tokens inspected).

- Project skills (`.claude/skills`): `design-taste-frontend`, `image-to-code`, `web-design-guidelines`. Also `skills-lock.json` at repo root pins skill sources.
- Skills listed in the user session (global/plugin): caveman family, context7, graphify, superpowers family, and others; review separately.
- MCP servers in the user session: Neon, playwright, claude-artifacts, claude.ai Claude Docs, easel; `21st` failed to connect. Neon is write-capable (destructive tools exposed), so it must NOT be reachable from the responder.
- Rule: the responder runs with its own minimal extension set, not the developer's session.

### Snyk Agent Scan (not run)
Would be run by a human as `uvx snyk-agent-scan` from the repo root (and against the user-level agent config) to flag risky skills and MCP servers (prompt injection, tool poisoning, toxic flows). Not executed here; it may send metadata to Snyk, so review its data policy first. Results would be added as findings with `source: scanner`.
