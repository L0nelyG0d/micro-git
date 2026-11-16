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

  echo "== action_id contract (stub proposes an id; policy builds; executor runs only the built argv)"
  check "stub response has action_id fault_off, empty params, no command field" "jq -e '.proposed_action.action_id==\"fault_off\" and .proposed_action.params=={} and (.proposed_action|has(\"command\")|not)' '$INC/response.json' >/dev/null"
  check "decision records action_id, params, argv" "jq -e '.action_id==\"fault_off\" and .params=={} and .argv[0]==\"docker\" and .env.FAULT_ENDPOINT_500==\"\"' '$INC/decision.json' >/dev/null"
  check "dry-run audit records action_id, params and command" "grep -q 'action_skipped' '$INC/audit.jsonl' && grep 'action_skipped' '$INC/audit.jsonl' | grep -q 'action_id=fault_off params={} command=FAULT_ENDPOINT_500= docker compose'"
  EVIDENCE_FIXTURE="$D/fixtures/evidence" "$D/collect-evidence.sh" drill-nocompose "$ROUTE" >/dev/null
  ( unset COMPOSE_FILES COMPOSE_FILE; "$D/run-responder.sh" drill-nocompose "$ROUTE" >/dev/null 2>&1 )
  check "stub does not need compose args: it still proposes fault_off, the policy fails closed without COMPOSE_FILES" "jq -e '.proposed_action.action_id==\"fault_off\"' '$INCIDENTS_DIR/drill-nocompose/response.json' >/dev/null && jq -e '.decision==\"escalate\" and .command==null' '$INCIDENTS_DIR/drill-nocompose/decision.json' >/dev/null && grep -q 'COMPOSE_FILES not set' '$INCIDENTS_DIR/drill-nocompose/decision.json'"
  mkdir -p "$T/dockbin"; cat > "$T/dockbin/docker" <<DOCK
#!/usr/bin/env bash
{ printf 'ARG=%s\\n' "\$@"; echo "FLAG=[\${FAULT_ENDPOINT_500-unset}]"; } > "$T/docker.out"
DOCK
  chmod +x "$T/dockbin/docker"
  EVIDENCE_FIXTURE="$D/fixtures/evidence" "$D/collect-evidence.sh" drill-exec "$ROUTE" >/dev/null
  ( PATH="$T/dockbin:$PATH" DRY_RUN=0 AUTO_EXECUTE=1 GLOBAL_COOLDOWN_SECONDS=0 VERIFY_MINUTES=0 WARMUP_SECONDS=0 VERIFY_INTERVAL=1 PROM_URL=http://127.0.0.1:1 APP_URL=http://127.0.0.1:1 "$D/run-responder.sh" drill-exec "$ROUTE" >/dev/null 2>&1 )
  check "executor runs exactly the built argv (fake docker) with the flag cleared" "grep -qx 'ARG=compose' '$T/docker.out' && grep -qx 'ARG=-f' '$T/docker.out' && grep -qx 'ARG=up' '$T/docker.out' && grep -qx 'ARG=--no-deps' '$T/docker.out' && grep -qx 'ARG=app' '$T/docker.out' && grep -qx 'FLAG=\[\]' '$T/docker.out'"
  check "exec audit line records action_id, params and command" "grep '\"kind\":\"exec\"' '$INCIDENTS_DIR/drill-exec/audit.jsonl' | grep -q 'action_id=fault_off params={} command=FAULT_ENDPOINT_500= docker compose'"
  check "action.lock holds the built action (JSON), not model text" "jq -e '.action_id==\"fault_off\" and (.argv|type)==\"array\"' '$INCIDENTS_DIR/drill-exec/action.lock' >/dev/null"

  echo "== non-finite confidence from a model must not execute (fake claude + fake docker, AUTO_EXECUTE=1)"
  for CASE in "nan:NaN:0" "inf:Infinity:0" "ok:0.9:1"; do
    CN="${CASE%%:*}"; CV="$(echo "$CASE" | cut -d: -f2)"; WANT="${CASE##*:}"
    mkdir -p "$T/nanbin-$CN"; cat > "$T/nanbin-$CN/claude" <<FAKE
#!/usr/bin/env bash
echo '{"incident_id":"drill-conf-$CN","summary":"s","root_cause_hypothesis":"h","confidence":$CV,"evidence_refs":["app_runtime.json"],"proposed_action":{"type":"restart","action_id":"restart_app","params":{},"files":[]},"risk":"r","needs_human":false}'
FAKE
    chmod +x "$T/nanbin-$CN/claude"; rm -f "$T/docker.out"
    EVIDENCE_FIXTURE="$D/fixtures/evidence" "$D/collect-evidence.sh" "drill-conf-$CN" "$ROUTE" >/dev/null
    ( PATH="$T/nanbin-$CN:$T/dockbin:$PATH" RESPONDER=claude DRY_RUN=0 AUTO_EXECUTE=1 GLOBAL_COOLDOWN_SECONDS=0 VERIFY_MINUTES=0 WARMUP_SECONDS=0 VERIFY_INTERVAL=1 PROM_URL=http://127.0.0.1:1 APP_URL=http://127.0.0.1:1 "$D/run-responder.sh" "drill-conf-$CN" "$ROUTE" >/dev/null 2>&1 )
    if [ "$WANT" = 1 ]; then
      check "control: finite confidence 0.9 executes the fake docker" "[ -f '$T/docker.out' ] && grep -qx 'ARG=restart' '$T/docker.out'"
    else
      check "confidence $CV: fake docker never called, no action.lock" "[ ! -f '$T/docker.out' ] && [ ! -f '$INCIDENTS_DIR/drill-conf-$CN/action.lock' ]"
      check "confidence $CV: escalated (policy decision or dropped response), never allowed" "[ -f '$INCIDENTS_DIR/drill-conf-$CN/escalation-packet.json' ] && ! grep -q '\"decision\":\"allow\"' '$INCIDENTS_DIR/drill-conf-$CN/audit.jsonl'"
    fi
  done
  echo "== hostile action.lock written before the policy step is not executed"
  EVIDENCE_FIXTURE="$D/fixtures/evidence" "$D/collect-evidence.sh" drill-hostile-lock "$ROUTE" >/dev/null
  echo '{"action_id":"restart_app","params":{},"argv":["touch","'"$T"'/PWNED2"],"env":{},"command":"touch x"}' > "$INCIDENTS_DIR/drill-hostile-lock/action.lock"; rm -f "$T/docker.out"
  ( PATH="$T/dockbin:$PATH" DRY_RUN=0 AUTO_EXECUTE=1 GLOBAL_COOLDOWN_SECONDS=0 VERIFY_MINUTES=0 WARMUP_SECONDS=0 VERIFY_INTERVAL=1 PROM_URL=http://127.0.0.1:1 APP_URL=http://127.0.0.1:1 "$D/run-responder.sh" drill-hostile-lock "$ROUTE" >/dev/null 2>&1 )
  check "pre-existing action.lock: nothing executed, escalated" "[ ! -e '$T/PWNED2' ] && [ ! -f '$T/docker.out' ] && [ -f '$INCIDENTS_DIR/drill-hostile-lock/escalation-packet.json' ]"

  echo "== executor refusals (fake python3 tampers action.lock after policy-check; fake docker records calls)"
  REALPY="$(command -v python3)"; mkdir -p "$T/tamperbin"
  cat > "$T/tamperbin/python3" <<TAMP
#!/usr/bin/env bash
"$REALPY" "\$@"; rc=\$?
case "\$*" in *policy-check.py*) [ -n "\${TAMPER:-}" ] && eval "\$TAMPER";; esac
exit \$rc
TAMP
  chmod +x "$T/tamperbin/python3"
  exec_case() { # id AUTO_EXECUTE-value tamper-command   (nothing may run unless tamper is empty and AUTO_EXECUTE is exactly 1)
    local id="$1" ae="$2" tamper="${3:-}" lock="$INCIDENTS_DIR/$1/action.lock"
    EVIDENCE_FIXTURE="$D/fixtures/evidence" "$D/collect-evidence.sh" "$id" "$ROUTE" >/dev/null; rm -f "$T/docker.out" "$T/PWNED3"
    ( PATH="$T/tamperbin:$T/dockbin:$PATH" TAMPER="$tamper" LOCKP="$lock" PWN="$T/PWNED3" DRY_RUN=0 AUTO_EXECUTE="$ae" GLOBAL_COOLDOWN_SECONDS=0 VERIFY_MINUTES=0 WARMUP_SECONDS=0 VERIFY_INTERVAL=1 PROM_URL=http://127.0.0.1:1 APP_URL=http://127.0.0.1:1 "$D/run-responder.sh" "$id" "$ROUTE" >/dev/null 2>&1 )
  }
  refused() { # label id reason-substring
    check "$1: nothing executed (fake docker and PWNED3 absent)" "[ ! -f '$T/docker.out' ] && [ ! -e '$T/PWNED3' ]"
    check "$1: refusal audited ($3)" "grep action_skipped '$INCIDENTS_DIR/$2/audit.jsonl' | grep -q '$3'"
    check "$1: no action_result audited" "! grep -q action_result '$INCIDENTS_DIR/$2/audit.jsonl'"
  }
  exec_case xr-symlink 1 'mv "$LOCKP" "$LOCKP.real" && ln -s "$LOCKP.real" "$LOCKP"'; refused "lock is a symlink" xr-symlink "action.lock is a symlink"
  exec_case xr-argv 1 'jq ".argv=[\"touch\",\"$PWN\"]" "$LOCKP" > "$LOCKP.n" && mv "$LOCKP.n" "$LOCKP"'; refused "lock argv differs from decision.json" xr-argv "differ from decision.json"
  exec_case xr-env 1 'jq ".env.FAULT_ENDPOINT_500=\"1\"" "$LOCKP" > "$LOCKP.n" && mv "$LOCKP.n" "$LOCKP"'; refused "lock env differs only" xr-env "differ from decision.json"
  exec_case xr-env-extra 1 'jq ".env.EXTRA=\"1\"" "$LOCKP" > "$LOCKP.n" && mv "$LOCKP.n" "$LOCKP"'; refused "lock has an extra env entry" xr-env-extra "differ from decision.json"
  exec_case xr-deleted 1 'rm -f "$LOCKP"'; refused "lock deleted after policy" xr-deleted "no action.lock"
  exec_case xr-emptyargv 1 'jq ".argv=[]" "$LOCKP" > "$LOCKP.n" && mv "$LOCKP.n" "$LOCKP"'; refused "lock with empty argv" xr-emptyargv "argv is empty"
  exec_case xr-garbled 1 'echo "{not json" > "$LOCKP"'; refused "lock is not JSON" xr-garbled "unreadable"
  exec_case xr-fifo 1 'rm -f "$LOCKP" && mkfifo "$LOCKP"'; refused "lock is a fifo" xr-fifo "not a regular file"
  for AE in true 01 " 1" ""; do
    n="xr-ae-$(printf '%s' "$AE" | cksum | cut -d' ' -f1)"; exec_case "$n" "$AE"
    check "AUTO_EXECUTE='$AE': nothing executes, no lock claimed" "[ ! -f '$T/docker.out' ] && [ ! -e '$INCIDENTS_DIR/$n/action.lock' ] && grep -q 'allowed but not executed' '$INCIDENTS_DIR/$n/audit.jsonl'"
  done
  exec_case xr-control 1
  check "control: executes exactly the template argv" "[ \"\$(grep '^ARG=' '$T/docker.out' | tr '\n' ' ')\" = 'ARG=compose ARG=-f ARG=docker-compose.yml ARG=-f ARG=observability/compose.yaml ARG=-f ARG=observability/app-telemetry.yaml ARG=up ARG=-d ARG=--no-deps ARG=app ' ]"
  check "control: template env applied (FAULT_ENDPOINT_500 set to empty, not unset)" "grep -qx 'FLAG=\[\]' '$T/docker.out'"
  check "control: action_result audited" "grep -q action_result '$INCIDENTS_DIR/xr-control/audit.jsonl'"

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
  for SIG in TERM HUP; do   # a signal to the wrapper must reach the child (own session) and exit 128+N
    rm -f "$T/sig.pid" "$T/sig.rc"
    printf 'echo "$$ $PPID" > "%s/sig.pid"\nexec sleep 60\n' "$T" > "$T/sigchild.sh"   # child records its pid and the wrapper's pid
    ( PATH="$T/minbin" bash -c ". '$D/lib.sh'; TMO=60 run_limited bash '$T/sigchild.sh'" >/dev/null 2>&1; echo $? > "$T/sig.rc" ) &
    BG=$!; for _ in $(seq 1 20); do [ -s "$T/sig.pid" ] && break; sleep 0.3; done
    read -r CP WP < "$T/sig.pid"; kill -$SIG "$WP" 2>/dev/null; wait "$BG" 2>/dev/null; sleep 0.5
    check "fallback: SIG$SIG kills the child" "! kill -0 $CP 2>/dev/null"
    check "fallback: SIG$SIG exits 128+N" "[ \"\$(cat '$T/sig.rc')\" = $([ $SIG = TERM ] && echo 143 || echo 129) ]"
  done
  echo "== probe audit line + token perms (N6, N5)"
  mkdir -p "$T/vinc"; ( DRY_RUN=0 INCIDENTS_DIR="$T/vinc" PROBE_URL='http://127.0.0.1:1/x?token=SECRETQ' VERIFY_MINUTES=0 WARMUP_SECONDS=0 VERIFY_INTERVAL=1 PROM_URL=http://127.0.0.1:1 APP_URL=http://127.0.0.1:1 PROBE_COUNT=1 "$D/runbooks/verify-recovery.sh" /x vinc >/dev/null 2>&1 )
  check "probe enabled is audited" "grep -q 'probe enabled' '$T/vinc/vinc/audit.jsonl'"
  check "probe audit drops the query string" "! grep -q SECRETQ '$T/vinc/vinc/audit.jsonl'"
  "$D/make-token.sh" "$T/tok-new" >/dev/null 2>&1; TM="$(stat -c %a "$T/tok-new" 2>/dev/null || stat -f %Lp "$T/tok-new")"
  check "make-token creates mode 600" "[ '$TM' = 600 ]"
  PU_OUT="$T/vinc2"; mkdir -p "$PU_OUT"; ( DRY_RUN=0 INCIDENTS_DIR="$PU_OUT" PROBE_URL='http://bob:SEC?RETPW@127.0.0.1:1/x?token=Q#f' VERIFY_MINUTES=0 WARMUP_SECONDS=0 VERIFY_INTERVAL=1 PROM_URL=http://127.0.0.1:1 APP_URL=http://127.0.0.1:1 PROBE_COUNT=1 "$D/runbooks/verify-recovery.sh" /x vinc2 >/dev/null 2>&1 )
  check "probe audit drops user:pass@" "grep -q 'http://127.0.0.1:1/x' '$PU_OUT/vinc2/audit.jsonl' && ! grep -q 'SEC\|RETPW\|bob' '$PU_OUT/vinc2/audit.jsonl'"
  PS_OUT="$T/vinc3"; mkdir -p "$PS_OUT"; ( DRY_RUN=0 INCIDENTS_DIR="$PS_OUT" PROBE_URL='http://bob:se/cr@et@127.0.0.1:1/x' VERIFY_MINUTES=0 WARMUP_SECONDS=0 VERIFY_INTERVAL=1 PROM_URL=http://127.0.0.1:1 APP_URL=http://127.0.0.1:1 PROBE_COUNT=1 "$D/runbooks/verify-recovery.sh" /x vinc3 >/dev/null 2>&1 )
  check "probe with a slash in the password: userinfo not logged" "grep -q 'http://127.0.0.1:1/x' '$PS_OUT/vinc3/audit.jsonl' && ! grep -q 'bob\|se/cr\|cr@et' '$PS_OUT/vinc3/audit.jsonl'"
  RJ_OUT="$T/vinc4"; mkdir -p "$RJ_OUT"; ( DRY_RUN=0 INCIDENTS_DIR="$RJ_OUT" PROBE_URL='ftp://bob:RJPW@host/x?k=Q' VERIFY_MINUTES=0 WARMUP_SECONDS=0 VERIFY_INTERVAL=1 PROM_URL=http://127.0.0.1:1 APP_URL=http://127.0.0.1:1 PROBE_COUNT=1 "$D/runbooks/verify-recovery.sh" /x vinc4 >/dev/null 2>&1 )
  check "invalid probe URL is rejected, logged without userinfo, no probe" "grep -q 'PROBE_URL rejected' '$RJ_OUT/vinc4/audit.jsonl' && ! grep -q 'bob\|RJPW\|SECRET\|k=Q' '$RJ_OUT/vinc4/audit.jsonl' && ! grep -q 'probe enabled' '$RJ_OUT/vinc4/audit.jsonl'"
  # password-with-slash shapes: pa/ss must not appear in stdout, stderr or audit (a rejected URL prints fixed text only)
  PWI=0; for PX in 'u:pa/ss@host/x' 'http:u:pa/ss@host/x' '//u:pa/ss@host/x' 'http://u:pa/ss@host/x'; do
    PWI=$((PWI+1)); PX_OUT="$T/pw$PWI"; mkdir -p "$PX_OUT"
    ( DRY_RUN=0 INCIDENTS_DIR="$PX_OUT" PROBE_URL="$PX" VERIFY_MINUTES=0 WARMUP_SECONDS=0 VERIFY_INTERVAL=1 PROM_URL=http://127.0.0.1:1 APP_URL=http://127.0.0.1:1 PROBE_COUNT=1 "$D/runbooks/verify-recovery.sh" /x pw$PWI >"$PX_OUT/stdout" 2>"$PX_OUT/stderr" )
    check "probe shape $PWI: audit written" "[ -s '$PX_OUT/pw$PWI/audit.jsonl' ]"
    check "probe shape $PWI: pa/ss not in stdout, stderr or audit" "! grep -q 'pa/ss' '$PX_OUT/stdout' '$PX_OUT/stderr' '$PX_OUT/pw$PWI/audit.jsonl'"
  done
  check "probe shape 4 (accepted http) audits scheme://host/path only" "grep -q 'to http://host/x' '$T/pw4/pw4/audit.jsonl'"
  # make-token re-run repairs mode and (Linux root path, stubbed uname/id/chown) ownership
  chmod 644 "$T/tok-new"; "$D/make-token.sh" "$T/tok-new" >/dev/null 2>&1; TM="$(stat -c %a "$T/tok-new" 2>/dev/null || stat -f %Lp "$T/tok-new")"
  check "make-token re-run repairs mode to 600" "[ '$TM' = 600 ]"
  mkdir -p "$T/lnx"; printf '#!/bin/sh\necho Linux\n' > "$T/lnx/uname"; printf '#!/bin/sh\necho 0\n' > "$T/lnx/id"; printf '#!/bin/sh\necho "$@" > "%s/chown.args"\n' "$T" > "$T/lnx/chown"; chmod +x "$T/lnx/"*
  H1="$(cat "$T/tok-new")"; PATH="$T/lnx:$PATH" "$D/make-token.sh" "$T/tok-new" >/dev/null 2>&1
  check "make-token re-run on Linux root chowns to 65534" "grep -q '65534:65534' '$T/chown.args'"
  check "make-token re-run keeps the token content" "[ \"\$(cat '$T/tok-new')\" = '$H1' ]"
  # re-run on a symlink: refused, and the target keeps its mode (chmod must not follow the link)
  echo target > "$T/sym-target"; chmod 644 "$T/sym-target"; ln -sf "$T/sym-target" "$T/sym-tok"
  "$D/make-token.sh" "$T/sym-tok" >/dev/null 2>&1; SRC=$?
  TM="$(stat -c %a "$T/sym-target" 2>/dev/null || stat -f %Lp "$T/sym-target")"
  check "make-token re-run refuses a symlink (exit non-zero)" "[ $SRC -ne 0 ]"
  check "make-token symlink refusal leaves the target mode alone" "[ '$TM' = 644 ]"
  # dangling symlink: -e is false, so the create branch used to write the token through the link
  ln -sf "$T/dang-target" "$T/dang-tok"; "$D/make-token.sh" "$T/dang-tok" >/dev/null 2>&1; DRC=$?
  check "make-token refuses a dangling symlink (exit non-zero)" "[ $DRC -ne 0 ]"
  check "make-token dangling symlink: link target NOT created" "[ ! -e '$T/dang-target' ] && [ ! -L '$T/dang-target' ]"
  echo "== listener: unreadable token is a config error"
  if [ "$(id -u)" != 0 ]; then
    mkdir -p "$T/utok"; echo dummy-not-a-secret > "$T/utok/tok"; chmod 000 "$T/utok/tok"
    UP=$((20000 + RANDOM % 20000)); ALERT_TOKEN_FILE="$T/utok/tok" INCIDENTS_DIR="$T/utok/inc" LISTEN_PORT=$UP python3 -I "$D/listener.py" >/dev/null 2>&1 & UPID=$!
    for _ in $(seq 1 30); do curl -s "http://127.0.0.1:$UP/healthz" >/dev/null && break; sleep 0.2; done
    ucode=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Authorization: Bearer x' -H 'Content-Type: application/json' -d '{}' "http://127.0.0.1:$UP/alert"); kill $UPID 2>/dev/null
    check "unreadable token -> 401" "[ '$ucode' = 401 ]"
    check "unreadable token audited as config error" "grep -q 'not readable by the listener' '$T/utok/inc/listener-audit.jsonl' && ! grep -q 'invalid bearer' '$T/utok/inc/listener-audit.jsonl'"
    chmod 600 "$T/utok/tok"
  fi
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
  FI="$(ls -d "$T"/incidents2/inc-* | head -1)"; MODE="$(stat -c %a "$FI" 2>/dev/null || stat -f %Lp "$FI")"
  check "incident ids have random suffix" "[[ '$(basename "$FI")' =~ ^inc-[0-9]{8}T[0-9]{6}-[A-Za-z0-9]+-[0-9a-f]{6}$ ]]"
  check "incident dir mode 700" "[ '$MODE' = 700 ]"
  check "non-dict labels audited"  "grep -q 'labels is not an object' '$T'/incidents2/listener-audit.jsonl"
  check "route=all escalated, not executed" "grep -l 'route=all' '$T'/incidents2/inc-*/audit.jsonl >/dev/null"
  check "no-token request audited"          "grep -q auth_rejected '$T'/incidents2/listener-audit.jsonl"
fi
[ $fails -eq 0 ] && echo "PASS" || { echo "FAILED: $fails"; exit 1; }
