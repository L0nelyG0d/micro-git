# micro git

A small git-hosting site. Users register, create repositories, commit file changes from the browser, and browse branches, history, files and diffs. The backend (Go) stores blob, tree and commit objects in git's own format on PostgreSQL, a SQLite file or in memory; the frontend (React + Vite) looks like a GitHub repository page. The git wire protocol (clone, push, fetch) is not supported; use the web UI or the `mg` command-line client.

Live: https://micro-git.onrender.com (Render). The AWS address and the status of both targets are in [`_docs/deploy-runbook.md`](_docs/deploy-runbook.md).

## Quick start

    make setup         # go mod download + npm ci
    make dev-sqlite    # backend on SQLite (data/mg.db) plus the frontend, no Postgres needed

Other ways to run it (memory store, Postgres, Docker Compose, mock frontend) and the full setup: [`_docs/README.md`](_docs/README.md#quick-start). `make help` lists every target.

## Repository layout

| Path | What it holds | Owner |
| --- | --- | --- |
| `backend/` | Go API server (standard library router), stores for Postgres, SQLite and memory, `gitobj/`, `linediff/`, `treebuild/`, `cmd/` (`mg` CLI, SQLite-to-Postgres migration), `db/schema.sql` | Software engineer |
| `frontend/` | React + Vite single-page app (`src/pages`, `src/components`, `src/services`, `src/lib`, `src/auth`) | Software engineer |
| `e2e/` | Playwright end-to-end suite | QA engineer |
| `scripts/` | Test matrix across the three stores, SQLite smoke test, Postgres URL helper | QA engineer |
| `.github/workflows/` | `ci.yml` (gates), `e2e.yml` (Playwright), `deploy.yml` (Render and AWS after CI and E2E are green) | DevOps |
| `Dockerfile`, `docker-compose.yml`, `.dockerignore`, `Makefile` | One image for API and SPA, local stack with Postgres, developer commands | DevOps |
| `deploy/` | `render.yaml` (Render Blueprint), `render/`, `aws/` (CloudFormation template, `deploy.sh`, `destroy.sh`, `bootstrap.sh`, policies) | DevOps |
| `observability/` | Local compose overlay: OpenTelemetry collector, Prometheus, Loki, Tempo, Grafana, Alertmanager | DevOps |
| `incident-response/` | Alert listener, evidence collection, autonomy policy, responder, runbooks | DevOps |
| `security-audit/` | Semgrep run, findings schema and validation, capability table, tracked results in `runs/` | DevOps |
| `docs/` | Reader-facing guides and reports | DevOps, QA engineer (`testing.md`) |
| `_docs/` | Process and project docs: how the team works, what is built, contracts, runbooks, decisions | Architect (layout); each file has its own owner, see the map |
| `.env.example`, `frontend/.env.example` | Names of the environment variables, no values | Software engineer |
| `.gitignore`, `skills-lock.json` | Ignore rules (local data, session exports, archives); pinned UI skills | Architect |

The full map, the layering rules and the ownership table: [`_docs/team/architect.md`](_docs/team/architect.md).

## Where to read

- Developer manual (setup, tests, architecture, CLI, migration): [`_docs/README.md`](_docs/README.md)
- What the app does (features, endpoints, tables, rules): [`_docs/plan.md`](_docs/plan.md); API contract: [`_docs/openapi.yaml`](_docs/openapi.yaml); visual design: [`_docs/DESIGN.md`](_docs/DESIGN.md)
- How the team works: [`_docs/process.md`](_docs/process.md), [`_docs/AGENTS.md`](_docs/AGENTS.md), role briefs in [`_docs/team/`](_docs/team/), backlog in [`_docs/tasks.md`](_docs/tasks.md), open questions in [`_docs/questions.md`](_docs/questions.md)
- Architecture decisions: [`_docs/decisions/`](_docs/decisions/README.md)
- Testing: [`docs/testing.md`](docs/testing.md), [`_docs/testing-guidelines.md`](_docs/testing-guidelines.md), [`_docs/e2e.md`](_docs/e2e.md)
- Deployment: [`docs/deployment.md`](docs/deployment.md), [`deploy/README.md`](deploy/README.md), [`docs/release-process.md`](docs/release-process.md), runbooks [`_docs/deploy-runbook.md`](_docs/deploy-runbook.md) and [`_docs/deploy-render.md`](_docs/deploy-render.md)
- Operations: [`observability/README.md`](observability/README.md), [`incident-response/README.md`](incident-response/README.md), [`security-audit/README.md`](security-audit/README.md), [`docs/operations-and-security-report.md`](docs/operations-and-security-report.md)
