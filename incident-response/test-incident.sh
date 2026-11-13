#!/usr/bin/env bash
# test-incident.sh [--dry-run] [route]
#   --dry-run : no stack needed. Uses fixtures/, stub responder, no commands executed, simulated verify.
#   (live)    : needs the compose + observability stack. Injects FAULT_ENDPOINT_500=<route>, drives
#               traffic, then runs collect -> respond (AUTO_EXECUTE=1) -> verify. Mutates the local stack.
set -uo pipefail
D="$(cd "$(dirname "$0")" && pwd)"
DRY=0; [ "${1:-}" = "--dry-run" ] && { DRY=1; shift; }
ROUTE="${1:-/repos/{owner}/{repo}}"
T="$(mktemp -d)"; trap 'rm -rf "$T"; [ -n "${LPID:-}" ] && kill "$LPID" 2>/dev/null' EXIT
export INCIDENTS_DIR="$T/incidents"; mkdir -p "$INCIDENTS_DIR"
export COMPOSE_FILES="${COMPOSE_FILES:-docker-compose.yml:observability/compose.yaml:observability/app-telemetry.yaml}"
export ALERT_TOKEN_FILE="$T/alert-token"; ( umask 077; echo "test-token-$RANDOM$RANDOM" > "$ALERT_TOKEN_FILE" )
TOK="$(cat "$ALERT_TOKEN_FILE")"
fails=0
check() { if eval "$2"; then echo "ok   $1"; else echo "FAIL $1"; fails=$((fails+1)); fi; }

if [ $DRY -eq 1 ]; then
  export EVIDENCE_FIXTURE="$D/fixtures/evidence" RESPONDER=stub DRY_RUN=1 AUTO_EXECUTE=0
  ID="drill-dry-1"
else
  export RESPONDER="${RESPONDER:-auto}" AUTO_EXECUTE=1   # explicit opt-in to execute
  ID="drill-live-$(date +%s)"
  echo "live: injecting FAULT_ENDPOINT_500 (local stack)"
  . "$D/lib.sh"; compose_args || { echo "set COMPOSE_FILES (and COMPOSE_PROJECT_NAME) for a live drill"; exit 2; }
  ( cd "$D/.." && FAULT_ENDPOINT_500="$ROUTE" docker compose "${COMPOSE_ARGS[@]}" up -d --no-deps app ) >/dev/null 2>&1
  sleep 10; for _ in $(seq 1 60); do curl -s -o /dev/null "${APP_URL:-http://localhost:${APP_PORT:-8080}}/api/repos/a/b"; done
fi
INC="$INCIDENTS_DIR/$ID"

echo "== collect-evidence"
"$D/collect-evidence.sh" "$ID" "$ROUTE" >/dev/null
check "manifest exists"        "[ -f '$INC/evidence-manifest.json' ]"
check "manifest has sha256+bytes" "jq -e '.sources|length>0 and all(.[];.sha256!=\"\" and .bytes>0)' '$INC/evidence-manifest.json' >/dev/null"
check "app_runtime evidence recorded"   "jq -e '.sources|map(select(.file==\"app_runtime.json\" and .status==\"ok\"))|length==1' '$INC/evidence-manifest.json' >/dev/null"
check "app_runtime has only flag+image tag" "jq -e 'keys==[\"container\",\"fault_endpoint_500\",\"fault_flag_active\",\"image_tag\"]' '$INC/evidence/app_runtime.json' >/dev/null"
check "no secrets in evidence" "! grep -rqiE 'Bearer [A-Za-z0-9]|hunter2|s3cr3t' '$INC/evidence'"
if [ $DRY -eq 1 ]; then
  check "loki secret redacted" "grep -q REDACTED '$INC/evidence/loki_errors.json'"
  check "escaped-JSON loki secrets redacted (password/api_key/Bearer)" "! grep -qE 'hunter2|s3cr3t|abc\\.def' '$INC/evidence/loki_errors.json' && grep -q 'password.*REDACTED' '$INC/evidence/loki_errors.json'"
  check "loki evidence is still valid JSON" "jq -e . '$INC/evidence/loki_errors.json' >/dev/null"
fi
check "redact unit tests" "'$D/test-redact.sh' >/dev/null"
echo "== responder + policy"
"$D/run-responder.sh" "$ID" "$ROUTE"
check "response.json valid + decision allow" "jq -e '.decision==\"allow\"' '$INC/decision.json' >/dev/null"
check "command is allowlisted template"      "jq -e '.command==\"FAULT_ENDPOINT_500= docker compose -f docker-compose.yml -f observability/compose.yaml -f observability/app-telemetry.yaml up -d --no-deps app\"' '$INC/decision.json' >/dev/null"
if [ $DRY -eq 1 ]; then
  check "nothing executed in dry run"        "[ ! -f '$INC/action-output.txt' ] && [ ! -f '$INC/action.lock' ]"
  check "action_skipped audited"             "grep -q action_skipped '$INC/audit.jsonl'"
fi
check "audit has required fields"            "jq -e 'select(.ts and .kind and .detail and .decision)' '$INC/audit.jsonl' >/dev/null"
check "audit has policy_decision"            "grep -q policy_decision '$INC/audit.jsonl'"

echo "== verify-recovery"
"$D/runbooks/verify-recovery.sh" "$ROUTE" "$ID" >/dev/null; check "verify-recovery exit 0" "[ $? -eq 0 ]"

if [ $DRY -eq 1 ]; then
  echo "== escalation path (no fault evidence -> stub low confidence)"
  mkdir -p "$T/ev-empty"; echo '{"status":"ok"}' > "$T/ev-empty/health.json"
  EVIDENCE_FIXTURE="$T/ev-empty" "$D/collect-evidence.sh" drill-dry-2 "$ROUTE" >/dev/null
  "$D/run-responder.sh" drill-dry-2 "$ROUTE" >/dev/null
  check "escalation-packet.json written" "[ -f '$INCIDENTS_DIR/drill-dry-2/escalation-packet.json' ]"

  echo "== model path isolation (fake claude on PATH)"
  mkdir -p "$T/fakebin"; cat > "$T/fakebin/claude" <<FAKE
#!/usr/bin/env bash
{ printf '%s\\n' "\$@"; echo "CWD=\$PWD"; echo "ENV_USER=\${USER:-}"; env | cut -d= -f1 | sed 's/^/ENVNAME=/'; ls .incident .incident/evidence 2>&1; [ -t 0 ] && echo STDIN_TTY || echo STDIN_NOT_TTY; } > "$T/fake.out"
echo '{"incident_id":"x"}'
FAKE
  chmod +x "$T/fakebin/claude"
  FAKE_OUT="$T/fake.out" PATH="$T/fakebin:$PATH" RESPONDER=claude EVIDENCE_FIXTURE="$D/fixtures/evidence" "$D/collect-evidence.sh" drill-fake "$ROUTE" >/dev/null
  FAKE_OUT="$T/fake.out" PATH="$T/fakebin:$PATH" RESPONDER=claude "$D/run-responder.sh" drill-fake "$ROUTE" >/dev/null 2>&1
  check "model sees only the temp copy, not the real incident dir" "! grep -q '$INCIDENTS_DIR' '$T/fake.out' && grep -q '/.incident' '$T/fake.out' && grep -q 'evidence-manifest.json' '$T/fake.out'"
  check "absolute .env / home deny rules passed" "grep -qF 'Read(//**/.env)' '$T/fake.out' && grep -qF 'Read(//**/.env.*)' '$T/fake.out' && grep -qF 'Read(~/**)' '$T/fake.out'"
  check "restricted mode, limited tools, MCP off" "grep -qx -- '--restricted' '$T/fake.out' && grep -qx -- '--strict-mcp-config' '$T/fake.out' && grep -qx 'Read,Grep,Glob' '$T/fake.out'"
  mkdir -p "$T/nomkbin"; printf '#!/bin/sh\nexit 1\n' > "$T/nomkbin/mktemp"; chmod +x "$T/nomkbin/mktemp"
  cp -R "$INCIDENTS_DIR/drill-fake" "$INCIDENTS_DIR/drill-nomk"
  PATH="$T/nomkbin:$T/fakebin:$PATH" RESPONDER=claude "$D/run-responder.sh" drill-nomk "$ROUTE" >/dev/null 2>&1; NOMK=$?
  check "mktemp failure -> exit 1 + escalation packet, no model run" "[ $NOMK = 1 ] && [ -f '$INCIDENTS_DIR/drill-nomk/escalation-packet.json' ] && grep -q 'mktemp' '$INCIDENTS_DIR/drill-nomk/audit.jsonl'"
  check "scrubbed env keeps USER (macOS keychain login), drops other vars" "[ -z \"\${USER:-}\" ] || { grep -qx \"ENV_USER=\$USER\" '$T/fake.out' && ! grep -qE '^ENVNAME=(INCIDENTS_DIR|COMPOSE_FILES|ALERT_TOKEN_FILE)$' '$T/fake.out'; }"
  check "model stdin is not a tty (/dev/null)" "grep -q STDIN_NOT_TTY '$T/fake.out'"
  echo "== compose arg validation"
  for bad in ":a.yml" "a.yml:" "a.yml::b.yml" "-f:b.yml" "a.yml:-x"; do
    check "bad COMPOSE_FILES rejected: $bad" "( . '$D/lib.sh'; COMPOSE_FILES='$bad'; ! compose_args )"
  done
  check "valid_id rejects leading dash/dot" "( . '$D/lib.sh'; ! valid_id -x && ! valid_id .x && valid_id a-1 )"

  echo "== pipeline timeout kills the whole process group"
  cat > "$T/slow.sh" <<'SLOW'
#!/usr/bin/env bash
sleep 300 &
echo $! > "$1/child.pid"
wait
SLOW
  chmod +x "$T/slow.sh"; mkdir -p "$T/tmo"
  RC=$(INCIDENTS_DIR="$T/incidents" python3 -I -c "
import sys; sys.path.insert(0, '$D'); import listener
print(listener.run('$T/tmo', ['$T/slow.sh', '$T/tmo'], timeout=2))")
  sleep 1
  check "timed-out command returns 98" "[ '$RC' = 98 ]"
  check "timeout audited"              "grep -q command_timeout '$T/tmo/audit.jsonl'"
  check "child process was killed too" "! kill -0 \$(cat '$T/tmo/child.pid') 2>/dev/null"

  echo "== run_limited fallback without timeout/gtimeout (N2)"
  mkdir -p "$T/minbin"; for b in python3 sleep cat bash dirname; do ln -sf "$(command -v $b)" "$T/minbin/$b"; done
  RL=$( PATH="$T/minbin" bash -c ". '$D/lib.sh'; ! command -v timeout >/dev/null && ! command -v gtimeout >/dev/null || exit 9; TMO=1 run_limited sleep 30; echo rc=\$?" 2>&1 )
  check "fallback times out with rc 124 (no timeout binary)" "[ '$RL' = rc=124 ]"
  RL=$( PATH="$T/minbin" bash -c ". '$D/lib.sh'; echo piped | TMO=5 run_limited cat" 2>&1 )
  check "fallback passes stdin through" "[ '$RL' = piped ]"
  echo "== probe audit line + token perms (N6, N5)"
  mkdir -p "$T/vinc"; ( DRY_RUN=0 INCIDENTS_DIR="$T/vinc" PROBE_URL='http://127.0.0.1:1/x?token=SECRETQ' VERIFY_MINUTES=0 WARMUP_SECONDS=0 VERIFY_INTERVAL=1 PROM_URL=http://127.0.0.1:1 APP_URL=http://127.0.0.1:1 PROBE_COUNT=1 "$D/runbooks/verify-recovery.sh" /x vinc >/dev/null 2>&1 )
  check "probe enabled is audited" "grep -q 'probe enabled' '$T/vinc/vinc/audit.jsonl'"
  check "probe audit drops the query string" "! grep -q SECRETQ '$T/vinc/vinc/audit.jsonl'"
  "$D/make-token.sh" "$T/tok-new" >/dev/null 2>&1; TM="$(stat -f %Lp "$T/tok-new" 2>/dev/null || stat -c %a "$T/tok-new")"
  check "make-token creates mode 600" "[ '$TM' = 600 ]"
  echo "== listener audit with group-readable token (N5 regression)"
  mkdir -p "$T/ltok"; echo dummy-not-a-secret > "$T/ltok/tok"; chmod 644 "$T/ltok/tok"
  LW=$( ALERT_TOKEN_FILE="$T/ltok/tok" INCIDENTS_DIR="$T/ltok/inc" python3 -I -c "
import sys; sys.path.insert(0, '$D'); import listener
listener.gaudit('auth_rejected', 'x', 'reject'); listener.gaudit('auth_rejected', 'y', 'reject')" 2>&1 ); LRC=$?
  check "gaudit with 644 token does not crash" "[ '$LRC' = 0 ]"
  check "original audit lines are written" "[ \$(grep -c auth_rejected '$T/ltok/inc/listener-audit.jsonl') = 2 ]"
  check "permission warning audited once" "[ \$(grep -c '\"config\"' '$T/ltok/inc/listener-audit.jsonl') = 1 ]"
  check "permission warning printed to stderr" "echo '$LW' | grep -q 'chmod 600'"
  echo "== redaction failure drops content (fake python3)"
  mkdir -p "$T/badpy"; printf '#!/bin/sh\nexit 1\n' > "$T/badpy/python3"; chmod +x "$T/badpy/python3"
  PATH="$T/badpy:$PATH" EVIDENCE_FIXTURE="$D/fixtures/evidence" "$D/collect-evidence.sh" drill-badredact "$ROUTE" >/dev/null 2>&1
  check "failed redaction replaces evidence with REDACTED-ERROR" "[ \"\$(cat '$INCIDENTS_DIR/drill-badredact/evidence/loki_errors.json')\" = '{\"raw\":\"[REDACTED-ERROR]\"}' ] && ! grep -rq hunter2 '$INCIDENTS_DIR/drill-badredact/evidence'"
  check "failed source recorded as failed" "jq -e '.failed|index(\"loki_errors.json\")' '$INCIDENTS_DIR/drill-badredact/evidence-manifest.json' >/dev/null"

  echo "== listener end to end"
  PORT=$((20000 + RANDOM % 20000))
  env -u AUTO_EXECUTE LISTEN_PORT=$PORT python3 -I "$D/listener.py" >/dev/null 2>&1 & LPID=$!   # AUTO_EXECUTE deliberately unset: default must be dry-run
  for _ in $(seq 1 30); do curl -s "http://127.0.0.1:$PORT/healthz" >/dev/null && break; sleep 0.2; done
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d @"$D/fixtures/alert.json" "http://127.0.0.1:$PORT/alert")
  check "listener accepts alert (202)" "[ '$code' = 202 ]"
  for _ in $(seq 1 50); do ls "$INCIDENTS_DIR"/inc-*/audit.jsonl >/dev/null 2>&1 && grep -q incident_done "$INCIDENTS_DIR"/inc-*/audit.jsonl 2>/dev/null && break; sleep 0.2; done
  LINC="$(ls -d "$INCIDENTS_DIR"/inc-* 2>/dev/null | head -1)"
  check "listener wrote alert.json"        "[ -f '$LINC/alert.json' ]"
  check "listener ran full pipeline"       "grep -q policy_decision '$LINC/audit.jsonl'"
  check "listener default AUTO_EXECUTE=0 audited" "grep -q 'effective AUTO_EXECUTE=0' '$LINC/audit.jsonl'"
  check "listener default executed nothing" "[ ! -f '$LINC/action.lock' ] && [ ! -f '$LINC/action-output.txt' ] && grep -q action_skipped '$LINC/audit.jsonl'"
  kill "$LPID" 2>/dev/null; wait "$LPID" 2>/dev/null
  # explicit AUTO_EXECUTE=1 is recorded (DRY_RUN=1 from this script still prevents any real command)
  PORT2=$((PORT + 1)); INCIDENTS_DIR="$T/incidents2" AUTO_EXECUTE=1 LISTEN_PORT=$PORT2 python3 -I "$D/listener.py" >/dev/null 2>&1 & LPID=$!
  for _ in $(seq 1 30); do curl -s "http://127.0.0.1:$PORT2/healthz" >/dev/null && break; sleep 0.2; done
  curl -s -o /dev/null -X POST -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d @"$D/fixtures/alert.json" "http://127.0.0.1:$PORT2/alert"
  for _ in $(seq 1 50); do grep -q incident_done "$T"/incidents2/inc-*/audit.jsonl 2>/dev/null && break; sleep 0.2; done
  check "listener explicit AUTO_EXECUTE=1 audited" "grep -q 'effective AUTO_EXECUTE=1' '$T'/incidents2/inc-*/audit.jsonl"
  PORT=$PORT2
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d 'nope' "http://127.0.0.1:$PORT/alert")
  check "listener rejects bad JSON (400)"  "[ '$code' = 400 ]"
  U="http://127.0.0.1:$PORT/alert"
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d @"$D/fixtures/alert.json" "$U");                                         check "no token -> 401" "[ '$code' = 401 ]"
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Authorization: Bearer wrong' -H 'Content-Type: application/json' -d @"$D/fixtures/alert.json" "$U");        check "wrong token -> 401" "[ '$code' = 401 ]"
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $TOK" -H 'Content-Type: text/plain' -d @"$D/fixtures/alert.json" "$U");               check "non-JSON content-type -> 415" "[ '$code' = 415 ]"
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -H 'Content-Length: abc' -d '{}' "$U" 2>/dev/null); check "non-integer Content-Length rejected" "[ '$code' != 202 ]"
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d '{"alerts":[{"status":"firing","fingerprint":"badlabels1","startsAt":"2026-11-12T10:00:00Z","labels":"x"},{"status":"firing","fingerprint":"allroute1","startsAt":"2026-11-12T10:00:01Z","labels":{"alertname":"A","route":"all"}}]}' "$U"); check "non-dict labels / route=all accepted without crash" "[ '$code' = 202 ]"
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d '{"alerts":[{"status":"firing","fingerprint":7,"startsAt":null,"labels":{"route":"/x"}}]}' "$U"); check "non-string fingerprint/startsAt accepted but rejected+audited" "[ '$code' = 202 ]"
  sleep 4; check "non-string fingerprint audited" "grep -q 'fingerprint/startsAt missing' '$T'/incidents2/listener-audit.jsonl"
  FI="$(ls -d "$T"/incidents2/inc-* | head -1)"; MODE="$(stat -f %Lp "$FI" 2>/dev/null || stat -c %a "$FI")"
  check "incident ids have random suffix" "[[ '$(basename "$FI")' =~ ^inc-[0-9]{8}T[0-9]{6}-[A-Za-z0-9]+-[0-9a-f]{6}$ ]]"
  check "incident dir mode 700" "[ '$MODE' = 700 ]"
  check "non-dict labels audited"  "grep -q 'labels is not an object' '$T'/incidents2/listener-audit.jsonl"
  check "route=all escalated, not executed" "grep -l 'route=all' '$T'/incidents2/inc-*/audit.jsonl >/dev/null"
  check "no-token request audited"          "grep -q auth_rejected '$T'/incidents2/listener-audit.jsonl"
fi
[ $fails -eq 0 ] && echo "PASS" || { echo "FAILED: $fails"; exit 1; }
