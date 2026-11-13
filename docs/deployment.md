# Deployment

One Docker image serves the API and the built SPA. Targets are defined under `deploy/`; `.github/workflows/deploy.yml` automates them. Per-target details: [`deploy/aws/README.md`](../deploy/aws/README.md), [`deploy/gcp/README.md`](../deploy/gcp/README.md), [`deploy/vercel/README.md`](../deploy/vercel/README.md). App configuration reference: [`_docs/README.md`](../_docs/README.md).

## Docker image

`Dockerfile` (3 stages): Node 22 builds `frontend/dist`; Go 1.26 builds `mg-server` (`CGO_ENABLED=0`); an `alpine:3.20` runtime holds the binary and the SPA in `/app/public`, runs as non-root user `app`, and defaults to `PORT=8080 STATIC_DIR=/app/public DB_DRIVER=postgres STORE=db`. `HEALTHCHECK` calls `GET /health`.

```
docker build -t mg:local .
```

## Docker Compose with Postgres

`docker-compose.yml` runs `app` (built from the Dockerfile, published on `${APP_PORT:-8080}`) and `db` (`postgres:16-alpine`, named volume `pgdata`). `app` waits for the `db` healthcheck.

Create a git-ignored `.env` with the required names below (compose refuses to start if a required one is missing), then:

```
docker compose up -d --wait
docker compose down        # keep data
docker compose down -v     # also delete the pgdata volume
```

For plain HTTP on localhost leave `COOKIE_SECURE` empty or `false`.

## Environment variables (names only)

| Name | Where | Notes |
| --- | --- | --- |
| `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` | compose, required | Used by `db` and to build the app's `DATABASE_URL` |
| `JWT_SECRET` | all targets, required | 32+ bytes; the backend refuses to start with less |
| `DATABASE_URL` | app, when `STORE=db` and `DB_DRIVER=postgres` | Built by compose; `fromDatabase` on Render |
| `STORE`, `DB_DRIVER` | app | `db`/`memory`; `postgres`/`sqlite` |
| `COOKIE_SECURE` | app | `true` behind HTTPS (Render sets it); unset logs a warning |
| `PORT`, `STATIC_DIR` | app | Image defaults 8080 and `/app/public` |
| `APP_PORT` | compose | Host port, default 8080 |
| `REGISTER_MAX_PER_IP` | compose, test stacks only | Raises the per-IP register limit |
| `SQLITE_PATH`, `DB_MAX_CONNS`, `DB_STATEMENT_TIMEOUT` | app, optional | See `_docs/README.md` |

## Schema and migrations

The Postgres store does not create tables. There is no migration tool, only `backend/db/schema.sql`, which is idempotent (`CREATE ... IF NOT EXISTS`).

- **Compose:** the file is mounted at `/docker-entrypoint-initdb.d/001-schema.sql`. Postgres runs it only on the first start of an empty `pgdata` volume. Later edits to `schema.sql` are not applied to an existing volume; either apply the file by hand (`psql ... -f backend/db/schema.sql`) or recreate the volume with `docker compose down -v` (destroys data).
- **Render:** the Blueprint provisions an empty managed database and nothing mounts or runs `schema.sql`, so a fresh Render database has no tables until someone applies it manually (for example `psql "<external connection string>" -f backend/db/schema.sql`, or `make schema` with `DATABASE_URL` in `.env`). The same manual step is needed after any schema change. Automating this on Render is NOT IMPLEMENTED (no pre-deploy command in `deploy/render.yaml`, no migration step in `deploy.yml`).
- **AWS:** schema is applied by hand once (see its README).

## Targets

| Target | Definition | What it deploys |
| --- | --- | --- |
| Render | `deploy/render.yaml` (Blueprint) | Docker web service `mg` + managed Postgres `mg-db`; `JWT_SECRET` generated; `autoDeploy: true`; free plan (sleeps when idle, free Postgres expires after 30 days) |
| Railway | `deploy/railway.json` | Dockerfile build, restart on failure (max 5). No database is defined; attach one and set the variables |
| AWS | `deploy/aws/main.yaml` (CloudFormation) | EC2 with Docker (Postgres, app, Caddy), ECR. README notes the job does not pass `JwtSecret` and no image is pushed, so the stack create fails or the app stays down until fixed |
| GCP | `deploy/gcp/*.tf` (Terraform) | e2-micro VM with compose, Artifact Registry, optional Cloud Run (memory store, demo only). Needs a remote state backend configured first |
| Vercel | `deploy/vercel/README.md` | Frontend only. Needs `frontend/vercel.json` with an `/api` rewrite, which does not exist yet |

## Deploy workflow gate (`deploy.yml`)

Trigger: `workflow_run` of `CI` or `E2E` completing on `main`. Job `gate` runs only if that run concluded `success`, was a `push`, and on `main`. It then queries `gh run list` for the same commit SHA and sets `ready=true` only if both `ci.yml` and `e2e.yml` have a completed `success` run. Whichever of the two finishes last sees both green, so one deploy starts per commit. `concurrency: deploy-main` does not cancel running deploys.

The gate also writes true/false flags (never values) for which targets are configured; a target job runs when `ready` and its flag are both true:

| Target job | Enabled by | Type |
| --- | --- | --- |
| `deploy-render` (POST to deploy hook) | `RENDER_DEPLOY_HOOK` | secret |
| `deploy-railway` (`railway up --ci`) | `RAILWAY_TOKEN` | secret |
| `deploy-aws` (CloudFormation, OIDC) | `AWS_ROLE_ARN` (optional `AWS_REGION`, `AWS_STACK_NAME`) | variable |
| `deploy-gcp` (Terraform, Workload Identity) | `GCP_WORKLOAD_IDENTITY` (also uses `GCP_SERVICE_ACCOUNT`) | variable |
| `deploy-vercel-frontend` | `VERCEL_TOKEN` (also uses vars `VERCEL_ORG_ID`, `VERCEL_PROJECT_ID`) | secret |

With none configured the workflow is a no-op.

## Health check

`GET /health` returns 200 `OK`. Used by the Docker `HEALTHCHECK`, Render (`healthCheckPath`) and Railway (`healthcheckPath`). The compose `db` service uses `pg_isready`.

## Current status

See [release-process.md](release-process.md#current-status). Summary: only `RENDER_DEPLOY_HOOK` is configured, so Render is the only automated target, and no public URL is documented.
