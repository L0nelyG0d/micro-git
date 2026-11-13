# Deploy runbook

Short operational view. Details: `deploy/README.md` and the per-host READMEs.

## Status per target

| Target | Status |
| --- | --- |
| Render | Live at https://micro-git.onrender.com, deployed by the Deploy workflow through `RENDER_DEPLOY_HOOK`. The server sets `X-Build` (`backend/build.go`: `RENDER_GIT_COMMIT`, else `APP_COMMIT`, else `dev`); last reported build `5acd89e...`. `autoDeploy` is `false`, so only the CI/E2E-gated deploy hook deploys. The `smoke` job in `deploy.yml` runs after `deploy-render`, waits until `X-Build` equals the deployed commit (the hook is pinned with `ref=<sha>`), then checks `/`. |
| AWS | Pending bootstrap. The `deploy-aws` job, `deploy/aws/main.yaml`, `bootstrap.sh` and `preflight.sh` exist but have never run against a real account; the job is skipped until `AWS_ROLE_ARN` is set. The hand-made stack `mg` (ap-southeast-2) is separate and must not be reused: deploy under a NEW stack name. |
| GCP | Not configured (`GCP_WORKLOAD_IDENTITY` / `GCP_SERVICE_ACCOUNT` unset), job skipped. |
| Railway | Not configured (`RAILWAY_TOKEN` unset), job skipped. |
| Vercel | Not configured (`VERCEL_TOKEN`, `VERCEL_ORG_ID`, `VERCEL_PROJECT_ID` unset), job skipped. |

## Check which build is live

    curl -sI https://micro-git.onrender.com/health | grep -i '^x-build'
    curl -s https://micro-git.onrender.com/health
    git rev-parse --short origin/main        # compare with the X-Build prefix

Other views: Render dashboard (latest deploy commit SHA); `gh run list --workflow deploy.yml --json headSha,conclusion,createdAt`. A green `deploy-render` job only means the hook was accepted; the build continues on Render. For AWS use `<AppUrl>/health` (the job polls it for 5 minutes) and the stack output `AppUrl`.

## Rollback

- Render: dashboard > service > Events/Deploys > pick the previous good deploy > Rollback. Or revert the bad commit on `main`; the next green CI and E2E redeploys. If Render's build fails, the previous deploy keeps serving (per Render docs, untested here). `autoDeploy` is `false`, so pushes alone do not deploy.
- AWS (once live): the image tag is `<repo>:<sha7>`; re-run `deploy-aws` on an older green commit, or run `deploy/aws/remote-deploy.sh` through SSM with an older tag. Postgres data is not rolled back and there are no backups. Never roll the AMI or replace `AppInstance` (the change-set guard blocks it; the data would be lost).
- Any target: `git revert <sha>` on `main` and let CI/E2E gate the redeploy.

## Known follow-ups

Tracked in issues #167, #160, #166 and #168 (AWS hardening, backups and other deferred review items; see `deploy/aws/README.md` Known gaps). Not done in this branch.

## AWS caveats

- After a failed FIRST create the stack sits in `ROLLBACK_COMPLETE`/`ROLLBACK_FAILED`: an admin must `aws cloudformation delete-stack` and wait for `DELETE_COMPLETE` before re-running; re-running alone does not help.
- `mg-instance-boundary` is a single account-wide policy and its name is fixed. It covers the secret path of the stack name used at bootstrap. A second stack name in the same account needs its `/<stack>/jwt-secret` path added by hand (`aws iam create-policy-version --set-as-default`); `bootstrap.sh` never modifies an existing policy.

## Owner checklist (AWS, your credentials only)

1. `aws login` (or `aws sso login --profile NAME`).
2. `deploy/aws/preflight.sh --region <region> --stack-name <new-stack>` and fix any `[FAIL]`.
3. `deploy/aws/bootstrap.sh --region <region> --stack-name <new-stack>` (dry-run), read the plan.
4. `deploy/aws/bootstrap.sh --apply --region <region> --stack-name <new-stack>`, type the account id.
5. Run the printed `gh variable set AWS_ROLE_ARN ...`, `gh variable set AWS_CFN_ROLE_ARN ...`, `openssl rand -hex 32 | gh secret set AWS_JWT_SECRET`, plus `AWS_STACK_NAME` / `AWS_REGION` if not the defaults.
6. Push to `main` (the Deploy workflow runs after CI and E2E are green), watch `deploy-aws`, then check `<AppUrl>/health`.
7. After a successful run: delete the old hand-made stack (back up Postgres first, see `deploy/aws/README.md`) and tighten `github-deploy` policy.
