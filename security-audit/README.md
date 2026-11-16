# security-audit/

Recurring audit: scanner, model review, human validation.

- `audit-brief.md` how the three passes work
- `findings.schema.json` finding and run format
- `capability-table.md` responder attack surface and agent supply chain
- `run-audit.sh [label]` runs Semgrep (local, docker, or `scanner_unavailable`), go vet, npm audit, grep secret scan, then a read-only model pass; raw output goes to `runs/<date>-<label>/` (git-ignored; Semgrep runs with the source mounted read-only and `extra.lines` excerpts are stripped; only `runs/<date>-<label>.json` is committed)
- `validate-findings.py [file...]` stdlib validator for run files (defaults to `runs/*.json`); `normalize-semgrep.py` turns raw Semgrep JSON into a schema-valid run with deterministic ids `SEC-<date>-<sha1(rule|file|line)[:8]>`
- Semgrep image is pinned in `run-audit.sh` (1.184.0 + digest); `disposition_reason` is an optional finding field (why the reviewer chose the disposition)
- `runs/` results; `2026-11-12-baseline.json` is the first run, `2026-11-12-semgrep.json` the Semgrep run (6 findings, dispositions still `open`)

Merge raw outputs into one `runs/<date>-<label>.json` per the schema, then a human sets dispositions. Never commit secret values.
