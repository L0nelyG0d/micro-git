# Deployment (CD)

Continuous delivery is one workflow: `.github/workflows/deploy.yml`. Per-host notes live next to it: `deploy/render/README.md`, `deploy/aws/README.md`, `deploy/gcp/README.md`, `deploy/vercel/README.md`.

## How CD works

1. The Deploy workflow triggers on `workflow_run` (type `completed`) of the `CI` and `E2E` workflows, for branch `main` only.
2. The `gate` job runs only if the finishing run succeeded and was a `push` to `main`. It then looks up the latest completed `ci.yml` and `e2e.yml` runs for the same commit SHA (`github.event.workflow_run.head_sha`) and sets `ready=true` only if both are `success`. Because the gate fires once for CI and once for E2E, only the later finisher sees both green, so one deploy starts per commit.
3. The gate also writes a true/false flag per target (render, railway, aws, gcp, vercel) from whether the credential below is non-empty. Values are never printed.
4. Each deploy job needs `gate` with `ready == 'true'` and its own flag `true`; otherwise the job is skipped.
5. `deploy-render` does `curl -fsS -X POST "$RENDER_DEPLOY_HOOK"`. The hook asks Render to build and deploy the latest commit of the branch the service tracks.
6. Runs are serialized by `concurrency: deploy-main` (`cancel-in-progress: false`).

## Secrets and variables

| Job | Kind | Name | Notes |
| --- | --- | --- | --- |
| deploy-render | secret | `RENDER_DEPLOY_HOOK` | The full hook URL (it is a secret) |
| deploy-railway | secret | `RAILWAY_TOKEN` | Project token for `railway up` |
| deploy-aws | variable | `AWS_ROLE_ARN` | Gate for the job; role assumed through GitHub OIDC |
| deploy-aws | variable | `AWS_REGION` | Optional, workflow default `us-east-1` |
| deploy-aws | variable | `AWS_STACK_NAME` | Optional, workflow default `mg` |
| deploy-gcp | variable | `GCP_WORKLOAD_IDENTITY` | Gate for the job; Workload Identity Provider resource name |
| deploy-gcp | variable | `GCP_SERVICE_ACCOUNT` | Service account email to impersonate |
| deploy-vercel-frontend | secret | `VERCEL_TOKEN` | Gate for the job |
| deploy-vercel-frontend | variable | `VERCEL_ORG_ID`, `VERCEL_PROJECT_ID` | Required by the job |

Never put these values in the repository or in an issue.

## Which jobs run today

Render is the only target set up for CD (the Render deploy hook). It is the only job intended to run on each green `main` commit. I did not check the repository's secret and variable settings, so confirm in Settings > Secrets and variables > Actions. Per the repository's own docs (`deploy/aws/README.md`, `deploy/render/LIVE.md`):

- Render: live at https://micro-git.onrender.com, deployed through `RENDER_DEPLOY_HOOK`.
- AWS: deployed by hand (stack `mg`, region `ap-southeast-2`, http://ec2-13-211-236-62.ap-southeast-2.compute.amazonaws.com). `AWS_ROLE_ARN` is not set, so `deploy-aws` is skipped.
- GCP, Railway, Vercel: `GCP_WORKLOAD_IDENTITY`, `RAILWAY_TOKEN` and `VERCEL_TOKEN` are unset (per the task QA notes), so those jobs are skipped.

## Which commit is live

- Render: the service page in the Render dashboard shows the deployed commit SHA in the latest deploy.
- GitHub: the Deploy run (Actions > Deploy) shows `headSha`; `gh run list --workflow deploy.yml --json headSha,conclusion,createdAt` lists them. A successful `deploy-render` job only means the hook call was accepted; the build itself continues on Render, so confirm the SHA in the dashboard.

## What happens on failure

- If the hook call fails (bad URL, non-2xx), `curl -fsS` exits non-zero and the `deploy-render` job and Deploy run fail.
- If the Render build fails after the hook was accepted, per Render documentation the previous successful deploy keeps serving. I did not test this.
- If CI or E2E is red for the commit, the gate sets `ready=false` and no deploy job runs.

## Render autoDeploy

`deploy/render.yaml` sets `autoDeploy: true`, which makes Render deploy every push to the tracked branch by itself, possibly before CI and E2E are green. This bypasses the gate. To make the deploy hook the only trigger, set `autoDeploy: false` in `deploy/render.yaml` (and sync the Blueprint), or switch Auto-Deploy to "Off" in the service settings in the Render dashboard. This document does not change that file.

## Known limitation

The `deploy-aws` job does not pass the `JwtSecret` parameter that `deploy/aws/main.yaml` requires, so it would fail if enabled as is. See `deploy/aws/README.md`.
