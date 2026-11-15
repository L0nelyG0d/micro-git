# Deployment (CD)

Two environments (issue #201):

| Environment | Where | How it changes | `APP_ENV` | Database |
| --- | --- | --- | --- | --- |
| dev | Render (https://micro-git.onrender.com) | automatic: every green `main` commit, [`.github/workflows/deploy.yml`](../.github/workflows/deploy.yml) | `dev` | Render Postgres `mg-db` |
| prod | AWS stack `micro-git-prod`, `ap-southeast-2` | manual: workflow [`Promote to production`](../.github/workflows/promote.yml), with approval | `prod` | Postgres container on the EC2 instance |

Per-target notes: [`render/README.md`](render/README.md) (Blueprint `render.yaml`) and [`aws/README.md`](aws/README.md) (CloudFormation and scripts). Operations (status, live check, promote, rollback): [`_docs/deploy-runbook.md`](../_docs/deploy-runbook.md). Image, compose and environment variables: [`docs/deployment.md`](../docs/deployment.md).

## How CD works

1. The Deploy workflow triggers on `workflow_run` (type `completed`) of the `CI` and `E2E` workflows, for branch `main` only.
2. The `gate` job runs only if the finishing run succeeded and was a `push` to `main`. It looks up the latest completed `ci.yml` and `e2e.yml` runs for the same commit (`github.event.workflow_run.head_sha`) and sets `ready=true` only if both are `success`. The gate fires once for CI and once for E2E, so only the later finisher sees both green and one deploy starts per commit.
3. The gate writes one true/false flag (never a value): `render` is true when secret `RENDER_DEPLOY_HOOK` is non-empty.
4. `deploy-render` needs `gate` with `ready == 'true'` and `render == 'true'`; otherwise it is skipped. `smoke` then checks the new build.
5. Runs are serialized by `concurrency: deploy-main` (`cancel-in-progress: false`).
6. Nothing in `deploy.yml` touches AWS. Production changes only through `promote.yml`.

## Jobs of Deploy (dev)

| Job | Runs when | What it does |
| --- | --- | --- |
| `gate` | a CI or E2E push run on `main` finished green | Checks that both workflows are green for the commit; writes `ready` and `render` |
| `deploy-render` | `ready` and `render` | `curl -fsS -X POST` to the hook URL with `ref=<sha>` appended (`?` or `&`), so Render builds exactly the gated commit and a newer, ungated commit cannot ride along. Only the HTTP status is printed |
| `smoke` | `ready` and `deploy-render` succeeded | Polls `$APP_URL/health` every 15 s for up to `SMOKE_WAIT_SECONDS` (default 480, digits only, capped at 780) until it is 200 with `X-Build` equal to the deployed sha; then `/` must return 200. Creates no users or repos |

## Promote to production

Workflow `Promote to production` (`promote.yml`), `workflow_dispatch` only. Input `sha` (optional, 40 hex): empty means the commit dev serves now. `concurrency: promote-production`, never cancelled in progress.

Run it: Actions > Promote to production > Run workflow, branch `main`; or

    gh workflow run promote.yml --ref main -f sha=<sha>

| Job | Environment | What it does |
| --- | --- | --- |
| `resolve` | none, no secrets | Fails unless started from `main`. Reads `X-Build` from `$APP_URL/health` of dev (retries about 3 minutes, Render free sleeps). Takes the input sha, else dev's `X-Build`; requires 40 lowercase hex. Checks the commit exists and is an ancestor of `origin/main` (full-depth checkout of `main`). Requires the latest completed `ci.yml` and `e2e.yml` push runs for that sha to be `success`. If no sha was typed, dev must serve it; if a sha was typed and dev serves another build, it prints a notice (rollback or skip-ahead) and continues. Outputs `sha`, `typed`, `dev_build`; writes a summary with the sha, its subject line and the dev build |
| `approve` | `production` | Pauses for the required reviewer, shows the sha being approved. No secrets, changes nothing |
| `deploy` | `aws-prod` (key, `main` only) | Checks out the sha (`persist-credentials: false`) and runs `deploy/aws/deploy.sh --apply --ci --ref <sha>` with the IAM-user key only in that step's env, `AWS_DEFAULT_REGION=ap-southeast-2` and `MG_AWS_ACCOUNT` from `AWS_ACCOUNT_ID`. `deploy.sh` fixes region and stack `micro-git-prod`; with `--ci` it skips preflight and bootstrap, deploys the guarded change set through role `cfn-deploy-micro-git-prod`, builds and pushes the image of the commit, rolls it out through SSM with `APP_COMMIT=<sha>` and `APP_ENV=prod`, and checks `/health` (`X-Build` == sha), `/` 200, `/api/repos` 200 and `/api/auth/me` 401 on prod |
| `verify` | none, no secrets | Re-reads `X-Build` on dev and writes the summary "prod = sha (checked by deploy.sh), dev = build". If the repository variable `PROD_URL` is set, it also requires `X-Build` == sha on `$PROD_URL/health` (retries about a minute) |

Two environments because the IAM key already lives in `aws-prod` (written by `bootstrap.sh --ci-user`); `production` carries the human approval, `aws-prod` carries the credential. A workflow started from another branch fails in `resolve`, and `aws-prod` only accepts `main`.

`gh workflow run` and the Run workflow button exist only for a workflow file that is on the default branch, so the first run is possible after this change is merged.

## Secrets and variables (names only)

Settings > Secrets and variables > Actions, and Settings > Environments:

| Scope | Kind | Name | Used by | Notes |
| --- | --- | --- | --- | --- |
| repository | secret | `RENDER_DEPLOY_HOOK` | `deploy.yml` gate, `deploy-render` | Full hook URL of the live Render service |
| repository | variable | `APP_URL` | `smoke`, promote `resolve` and `verify` | Optional, default `https://micro-git.onrender.com` (the dev URL) |
| repository | variable | `SMOKE_WAIT_SECONDS` | `smoke` | Optional, default 480, capped at 780 |
| repository | variable | `AWS_ACCOUNT_ID` | promote `deploy` | Passed as `MG_AWS_ACCOUNT`; `deploy.sh` aborts if the key belongs to another account |
| repository | variable | `PROD_URL` | promote `verify` | Optional. The prod URL (stack output `AppUrl`). When set, `verify` requires `X-Build` == sha there. The owner can set it |
| environment `aws-prod` | secret | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | promote `deploy` | Key of IAM user `github-deploy-micro-git-prod-user`, piped in by `bootstrap.sh --ci-user` (rotate with `--rotate-ci-key`) |
| environment `production` | setting | required reviewer | promote `approve` | Configured by the owner; without a reviewer `approve` passes at once |

No longer read by any workflow: `AWS_DEPLOY_MODE`, `AWS_ROLE_ARN`, `AWS_JWT_SECRET`, `AWS_REGION`, `AWS_STACK_NAME`, `AWS_CFN_ROLE_ARN` (they belonged to the removed `deploy-aws-keys` gate switch and the OIDC job). `bootstrap.sh --ci-user` still sets some of them and `destroy.sh` still deletes `AWS_DEPLOY_MODE`; both are harmless. The prod JWT secret lives in SSM `/micro-git-prod/jwt-secret`, generated once by a local `deploy.sh --apply`. OIDC support remains in `bootstrap.sh`; no workflow uses it.

Never put these values in the repository, an issue or a commit message.

## Which jobs run today

- dev (Render): `deploy-render`, then `smoke`, on every green `main` commit. Live at https://micro-git.onrender.com.
- prod (AWS): only `Promote to production`. Stack `micro-git-prod` in `ap-southeast-2` (URL: stack output `AppUrl`, printed by `deploy.sh`; the current address is in the runbook). Until the first promotion runs, prod stays on the last automatically deployed commit and reports no `env`.

`micro-git-prod` is the only stack: the owner deleted the earlier hand-made stack `mg` (2026-11). `deploy.sh` and `destroy.sh` fix the stack name and refuse `mg`.

## Environment label

`APP_ENV` (`[a-z0-9-]{1,16}`, default `local`; an invalid value falls back to `local` with a startup warning) becomes field `env` in every JSON log line, label `env` on the `build_info` metric and resource attribute `deployment.environment` on spans. Render sets `dev` (`render.yaml`); the AWS rollout starts the container with `APP_ENV=prod` (`remote-deploy.sh`).

## Which commit is live

    curl -sI https://micro-git.onrender.com/health | grep -i '^x-build'
    curl -sI "<AppUrl>/health" | grep -i '^x-build'

`X-Build` comes from `RENDER_GIT_COMMIT` (Render), else `APP_COMMIT` (set by the AWS rollout), else `dev` (`backend/build.go`). On GitHub: `gh run list --workflow deploy.yml --json headSha,conclusion,createdAt`.

## What happens on failure

- CI or E2E red for the commit: the gate sets `ready=false` and no dev deploy job runs. `Promote to production` refuses such a commit in `resolve`.
- Render hook call fails (bad URL, non-2xx): `curl -fsS` exits non-zero, `deploy-render` fails and `smoke` is skipped.
- Render build fails after the hook was accepted, or the new build is not live in time: `smoke` fails because `X-Build` never matches. Per Render's documentation the previous successful deploy keeps serving (not tested here).
- Promotion, any failed check in `resolve` (wrong branch, bad sha, not on `main`, CI or E2E not green, dev serves another build when no sha was typed) or a rejected approval: stops before AWS is touched; prod is unchanged.
- Promotion, rollout: `deploy.sh` stops at the first failing step (a change set that would replace or remove `AppInstance`, an SSM command that is not `Success`, a smoke mismatch) and the `deploy` job fails. Automatic container rollback is not implemented (#182). A stack left in `ROLLBACK_COMPLETE` or `REVIEW_IN_PROGRESS` is not repaired in CI; the job asks for a local `deploy.sh --apply`.

Rollback: promote an older sha (input `sha`). Details: [`_docs/deploy-runbook.md`](../_docs/deploy-runbook.md#rollback).

## Render autoDeploy

`deploy/render.yaml` sets `autoDeploy: false`, so the CI/E2E-gated deploy hook is the only deploy trigger. If the live service is not managed by the Blueprint, switch Auto-Deploy to "Off" in the service settings in the Render dashboard, otherwise every push deploys by itself, possibly before CI and E2E are green.
