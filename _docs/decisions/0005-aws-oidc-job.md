# ADR 0005: AWS OIDC deploy job

Status: Accepted (2026-11-14), changed by #201 (2026-11-15): option B

Issue: #197

## Context

`.github/workflows/deploy.yml` has two AWS jobs. `deploy-aws-keys` is the production path. `deploy-aws` (about 210 lines) assumes a role through GitHub OIDC; it is dormant because an organization SCP in the account denies creating the OIDC provider, and `AWS_ROLE_ARN` and `AWS_JWT_SECRET` are not set. #174 kept it, marked it optional and changed its defaults to `micro-git-prod` / `ap-southeast-2`. `deploy/aws/policies/github-deploy-trust.json` and `github-deploy-policy.json` are used only by the OIDC role (`bootstrap.sh` without `--no-oidc`), and the OIDC sections of `deploy/aws/README.md` serve only this job.

## Options

- A: keep the job as optional. Cost: dead code to keep in sync; risk low.
- B: drop the job, the two policies and the README sections; keep OIDC support in `bootstrap.sh`. Cost small.
- C: drop OIDC everywhere, including `bootstrap.sh` and `preflight.sh`. Cost medium; loses the path for an account without the SCP.

## Recommendation

A until the user confirms the account stays under the SCP, then B.

## Decision

First accepted as option A (2026-11-14). Changed by #201 (2026-11-15): Applied in #201: the OIDC job `deploy-aws` was removed from `deploy.yml` together with the automatic AWS deploy (option B); OIDC support stays in `bootstrap.sh`. Production deploys through job `deploy` of `promote.yml`.

## Consequences

The two `github-deploy-*.json` policies and the OIDC sections of `deploy/aws/README.md` are reviewed in #201 (kept only if `bootstrap.sh` still needs them). Related: #181.

## Preconditions (for B)

`actionlint` clean after the removal; Deploy green on the #201 branch run.
