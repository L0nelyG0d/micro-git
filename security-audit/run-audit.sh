#!/usr/bin/env bash
# Recurring audit runner. Writes security-audit/runs/<date>-<label>.json-adjacent raw outputs.
# Usage: security-audit/run-audit.sh [label]   (default label: scheduled)
# Raw outputs under runs/<date>-<label>/ are git-ignored (root .gitignore); only runs/<date>-<label>.json is meant to be committed.
# Never prints secret values: grep results are reduced to file:line.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; cd "$ROOT"
DATE="$(date +%F)"; LABEL="${1:-scheduled}"
OUT="security-audit/runs/${DATE}-${LABEL}"; mkdir -p "$OUT"
# Pinned: tag 1.184.0 (was latest at audit time); digest recorded for reproducibility.
SEMGREP_IMAGE="semgrep/semgrep:1.184.0@sha256:cc1b68266f267878b39739373ea9985b9a78e5d64497dd875c8de100b22695ed"
SEMGREP_ARGS=(--config p/golang --config p/typescript --config p/secrets --json)

# 1. Deterministic scanner (scope: backend/ and frontend/src)
if command -v semgrep >/dev/null 2>&1; then
  semgrep "${SEMGREP_ARGS[@]}" backend frontend/src > "$OUT/semgrep.json" 2>"$OUT/semgrep.err"; echo "semgrep: ran"
elif command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  docker run --rm -v "$PWD:/src:ro" "$SEMGREP_IMAGE" semgrep "${SEMGREP_ARGS[@]}" /src/backend /src/frontend/src > "$OUT/semgrep.json" 2>"$OUT/semgrep.err"; echo "semgrep: ran via docker"
else
  echo '{"status":"scanner_unavailable"}' > "$OUT/semgrep.json"; echo "semgrep: scanner_unavailable"
fi

# Raw output may embed source excerpts (results[].extra.lines); strip them from anything kept on disk.
if [ -s "$OUT/semgrep.json" ] && command -v jq >/dev/null; then
  jq 'if .results then .results |= map(del(.extra.lines, .extra.metavars)) else . end' "$OUT/semgrep.json" > "$OUT/semgrep.json.tmp" && mv "$OUT/semgrep.json.tmp" "$OUT/semgrep.json"
fi

# 2. Cheap local checks
(cd backend && go vet ./... > "$ROOT/$OUT/go-vet.txt" 2>&1; echo "go vet exit $?")
(cd frontend && npm audit --omit=dev --json > "$ROOT/$OUT/npm-audit.json" 2>/dev/null; echo "npm audit exit $?")
git grep -nIE 'postgres(ql)?://[^:/@ ]+:[^@ ]+@|npg_[A-Za-z0-9]+' -- . ':(exclude).github/workflows/ci.yml' \
  | grep -v 'USER:PASSWORD@HOST' | cut -d: -f1,2 > "$OUT/secret-grep.txt" || true

# 3. Model review pass: headless, read-only, findings JSON only. Same isolation as incident-response/run-responder.sh:
# clean `git archive HEAD` copy in a 0700 temp dir (untracked files such as .env are absent; the committed state is
# reviewed, plus the audit brief and schema copied in), scrubbed env (PATH/HOME/TERM + provider key), stdin from
# /dev/null, restricted tools, MCP servers off, absolute deny rules. Skipped if git tracks a .env* file.
PROMPT="Follow security-audit/audit-brief.md. Read-only review. Output only JSON matching security-audit/findings.schema.json. Never read .env or credential files; never output secret values."
MODEL_TMP=""; cleanup() { [ -n "$MODEL_TMP" ] && rm -rf "$MODEL_TMP"; return 0; }
trap cleanup EXIT; trap 'cleanup; exit 143' INT TERM
model_pass() {
  command -v claude >/dev/null 2>&1 || { echo '{"status":"not_run"}' > "$OUT/model.json"; return; }
  if git ls-files | grep -E '(^|/)\.env($|\.)' | grep -vqE '\.example$'; then
    echo '{"status":"not_run","reason":"a .env file is tracked in git"}' > "$OUT/model.json"; echo "model pass skipped: tracked .env"; return
  fi
  MODEL_TMP="$(mktemp -d)" && [ -d "$MODEL_TMP" ] && chmod 700 "$MODEL_TMP" || {
    MODEL_TMP=""; echo '{"status":"not_run","reason":"mktemp failed"}' > "$OUT/model.json"; echo "model pass skipped: mktemp failed"; return; }
  git archive HEAD | tar -x -C "$MODEL_TMP" || { echo '{"status":"not_run","reason":"git archive failed"}' > "$OUT/model.json"; return; }
  mkdir -p "$MODEL_TMP/security-audit"; cp security-audit/audit-brief.md security-audit/findings.schema.json "$MODEL_TMP/security-audit/" 2>/dev/null
  ( cd "$MODEL_TMP" && env -i PATH="$PATH" HOME="$HOME" TERM=dumb ${ANTHROPIC_API_KEY:+ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY"} \
      claude -p "$PROMPT" --restricted --tools "Read,Grep,Glob" --strict-mcp-config --permission-mode plan \
      --allowedTools "Read,Grep,Glob" --disallowedTools "Bash" "Edit" "Write" "WebFetch" "Read(//**/.env)" "Read(//**/.env.*)" "Read(//**/*.pem)" "Read(~/**)" \
      < /dev/null ) > "$OUT/model.raw" 2>"$OUT/model.err" || echo "model pass failed"
  # best-effort redaction of the stored model output; drop it if redaction fails
  if python3 -I "$ROOT/incident-response/redact.py" < "$OUT/model.raw" > "$OUT/model.json" 2>/dev/null; then rm -f "$OUT/model.raw"
  else rm -f "$OUT/model.raw" "$OUT/model.json"; echo '{"status":"not_run","reason":"model output could not be redacted; dropped"}' > "$OUT/model.json"; fi
  # codex is not used here: its read-only sandbox can still read files outside the clean copy.
}
model_pass

# 4. Emit schema-valid merged run (semgrep findings; deterministic ids), then validate.
python3 security-audit/normalize-semgrep.py "$OUT/semgrep.json" "${DATE}-${LABEL}" "$DATE" "${OUT}.json" "$(git rev-parse --short HEAD 2>/dev/null)"
python3 security-audit/validate-findings.py "${OUT}.json"
echo "Raw outputs in $OUT; merged run ${OUT}.json (all dispositions 'open'). Add go vet/npm audit/model results as checks/findings, then a human sets dispositions."
