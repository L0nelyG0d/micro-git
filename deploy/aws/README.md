# AWS deploy (CloudFormation)

Used by `.github/workflows/deploy.yml`, job `deploy-aws`. The job runs only when the repository variable `AWS_ROLE_ARN` is set and CI and E2E are green for the commit on `main`.

Template: `deploy/aws/main.yaml`. Instance-side script: `deploy/aws/remote-deploy.sh`.

## Status: what is verified and what is not

- Verified (static only): `cfn-lint` on `deploy/aws/main.yaml` is clean; `actionlint` on `.github/workflows/deploy.yml` is clean; `shellcheck` on `deploy/aws/remote-deploy.sh` is clean.
- NOT verified: nothing here has ever been run against a real AWS account (no credentials were available). The OIDC role, the stack create, the SSM Run Command step, the first boot and the health check are untested. Expect to fix small things on the first real run.

## How the deploy works (job `deploy-aws`)

1. Fails early if secret `AWS_JWT_SECRET` is missing or shorter than 32 characters (the value is never printed).
2. Assumes the role in `AWS_ROLE_ARN` through GitHub OIDC (no stored AWS keys). Fails early if `AWS_CFN_ROLE_ARN` is not set.
3. Writes the secret to SSM Parameter Store as SecureString `/<stack>/jwt-secret`. It is not in the template, the stack parameters or EC2 user data (this fixes the earlier exposure through `ec2:DescribeInstanceAttribute`). This parameter lives outside the stack and is NOT deleted with it.
4. Picks the AMI: for an existing stack it reuses the running instance's `ImageId` (read with `describe-instances`); only for a new stack it resolves the latest Amazon Linux 2023 AMI from the public SSM parameter. `LatestAmiId` is a plain `AWS::EC2::Image::Id` parameter, not an SSM-resolved one, because those are re-resolved on every update and a new AMI release would replace the instance and wipe Postgres.
5. Guard: `aws cloudformation deploy --no-execute-changeset --role-arn <AWS_CFN_ROLE_ARN>`, then the job fails if the change set removes or replaces (`Replacement` True or Conditional) `AppInstance`. Only a safe change set is executed (a second `deploy` with the same inputs). The stack has no `ImageTag` parameter: image rollout is not a stack change.
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

## One-time bootstrap checklist (you, in the AWS account)

Two roles keep privileges apart: a GitHub deploy role (small, assumed through OIDC) and a CloudFormation service role (holds the EC2/IAM/ECR rights and is only usable by CloudFormation). Neither is named `mg-*`, because the policies below let the service role manage only `mg-*` IAM roles and the stack's own roles must not be able to touch the deployers. Replace `<account-id>`; examples assume stack `mg` (adjust the names and ARNs if you change `AWS_STACK_NAME`).

1. Pick the region (match `AWS_REGION`) and the stack name. Use a NEW stack name if you already have a hand-made stack (see Gotchas).
2. IAM > Identity providers > Add provider: OpenID Connect, URL `https://token.actions.githubusercontent.com`, audience `sts.amazonaws.com`. (Skip if it already exists.)
3. Create the CloudFormation service role `cfn-deploy-mg`. Trust policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Service": "cloudformation.amazonaws.com"},
    "Action": "sts:AssumeRole"
  }]
}
```

   Permissions policy (what the template creates: EC2 instance and security group, ECR repository, IAM role and instance profile; no `*` on IAM):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {"Effect": "Allow",
     "Action": ["ec2:RunInstances", "ec2:TerminateInstances", "ec2:StartInstances", "ec2:StopInstances",
                "ec2:ModifyInstanceAttribute", "ec2:CreateTags", "ec2:DeleteTags",
                "ec2:CreateSecurityGroup", "ec2:DeleteSecurityGroup",
                "ec2:AuthorizeSecurityGroupIngress", "ec2:RevokeSecurityGroupIngress",
                "ec2:AuthorizeSecurityGroupEgress", "ec2:RevokeSecurityGroupEgress",
                "ec2:Describe*"],
     "Resource": "*"},
    {"Effect": "Allow",
     "Action": ["ecr:CreateRepository", "ecr:DeleteRepository", "ecr:DescribeRepositories",
                "ecr:PutLifecyclePolicy", "ecr:GetLifecyclePolicy", "ecr:DeleteLifecyclePolicy",
                "ecr:PutImageScanningConfiguration", "ecr:PutImageTagMutability",
                "ecr:ListTagsForResource", "ecr:TagResource", "ecr:ListImages", "ecr:BatchDeleteImage"],
     "Resource": "arn:aws:ecr:*:<account-id>:repository/mg-*"},
    {"Effect": "Allow",
     "Action": ["iam:CreateRole", "iam:DeleteRole", "iam:GetRole", "iam:TagRole",
                "iam:AttachRolePolicy", "iam:DetachRolePolicy", "iam:PutRolePolicy",
                "iam:DeleteRolePolicy", "iam:GetRolePolicy",
                "iam:CreateInstanceProfile", "iam:DeleteInstanceProfile", "iam:GetInstanceProfile",
                "iam:AddRoleToInstanceProfile", "iam:RemoveRoleFromInstanceProfile"],
     "Resource": ["arn:aws:iam::<account-id>:role/mg-*", "arn:aws:iam::<account-id>:instance-profile/mg-*"]},
    {"Effect": "Allow", "Action": "iam:PassRole",
     "Resource": "arn:aws:iam::<account-id>:role/mg-*",
     "Condition": {"StringEquals": {"iam:PassedToService": "ec2.amazonaws.com"}}}
  ]
}
```

4. Create the GitHub deploy role `github-deploy-mg`. Trust policy (restricted to this repository and `main`):

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Federated": "arn:aws:iam::<account-id>:oidc-provider/token.actions.githubusercontent.com"},
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "token.actions.githubusercontent.com:aud": "sts.amazonaws.com",
        "token.actions.githubusercontent.com:sub": "repo:L0nelyG0d/micro-git:ref:refs/heads/main"
      }
    }
  }]
}
```

   Permissions policy (a starting point; tighten after the first successful run):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {"Effect": "Allow",
     "Action": ["cloudformation:CreateChangeSet", "cloudformation:DescribeChangeSet",
                "cloudformation:ExecuteChangeSet", "cloudformation:DeleteChangeSet",
                "cloudformation:ListChangeSets", "cloudformation:DescribeStacks",
                "cloudformation:DescribeStackEvents", "cloudformation:DescribeStackResource",
                "cloudformation:GetTemplate"],
     "Resource": "arn:aws:cloudformation:*:<account-id>:stack/mg/*"},
    {"Effect": "Allow", "Action": "cloudformation:GetTemplateSummary", "Resource": "*"},
    {"Effect": "Allow", "Action": "iam:PassRole",
     "Resource": "arn:aws:iam::<account-id>:role/cfn-deploy-mg",
     "Condition": {"StringEquals": {"iam:PassedToService": "cloudformation.amazonaws.com"}}},
    {"Effect": "Allow", "Action": "ecr:GetAuthorizationToken", "Resource": "*"},
    {"Effect": "Allow",
     "Action": ["ecr:BatchCheckLayerAvailability", "ecr:InitiateLayerUpload", "ecr:UploadLayerPart",
                "ecr:CompleteLayerUpload", "ecr:PutImage", "ecr:BatchGetImage"],
     "Resource": "arn:aws:ecr:*:<account-id>:repository/mg-app"},
    {"Effect": "Allow", "Action": "ssm:PutParameter",
     "Resource": "arn:aws:ssm:*:<account-id>:parameter/mg/jwt-secret"},
    {"Effect": "Allow", "Action": "ssm:GetParameter",
     "Resource": "arn:aws:ssm:*::parameter/aws/service/ami-amazon-linux-latest/*"},
    {"Effect": "Allow", "Action": "ec2:DescribeInstances", "Resource": "*"},
    {"Effect": "Allow", "Action": "ssm:SendCommand",
     "Resource": "arn:aws:ssm:*::document/AWS-RunShellScript"},
    {"Effect": "Allow", "Action": "ssm:SendCommand",
     "Resource": "arn:aws:ec2:*:<account-id>:instance/*",
     "Condition": {"StringEquals": {"ssm:resourceTag/aws:cloudformation:stack-name": "mg"}}},
    {"Effect": "Allow",
     "Action": ["ssm:GetCommandInvocation", "ssm:ListCommandInvocations", "ssm:DescribeInstanceInformation"],
     "Resource": "*"}
  ]
}
```

5. GitHub > repository Settings > Secrets and variables > Actions: add variables `AWS_ROLE_ARN` (`github-deploy-mg` ARN) and `AWS_CFN_ROLE_ARN` (`cfn-deploy-mg` ARN), secret `AWS_JWT_SECRET`, and optionally variables `AWS_REGION`, `AWS_STACK_NAME`. Generate a NEW `AWS_JWT_SECRET` with `openssl rand -hex 32`; do not reuse any value that was ever in the old template parameters, user data, chat or logs.
6. Make sure the account has a default VPC in that region (the template relies on it) and has EC2 capacity/quotas for a `t3.micro`.
7. Push to `main` (or re-run the Deploy workflow after CI and E2E pass). Watch the `deploy-aws` job; the first run takes several minutes (instance boot, image pull).
8. If it fails, read the job log (the SSM step prints the instance command output) and fix; the job is safe to re-run.

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

Use the region the stack lives in (`ap-southeast-2` for the hand-made deployment). The ECR repository does not need to be emptied first: `deploy/aws/main.yaml` sets `EmptyOnDelete: true` on `AppRepository`, so CloudFormation deletes the images with it. Deleting the stack removes the instance, its root volume, the security group, the IAM role and the repository. Postgres data is a bind mount (`/opt/mg/pgdata`) on the instance's root EBS volume. There are no backups, and the data is lost when the instance is terminated or replaced (including stack deletion); take a backup first if you need it. Check the CloudFormation console that the stack reached `DELETE_COMPLETE`, and check EC2 and ECR for leftovers.

What can cost money once the free tier is over (free tier ended, or you are past the limits):

- EC2 hours beyond 750 per month, or any hours after the first 12 months on the legacy free tier.
- The public IPv4 address, charged per hour (free allowance is the same 750 h/month on the legacy plan only).
- EBS storage over 30 GB (the template defaults to 20 GB gp3).
- ECR storage over the free quota (the lifecycle policy keeps the last 5 images).
- Data transfer out of AWS.

Newer AWS accounts may be on a credit-based plan instead of the legacy free tier; check Billing. Stopping the instance stops EC2 hours but not EBS, and the public IP changes on restart (see Gotchas). This section is from reading the template; I did not run the delete.

## Known gaps

Deferred review findings (no backups, hardening, and other follow-ups) are tracked in issue #166. Not done in this branch: Postgres backups, a separate data volume, and anything that was never run against a real account.
