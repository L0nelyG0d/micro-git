#!/usr/bin/env bash
# Shared helpers. Source me; do not execute.
IR_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INCIDENTS_DIR="${INCIDENTS_DIR:-$IR_DIR/incidents}"
PROM_URL="${PROM_URL:-http://localhost:9090}"
LOKI_URL="${LOKI_URL:-http://localhost:3100}"
TEMPO_URL="${TEMPO_URL:-http://localhost:3200}"
# APP_URL follows APP_PORT (the published host port in docker-compose.yml); set APP_URL to override.
APP_URL="${APP_URL:-http://localhost:${APP_PORT:-8080}}"

valid_id()    { [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$ && "$1" != *..* ]]; }
valid_route() { [[ "$1" =~ ^[A-Za-z0-9_/.:{}*-]{1,120}$ && "$1" != *..* ]]; }

# audit <incident-dir> <kind> <detail> [decision] : append one JSON line to audit.jsonl
audit() {
  local dir="$1" kind="$2" detail="$3" decision="${4:-n/a}"
  mkdir -p "$dir"
  jq -nc --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg actor "${AUDIT_ACTOR:-script:$(basename "$0")}" \
    --arg kind "$kind" --arg detail "$detail" --arg decision "$decision" '{ts:$ts,actor:$actor,kind:$kind,detail:$detail,decision:$decision}' >> "$dir/audit.jsonl"
}

# redact secrets from stdin: see redact.py. Input is capped (REDACT_MAX_BYTES, default 4 MiB) and the run is
# time-limited (REDACT_TIMEOUT, default 20 s). Returns non-zero on ANY failure: callers must then DROP the content
# (never keep the unredacted file).
redact() { head -c "${REDACT_MAX_BYTES:-4194304}" | TMO="${REDACT_TIMEOUT:-20}" run_limited python3 -I "$IR_DIR/redact.py"; }

# run_limited <cmd...> : run with a timeout of ${TMO:-300}s (exit 124 on expiry): `timeout`, else `gtimeout`, else a python3 process-group fallback (macOS ships neither by default).
run_limited() {
  local t="${TMO:-300}"
  if command -v timeout >/dev/null; then timeout "$t" "$@"
  elif command -v gtimeout >/dev/null; then gtimeout "$t" "$@"
  else
    # Neither exists (stock macOS): python3 runs the command in its own process group and kills the group on expiry (rc 124, like timeout).
    # stdin/stdout/stderr are inherited, so pipes work. Last resort without python3: run unlimited.
    command -v python3 >/dev/null || { "$@"; return; }
    python3 -I -c '
import os, signal, subprocess, sys
t = float(sys.argv[1])
p = subprocess.Popen(sys.argv[2:], start_new_session=True)
def fwd(signum, _frame):
    # the child lives in its own session: forward the signal to its group, then exit 128+N like a shell would
    try: os.killpg(p.pid, signal.SIGTERM)
    except ProcessLookupError: pass
    try: p.wait(timeout=5)
    except subprocess.TimeoutExpired:
        try: os.killpg(p.pid, signal.SIGKILL)
        except ProcessLookupError: pass
    sys.exit(128 + signum)
for s in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
    signal.signal(s, fwd)
try:
    rc = p.wait(timeout=t)
    sys.exit(128 - rc if rc < 0 else rc)   # killed by signal N: popen reports -N, shells report 128+N
except subprocess.TimeoutExpired:
    for sig in (signal.SIGTERM, signal.SIGKILL):
        try: os.killpg(p.pid, sig)
        except ProcessLookupError: break
        try: p.wait(timeout=5); break
        except subprocess.TimeoutExpired: continue
    sys.exit(124)
' "$t" "$@"
  fi
}

# compose_args : fills the array COMPOSE_ARGS from COMPOSE_FILES (colon separated, falls back to COMPOSE_FILE)
# and COMPOSE_PROJECT_NAME. Returns 1 (fail closed, array empty) when no compose file list is configured.
compose_args() {
  COMPOSE_ARGS=(); local f list="${COMPOSE_FILES:-${COMPOSE_FILE:-}}"
  [ -n "$list" ] || return 1
  [[ "$list" =~ ^[A-Za-z0-9_./:-]+$ && "$list" != *..* ]] || return 1
  # no empty segments (leading/trailing/double ":"), no entry may start with "-" (would read as a compose flag)
  [[ "$list" != :* && "$list" != *: && "$list" != *::* && "$list" != -* && "$list" != *:-* ]] || return 1
  [ -z "${COMPOSE_PROJECT_NAME:-}" ] || [[ "$COMPOSE_PROJECT_NAME" =~ ^[a-z0-9][a-z0-9_-]*$ ]] || return 1
  [ -z "${COMPOSE_PROJECT_NAME:-}" ] || COMPOSE_ARGS+=(-p "$COMPOSE_PROJECT_NAME")
  local IFS=:; for f in $list; do COMPOSE_ARGS+=(-f "$f"); done
}

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
