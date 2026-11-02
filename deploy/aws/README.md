# AWS deploy (CloudFormation)

Used by `.github/workflows/deploy.yml`, job `deploy-aws`. The job runs only when the repository variable `AWS_ROLE_ARN` is set and CI and E2E are green for the commit on `main`.

Template: `deploy/aws/main.yaml` (written separately; the workflow expects this exact path).

## Required GitHub setup

| Kind | Name | Purpose |
| --- | --- | --- |
| Variable | `AWS_ROLE_ARN` | IAM role ARN that GitHub OIDC assumes (gate for the job) |
| Variable | `AWS_REGION` | Optional, default `us-east-1` |
| Variable | `AWS_STACK_NAME` | Optional, default `mg` |

## IAM role (OIDC trust)

- Identity provider: `token.actions.githubusercontent.com`, audience `sts.amazonaws.com`.
- Trust condition: restrict `sub` to this repository and `refs/heads/main`.
- Permissions: only what the template creates (CloudFormation, plus the services it manages).

No long-lived AWS access keys are stored in GitHub.

## Manual run

    aws cloudformation deploy --template-file deploy/aws/main.yaml \
      --stack-name mg --capabilities CAPABILITY_IAM --no-fail-on-empty-changeset

Template parameters, when the template takes them, are passed with `--parameter-overrides`; add them to the workflow once `main.yaml` defines them.

`main.yaml` now defines them. The `deploy-aws` job (and the manual command) must pass **`JwtSecret`**, which has no default and must be 32+ characters. Without it the stack create fails. Example:

    aws cloudformation deploy --template-file deploy/aws/main.yaml \
      --stack-name mg --capabilities CAPABILITY_IAM --no-fail-on-empty-changeset \
      --parameter-overrides JwtSecret="$JWT_SECRET" [DomainName=app.example.com] [KeyName=mykey]

Read `main.yaml` for the other parameters (`InstanceType`, `AdminCidr`, `ImageTag`, `VolumeSizeGb`). The template uses the account's default VPC, so no network parameters are needed.

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
- **Secrets in user data**: `JwtSecret` and the generated DB password are written to `/opt/mg/*.env` (mode 600) and `JwtSecret` also appears in the instance user data, which is readable with `ec2:DescribeInstanceAttribute`. Move it to SSM Parameter Store SecureString if that is a concern.
- **Schema is applied by hand** once (`backend/db/schema.sql`, see the template header). The app does not migrate Postgres itself.
- **The image serves the SPA and the API** (root `Dockerfile`). Caddy forwards everything to `mg-app:8080`.
- **CI (`.github/workflows/deploy.yml`, job `deploy-aws`) does not pass `JwtSecret`**, so the stack create fails until the job adds `--parameter-overrides JwtSecret=...` from a secret. The job also deploys with no image pushed, so the app container stays down until the first `docker run` on the instance (see the template header).
- **Swap**: `t3.micro` has 1 GB RAM; user data adds a 1 GB swap file.
- **`cfn-lint` was not run**: it is not installed. The template was checked with a YAML parse and by hand (intrinsic tags, Ref and GetAtt targets).

## What was actually deployed

- Stack `mg` in `ap-southeast-2`, created by hand with `aws cloudformation deploy` (not by CI). Public address: http://ec2-13-211-236-62.ap-southeast-2.compute.amazonaws.com
- The `deploy-aws` job in `.github/workflows/deploy.yml` is unused: it runs only when the variable `AWS_ROLE_ARN` exists, and it does not.
- Known limitation: that job does not pass the `JwtSecret` parameter, so it would fail at stack create/update if enabled as is. It also defaults `AWS_REGION` to `us-east-1`, not `ap-southeast-2`, unless the variable is set.

## Delete the stack / avoid charges

    aws cloudformation delete-stack --stack-name mg --region <region>
    aws cloudformation wait stack-delete-complete --stack-name mg --region <region>

Use the region the stack lives in (`ap-southeast-2` for the hand-made deployment). The ECR repository does not need to be emptied first: `deploy/aws/main.yaml` sets `EmptyOnDelete: true` on `AppRepository`, so CloudFormation deletes the images with it. Deleting the stack removes the instance, its root volume, the security group, the IAM role and the repository. The Postgres data lives in a Docker volume on the instance and is lost with it; take a backup first if you need it. Check the CloudFormation console that the stack reached `DELETE_COMPLETE`, and check EC2 and ECR for leftovers.

What can cost money once the free tier is over (free tier ended, or you are past the limits):

- EC2 hours beyond 750 per month, or any hours after the first 12 months on the legacy free tier.
- The public IPv4 address, charged per hour (free allowance is the same 750 h/month on the legacy plan only).
- EBS storage over 30 GB (the template defaults to 20 GB gp3).
- ECR storage over the free quota (the lifecycle policy keeps the last 5 images).
- Data transfer out of AWS.

Newer AWS accounts may be on a credit-based plan instead of the legacy free tier; check Billing. Stopping the instance stops EC2 hours but not EBS, and the public IP changes on restart (see Gotchas). This section is from reading the template; I did not run the delete.
