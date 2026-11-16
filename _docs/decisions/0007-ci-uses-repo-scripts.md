# ADR 0007: CI uses the repository scripts

Status: Accepted (2026-11-14): option C. Part 1 applied in `issue-198-ci-repo-scripts` (merged 605dd36); part 2 applied on branch `issue-203-198-responder-ci` (merge pending)

Issue: #198

## Context

- The CI `backend-store` job runs `go test` directly, so the `backend/known_skips.txt` check in `scripts/test-matrix.sh` never runs in CI.
- `scripts/test-pgurl.sh`, `incident-response/test-policy.sh`, `test-redact.sh`, `test-incident.sh --dry-run` and `security-audit/validate-findings.py` run only by hand.
- `e2e.yml` declares a Postgres service, an "Apply schema" step, a `DATABASE_URL` export and a `docker build -t mg:ci` step that the suite does not use: `docker compose up` starts its own `db` and builds `mg-app:local`.
- The secret-scan pattern is duplicated in `ci.yml` and `security-audit/run-audit.sh` and has drifted (CI also skips lines with `${` and `%s@`).
- Action pinning is tracked in #183.

## Options

- A: do nothing.
- B: CI calls the scripts (`test-matrix.sh` per store) and a new no-stack `ops-tests` job runs the incident-response and security-audit checks. Cost small to medium; risk: a new gate that can go red.
- C: B plus removing the unused `e2e.yml` steps. Saves CI time; risk: E2E breaks if a step was needed after all.

## Recommendation

C in one DevOps change, tried on a branch run first.

## Decision

Option C, applied in branch `issue-198-ci-repo-scripts`: `scripts/test-pgurl.sh` step in `backend`, no-stack `ops-tests` job, unused `e2e.yml` steps (Postgres service, `DATABASE_URL`, Apply schema, `docker build -t mg:ci`, `MG_IMAGE`) removed.

Part 2 (branch `issue-203-198-responder-ci`):

- `backend-store` calls `scripts/test-matrix.sh <store>` (sqlite, memory). `backend/known_skips.txt` gained the 8 Postgres-only skips per store that the matrix reported (`TestApplySchema*`, `TestMigrate*`).
- `ops-tests` runs `incident-response/test-incident.sh --dry-run`. The `stat` order is now `stat -c %a ... || stat -f %Lp` (GNU first) in every script.
- The secret-scan pattern is one file, `security-audit/secret-patterns.txt`, read by the CI `secret-scan` job and by `security-audit/run-audit.sh`. The audit now applies the CI allow-list too (`USER:PASSWORD@HOST`, `${`, `%s@`), so it reports fewer placeholder lines. The scan still excludes `.github/workflows/ci.yml`.

The Postgres job keeps its own steps.

## Consequences

Local and CI runs use the same scripts; the skip accounting is enforced in CI.

## Preconditions

A green CI and E2E run on the branch before merge; Deploy green after it.
