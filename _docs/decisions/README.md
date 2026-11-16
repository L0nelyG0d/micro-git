# Architecture decisions (ADRs)

One file per decision: context, options with cost and risk, recommendation, decision, consequences, preconditions. Each ADR has its own GitHub issue.

An ADR is applied only after the user approves it on its issue; record the approval date in the Status line.

| Number | Title | Status | Decision | Issue | Applied in |
| --- | --- | --- | --- | --- | --- |
| [0001](0001-backend-package-split.md) | Backend package split | Accepted (2026-11-14) | A: no split now; layers documented in `_docs/team/architect.md`; B (`internal/store`) may be reopened in a feature freeze | #191 | nothing to apply |
| [0002](0002-go-module-path.md) | Go module path and go.mod location | Accepted (2026-11-14) | Keep `backend/go.mod` and the module path | #192 | nothing to apply |
| [0003](0003-ops-and-deploy-directory-moves.md) | Ops and deploy directory moves | Accepted (2026-11-14) | No moves: the three ops directories and `deploy/render.yaml` stay | #195 | nothing to apply |
| [0004](0004-production-topology-and-data.md) | Production topology and data | Accepted (2026-11-15) | B: Render = dev, AWS `micro-git-prod` = prod, separate databases; AWS backups in #182 | #196 | Applied in #201 (promotion workflow); #182 (backups) open |
| [0005](0005-aws-oidc-job.md) | AWS OIDC deploy job | Accepted (2026-11-14), changed by #201 (2026-11-15) | B: remove the `deploy-aws` job with the automatic AWS deploy; OIDC stays in `bootstrap.sh` | #197 | Applied in #201 |
| [0006](0006-frontend-helpers-and-e2e-journey.md) | Frontend class helper and the manual e2e journey | Accepted (2026-11-14) | C: ui files use `@/lib/utils`, drop the `cn` package; retire the manual journey when the suite covers it | #193 | applied (#193); journey not retired |
| [0007](0007-ci-uses-repo-scripts.md) | CI uses the repository scripts | Accepted (2026-11-14) | C: CI calls the scripts, no-stack `ops-tests` job, unused `e2e.yml` steps removed | #198 | branch `issue-198-ci-repo-scripts`; `backend-store` switch pending |
| [0008](0008-docs-moves.md) | Docs moves and role models | Accepted (2026-11-14) | `_docs/README.md` -> `docs/developer-guide.md`; delete `_docs/design-refs/`; architect on Fable, sub-architects and DevOps on Opus | #199 | #199 (moves), `_docs/process.md` (models) |
