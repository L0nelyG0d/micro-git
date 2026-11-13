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
5. Stack through a change set with `--role-arn` of the service role. Same guard as the workflow: abort if `AppInstance` would be removed or replaced. New stack: latest AL2023 AMI; existing stack: the running instance's AMI. A stack left in `ROLLBACK_COMPLETE`/`REVIEW_IN_PROGRESS` by a failed first create is deleted and recreated automatically (local runs only).
6. Image: `git archive <sha> | docker build --platform linux/amd64 -` (exactly the commit: no uncommitted or git-ignored files), pushed as `<sha7>` and `latest`. Skipped if `<sha7>` is already in ECR.
7. `remote-deploy.sh` through SSM Run Command with `APP_COMMIT=<full sha>`, so `X-Build` names the commit.
8. Smoke: waits until `/health` is 200 with `X-Build` equal to the sha, then `/` 200, `/api/repos` 200, `/api/auth/me` 401 (all with the same `X-Build`), prints the URL.

Options: `--ref <commit>` deploys another commit (default `HEAD`), `--profile`, `--rotate-jwt`, `--ci` (used by GitHub Actions: skips preflight and bootstrap, Free plan check informational). A lock in `$TMPDIR` stops two runs overlapping.

Automating updates: any run of `deploy.sh --apply --account 228619515667` is the update. Options: a local git hook (for example `.git/hooks/post-merge` that runs it when `main` is pulled), a scheduled run (cron or launchd), or the `deploy-aws-keys` GitHub job below. Local hooks and schedules need a live `aws login` session; there are no stored keys on the machine.

### GitHub Actions without OIDC: CI IAM user

User decision (2026-11-13): GitHub deploys with one dedicated IAM user, because OIDC is denied by the SCP.

- `deploy/aws/bootstrap.sh --no-oidc --ci-user --apply --confirm-account 228619515667 --region ap-southeast-2 --stack-name micro-git-prod` creates user `github-deploy-micro-git-prod-user` (tag `Project=micro-git`) with inline policy `policies/ci-user-policy.json`: change sets on `micro-git-prod` only and only with `cloudformation:RoleArn` = the service role, `iam:PassRole` only for `cfn-deploy-micro-git-prod`, ECR push to `micro-git-prod-app`, `PutParameter` on `/micro-git-prod/jwt-secret` (no read), `SendCommand` only to instances tagged with the stack, explicit denies for stack `mg`, its repo and instance, and for every region except `ap-southeast-2`. No other IAM rights.
- If the user has no key, one is created and its JSON is piped through `python3` straight into `gh secret set AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` (stdin). It is never printed, logged or written to a file. If GitHub is unreachable no key is created; if storing fails, the new key is deleted again. It also sets the variables `AWS_REGION`, `AWS_STACK_NAME`, `AWS_CFN_ROLE_ARN`, `AWS_ACCOUNT_ID` (not secrets).
- Rotation: same command plus `--rotate-ci-key`: new key into the secrets, then the old key is deleted, so there is one active key.
- Workflow: the gate sets `aws_keys=true` when the secret `AWS_ACCESS_KEY_ID` exists and `AWS_ROLE_ARN` does not; job `deploy-aws-keys` (after CI and E2E are green on `main`, workflow concurrency `deploy-main`) runs `deploy/aws/deploy.sh --apply --ci --ref <sha>`, which includes the change-set guard and the X-Build smoke.
- Teardown: `destroy.sh --apply` deletes the user, its keys and the two GitHub secrets.

## OIDC path (job `deploy-aws`; not usable in account 228619515667)

Used by `.github/workflows/deploy.yml`, job `deploy-aws`. The job runs only when the repository variable `AWS_ROLE_ARN` is set and CI and E2E are green for the commit on `main`.

## Status: what is verified and what is not

- Verified (static only): `cfn-lint` on `deploy/aws/main.yaml` is clean; `actionlint` on `.github/workflows/deploy.yml` is clean; `shellcheck` on `deploy/aws/remote-deploy.sh` is clean.
- NOT verified: nothing here has ever been run against a real AWS account (no credentials were available). The OIDC role, the stack create, the SSM Run Command step, the first boot and the health check are untested. Expect to fix small things on the first real run.

## How the deploy works (job `deploy-aws`)

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

Two roles keep privileges apart: a GitHub deploy role (small, assumed through OIDC) and a CloudFormation service role (holds the EC2/IAM/ECR rights and is only usable by CloudFormation, trust pinned with `aws:SourceAccount`). The role names are `cfn-deploy-<stack>` and `github-deploy-<stack>` (default stack `mg`). Neither is named `<stack>-*`, because the service role may manage only `<stack>-*` IAM roles, and the stack's own roles must not be able to touch the deployers.

The exact JSON is in `deploy/aws/policies/` (single source of truth; `bootstrap.sh` substitutes `<ACCOUNT_ID>`, `<STACK_NAME>` and `<REPO>` into a temp dir):

| File | What |
| --- | --- |
| `policies/mg-instance-boundary.json` | Customer-managed policy `mg-instance-boundary`: permissions boundary the template puts on the instance role (ECR pull, SSM agent, and `GetParameter` of `/<stack>/jwt-secret` only with credentials issued to an EC2 instance through the instance profile (`ec2:SourceInstanceARN`)). The name is fixed and account-wide because `main.yaml` references it, and its secret path is `/<stack>/jwt-secret` of the stack name used at bootstrap. A second stack name in the same account needs its own path added by hand (`aws iam create-policy-version --set-as-default` on this policy; the bootstrap script never changes an existing policy), otherwise its instance cannot read the secret. If it already exists the script leaves it unchanged. Never grant anyone `iam:CreatePolicyVersion` or `iam:SetDefaultPolicyVersion` on it. |
| `policies/cfn-deploy-trust.json`, `policies/cfn-deploy-policy.json` | CloudFormation service role. IAM rights only for `<stack>-*` roles carrying the boundary (create, inline policy, delete, tag, attach/detach of the two AWS managed policies the template uses); EC2 limited to `t2.micro`/`t3.micro` and, for existing resources, to resources tagged with this stack. |
| `policies/github-deploy-trust.json`, `policies/github-deploy-policy.json` | GitHub OIDC role: trust pinned to `repo:<REPO>:ref:refs/heads/main` with audience `sts.amazonaws.com`; policy is a starting point (tighten after the first successful run). |

Steps:

1. Log in with your own credentials: `aws login` (or `aws sso login --profile NAME`).
2. Check first (read-only, changes nothing): `deploy/aws/preflight.sh --region <region> --stack-name <stack>`. It verifies the identity, default VPC, `t3.micro`/`t2.micro` offering, whether a stack with that name exists (use a NEW stack name if the hand-made `mg` exists, see Gotchas), whether the roles and policies already exist, and which GitHub variables and secrets are set (names only).
3. Review the plan: `deploy/aws/bootstrap.sh --region <region> --stack-name <stack>` (dry-run is the default; it prints the aws commands and runs no mutating call).
4. Apply: `deploy/aws/bootstrap.sh --apply --region <region> --stack-name <stack>` (add `--profile NAME` if needed). It prints the account id and asks you to type it. It creates, in order and idempotently: the GitHub OIDC provider (if missing), `mg-instance-boundary` (if missing), `cfn-deploy-<stack>` with its policy, `github-deploy-<stack>` with its policy (existing roles get their trust and policy refreshed).
5. Run the `gh` commands the script prints at the end (it does not run them):

        gh variable set AWS_ROLE_ARN --repo L0nelyG0d/micro-git --body <github-deploy ARN>
        gh variable set AWS_CFN_ROLE_ARN --repo L0nelyG0d/micro-git --body <cfn-deploy ARN>
        openssl rand -hex 32 | gh secret set AWS_JWT_SECRET --repo L0nelyG0d/micro-git
        # only if not the defaults:
        gh variable set AWS_STACK_NAME --repo L0nelyG0d/micro-git --body <stack>
        gh variable set AWS_REGION --repo L0nelyG0d/micro-git --body <region>

   Use a NEW random `AWS_JWT_SECRET`; do not reuse any value that was ever in the old template parameters, user data, chat or logs.
6. Push to `main` (or re-run the Deploy workflow after CI and E2E pass). Watch the `deploy-aws` job; the first run takes several minutes (instance boot, image pull). If it fails, read the job log (the SSM step prints the instance command output); re-running is safe after a failure of an ordinary update. After a FAILED FIRST CREATE (stack in `ROLLBACK_COMPLETE` or `ROLLBACK_FAILED`) an admin must first run `aws cloudformation delete-stack --stack-name <stack>` and wait for `DELETE_COMPLETE`; the job cannot update a stack in that state. `AppInstance` uses `DeletionPolicy: RetainExceptOnCreate`, so a failed first create deletes the half-built instance, while a later `delete-stack` still retains it.

Manual equivalent: the same IAM objects can be created in the console from the JSON files (replace the placeholders by hand). The account also needs a default VPC in the region and EC2 capacity for a `t3.micro`.

`DomainName` is not passed by CI, and `aws cloudformation deploy` keeps the previous value of parameters it is not given, so a domain set by a manual deploy is not dropped by later CI runs. The AMI is passed on every run (see step 4 above).

## Expected cost

Within the free tier (legacy accounts, first 12 months): about 0 USD/month for one `t3.micro`, 20 GB gp3 and a few ECR images. Outside it, rough order of magnitude: t3.micro about 8-10 USD/month, 20 GB gp3 about 2 USD, public IPv4 about 3.6 USD, ECR storage cents, data transfer out beyond 100 GB/month. No load balancer, NAT gateway, RDS or Elastic IP is created; SSM Parameter Store standard parameters and Run Command are free. Verify in Billing; these are estimates, not measured.

## Manual run

    export AWS_REGION=<region>
    AMI=$(aws ssm get-parameter --name /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64 --query Parameter.Value --output text)   # new stack only
    printf '%s' "$JWT_SECRET" > /tmp/jwt && aws ssm put-parameter --name /mg/jwt-secret --type SecureString --overwrite --value file:///tmp/jwt; rm /tmp/jwt
    aws cloudformation deploy --template-file deploy/aws/main.yaml \
      --stack-name mg --capabilities CAPABILITY_IAM --no-fail-on-empty-changeset \
      --role-arn <AWS_CFN_ROLE_ARN> --parameter-overrides LatestAmiId=$AMI [DomainName=app.example.com] [KeyName=mykey]

Then push the image and run `remote-deploy.sh` through SSM as the workflow does (see the `deploy-aws` job). Other parameters: `InstanceType`, `AdminCidr`, `VolumeSizeGb`. For an existing stack pass the running instance's current AMI id, never a newer one.

## What this creates and what is free

| Resource | Free tier |
| --- | --- |
| EC2 `t3.micro` (or `t2.micro`), Amazon Linux 2023, 20 GB gp3 root volume | Free-tier eligible: 750 h/month and 30 GB EBS for 12 months on accounts on the legacy free tier. Check the plan in Billing; newer plans use credits. |
| ECR private repository (`<stack>-app`), scan on push, keeps the last 5 images | 500 MB/month of storage free for 12 months (legacy plan) |
| Public IPv4 address on the instance | Charged per hour since Feb 2024; the free allowance is the same 750 h/month on the legacy plan. No Elastic IP is created. |
| Security group (80, 443 public; 22 from `AdminCidr`) | Free |
| IAM role and instance profile (ECR read, SSM Session Manager) | Free |
| Docker on the instance: Postgres 16, the app image, Caddy (TLS) | Runs on the instance |

Not used on purpose: App Runner (not free), RDS (`db.t3.micro` is free only for 12 months, and needs a second instance plus subnet group), NAT gateway, load balancer, Elastic IP.

## Gotchas

- **Stop/start changes the public address** because there is no Elastic IP. DNS for `DomainName` must be updated after a restart.
- **Secrets**: the JWT secret is in SSM Parameter Store (SecureString, default `aws/ssm` key) and in `/opt/mg/app.env` (mode 600) on the instance; it is not in user data. The generated DB password is in `/opt/mg/*.env` and is generated in user data on the instance, never in the template. Anyone with `ssm:GetParameter` (with decryption) on `/<stack>/jwt-secret` or shell access to the instance can read the JWT secret.
- **Rotating the JWT secret**: update the GitHub secret and re-run the deploy; all sessions are invalidated.
- **Existing hand-made stacks must be recreated under a NEW stack name.** First-boot user data runs once, so editing `UserData` on an existing stack only stops and starts the instance and does not re-run it; an old instance would lack the new layout, and the removed `JwtSecret`/`ImageTag` parameters change the stack. Deploy to a new name (set `AWS_STACK_NAME`), migrate data if needed, then delete the old stack.
- **AMI upgrades are a deliberate manual action.** CI never changes the AMI of a running stack, and the change-set guard fails the job if `AppInstance` would be replaced or removed. To move to a newer AMI: back up Postgres, then deploy manually with a new `LatestAmiId` knowing the instance is replaced and the data is lost unless restored.
- **The image serves the SPA and the API** (root `Dockerfile`). Caddy forwards everything to `mg-app:8080`.
- **Swap**: `t3.micro` has 1 GB RAM; user data adds a 1 GB swap file.
- **Lint**: `cfn-lint` ran clean through Docker (see Status above).

## What was actually deployed

- Stack `mg` in `ap-southeast-2`, created by hand with `aws cloudformation deploy` (not by CI). Public address: http://ec2-13-211-236-62.ap-southeast-2.compute.amazonaws.com
- The `deploy-aws` job in `.github/workflows/deploy.yml` runs only when the variable `AWS_ROLE_ARN` exists, and it did not when this was written. It defaults `AWS_REGION` to `us-east-1`, not `ap-southeast-2`, unless the variable is set.

## Delete the stack / avoid charges

    aws cloudformation delete-stack --stack-name mg --region <region>
    aws cloudformation wait stack-delete-complete --stack-name mg --region <region>

Use the region the stack lives in (`ap-southeast-2` for the hand-made deployment). The ECR repository does not need to be emptied first: `deploy/aws/main.yaml` sets `EmptyOnDelete: true` on `AppRepository`, so CloudFormation deletes the images with it. `AppInstance` has `DeletionPolicy: RetainExceptOnCreate` (retained on stack delete, deleted only when the first create fails) and `UpdateReplacePolicy: Retain`, so deleting the stack leaves the instance and its root volume behind. **Back up Postgres BEFORE `delete-stack`**: deleting the stack tries to remove the instance role and profile of the retained instance (if that succeeds SSM goes offline; if the instance still holds them they end `DELETE_FAILED`), and SSH is closed by default (`AdminCidr`). The security group ends in `DELETE_FAILED` (the instance still uses it) and, because the retained instance still uses the instance profile, the profile and role can also end in `DELETE_FAILED` (delete them by hand once the instance is terminated). To recover access afterwards, re-attach an instance profile or open SSH through the security group (`AdminCidr`), take the backup, then terminate the instance by hand and delete the leftover security group. The repository is deleted with the stack. Postgres data is a bind mount (`/opt/mg/pgdata`) on the instance's root EBS volume. There are no backups, and the data is lost when you terminate the instance; take a backup first if you need it. Check the CloudFormation console that the stack reached `DELETE_COMPLETE`, and check EC2 and ECR for leftovers.

What can cost money once the free tier is over (free tier ended, or you are past the limits):

- EC2 hours beyond 750 per month, or any hours after the first 12 months on the legacy free tier.
- The public IPv4 address, charged per hour (free allowance is the same 750 h/month on the legacy plan only).
- EBS storage over 30 GB (the template defaults to 20 GB gp3).
- ECR storage over the free quota (the lifecycle policy keeps the last 5 images).
- Data transfer out of AWS.

Newer AWS accounts may be on a credit-based plan instead of the legacy free tier; check Billing. Stopping the instance stops EC2 hours but not EBS, and the public IP changes on restart (see Gotchas). This section is from reading the template; I did not run the delete.

## Known gaps

Deferred review findings (no backups, hardening, and other follow-ups) are tracked in issue #166. Not done in this branch: Postgres backups, a separate data volume, and anything that was never run against a real account.

Also deferred to #166:

- Stale DNS after a stop/start when `DomainName` is used (no Elastic IP).
- `aws:SourceArn` on the CloudFormation service role trust policy (`aws:SourceAccount` is now set).
- Scoping the IAM roles by `Path`.
- Restricting the OIDC trust further (GitHub environment or `job_workflow_ref` condition).
- Remaining EC2 rights are account-wide where AWS does not support tag conditions (for example `ec2:CreateSecurityGroup`, `ec2:Describe*`, `ec2:RunInstances` on non-instance resources). The account must not host other, more privileged instances or roles that the service role could pass or affect.
- The `CreateRole` trust policy of stack roles is unconstrained (the boundary caps permissions, not who can assume). The boundary limits the secret read to credentials issued to an EC2 instance through the instance profile (`ec2:SourceInstanceARN`) and to `parameter/mg/jwt-secret`.
- An old unbounded `mg-InstanceRole-*` from the hand-made stack can still be passed to EC2 by the service role. Delete the hand-made stack, use a NEW stack name, and keep no other `mg-*` roles in the account.
- Unverified on a real account, check on the first run: `kms:Decrypt` for the `aws/ssm` key under the boundary, the `iam:PassRole` context for `AddRoleToInstanceProfile`, and an orphaned instance after a failed first stack create (it is `Retain`ed and must be terminated by hand).
