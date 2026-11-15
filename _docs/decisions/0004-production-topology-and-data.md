# ADR 0004: Production topology and data

Status: Accepted (2026-11-15): option B (user decision on #201: Render = dev, AWS = prod)

Issue: #196

## Context

The same commit is deployed to two targets. Render runs the image with a managed free Postgres `mg-db`; `deploy/render.yaml` notes that the free Postgres expires after 30 days. AWS runs stack `micro-git-prod` in `ap-southeast-2`: the app and a Postgres container on one EC2 instance, data on the root volume, no backups. Users and data on the two hosts are separate. AWS has one stack: the earlier hand-made stack `mg` was deleted by the owner on 2026-11-13/14, so that part of #178 is done.

## Options

- A: do nothing. Cost none; risk: Render data is lost when the free database expires, AWS data is lost with the instance.
- B: name one target primary and the other a demo; add `pg_dump` backups on AWS (#182). Cost small to medium.
- C: one shared database for both targets. Cost: cross-provider egress, latency, a paid database; risk: one outage takes down both.

## Recommendation

B. Hosting and data decisions belong to the user.

## Decision

Option B. Render is the dev environment and AWS (`micro-git-prod`) is production; each keeps its own database. Backups on AWS are tracked in #182. Applied in #201: `deploy.yml` deploys every green `main` commit to dev (Render) only; the manual workflow "Promote to production" (`promote.yml`) deploys to prod (AWS).

## Consequences

Dev = Render on every green push, prod = AWS by manual promotion (#201). Related: #178 (HTTPS, domain), #182.

## Preconditions

#182 backups in place and a restore tested; #201 merged before the docs describe promotion as existing (the #201 branch describes it; do not merge it before the owner has set the required reviewer on `production`).
