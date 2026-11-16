# Deploy runbook

Short operational view. Details: `deploy/README.md` and the per-host READMEs (`deploy/render/README.md`, `deploy/aws/README.md`). Two environments (#201): dev = Render (automatic on every green `main` commit), prod = AWS (only through the manual workflow `Promote to production`, which uses the IAM-user key path and `deploy/aws/deploy.sh`).

The GitHub OIDC path is no longer in CI (the job `deploy-aws` was removed in #201). `bootstrap.sh` still supports it; the "Owner checklist" for it at the bottom of this file is kept for an account without the SCP. In account 228619515667 an organization SCP denies creating the OIDC provider.

## Status per environment

| Environment | Status |
| --- | --- |
| dev (Render) | Live at https://micro-git.onrender.com, deployed by the Deploy workflow through `RENDER_DEPLOY_HOOK`. The server sets `X-Build` (`backend/build.go`: `RENDER_GIT_COMMIT`, else `APP_COMMIT`, else `dev`); last reported build `5acd89e...`. `autoDeploy` is `false`, so only the CI/E2E-gated deploy hook deploys. The `smoke` job in `deploy.yml` runs after `deploy-render`, waits until `X-Build` equals the deployed commit (the hook is pinned with `ref=<sha>`), then checks `/`. `APP_ENV=dev` (`render.yaml`), so logs, `build_info` and spans carry `env=dev`. |
| prod (AWS) | Stack `micro-git-prod` in `ap-southeast-2`, deployed by `deploy/aws/deploy.sh --apply --account 228619515667` (one command, no OIDC: an org SCP denies `oidc-provider/*`). URL: stack output `AppUrl` (printed by `deploy.sh`). Live at http://3.26.221.66. Since #201 GitHub deploys it only through workflow `Promote to production` (job `deploy`: IAM user `github-deploy-micro-git-prod-user`, key in environment `aws-prod`, main only, after approval in environment `production`). Before #201 the job `deploy-aws-keys` deployed every green `main` commit (last automatic run: Deploy run 43480095153 for `39e71e0`, 2026-11-13). Until the first promotion runs, prod stays on the last automatically deployed commit and reports no `env`; after it, `APP_ENV=prod` (set by `remote-deploy.sh`). It is the only stack: the earlier hand-made stack `mg` was deleted by the owner in 2026-11. |

## Check which build is live

    curl -sI https://micro-git.onrender.com/health | grep -i '^x-build'
    curl -s https://micro-git.onrender.com/health
    git rev-parse --short origin/main        # compare with the X-Build prefix

Other views: Render dashboard (latest deploy commit SHA); `gh run list --workflow deploy.yml --json headSha,conclusion,createdAt`. A green `deploy-render` job only means the hook was accepted; the build continues on Render. For AWS use `curl -sI <AppUrl>/health | grep -i '^x-build'` with the stack output `AppUrl` (`deploy.sh`, also in the `deploy` job of the promote workflow, waits up to 5 minutes for `X-Build` to equal the sha).

## Promote to production

1. Merge to `main`; wait for Deploy (dev) to be green and check `curl -sI https://micro-git.onrender.com/health | grep -i '^x-build'`.
2. Actions > Promote to production > Run workflow, branch `main`, leave `sha` empty (promotes what dev serves now) or paste a 40-hex sha. CLI: `gh workflow run promote.yml --ref main -f sha=<sha>`.
3. `resolve` checks the commit (see `deploy/README.md`). Open the run, read the summary (sha, subject, dev build) and approve `production` as the required reviewer.
4. `deploy` runs `deploy.sh --apply --ci --ref <sha>` and checks prod itself; `verify` re-reads dev and, if the variable `PROD_URL` is set, prod `X-Build`.
5. A failure in `resolve` or a rejected approval leaves prod untouched. A failure in `deploy` stops at the first failing step of `deploy.sh`; check `curl -sI <AppUrl>/health | grep -i '^x-build'` before deciding.

## AWS: deploy, update, tear down (account 228619515667)

    deploy/aws/deploy.sh                                   # dry-run, read-only
    deploy/aws/deploy.sh --apply --account 228619515667    # create or update (same command)
    deploy/aws/deploy.sh --apply --account 228619515667 --ref <sha>   # a specific commit (rollback)
    deploy/aws/destroy.sh [--all]                          # dry-run of the teardown
    deploy/aws/destroy.sh --apply --account 228619515667 [--all]

- Needs `aws login` (your own session), Docker running, `jq`, `openssl`. Region and stack are fixed in the scripts.
- `deploy.sh` stops if the account is not on the Free plan. It runs preflight and bootstrap (`--no-oidc`), keeps the JWT secret in SSM (generated once, never printed; `--rotate-jwt` to replace), deploys the stack through a guarded change set, builds the exact commit for `linux/amd64`, rolls it out through SSM with `APP_COMMIT`, and smoke-checks `/health` (X-Build == sha), `/`, `/api/repos` (200) and `/api/auth/me` (401).
- Automation: a git `post-merge` hook or a cron/launchd job can call the `--apply` command (needs a live `aws login`); GitHub runs the same script in the `deploy` job of `Promote to production` (manual, approved).
- `destroy.sh` terminates the instance first (it is `RetainExceptOnCreate`, so a plain stack delete would leave it running), deletes the stack (ECR images with it), the CI user, its keys, the variable `AWS_DEPLOY_MODE` (no workflow reads it any more) and the `aws-prod` environment secrets `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`. `--all` also deletes `/micro-git-prod/jwt-secret`, role `cfn-deploy-micro-git-prod` and `mg-instance-boundary` (if unused). The stack delete snapshots the data disk (`DeletionPolicy: Snapshot`), so the data survives as a snapshot you delete by hand; the nightly dumps on that disk go with it unless you copied them off the instance first.
- Check credits: `aws freetier get-account-plan-state --region us-east-1`.

### Changing the AMI on purpose

The stack policy set by `deploy.sh` denies replacing or deleting `AppInstance`. To move to a new AMI deliberately (the instance and its Postgres data are replaced; back up first), deploy by hand with a temporary override: `aws cloudformation deploy ... --parameter-overrides LatestAmiId=<new> --role-arn <cfn-deploy ARN>` after `aws cloudformation set-stack-policy --stack-name micro-git-prod --stack-policy-body '{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}]}'`; the next `deploy.sh --apply` puts the protective policy back. The `deploy.sh` change-set guard refuses this change, so it cannot be done through `deploy.sh`.

### CI key rotation

    deploy/aws/bootstrap.sh --no-oidc --ci-user --rotate-ci-key --apply \
      --confirm-account 228619515667 --region ap-southeast-2 --stack-name micro-git-prod

Creates a new key, pipes it into the secrets of GitHub environment `aws-prod` (main only; never printed), then deletes the old key, leaving one active key. Needs github.com reachable (no key is created otherwise). It sets `AWS_DEPLOY_MODE=keys` only when both secrets are in `aws-prod` and no repository-level secret of the same name exists; since #201 no workflow reads that variable. Re-running it re-applies the `aws-prod` branch policy and may reset protection rules added by hand. Without `--rotate-ci-key` the same command only refreshes the user policy and sets the GitHub variables.

## Rollback

- Render: dashboard > service > Events/Deploys > pick the previous good deploy > Rollback. Or revert the bad commit on `main`; the next green CI and E2E redeploys. If Render's build fails, the previous deploy keeps serving (per Render docs, untested here). `autoDeploy` is `false`, so pushes alone do not deploy.
- dev: as above.
- prod (AWS): run `Promote to production` with an older `sha` (it must be on `main` with green CI and E2E; a sha older than dev is allowed and only prints a notice). Locally: `deploy/aws/deploy.sh --apply --account 228619515667 --ref <older-sha>`. The image tag is `<repo>:<sha7>` (reused if still in ECR, which keeps the last 5). If the new `mg-app` fails its health check within 60 s, `remote-deploy.sh` starts the previous image again with its previous `APP_COMMIT` and `deploy.sh` fails (#182). The schema is not rolled back (additive and idempotent). Postgres data is not rolled back: use the nightly dump or a snapshot (below). Never roll the AMI or replace `AppInstance` (the change-set guard blocks it; the data would be lost).
- Any target: `git revert <sha>` on `main` and let CI/E2E gate the redeploy.

## Known follow-ups

Tracked in issues #167, #160, #166 and #168 (AWS hardening, backups and other deferred review items; see `deploy/aws/README.md` Known gaps). Not done in this branch.

## AWS caveats

- After a failed FIRST create the stack sits in `ROLLBACK_COMPLETE`/`ROLLBACK_FAILED`: a local `deploy.sh --apply` deletes and recreates it; the CI job stops and asks for that local run.
- `mg-instance-boundary` is a single account-wide policy and its name is fixed. It covers the secret path of the stack name used at bootstrap. A second stack name in the same account needs its `/<stack>/jwt-secret` path added by hand (`aws iam create-policy-version --set-as-default`); `bootstrap.sh` never modifies an existing policy.

## Data volume, backups and rollback (#182)

Postgres data is on its own EBS disk `DataVolume` (5 GB by default, `/opt/mg/data`, see `deploy/aws/README.md` "Data volume, nightly dump and rollback (#182)"). What protects it:

- **Nightly dump**: `mg-pgdump.timer` (03:15 UTC) writes `/opt/mg/data/backups/mg-<UTC time>.sql.gz` and deletes dumps older than 7 days. The dumps are on the same disk, not off the instance. Run one now: `systemctl start mg-pgdump.service`.
- **Snapshot on delete or replace**: CloudFormation snapshots `DataVolume` before deleting or replacing it. Nothing else snapshots it.
- **Snapshots, design question on #182 (default used: B)**: A = a DLM daily snapshot, keep 7, about 0.05 USD per GB-month (needs an extra IAM role); B = a manual `aws ec2 create-snapshot` before a risky change (the default, below); C = no snapshots, dumps only. The owner decides; until then B. Find the volume id with `aws ec2 describe-volumes --region ap-southeast-2 --filters Name=tag:aws:cloudformation:logical-id,Values=DataVolume --query 'Volumes[0].VolumeId' --output text`, then `aws ec2 create-snapshot --volume-id <id> --description "before <change>" --region ap-southeast-2`. Snapshots cost money and nothing deletes them for you (`aws ec2 delete-snapshot --snapshot-id <id>` by hand).
- **Schema rollback**: none. `schema.sql` is additive and idempotent; an older image ignores newer tables and columns. A dump restore is the only data rollback.

### Move Postgres to the data volume (existing stack, one time, owner)

User data runs only on first boot, so a stack created before #182 keeps Postgres in `/opt/mg/pgdata` on the root volume. The data disk is attached by the stack update but not mounted. Do this in order, as root on the instance (`aws ssm start-session --target <InstanceId>`):

1. Dump and snapshot first (default B). On the instance: `docker exec mg-db pg_dump -U mg -d mg | gzip > /root/pg-before-move.sql.gz`; from your machine: a manual snapshot of the root volume.
2. From your machine: `deploy/aws/deploy.sh --apply --account 228619515667`. The stack update attaches the data disk and stops and starts the instance (`UserData` changed: the public IP changes, and the containers restart on the old `/opt/mg/pgdata`, so nothing is lost).
3. On the instance, as root:

        dnf install -y xfsprogs
        lsblk -o NAME,SIZE,TYPE,MOUNTPOINT      # the 5 GB disk with no mountpoint is the data disk
        DEV=/dev/nvme1n1                          # replace with that disk; never the root disk
        docker stop mg-app mg-db
        blkid "$DEV" || mkfs.xfs -q "$DEV"        # formats only a disk with no filesystem
        UUID="$(blkid -o value -s UUID "$DEV")"
        mkdir -p /opt/mg/data
        # same options as user data in deploy/aws/main.yaml: nofail (boot without the disk), and docker and mg-db
        # wait for and require the mount, so they never start on an empty /opt/mg/data
        echo "UUID=$UUID /opt/mg/data xfs defaults,nofail,x-systemd.before=docker.service,x-systemd.required-by=docker.service 0 2" >> /etc/fstab
        systemctl daemon-reload
        mount /opt/mg/data
        mkdir -p /opt/mg/data/pgdata /opt/mg/data/backups
        cp -a /opt/mg/pgdata/. /opt/mg/data/pgdata/
        docker rm -f mg-db
        docker run -d --name mg-db --restart unless-stopped --network mg --env-file /opt/mg/db.env \
          -v /opt/mg/data/pgdata:/var/lib/postgresql/data postgres:16-alpine
        docker exec mg-db pg_isready -U mg -d mg

4. From your machine: `deploy/aws/deploy.sh --apply --account 228619515667` again. It restarts `mg-app` (with the rollback check) and installs `mg-pgdump`.
5. Check `curl -sI <AppUrl>/health | grep -i '^x-build'`, log in, look at a repository, then on the instance `systemctl start mg-pgdump.service && ls -l /opt/mg/data/backups`.
6. Keep `/opt/mg/pgdata` until step 5 has passed for a while, then `rm -rf /opt/mg/pgdata` to free root space.

Undo before step 6: `docker rm -f mg-db` and run the original `mg-db` line with `-v /opt/mg/pgdata:/var/lib/postgresql/data`; the copy only read the old directory.

## Owner steps for the IAM change (#181)

Details in `deploy/aws/README.md` "IAM scoping (#181)". Not done by an agent; nothing here has run against the account yet.

1. Publish the new boundary version (the statement `OnlyFromTheInstance` is not in effect until you do): render `deploy/aws/policies/mg-instance-boundary.json` (`<ACCOUNT_ID>` = 228619515667, `<STACK_NAME>` = micro-git-prod) and run `aws iam create-policy-version --policy-arn arn:aws:iam::228619515667:policy/mg-instance-boundary --policy-document file://<rendered> --set-as-default --region ap-southeast-2`. Before publishing, note the current default version (`aws iam get-policy --policy-arn arn:aws:iam::228619515667:policy/mg-instance-boundary --query Policy.DefaultVersionId --output text`); that is the rollback target.
   Then verify (the ECR calls made with the docker token may not carry `ec2:SourceInstanceARN`, so this is not proven yet): run `deploy.sh --apply` or, over SSM, `docker pull` of the app image on the instance. If anything is denied, roll back at once: `aws iam set-default-policy-version --policy-arn arn:aws:iam::228619515667:policy/mg-instance-boundary --version-id <previous version>`.
2. Run `deploy/aws/preflight.sh --stack-name micro-git-prod` and check the `mg-InstanceRole-*` line (it warns if an old unbounded role exists; delete it when no instance uses it).
3. `deploy.sh --apply --account 228619515667` (the first apply moves the instance role and profile to the `/mg-stack/micro-git-prod/` path; the instance is re-associated, not stopped by IAM; the stack update also stops and starts it because `UserData` changed, see #182).
4. After it succeeded: remove `TransitionDeleteRootPathRoles` from `deploy/aws/policies/cfn-deploy-policy.json`, commit, and run `deploy.sh --apply` again so the deployed service-role policy loses it.

## Owner checklist (AWS, your credentials only)

OIDC path only. It cannot be completed in account 228619515667: the organization SCP denies creating the GitHub OIDC provider in step 4. Removed from CI in #201: no workflow uses OIDC any more (`bootstrap.sh` still supports it). Production uses the keys path above (`deploy.sh`, `Promote to production`); this list is kept only for an account without that SCP and would need a workflow job added back.

1. `aws login` (or `aws sso login --profile NAME`).
2. `deploy/aws/preflight.sh --region <region> --stack-name <new-stack>` and fix any `[FAIL]`.
3. `deploy/aws/bootstrap.sh --region <region> --stack-name <new-stack>` (dry-run), read the plan.
4. `deploy/aws/bootstrap.sh --apply --region <region> --stack-name <new-stack>`, type the account id.
5. Run the printed `gh variable set AWS_ROLE_ARN ...`, `gh variable set AWS_CFN_ROLE_ARN ...`, `openssl rand -hex 32 | gh secret set AWS_JWT_SECRET`, plus `AWS_STACK_NAME` / `AWS_REGION` if not the defaults.
6. (No OIDC job exists in the workflows any more; deploy with `deploy.sh` or add a job.) Then check `<AppUrl>/health`.
7. After a successful run: tighten the `github-deploy` policy. (The old hand-made stack `mg` no longer exists; the owner deleted it in 2026-11.)

## Owner checklist (#201, GitHub and Render)

1. Settings > Environments > `production`: add yourself as required reviewer (restrict to branch `main`). Without a reviewer the `approve` job passes immediately.
1a. Settings > Environments > `aws-prod`: add yourself as required reviewer too. It holds the IAM-user key, and any workflow that runs on `main` and names this environment gets the key without an approval otherwise. Deployment branches: `main` only (set by bootstrap).
1b. Settings > Branches: protect `main` (pull request before merging, required checks CI and E2E, no force pushes). The promote workflow runs from `main`; anyone who can push to `main` can change what runs there.
2. Render dashboard: if the service is not synced from the Blueprint, add `APP_ENV=dev` to its environment variables (the Blueprint sets it).
3. Optional: Settings > Secrets and variables > Actions > Variables: `PROD_URL` = the stack output `AppUrl` (without a trailing path), so `verify` checks `X-Build` on prod.
4. Environment `aws-prod` keeps the IAM-user key (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`); nothing to change there. The repository variables `AWS_DEPLOY_MODE`, `AWS_REGION`, `AWS_STACK_NAME` and `AWS_CFN_ROLE_ARN` are unused (`deploy.sh` fixes region and stack; `bootstrap.sh` may still set them) and may be deleted, and so may `AWS_ROLE_ARN` and the secret `AWS_JWT_SECRET` (the OIDC path is not used). The workflow needs `AWS_ACCOUNT_ID` (variable, read as `MG_AWS_ACCOUNT`).