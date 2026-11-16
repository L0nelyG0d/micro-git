# ADR 0007: CI uses the repository scripts

Status: Accepted (2026-11-14): option C. Applied in branch `issue-198-ci-repo-scripts` except the `backend-store` switch (issue #198)

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

Not applied: `backend-store` still calls `go test` directly. `scripts/test-matrix.sh sqlite memory` runs offline in about 2 minutes but fails today because `backend/known_skips.txt` lacks 8 Postgres-only skips (`TestApplySchema*`, `TestMigrate*`) for sqlite and memory. An engineer runs `scripts/test-matrix.sh --update-skips`, reviews the diff, and the job then calls the script. `ops-tests` also omits `incident-response/test-incident.sh --dry-run`: on Linux `stat -f %Lp` succeeds with filesystem info, so the three mode checks (`make-token` 600 twice, incident dir 700) fail; swapping to `stat -c %a ... || stat -f %Lp` fixes it. The Postgres job keeps its own steps. The secret-scan drift is not addressed.

## Consequences

Local and CI runs use the same scripts; the skip accounting is enforced in CI.

## Preconditions

A green CI and E2E run on the branch before merge; Deploy green after it.
