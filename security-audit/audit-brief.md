# Recurring security audit brief

Goal: find security defects in this repo (Go backend, TypeScript frontend, deploy config) on a schedule, with every finding ending in a human-assigned disposition.

## Three passes
1. **Deterministic scanner (Semgrep).** `semgrep --config p/golang --config p/typescript --config p/secrets --json`. `run-audit.sh` uses a local semgrep if installed, else `docker run --rm -v "$PWD:/src" semgrep/semgrep`, else records `scanner_unavailable`. Scanner output is evidence, not a verdict.
2. **Model review pass.** Headless `claude -p` or `codex exec`, read-only (no write/exec tools). Scope: authn/authz and JWT handling, rate limiting, SQL construction, input validation, static file serving, CORS, Docker/compose/deploy workflows, logging of sensitive data, and the responder's capability limits (see capability-table.md). Must read the scanner output and explain, not duplicate, its hits. Must NOT read `.env` or any file holding credentials, and must never emit secret values (redact to `<redacted>`).
3. **Human validation.** A person reviews each finding and sets `disposition`: `confirmed`, `false_positive`, `accepted_risk`, or `fixed` (open until then), plus `reviewer` and `date`. Model-only findings are never auto-confirmed. Accepted risks need a reason in `description` and a review date.

## Output
JSON conforming to `findings.schema.json`, saved as `runs/<date>-<label>.json`. Findings caused by or linked to an incident set `related_incident` (INC-YYYYMMDD-NNN) so the report in `docs/operations-and-security-report.md` can cite them.

## Cadence and gates
Weekly and before each release. A `confirmed` high/critical finding blocks release until `fixed` or `accepted_risk`.

## Baseline
`runs/2026-11-12-baseline.json`: go vet, npm audit, and the CI grep secret scan only. Semgrep and the model pass have not been run.
