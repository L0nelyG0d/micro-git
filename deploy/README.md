# Deployment (CD)

Two targets: Render and AWS. Continuous delivery is one workflow, [`.github/workflows/deploy.yml`](../.github/workflows/deploy.yml). Per-target notes: [`render/README.md`](render/README.md) (Blueprint `render.yaml`) and [`aws/README.md`](aws/README.md) (CloudFormation and scripts). Operations (status, live check, rollback): [`_docs/deploy-runbook.md`](../_docs/deploy-runbook.md). Image, compose and environment variables: [`docs/deployment.md`](../docs/deployment.md).

## How CD works

1. The Deploy workflow triggers on `workflow_run` (type `completed`) of the `CI` and `E2E` workflows, for branch `main` only.
2. The `gate` job runs only if the finishing run succeeded and was a `push` to `main`. It looks up the latest completed `ci.yml` and `e2e.yml` runs for the same commit (`github.event.workflow_run.head_sha`) and sets `ready=true` only if both are `success`. The gate fires once for CI and once for E2E, so only the later finisher sees both green and one deploy starts per commit.
3. The gate writes true/false flags (never values):
   - `render`: secret `RENDER_DEPLOY_HOOK` is non-empty.
   - `aws`: variable `AWS_ROLE_ARN` is non-empty (OIDC path).
   - `aws_keys`: `AWS_ROLE_ARN` is empty and variable `AWS_DEPLOY_MODE` is `keys` (IAM-user key path).
4. Each deploy job needs `gate` with `ready == 'true'` and its own flag `true`; otherwise it is skipped.
5. Runs are serialized by `concurrency: deploy-main` (`cancel-in-progress: false`).

## Jobs

| Job | Runs when | What it does |
| --- | --- | --- |
| `gate` | a CI or E2E push run on `main` finished green | Checks that both workflows are green for the commit; writes `ready`, `render`, `aws`, `aws_keys` |
| `deploy-render` | `ready` and `render` | `curl -fsS -X POST` to the hook URL with `ref=<sha>` appended (`?` or `&`), so Render builds exactly the gated commit and a newer, ungated commit cannot ride along. Only the HTTP status is printed |
| `smoke` | `ready` and `deploy-render` succeeded | Polls `$APP_URL/health` every 15 s for up to `SMOKE_WAIT_SECONDS` (default 480, digits only, capped at 780) until it is 200 with `X-Build` equal to the deployed sha; then `/` must return 200. Creates no users or repos |
| `deploy-aws-keys` | `ready` and `aws_keys` | The production AWS path. Environment `aws-prod` (deployments from `main` only). Checks out the sha (`persist-credentials: false`) and runs `deploy/aws/deploy.sh --apply --ci --ref <sha>` with the IAM-user key only in that step's env, `AWS_DEFAULT_REGION=ap-southeast-2` and `MG_AWS_ACCOUNT` from `AWS_ACCOUNT_ID`. `deploy.sh` fixes region `ap-southeast-2` and stack `micro-git-prod`; with `--ci` it skips preflight and bootstrap, deploys the guarded change set through role `cfn-deploy-micro-git-prod`, builds and pushes the image of the commit, rolls it out through SSM with `APP_COMMIT=<sha>`, and checks `/health` (`X-Build` == sha), `/` 200, `/api/repos` 200 and `/api/auth/me` 401 |
| `deploy-aws` | `ready` and `aws` | OPTIONAL OIDC variant, not used: an organization SCP in the current AWS account denies creating the GitHub OIDC provider, so `AWS_ROLE_ARN` is unset. If enabled, it assumes `AWS_ROLE_ARN`, writes `AWS_JWT_SECRET` to SSM, deploys the same guarded change set, pushes the image, runs `remote-deploy.sh` through SSM and polls `<AppUrl>/health` for 5 minutes (no `X-Build` check). Details: [`aws/README.md`](aws/README.md) |

## Secrets and variables (names only)

Current configuration (Settings > Secrets and variables > Actions, and Settings > Environments > `aws-prod`):

| Scope | Kind | Name | Used by | Notes |
| --- | --- | --- | --- | --- |
| repository | secret | `RENDER_DEPLOY_HOOK` | gate, `deploy-render` | Full hook URL of the live Render service |
| repository | variable | `AWS_DEPLOY_MODE` | gate | `keys` switches `deploy-aws-keys` on. Set last by `deploy/aws/bootstrap.sh --ci-user`, deleted by `destroy.sh` |
| repository | variable | `AWS_ACCOUNT_ID` | `deploy-aws-keys` | Passed as `MG_AWS_ACCOUNT`; `deploy.sh` aborts if the key belongs to another account |
| repository | variable | `AWS_REGION`, `AWS_STACK_NAME`, `AWS_CFN_ROLE_ARN` | `deploy-aws` only | Set by `bootstrap.sh --ci-user` (`ap-southeast-2`, `micro-git-prod`, role `cfn-deploy-micro-git-prod`). `deploy-aws-keys` does not read them: `deploy.sh` fixes region, stack and role itself. Without them `deploy-aws` would default to region `us-east-1` and stack `mg` |
| environment `aws-prod` | secret | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | `deploy-aws-keys` | Key of IAM user `github-deploy-micro-git-prod-user`, piped in by `bootstrap.sh --ci-user` (rotate with `--rotate-ci-key`) |
| repository | variable | `APP_URL` | `smoke` | Optional, default `https://micro-git.onrender.com` |
| repository | variable | `SMOKE_WAIT_SECONDS` | `smoke` | Optional, default 480, capped at 780 |
| repository | variable | `AWS_ROLE_ARN` | gate, `deploy-aws` | Optional (OIDC only), not set. Setting it switches `deploy-aws` on and `deploy-aws-keys` off |
| repository | secret | `AWS_JWT_SECRET` | `deploy-aws` | Optional (OIDC only), 32+ characters. The keys path keeps its JWT secret in SSM `/micro-git-prod/jwt-secret`, generated once by a local `deploy.sh --apply` |

Never put these values in the repository, an issue or a commit message.

## Which jobs run today

- Render: `deploy-render`, then `smoke`, on every green `main` commit. Live at https://micro-git.onrender.com.
- AWS: `deploy-aws-keys` on every green `main` commit, stack `micro-git-prod` in `ap-southeast-2` (URL: stack output `AppUrl`, printed by `deploy.sh`; the current address is in the runbook). First verified in Deploy run 43480095153.
- `deploy-aws` (OIDC): skipped, `AWS_ROLE_ARN` is not set.

The hand-made stack `mg` is separate; `deploy.sh` refuses to touch it.

## Which commit is live

    curl -sI https://micro-git.onrender.com/health | grep -i '^x-build'
    curl -sI "<AppUrl>/health" | grep -i '^x-build'

`X-Build` comes from `RENDER_GIT_COMMIT` (Render), else `APP_COMMIT` (set by the AWS rollout), else `dev` (`backend/build.go`). On GitHub: `gh run list --workflow deploy.yml --json headSha,conclusion,createdAt`.

## What happens on failure

- CI or E2E red for the commit: the gate sets `ready=false` and no deploy job runs.
- Render hook call fails (bad URL, non-2xx): `curl -fsS` exits non-zero, `deploy-render` fails and `smoke` is skipped.
- Render build fails after the hook was accepted, or the new build is not live in time: `smoke` fails because `X-Build` never matches. Per Render's documentation the previous successful deploy keeps serving (not tested here).
- AWS: `deploy.sh` stops at the first failing step (a change set that would replace or remove `AppInstance`, an SSM command that is not `Success`, a smoke mismatch) and the job fails. A stack left in `ROLLBACK_COMPLETE` or `REVIEW_IN_PROGRESS` is not repaired in CI; the job asks for a local `deploy.sh --apply`.

Rollback for both targets: [`_docs/deploy-runbook.md`](../_docs/deploy-runbook.md#rollback).

## Render autoDeploy

`deploy/render.yaml` sets `autoDeploy: false`, so the CI/E2E-gated deploy hook is the only deploy trigger. If the live service is not managed by the Blueprint, switch Auto-Deploy to "Off" in the service settings in the Render dashboard, otherwise every push deploys by itself, possibly before CI and E2E are green.
