# Deploy runbook

Short operational view. Details: `deploy/README.md` and the per-host READMEs.

## Status per target

| Target | Status |
| --- | --- |
| Render | Live at https://micro-git.onrender.com, deployed by the Deploy workflow through `RENDER_DEPLOY_HOOK`. The server sets `X-Build` (`backend/build.go`: `RENDER_GIT_COMMIT`, else `APP_COMMIT`, else `dev`); last reported build `5acd89e...`. `autoDeploy` is `false`, so only the CI/E2E-gated deploy hook deploys. The `smoke` job in `deploy.yml` runs after `deploy-render`, waits until `X-Build` equals the deployed commit (the hook is pinned with `ref=<sha>`), then checks `/`. |
| AWS | Stack `micro-git-prod` in `ap-southeast-2`, deployed by `deploy/aws/deploy.sh --apply --account 228619515667` (one command, no OIDC: an org SCP denies `oidc-provider/*`). URL: stack output `AppUrl` (printed by `deploy.sh`). GitHub deploys through job `deploy-aws-keys` with IAM user `github-deploy-micro-git-prod-user`. The hand-made stack `mg` is separate and never touched. |
| GCP | Not configured (`GCP_WORKLOAD_IDENTITY` / `GCP_SERVICE_ACCOUNT` unset), job skipped. |
| Railway | Not configured (`RAILWAY_TOKEN` unset), job skipped. |
| Vercel | Not configured (`VERCEL_TOKEN`, `VERCEL_ORG_ID`, `VERCEL_PROJECT_ID` unset), job skipped. |

## Check which build is live

    curl -sI https://micro-git.onrender.com/health | grep -i '^x-build'
    curl -s https://micro-git.onrender.com/health
    git rev-parse --short origin/main        # compare with the X-Build prefix

Other views: Render dashboard (latest deploy commit SHA); `gh run list --workflow deploy.yml --json headSha,conclusion,createdAt`. A green `deploy-render` job only means the hook was accepted; the build continues on Render. For AWS use `<AppUrl>/health` (the job polls it for 5 minutes) and the stack output `AppUrl`.

## AWS: deploy, update, tear down (account 228619515667)

    deploy/aws/deploy.sh                                   # dry-run, read-only
    deploy/aws/deploy.sh --apply --account 228619515667    # create or update (same command)
    deploy/aws/deploy.sh --apply --account 228619515667 --ref <sha>   # a specific commit (rollback)
    deploy/aws/destroy.sh [--all]                          # dry-run of the teardown
    deploy/aws/destroy.sh --apply --account 228619515667 [--all]

- Needs `aws login` (your own session), Docker running, `jq`, `openssl`. Region and stack are fixed in the scripts.
- `deploy.sh` stops if the account is not on the Free plan. It runs preflight and bootstrap (`--no-oidc`), keeps the JWT secret in SSM (generated once, never printed; `--rotate-jwt` to replace), deploys the stack through a guarded change set, builds the exact commit for `linux/amd64`, rolls it out through SSM with `APP_COMMIT`, and smoke-checks `/health` (X-Build == sha), `/`, `/api/repos` (200) and `/api/auth/me` (401).
- Automation: a git `post-merge` hook or a cron/launchd job can call the `--apply` command (needs a live `aws login`); GitHub runs the same script in job `deploy-aws-keys` after CI and E2E are green on `main`.
- `destroy.sh` terminates the instance first (it is `RetainExceptOnCreate`, so a plain stack delete would leave it running), deletes the stack (ECR images with it), the CI user, its keys, the variable `AWS_DEPLOY_MODE` (switches the CI job off) and the `aws-prod` environment secrets `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`. `--all` also deletes `/micro-git-prod/jwt-secret`, role `cfn-deploy-micro-git-prod` and `mg-instance-boundary` (if unused). Postgres data is lost; there are no backups.
- Check credits: `aws freetier get-account-plan-state --region us-east-1`.

### Changing the AMI on purpose

The stack policy set by `deploy.sh` denies replacing or deleting `AppInstance`. To move to a new AMI deliberately (the instance and its Postgres data are replaced; back up first), deploy by hand with a temporary override: `aws cloudformation deploy ... --parameter-overrides LatestAmiId=<new> --role-arn <cfn-deploy ARN>` after `aws cloudformation set-stack-policy --stack-name micro-git-prod --stack-policy-body '{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}]}'`; the next `deploy.sh --apply` puts the protective policy back. The `deploy.sh` change-set guard refuses this change, so it cannot be done through `deploy.sh`.

### CI key rotation

    deploy/aws/bootstrap.sh --no-oidc --ci-user --rotate-ci-key --apply \
      --confirm-account 228619515667 --region ap-southeast-2 --stack-name micro-git-prod

Creates a new key, pipes it into the secrets of GitHub environment `aws-prod` (main only; never printed), then deletes the old key, leaving one active key. Needs github.com reachable (no key is created otherwise). It switches the CI job on (`AWS_DEPLOY_MODE=keys`) only when both secrets are in `aws-prod` and no repository-level secret of the same name exists. Re-running it re-applies the `aws-prod` branch policy and may reset protection rules added by hand. Without `--rotate-ci-key` the same command only refreshes the user policy and sets the GitHub variables.

## Rollback

- Render: dashboard > service > Events/Deploys > pick the previous good deploy > Rollback. Or revert the bad commit on `main`; the next green CI and E2E redeploys. If Render's build fails, the previous deploy keeps serving (per Render docs, untested here). `autoDeploy` is `false`, so pushes alone do not deploy.
- AWS: the image tag is `<repo>:<sha7>`; `deploy/aws/deploy.sh --apply --account 228619515667 --ref <older-sha>` (the image is reused if still in ECR, which keeps the last 5). Postgres data is not rolled back and there are no backups. Never roll the AMI or replace `AppInstance` (the change-set guard blocks it; the data would be lost).
- Any target: `git revert <sha>` on `main` and let CI/E2E gate the redeploy.

## Known follow-ups

Tracked in issues #167, #160, #166 and #168 (AWS hardening, backups and other deferred review items; see `deploy/aws/README.md` Known gaps). Not done in this branch.

## AWS caveats

- After a failed FIRST create the stack sits in `ROLLBACK_COMPLETE`/`ROLLBACK_FAILED`: a local `deploy.sh --apply` deletes and recreates it; the CI job stops and asks for that local run.
- `mg-instance-boundary` is a single account-wide policy and its name is fixed. It covers the secret path of the stack name used at bootstrap. A second stack name in the same account needs its `/<stack>/jwt-secret` path added by hand (`aws iam create-policy-version --set-as-default`); `bootstrap.sh` never modifies an existing policy.

## Owner checklist (AWS, your credentials only)

1. `aws login` (or `aws sso login --profile NAME`).
2. `deploy/aws/preflight.sh --region <region> --stack-name <new-stack>` and fix any `[FAIL]`.
3. `deploy/aws/bootstrap.sh --region <region> --stack-name <new-stack>` (dry-run), read the plan.
4. `deploy/aws/bootstrap.sh --apply --region <region> --stack-name <new-stack>`, type the account id.
5. Run the printed `gh variable set AWS_ROLE_ARN ...`, `gh variable set AWS_CFN_ROLE_ARN ...`, `openssl rand -hex 32 | gh secret set AWS_JWT_SECRET`, plus `AWS_STACK_NAME` / `AWS_REGION` if not the defaults.
6. Push to `main` (the Deploy workflow runs after CI and E2E are green), watch `deploy-aws`, then check `<AppUrl>/health`.
7. After a successful run: delete the old hand-made stack (back up Postgres first, see `deploy/aws/README.md`) and tighten `github-deploy` policy.
