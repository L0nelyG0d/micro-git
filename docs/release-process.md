# Release process

Source of truth for the workflow: [`_docs/process.md`](../_docs/process.md). Build and deploy details: [deployment.md](deployment.md) and [`deploy/README.md`](../deploy/README.md). Test details: [testing.md](testing.md).

## Merge flow

1. Work happens on an issue branch `issue-<N>-<slug>` cut from `main`.
2. QA verifies the branch against the acceptance criteria and posts PASS.
3. The code reviewer reviews the diff and posts APPROVE.
4. The reviewer merges with `git merge --no-ff` (message `Merge #<N>`), runs the suite once on `main`, pushes `main`, deletes the branch. If the merge conflicts or the suite goes red, the merge is undone and the issue returns to the engineer.
5. Push to `main` triggers CI and E2E. When both succeed for the commit, `Deploy` ships it to dev (Render; see the gate in [deployment.md](deployment.md#deploy-workflow-deployyml-dev)).
6. Production is a separate, manual step: someone runs `Promote to production` for the commit dev serves (or an older one) and a required reviewer approves it ([deployment.md](deployment.md#promotion-workflow-promoteyml-prod)).
7. QA confirms the merge on `main` and closes the issue.

Pull requests also run CI and E2E, but Deploy only reacts to pushes on `main`.

## Post-deploy smoke test

Render: the `smoke` job in `.github/workflows/deploy.yml` runs after `deploy-render`. It polls `$APP_URL/health` (default `https://micro-git.onrender.com`) until it answers 200 with `X-Build` equal to the deployed commit, then checks that `/` answers 200. It creates no users or repositories. A failing smoke job fails the Deploy run.

AWS: `deploy/aws/deploy.sh` (run by job `deploy` of `Promote to production`) ends with its own smoke: `/health` 200 with `X-Build` equal to the commit, `/` 200, `/api/repos` 200 and `/api/auth/me` 401.

## Staging vs production

There is no separate staging environment. Dev (Render) is the pre-production environment: every green `main` commit goes there, and production (AWS) receives only a commit that dev serves (or an older, still green one) after a manual promotion and approval. E2E also runs the built image on Postgres before any deploy.

Render does not deploy on its own: `deploy/render.yaml` sets `autoDeploy: false`, so the gated deploy hook is the only trigger. If the live service is not managed by the Blueprint, the dashboard setting must be off too (see [`deploy/README.md`](../deploy/README.md#render-autodeploy) and #177).

## Rollback

| Path | How |
| --- | --- |
| dev (Render) and prod (AWS) | [`_docs/deploy-runbook.md`](../_docs/deploy-runbook.md#rollback) |
| Compose: image tag rollback | `docker-compose.yml` uses `image: mg-app:local`, rebuilt on each `build`; no versioned tags are published. Before upgrading, `docker tag mg-app:local mg-app:prev`; to roll back, set `image: mg-app:prev` (remove `build`) and `docker compose up -d` |
| Git revert | `git revert <sha>` (or the merge: `git revert -m 1 <merge-sha>`) on a branch, merged through the normal flow; CI, E2E and Deploy then ship it |
| Database | No down-migrations. `schema.sql` is additive and idempotent and is not rolled back: an older app ignores the newer tables and columns. AWS Postgres: nightly `pg_dump` on the data disk (7 days), snapshot of the data disk on stack delete; data is not rolled back with the app (#182, [runbook](../_docs/deploy-runbook.md#data-volume-backups-and-rollback-182)) |

## Promotion: concurrency, rollback and schema (#204)

- **Concurrency.** `Promote to production` uses the concurrency group `promote-production` without cancel-in-progress. A run that waits for the approval of environment `production` holds the group, so other promotions wait behind it. A third dispatch while one run is running and one is queued cancels the queued run (GitHub keeps one pending run per group). Dispatch again if your run was cancelled.
- **Rollback to an older sha.** Dispatch the workflow with that sha (it must be on `main` with green CI and E2E). The `deploy` job runs the workflow's own commit, so the current `deploy/aws/` scripts and `main.yaml` are used; `deploy.sh --ref <sha>` builds the app image from the older sha with `git archive`. Infrastructure is not rolled back, and neither is the database.
- **Schema.** `deploy.sh` applies `backend/db/schema.sql` from the promoted sha. The schema is additive and idempotent, so a rollback leaves newer tables and columns in place; the older app ignores them. A schema change that drops or renames something is not allowed in this repository without a plan (see `_docs/deploy-runbook.md`).
- **Automatic container rollback.** If the new `mg-app` does not pass `/health` within 60 s, `remote-deploy.sh` starts the previous image on the instance again and the job fails (#182).

## Which commit is live

    curl -sI https://micro-git.onrender.com/health | grep -i '^x-build'
    gh run list --workflow deploy.yml --json headSha,conclusion,createdAt

For prod (AWS) use the stack output `AppUrl` (the current address is in [`_docs/deploy-runbook.md`](../_docs/deploy-runbook.md)).
