# AWS deploy (CloudFormation)

Template: `deploy/aws/main.yaml`. Instance-side script: `deploy/aws/remote-deploy.sh`.

## One command: `deploy.sh` (the path that works in this account)

Account 228619515667 is on the AWS Free plan. Its organization SCP (`p-lao05jll`, part of AWS's free-plan setup; lifting it means upgrading to a paid account) denies everything on `oidc-provider/*` and denies CloudFormation, EC2, ECR and SSM outside `ap-southeast-2`. So the GitHub OIDC path below cannot be used here. Instead:

    deploy/aws/deploy.sh                                  # dry-run: read-only checks and the plan
    deploy/aws/deploy.sh --apply --account 228619515667   # first deploy, and every later update
    deploy/aws/destroy.sh                                 # dry-run of the teardown
    deploy/aws/destroy.sh --apply --account 228619515667  # stop spending credits

Region `ap-southeast-2` and stack `micro-git-prod` are fixed in the scripts; the hand-made stack `mg` is never touched. `deploy.sh` steps, each idempotent:

1. Account guard (`--account` or `MG_AWS_ACCOUNT`, or a typed answer on a terminal) and `aws freetier get-account-plan-state`: stops unless the plan is `FREE`.
2. `preflight.sh --no-oidc` (read-only).
3. `bootstrap.sh --no-oidc --apply`: only the boundary policy `mg-instance-boundary` and the CloudFormation service role `cfn-deploy-micro-git-prod`.
4. JWT secret: if `/micro-git-prod/jwt-secret` does not exist, `openssl rand -hex 32` is piped straight into `ssm put-parameter --type SecureString --value file:///dev/stdin` (never in argv, a file or the output). Kept on later runs; `--rotate-jwt` replaces it (logs everyone out).
5. Stack through a change set with `--role-arn` of the service role. Same guard as the workflow: abort if `AppInstance`, `DataVolume` or `DataVolumeAttachment` would be removed or replaced (`Replacement` `True` or `Conditional`; the offending properties are printed). See "Why `UserData` is frozen (#207)". New stack: latest AL2023 AMI; existing stack: the running instance's AMI. A stack left in `ROLLBACK_COMPLETE`/`REVIEW_IN_PROGRESS` by a failed first create is deleted and recreated automatically (local runs only).
6. Image: `git archive <sha> | docker build --platform linux/amd64 -` (exactly the commit: no uncommitted or git-ignored files), pushed as `<sha7>` and `latest`. Skipped if `<sha7>` is already in ECR.
7. `remote-deploy.sh` through SSM Run Command with `APP_COMMIT=<full sha>`, so `X-Build` names the commit, and the stack output `DataVolumeId` when the data disk is on (it mounts the disk, see #182 below).
8. Smoke: waits until `/health` is 200 with `X-Build` equal to the sha, then `/` 200, `/api/repos` 200, `/api/auth/me` 401 (all with the same `X-Build`), prints the URL.

Options: `--ref <commit>` deploys another commit (default `HEAD`), `--profile`, `--rotate-jwt`, `--ci` (used by GitHub Actions: skips preflight and bootstrap, Free plan check informational), `--data-volume on|off` (sets the stack parameter `EnableDataVolume`; without it the stack keeps its current value, so CI never changes it), `--check-change-set FILE` (offline: runs the guard on saved `describe-change-set` JSON, no AWS call). A lock in `$TMPDIR` stops two runs overlapping.

Automating updates: any run of `deploy.sh --apply --account 228619515667` is the update. Options: a local git hook (for example `.git/hooks/post-merge` that runs it when `main` is pulled), a scheduled run (cron or launchd), or the `Promote to production` GitHub workflow below. Local hooks and schedules need a live `aws login` session; there are no stored keys on the machine.

### GitHub Actions without OIDC: CI IAM user

User decision (2026-11-13): GitHub deploys with one dedicated IAM user, because OIDC is denied by the SCP.

- `deploy/aws/bootstrap.sh --no-oidc --ci-user --apply --confirm-account 228619515667 --region ap-southeast-2 --stack-name micro-git-prod` creates user `github-deploy-micro-git-prod-user` (tag `Project=micro-git`) with the customer-managed policy `github-deploy-micro-git-prod-user-policy` from `policies/ci-user-policy.json` (too big for an inline user policy, 2 KB limit): change sets on `micro-git-prod` only and only with `cloudformation:RoleArn` = the service role, `iam:PassRole` only for `cfn-deploy-micro-git-prod`, ECR push to `micro-git-prod-app`, `PutParameter` on `/micro-git-prod/jwt-secret` (no read), `SendCommand` only to instances tagged with the stack, explicit denies for stack `mg`, its repo and instance, and for every region except `ap-southeast-2`. No other IAM rights.
- It creates (idempotently) the GitHub environment `aws-prod` with a deployment branch policy that allows only `main`, so workflows on other branches cannot read its secrets.
- If the user has no key, one is created and its JSON is piped through `python3` straight into `gh secret set --env aws-prod AWS_SECRET_ACCESS_KEY` / `AWS_ACCESS_KEY_ID` (stdin). It is never printed, logged or written to a file; only the last 4 characters of the key id are shown. If GitHub is unreachable no key is created; if storing fails, the new key is deleted again and the script says to re-run with `--rotate-ci-key` (the secrets may be a mismatched pair). It then sets the repository variables `AWS_REGION`, `AWS_STACK_NAME`, `AWS_CFN_ROLE_ARN`, `AWS_ACCOUNT_ID` and, last, `AWS_DEPLOY_MODE=keys` (not secrets).
- Rotation: same command plus `--rotate-ci-key`: new key into the secrets, then the old key is deleted, so there is one active key.
- Workflow: `.github/workflows/promote.yml` (manual, `workflow_dispatch`; since #201 nothing deploys AWS on push). Job `deploy` (environment `aws-prod`, after the `resolve` checks and the approval in environment `production`, workflow concurrency `promote-production`) passes the key only as env of the step that runs `deploy/aws/deploy.sh --apply --ci --ref <sha>` (no third-party action sees it); that includes the change-set guard and the X-Build smoke. `remote-deploy.sh` starts the container with `APP_ENV=prod`. The variable `AWS_DEPLOY_MODE` is still set by `bootstrap.sh` but no workflow reads it.
- Server-side guard: local `deploy.sh --apply` runs set a stack policy that denies `Update:Replace` and `Update:Delete` on `AppInstance`, `DataVolume` and `DataVolumeAttachment`, so even a hand-made change set cannot replace the instance or drop the data disk.
- Teardown: `destroy.sh --apply` deletes the user, its keys, the variable `AWS_DEPLOY_MODE` and the two environment secrets.
- Accepted risk: anyone holding this key (or able to change the workflow on `main`) can deploy any image and run any command as root on the `micro-git-prod` instance through `ssm:SendCommand` (scoped to instances tagged with this stack). That is what a deploy credential does; it cannot reach other stacks, IAM, or other regions. Rotate it with `--rotate-ci-key` and delete it with `destroy.sh` when not needed.

## OIDC path (former OIDC job, removed from CI in #201; not usable in account 228619515667)

Formerly used by a job of `.github/workflows/deploy.yml` (removed in #201), which ran when the repository variable `AWS_ROLE_ARN` was set. The job was removed in #201 (ADR 0005, option B); `bootstrap.sh` still creates the OIDC provider and role. The text below describes what the job did.

## Status: what is verified and what is not

- Static: `cfn-lint` on `deploy/aws/main.yaml`, `actionlint` on `.github/workflows/deploy.yml` and `shellcheck` on all `deploy/aws/*.sh` are clean.
- Real account (2026-11-13, account 228619515667): `deploy.sh --apply` (no-OIDC path) was run end to end; see `_docs/deploy-runbook.md` for the result. The OIDC path (the former OIDC job) is still unverified and cannot run in this account (SCP).

## How the deploy worked (former OIDC job)

1. Fails early if secret `AWS_JWT_SECRET` is missing or shorter than 32 characters (the value is never printed).
2. Assumes the role in `AWS_ROLE_ARN` through GitHub OIDC (no stored AWS keys). Fails early if `AWS_CFN_ROLE_ARN` is not set.
3. Writes the secret to SSM Parameter Store as SecureString `/<stack>/jwt-secret`. It is not in the template, the stack parameters or EC2 user data (this fixes the earlier exposure through `ec2:DescribeInstanceAttribute`). This parameter lives outside the stack and is NOT deleted with it.
4. Picks the AMI: for an existing stack it reuses the running instance's `ImageId` (read with `describe-instances`); only for a new stack it resolves the latest Amazon Linux 2023 AMI from the public SSM parameter. `LatestAmiId` is a plain `AWS::EC2::Image::Id` parameter, not an SSM-resolved one, because those are re-resolved on every update and a new AMI release would replace the instance and wipe Postgres.
5. Guard: `aws cloudformation deploy --no-execute-changeset --role-arn <AWS_CFN_ROLE_ARN>`, then the job fails if the change set removes or replaces (`Replacement` True or Conditional) `AppInstance`. The guard inspects the change set ARN printed by `deploy`, and that exact change set is the one executed with `execute-change-set`, followed by `wait stack-create-complete` or `stack-update-complete`. The stack has no `ImageTag` parameter: image rollout is not a stack change.
6. Logs in to ECR (`aws-actions/amazon-ecr-login@v2`), builds the root `Dockerfile`, pushes `<repo>:<sha7>` and `<repo>:latest`.
7. Sends `deploy/aws/remote-deploy.sh` to the instance with SSM `AWS-RunShellScript`. On the instance it waits for `cloud-init status --wait` (fails on error) and checks that `mg-db` and `mg-proxy` exist, pulls the image, reads the JWT secret from SSM with the instance role, applies `backend/db/schema.sql` (idempotent, so the schema now follows every deploy instead of being applied by hand) and recreates the `mg-app` container. The job polls `get-command-invocation` for up to 15 minutes (`executionTimeout` 900 s) and fails unless the status is `Success`.
8. Polls `<AppUrl>/health` for up to 5 minutes. With `DomainName` set, AppUrl is `https://<domain>`, so its DNS record must already point at the instance.

Schema is applied by the deploy step rather than in user data so there is a single source (`backend/db/schema.sql`) and schema changes also reach existing instances.

## Required GitHub variables and secrets (by name)

| Kind | Name | Required | Purpose |
| --- | --- | --- | --- |
| Variable | `AWS_ROLE_ARN` | yes | IAM role ARN that GitHub OIDC assumes (also the gate: job is skipped without it) |
| Variable | `AWS_CFN_ROLE_ARN` | yes | ARN of the CloudFormation service role (passed as `--role-arn`; the job fails early without it) |
| Secret | `AWS_JWT_SECRET` | yes | Session-token HMAC key, 32+ characters, e.g. `openssl rand -hex 32` |
| Variable | `AWS_REGION` | no | Default `us-east-1` (the hand-made stack is in `ap-southeast-2`) |
| Variable | `AWS_STACK_NAME` | no | Default `mg` |

## One-time bootstrap (you, in the AWS account)

Honesty label: `bootstrap.sh` and `preflight.sh` are tested only offline (shellcheck, `bash -n`, a stubbed `aws`); they have never been run against a real AWS account. Read the dry-run output before using `--apply`, and never point them at an account you do not own.

Two roles keep privileges apart: a GitHub deploy role (small, assumed through OIDC) and a CloudFormation service role (holds the EC2/IAM/ECR rights and is only usable by CloudFormation, trust pinned with `aws:SourceAccount`). The role names are `cfn-deploy-<stack>` and `github-deploy-<stack>` (default stack `mg`). The deploy roles are named `cfn-deploy-<stack>` and `github-deploy-<stack>` at the root IAM path. The stack's own roles and instance profile live under the path `/mg-stack/<stack>/` (since #181), so the service role cannot touch the deployers, and a stack's policies match only its own path.

The exact JSON is in `deploy/aws/policies/` (single source of truth; `bootstrap.sh` substitutes `<ACCOUNT_ID>`, `<STACK_NAME>` and `<REPO>` into a temp dir):

| File | What |
| --- | --- |
| `policies/mg-instance-boundary.json` | Customer-managed policy `mg-instance-boundary`: permissions boundary the template puts on the instance role (ECR pull, SSM agent, and `GetParameter` of `/<stack>/jwt-secret` only with credentials issued to an EC2 instance through the instance profile (`ec2:SourceInstanceARN`)). The name is fixed and account-wide because `main.yaml` references it, and its secret path is `/<stack>/jwt-secret` of the stack name used at bootstrap. A second stack name in the same account needs its own path added by hand (`aws iam create-policy-version --set-as-default` on this policy; the bootstrap script never changes an existing policy), otherwise its instance cannot read the secret. If it already exists the script leaves it unchanged. Never grant anyone `iam:CreatePolicyVersion` or `iam:SetDefaultPolicyVersion` on it. |
| `policies/cfn-deploy-trust.json`, `policies/cfn-deploy-policy.json` | CloudFormation service role. IAM rights only for roles and instance profiles under `/mg-stack/<stack>/` (create, inline policy, delete, attach/detach of the two AWS managed policies the template uses), plus the one-time transition statement described in "IAM scoping" below; EC2 limited to `t2.micro`/`t3.micro` and, for existing resources, to resources tagged with this stack. |
| `policies/github-deploy-trust.json`, `policies/github-deploy-policy.json` | GitHub OIDC role: trust pinned to GitHub environment `aws-prod` (`repo:<REPO>:environment:aws-prod`) with audience `sts.amazonaws.com`. No workflow uses this role today (ADR 0005: production uses the IAM user key), so nothing assumes it. The policy is a starting point. |

Steps:

1. Log in with your own credentials: `aws login` (or `aws sso login --profile NAME`).
2. Check first (read-only, changes nothing): `deploy/aws/preflight.sh --region <region> --stack-name <stack>`. It verifies the identity, default VPC, `t3.micro`/`t2.micro` offering, whether a stack with that name exists (use a NEW stack name if the hand-made `mg` exists, see Gotchas), whether the roles and policies already exist, and which GitHub variables and secrets are set (names only).
3. Review the plan: `deploy/aws/bootstrap.sh --region <region> --stack-name <stack>` (dry-run is the default; it prints the aws commands and runs no mutating call).
4. Apply: `deploy/aws/bootstrap.sh --apply --region <region> --stack-name <stack>` (add `--profile NAME` if needed). It prints the account id and asks you to type it. It creates, in order and idempotently: the GitHub OIDC provider (if missing), `mg-instance-boundary` (if missing), `cfn-deploy-<stack>` with its policy, `github-deploy-<stack>` with its policy (existing roles get their trust and policy refreshed; if an existing role's trust policy differs from the repo file, `--apply` aborts unless you pass `--force`, and the dry-run says it would overwrite). The OIDC provider is created without `--thumbprint-list`, which needs AWS CLI v2 2.15.38 or newer (older versions require the parameter; checked against the AWS CLI v2 changelog, v1 not verified).
5. Run the `gh` commands the script prints at the end (it does not run them):

        gh variable set AWS_ROLE_ARN --repo L0nelyG0d/micro-git --body <github-deploy ARN>
        gh variable set AWS_CFN_ROLE_ARN --repo L0nelyG0d/micro-git --body <cfn-deploy ARN>
        openssl rand -hex 32 | gh secret set AWS_JWT_SECRET --repo L0nelyG0d/micro-git
        # only if not the defaults:
        gh variable set AWS_STACK_NAME --repo L0nelyG0d/micro-git --body <stack>
        gh variable set AWS_REGION --repo L0nelyG0d/micro-git --body <region>

   Use a NEW random `AWS_JWT_SECRET`; do not reuse any value that was ever in the old template parameters, user data, chat or logs.
6. Deploy with `deploy.sh` or the `Promote to production` workflow and watch the job; the first run takes several minutes (instance boot, image pull). If it fails, read the job log (the SSM step prints the instance command output); re-running is safe after a failure of an ordinary update. After a FAILED FIRST CREATE (stack in `ROLLBACK_COMPLETE` or `ROLLBACK_FAILED`) an admin must first run `aws cloudformation delete-stack --stack-name <stack>` and wait for `DELETE_COMPLETE`; the job cannot update a stack in that state. `AppInstance` uses `DeletionPolicy: RetainExceptOnCreate`, so a failed first create deletes the half-built instance, while a later `delete-stack` still retains it.

Manual equivalent: the same IAM objects can be created in the console from the JSON files (replace the placeholders by hand). The account also needs a default VPC in the region and EC2 capacity for a `t3.micro`.

`DomainName` is not passed by CI, and `aws cloudformation deploy` keeps the previous value of parameters it is not given, so a domain set by a manual deploy is not dropped by later CI runs. The AMI is passed on every run (see step 4 above).

## IAM scoping (#181)

What changed, and what the owner must do with it:

- **Paths.** `main.yaml` gives `InstanceRole` and `InstanceProfile` the path `/mg-stack/<stack>/`. `cfn-deploy-policy.json` allows create, pass, attach and delete only on `role/mg-stack/<stack>/*` and `instance-profile/mg-stack/<stack>/*`. Nothing matches by name prefix any more.
- **Path and the first apply.** IAM cannot change a role's path in place, so CloudFormation replaces the role on the first update after this change: it creates the new role and profile under `/mg-stack/`, moves the instance to the new profile (`ReplaceIamInstanceProfileAssociation`; `IamInstanceProfile` is not a create-only property of `AWS::EC2::Instance`, so the change set should show `AppInstance` as `Modify` with `Replacement: False`; the instance keeps running, IAM does not need a reboot), then deletes the old `<stack>-InstanceRole-*` and `<stack>-InstanceProfile-*` objects at the root path. The statement `TransitionDeleteRootPathRoles` allows only the delete side on those two names. `deploy.sh` (and `bootstrap.sh --apply`) refresh the service role's policy from the repo file before the stack update, so the statement is in place for that first run. `deploy.sh --ci` (the Promote workflow) skips that step, so make the first apply after #181 a local `deploy.sh --apply` (runbook, "Owner steps after #207").
  **Owner, after the first apply succeeded:** delete the statement `TransitionDeleteRootPathRoles` from `policies/cfn-deploy-policy.json`, commit it, and run `deploy.sh --apply` (or `bootstrap.sh --apply`) once so the deployed policy loses it. Until then the service role can delete roles named `<stack>-InstanceRole-*` at the root path; nothing else is widened by it.
- **Instance profile association.** The instance statement in `cfn-deploy-policy.json` now also allows `ec2:ReplaceIamInstanceProfileAssociation` and `ec2:DisassociateIamInstanceProfile`, scoped by the stack-name tag like the other instance actions. `ec2:DescribeIamInstanceProfileAssociations` is already covered by `ec2:Describe*`. Not verified on a real account: if the first apply fails with AccessDenied on one of these, the error names the resource IAM checks; add it with the same tag scope.
- **Boundary policy (account-wide, needs your action).** `policies/mg-instance-boundary.json` has a new statement `OnlyFromTheInstance`: it denies every action except `kms:Decrypt` and the four ECR pull actions listed below when the request has no `ec2:SourceInstanceARN`, i.e. when the credentials are not issued to an EC2 instance. `bootstrap.sh` never changes an existing policy, so publish a new version by hand (render `<ACCOUNT_ID>` and `<STACK_NAME>` first, keep the account's version count at 5 or fewer):

        aws iam create-policy-version --policy-arn arn:aws:iam::<ACCOUNT_ID>:policy/mg-instance-boundary \
          --policy-document file://<rendered mg-instance-boundary.json> --set-as-default --region ap-southeast-2

  The `NotAction` list also exempts the four ECR pull actions (`ecr:GetAuthorizationToken`, `ecr:BatchCheckLayerAvailability`, `ecr:GetDownloadUrlForLayer`, `ecr:BatchGetImage`): registry calls made with the docker token may not carry `ec2:SourceInstanceARN`, and an image pull exposes no secret. Whether ECR pull still works from the instance is NOT verified until the owner checks it. Until the new version is set, the statement is not in effect.
  **Owner, right after `--set-as-default`:** note the previous default version first (`aws iam get-policy --policy-arn arn:aws:iam::<ACCOUNT_ID>:policy/mg-instance-boundary --query Policy.DefaultVersionId --output text`), then verify with a deploy, or a `docker pull` of the app image on the instance over SSM. If anything is denied, roll back at once: `aws iam set-default-policy-version --policy-arn arn:aws:iam::<ACCOUNT_ID>:policy/mg-instance-boundary --version-id <previous version>`.
- **OIDC trust.** `policies/github-deploy-trust.json` now pins `sub` to `repo:<REPO>:environment:aws-prod` (audience kept). IAM cannot check `job_workflow_ref` (which workflow file ran) with the default OIDC subject; that needs a custom OIDC subject template on the repository, which is a repository setting and the owner's call. The role is not used by any workflow (ADR 0005). `bootstrap.sh --apply` (without `--no-oidc`) refuses to overwrite the existing trust unless `--force`, as before.
- **CreateRole trust (residual risk).** IAM has no condition key that limits which principal a new role may trust, so `iam:CreateRole` cannot be constrained to "trust only EC2". Mitigations: (a) the boundary's `OnlyFromTheInstance` statement leaves a role with no useful power for any principal other than an EC2 instance (`kms:Decrypt` stays possible only through SSM, as before); (b) `cfn-deploy-policy.json` denies `iam:UpdateAssumeRolePolicy` to the service role. Residual: the service role (usable by the CI user and the owner only, through CloudFormation with `cloudformation:RoleArn`) can still create a boundary-capped role under `/mg-stack/<stack>/` that trusts another principal, and that principal gets only the boundary's ECR-pull actions (the SSM-agent actions and `GetParameter` are denied without `ec2:SourceInstanceARN`). Side effect of (b): a future template change that edits a role's trust policy needs the deny removed first (owner).
- **Old unbounded `mg-InstanceRole-*`.** The hand-made stack `mg` was deleted (2026-11-13/14). `preflight.sh` now also lists root-path roles named `mg-InstanceRole-*` (read-only) and warns if any exist; because PassRole is limited to `/mg-stack/<stack>/`, such a role cannot be passed to the new instance. Delete it when no instance uses it (owner).

## Why `UserData` is frozen (#207)

The first promote after #181/#182 failed in step 5 with `the change set would replace or remove AppInstance`. Cause: #182 edited `AppInstance.UserData`. `UserData` is in the `conditionalCreateOnlyProperties` of `AWS::EC2::Instance` (CloudFormation resource schema, together with `InstanceType`, `BlockDeviceMappings`, `SecurityGroupIds` and others), so the change set reports `AppInstance` as `Replacement: Conditional`, and the guard refuses `Conditional` as well as `True`. (For an EBS-backed instance the update would most likely have been a stop and start, not a replacement, but the change set does not say which, and the new user data would never have run on the existing instance anyway: user data runs on first boot only.)

Rules since #207:

- `UserData` is byte-identical to the template that is live on `micro-git-prod` (deployed from 5828285). `deploy/aws/test-template.sh` (CI job `ops-tests`) fails if the block changes. Instance changes go into `remote-deploy.sh`, which runs over SSM on every deploy.
- Do not change other create-only or conditionally create-only properties of `AppInstance` either (`ImageId`, `KeyName`, `AvailabilityZone`, `SubnetId`, `InstanceType`, `BlockDeviceMappings`, `SecurityGroupIds`, ...). Changes that are safe for the instance: `Tags`, `IamInstanceProfile`, `MetadataOptions`, `Monitoring`, and new resources that only reference it.
- The guard stays: `AppInstance`, `DataVolume`, `DataVolumeAttachment` must not be removed or replaced (`True` or `Conditional`). On a refusal `deploy.sh` prints each offending property with `RequiresRecreation` and its cause.
- `deploy.sh --apply` creates the change set and deletes it again without executing it when it is unsafe, so a run is itself the check. To look at a change set you made by hand: `aws cloudformation describe-change-set --change-set-name <arn> --output json > cs.json`, then `deploy/aws/deploy.sh --check-change-set cs.json` (offline).

## Data volume, nightly dump and rollback (#182)

- **Data volume (optional, off by default since #207).** Stack parameter `EnableDataVolume` (`false` by default; `deploy.sh --data-volume on|off`). With `true`, `DataVolume` (gp3, encrypted, `DataVolumeSizeGb`, default 5, range 1 to 10) is created and attached as `/dev/sdf` (`DataVolumeAttachment`); the stack output `DataVolumeId` names it. Turning it on only adds these two resources: `AppInstance` is not modified. Turning it off again would delete the disk (after a snapshot); `deploy.sh` and the stack policy refuse that.
- **Mounting.** `remote-deploy.sh` (step 7) gets the volume id from `deploy.sh` and finds the disk by it (Nitro `t3`: `/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_vol<id>`; Xen `t2`: `/dev/xvdf`; never the root disk). It formats it with XFS only when it has no filesystem and no partitions, refuses any other filesystem, and mounts it at `/opt/mg/data` by UUID with `nofail,x-systemd.before=docker.service,x-systemd.required-by=docker.service` (`nofail` so the instance still boots for SSM; the systemd options so that, after the Postgres move, docker and `mg-db` never start without the disk and never initialise an empty cluster on the root disk). If `/opt/mg/data` already holds files on the root disk (dumps written before the disk existed), they are moved to `/opt/mg/data.root-<UTC time>` and the dumps are copied onto the disk; if a running container uses that directory it refuses. Every run after that only checks the mount. A failure stops the deploy before `mg-app` is touched.
- **Postgres stays where it is.** User data (frozen) starts `mg-db` on `/opt/mg/pgdata` on the root volume, for old and new stacks. Moving it onto the data disk is a one-time owner step: `_docs/deploy-runbook.md`, "Move Postgres to the data volume (#182)". Until then the data disk only holds the nightly dumps.
- **Snapshots.** `DataVolume` has `DeletionPolicy: Snapshot` and `UpdateReplacePolicy: Snapshot`: CloudFormation snapshots the volume before it deletes or replaces it. No scheduled snapshots (DLM) are created: that needs an extra IAM role and costs money, so it is an owner decision (options on issue #182). Default in use: manual snapshots before risky changes (runbook). Snapshots are billed per GB-month and `destroy.sh` does not delete them.
- **Nightly dump.** `remote-deploy.sh` installs `/opt/mg/bin/pgdump.sh` and the systemd timer `mg-pgdump.timer` (03:15 UTC, `Persistent=true`). Each run writes `docker exec mg-db pg_dump -U mg -d mg`, gzipped, to `/opt/mg/data/backups/mg-<UTC timestamp>.sql.gz` (umask 077), and deletes dumps older than 7 days. `/opt/mg/data` is the data disk when it is on, otherwise a directory on the root volume. The dumps are not copied off the instance. Manual run: `systemctl start mg-pgdump.service`, then `journalctl -u mg-pgdump`.
- **Rollback (#182, item 9).** `remote-deploy.sh` records the running `mg-app` image and its `APP_COMMIT`, starts the new container and waits up to 60 s for `/health` to return HTTP 200, with `X-Build` equal to the commit when one is given. On failure it prints the new container's last 30 log lines, removes it, starts the previous image with its previous `APP_COMMIT` (same flags), prints `rolled back to <image>` and exits 1, so `deploy.sh` fails before its smoke checks. On the first deploy there is nothing to go back to: it exits 1.
- **The schema is not rolled back.** `schema.sql` is additive and idempotent, so the previous image runs against the newer schema and ignores the tables and columns it does not know.
- **APP_COMMIT and X-Build (#182, item 14)** were already in place (`deploy.sh` step 8, `remote-deploy.sh`). Unchanged.

Checked offline only: `deploy/aws/test-remote-deploy.sh` (device lookup by volume id, Xen fallback, never the root disk, format only a blank disk, refuse another filesystem or partitions, idempotent second run, old dumps moved aside, refuse when a container uses the directory) and `deploy/aws/test-template.sh` (frozen `UserData`, the data disk conditional, the guard on saved change sets, `bash -n`, `cfn-lint` when installed). Not verified on a real account: the real change set, attach, `mkfs`/mount on EC2, the CloudFormation volume rights, and the timer.

**Owner checks at the first apply with `--data-volume on` (volume rights, #182):** `cfn-deploy-policy.json` now grants `ec2:CreateSnapshot` on `volume/*` only when the volume has the stack-name tag (`aws:ResourceTag/aws:cloudformation:stack-name`), `ec2:CreateSnapshot` on `snapshot/*` with no tag condition, `ec2:CreateTags` on `snapshot/*` only with `ec2:CreateAction = CreateSnapshot`, and `ec2:ModifyVolume` on `volume/*` with the same tag condition. Check at the first apply that CloudFormation can snapshot the data volume (the snapshot is taken on replace or delete). If an AccessDenied names one of these actions, add it with the same scope and tell the reviewer.

## Expected cost

Within the free tier (legacy accounts, first 12 months): about 0 USD/month for one `t3.micro`, 20 GB gp3 and a few ECR images. Outside it, rough order of magnitude: t3.micro about 8-10 USD/month, 20 GB gp3 about 2 USD, public IPv4 about 3.6 USD, ECR storage cents, data transfer out beyond 100 GB/month. No load balancer, NAT gateway, RDS or Elastic IP is created; SSM Parameter Store standard parameters and Run Command are free. Verify in Billing; these are estimates, not measured.

## Manual run

    export AWS_REGION=<region>
    AMI=$(aws ssm get-parameter --name /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64 --query Parameter.Value --output text)   # new stack only
    printf '%s' "$JWT_SECRET" > /tmp/jwt && aws ssm put-parameter --name /mg/jwt-secret --type SecureString --overwrite --value file:///tmp/jwt; rm /tmp/jwt
    aws cloudformation deploy --template-file deploy/aws/main.yaml \
      --stack-name mg --capabilities CAPABILITY_IAM --no-fail-on-empty-changeset \
      --role-arn <AWS_CFN_ROLE_ARN> --parameter-overrides LatestAmiId=$AMI [DomainName=app.example.com] [KeyName=mykey]

Then push the image and run `remote-deploy.sh` through SSM as the workflow does (see `deploy.sh`). Other parameters: `InstanceType`, `AdminCidr`, `VolumeSizeGb`, `EnableDataVolume` (`true`/`false`, default `false`), `DataVolumeSizeGb`. For an existing stack pass the running instance's current AMI id, never a newer one.

## What this creates and what is free

| Resource | Free tier |
| --- | --- |
| EC2 `t3.micro` (or `t2.micro`), Amazon Linux 2023, 20 GB gp3 root volume | Free-tier eligible: 750 h/month and 30 GB EBS for 12 months on accounts on the legacy free tier. Check the plan in Billing; newer plans use credits. |
| ECR private repository (`<stack>-app`), scan on push, keeps the last 5 images | 500 MB/month of storage free for 12 months (legacy plan) |
| Public IPv4 address on the instance | Charged per hour since Feb 2024; the free allowance is the same 750 h/month on the legacy plan. No Elastic IP is created. |
| Data EBS gp3 volume, only with `EnableDataVolume=true` (`DataVolumeSizeGb`, default 5 GB, encrypted, `/opt/mg/data`) | Inside the same 30 GB EBS allowance as the root volume (root + data at most 30 GB). Snapshots (taken on delete, and manually) are billed per GB-month |
| Security group (80, 443 public; 22 from `AdminCidr`) | Free |
| IAM role and instance profile (ECR read, SSM Session Manager) | Free |
| Docker on the instance: Postgres 16, the app image, Caddy (TLS) | Runs on the instance |

Not used on purpose: App Runner (not free), RDS (`db.t3.micro` is free only for 12 months, and needs a second instance plus subnet group), NAT gateway, load balancer, Elastic IP.

## Gotchas

- **Stop/start changes the public address** because there is no Elastic IP. DNS for `DomainName` must be updated after a restart.
- **Secrets**: the JWT secret is in SSM Parameter Store (SecureString, default `aws/ssm` key) and in `/opt/mg/app.env` (mode 600) on the instance; it is not in user data. The generated DB password is in `/opt/mg/*.env` and is generated in user data on the instance, never in the template. Anyone with `ssm:GetParameter` (with decryption) on `/<stack>/jwt-secret` or shell access to the instance can read the JWT secret.
- **Rotating the JWT secret**: update the GitHub secret and re-run the deploy; all sessions are invalidated.
- **Existing hand-made stacks must be recreated under a NEW stack name.** First-boot user data runs once, so editing `UserData` on an existing stack would not re-run it (and `deploy.sh` refuses the edit, #207); an old instance would lack the new layout, and the removed `JwtSecret`/`ImageTag` parameters change the stack. Deploy to a new name (set `AWS_STACK_NAME`), migrate data if needed, then delete the old stack.
- **JWT secret.** `main.yaml` has no JWT parameter and no user-data secret. For stack `micro-git-prod`, a manual `deploy.sh` run generates a fresh random secret (`openssl rand -hex 32`, piped straight into SSM, never printed) if none exists, so the value that was exposed in the hand-made `mg` stack's user data is not used by it. The former OIDC job instead overwrote `/<stack>/jwt-secret` from the GitHub secret `AWS_JWT_SECRET` on every deploy: generate that with `openssl rand -hex 32` and never reuse the `mg` value. Never copy a secret from another stack into SSM. Rotating the live secret is an operator action and logs every user out: `deploy.sh --apply --rotate-jwt` (and, if an OIDC job is ever added back, also update `AWS_JWT_SECRET`, or its next deploy reverts it). Rotate the `mg` stack's secret or delete that stack if it is still running.
- **AMI upgrades are a deliberate manual action.** CI never changes the AMI of a running stack, and the change-set guard fails the job if `AppInstance` would be replaced or removed. To move to a newer AMI: back up Postgres, then deploy manually with a new `LatestAmiId` knowing the instance is replaced and the data is lost unless restored.
- **The image serves the SPA and the API** (root `Dockerfile`). Caddy forwards everything to `mg-app:8080`.
- **Swap**: `t3.micro` has 1 GB RAM; user data adds a 1 GB swap file.
- **Lint**: `cfn-lint` ran clean through Docker (see Status above).

## What was actually deployed

- Stack `mg` in `ap-southeast-2`, created by hand with `aws cloudformation deploy` (not by CI). Public address: http://ec2-13-211-236-62.ap-southeast-2.compute.amazonaws.com
- The former OIDC job in `.github/workflows/deploy.yml` (removed in #201) ran only when the variable `AWS_ROLE_ARN` exists, and it did not when this was written. It defaults `AWS_REGION` to `us-east-1`, not `ap-southeast-2`, unless the variable is set.

## Delete the stack / avoid charges

    aws cloudformation delete-stack --stack-name mg --region <region>
    aws cloudformation wait stack-delete-complete --stack-name mg --region <region>

Use the region the stack lives in (`ap-southeast-2` for the hand-made deployment). The ECR repository does not need to be emptied first: `deploy/aws/main.yaml` sets `EmptyOnDelete: true` on `AppRepository`, so CloudFormation deletes the images with it. `AppInstance` has `DeletionPolicy: RetainExceptOnCreate` (retained on stack delete, deleted only when the first create fails) and `UpdateReplacePolicy: Retain`, so deleting the stack leaves the instance and its root volume behind. **Back up Postgres BEFORE `delete-stack`**: deleting the stack tries to remove the instance role and profile of the retained instance (if that succeeds SSM goes offline; if the instance still holds them they end `DELETE_FAILED`), and SSH is closed by default (`AdminCidr`). The security group ends in `DELETE_FAILED` (the instance still uses it) and, because the retained instance still uses the instance profile, the profile and role can also end in `DELETE_FAILED` (delete them by hand once the instance is terminated). To recover access afterwards, re-attach an instance profile or open SSH through the security group (`AdminCidr`), take the backup, then terminate the instance by hand and delete the leftover security group. The repository is deleted with the stack. Postgres data is a bind mount of `/opt/mg/pgdata` on the root volume, or of `/opt/mg/data/pgdata` on the data volume after the owner's move (`DataVolume`, #182/#207). Deleting the stack snapshots the data volume, if there is one (`DeletionPolicy: Snapshot`), so the data survives as a snapshot that you delete by hand; terminating the instance alone does not delete the volume. The nightly dumps are on the same volume: copy them off the instance if you need them after a teardown. Check the CloudFormation console that the stack reached `DELETE_COMPLETE`, and check EC2 and ECR for leftovers.

What can cost money once the free tier is over (free tier ended, or you are past the limits):

- EC2 hours beyond 750 per month, or any hours after the first 12 months on the legacy free tier.
- The public IPv4 address, charged per hour (free allowance is the same 750 h/month on the legacy plan only).
- EBS storage over 30 GB (the template defaults to 20 GB gp3).
- ECR storage over the free quota (the lifecycle policy keeps the last 5 images).
- Data transfer out of AWS.

Newer AWS accounts may be on a credit-based plan instead of the legacy free tier; check Billing. Stopping the instance stops EC2 hours but not EBS, and the public IP changes on restart (see Gotchas). This section is from reading the template; I did not run the delete.

## Known gaps

Deferred review findings (no backups, hardening, and other follow-ups) are tracked in issue #166. Done in #182, made non-replacing in #207 (not verified on a real account): an optional data volume, nightly `pg_dump` and automatic rollback of `mg-app` (see "Data volume, nightly dump and rollback (#182)"). Open: scheduled EBS snapshots (the owner's decision, question on #182), off-instance copies of the dumps, and anything else that was never run against a real account.

Also deferred to #166:

- Stale DNS after a stop/start when `DomainName` is used (no Elastic IP).
- `aws:SourceArn` on the CloudFormation service role trust policy (`aws:SourceAccount` is now set).
- Restricting the OIDC trust to `job_workflow_ref`: needs a custom OIDC subject template on the repository (a repository setting, owner's call). The trust itself is now pinned to environment `aws-prod` (#181).
- Remaining EC2 rights are account-wide where AWS does not support tag conditions (for example `ec2:CreateSecurityGroup`, `ec2:Describe*`, `ec2:RunInstances` on non-instance resources). The account must not host other, more privileged instances or roles that the service role could pass or affect.
- The `CreateRole` trust policy of stack roles cannot be constrained by IAM; mitigated and residual risk as described in "IAM scoping (#181)".
- An old unbounded `mg-InstanceRole-*` from the hand-made stack: the stack is deleted; `preflight.sh` warns if such a root-path role still exists (see "IAM scoping (#181)").
- Unverified on a real account, check on the first run: `kms:Decrypt` for the `aws/ssm` key under the boundary, the `iam:PassRole` context for `AddRoleToInstanceProfile`, and an orphaned instance after a failed first stack create (it is `Retain`ed and must be terminated by hand).
