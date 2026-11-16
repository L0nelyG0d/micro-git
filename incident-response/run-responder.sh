#!/usr/bin/env bash
# run-responder.sh <incident-id> <route>
# observe(done) -> reason (LLM or stub) -> authorize (policy-check.py) -> act (optional) -> verify -> remember.
# Env: RESPONDER=auto|claude|codex|stub (auto: claude, else codex, else stub)
#      AUTO_EXECUTE=1 run an allowed action (default 0 = dry-run: decision + planned action logged only)   DRY_RUN=1 never executes
#      COMPOSE_FILES (colon separated; COMPOSE_FILE accepted) / COMPOSE_PROJECT_NAME become the -f / -p flags inside the
#      policy's action templates (the model proposes an action_id, policy-check.py builds the argv; no model text is executed). Unset COMPOSE_FILES = fail closed (no action can be allowed or run).
#      ACTION_ENV_PASSTHROUGH: names (not values) of extra env vars forwarded to the allowlisted command, default
#      lists the docker-compose.yml interpolation vars (POSTGRES_*, JWT_SECRET, APP_PORT, APP_VERSION, ...).
#      RESPONDER_TIMEOUT=300  INCIDENTS_DIR
# The LLM CLI (auto = claude only; codex only when RESPONDER=codex is set explicitly, with a warning) runs with a
# scrubbed environment (PATH/HOME/TERM + the LLM provider key if set), stdin from /dev/null, and in a temp dir
# holding a clean `git archive HEAD` copy plus a copy of the redacted incident files (.incident/), so untracked
# files such as .env and the real checkout path are not reachable; absolute deny rules cover //**/.env*, *.pem and ~/**.
# The run aborts (escalation packet) if git tracks any .env* file other than *.example. response.json is passed through redact before it is saved.
set -uo pipefail
. "$(dirname "$0")/lib.sh"
ID="${1:-}"; ROUTE="${2:-}"
valid_id "$ID" && valid_route "$ROUTE" || { echo "usage: $0 <incident-id> <route>" >&2; exit 2; }
INC="$INCIDENTS_DIR/$ID"; [ -d "$INC/evidence" ] || { echo "no evidence in $INC" >&2; exit 2; }
REPO="$(git -C "$IR_DIR" rev-parse --show-toplevel 2>/dev/null || echo "$IR_DIR/..")"
compose_args && CSTR="${COMPOSE_ARGS[*]}" || CSTR=""
[ -n "$CSTR" ] || audit "$INC" config "COMPOSE_FILES not set: fail closed, no automatic action can be proposed or run"
R="${RESPONDER:-auto}"
if [ "$R" = auto ]; then
  # codex is never chosen automatically: its read-only sandbox can still read files outside the clean copy.
  if command -v claude >/dev/null; then R=claude; else R=stub; fi
fi
if [ "$R" = codex ]; then
  echo "WARNING: RESPONDER=codex: the codex read-only sandbox can still READ files outside the clean repo copy (incl. ~/ and .env). Use only on a machine with no secrets." >&2
  audit "$INC" config "WARNING explicit RESPONDER=codex; read-only sandbox can still read files"
fi
audit "$INC" responder_start "responder=$R"

RAW="$INC/responder-raw.txt"; : > "$RAW"
# USER is kept (not a secret): on macOS the claude CLI finds its keychain login by user name; without it a
# subscription login reads "Not logged in" (drill 2026-11-13, #176).
CLEAN=(env -i PATH="$PATH" HOME="$HOME" TERM=dumb ${USER:+USER="$USER"} ${ANTHROPIC_API_KEY:+ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY"} ${OPENAI_API_KEY:+OPENAI_API_KEY="$OPENAI_API_KEY"})
TMO="${RESPONDER_TIMEOUT:-300}"
export TMO
CLEANREPO=""; cleanup() { [ -n "$CLEANREPO" ] && rm -rf "$CLEANREPO" "$INC/.cleanrepo"; return 0; }; trap cleanup EXIT; trap 'cleanup; exit 143' INT TERM
if [ "$R" = claude ] || [ "$R" = codex ]; then
  # Abort when the repo tracks a real env file: it would be inside the archive and readable by the model.
  BADENV="$(git -C "$REPO" ls-files | grep -E '(^|/)\.env($|\.)' | grep -vE '\.example$' | head -3)"
  if [ -n "$BADENV" ]; then
    audit "$INC" responder_abort "tracked env file(s) in git: $(echo $BADENV | tr '\n' ' '); refusing to run a model on this repo" error
    jq -n --arg id "$ID" '{incident_id:$id,why_escalated:["responder aborted: a .env file is tracked in git"]}' > "$INC/escalation-packet.json"
    echo "responder aborted: tracked .env in git" >&2; exit 1
  fi
  CLEANREPO="$(mktemp -d)" && [ -d "$CLEANREPO" ] && chmod 700 "$CLEANREPO" || {
    CLEANREPO=""; audit "$INC" responder_abort "mktemp -d failed; not running a model without an isolated copy" error
    jq -n --arg id "$ID" '{incident_id:$id,why_escalated:["responder aborted: could not create an isolated temp dir"]}' > "$INC/escalation-packet.json"
    echo "responder aborted: mktemp failed" >&2; exit 1; }
  git -C "$REPO" archive HEAD 2>/dev/null | tar -x -C "$CLEANREPO" || audit "$INC" config "git archive failed; LLM runs on an empty dir"
  echo "$CLEANREPO" > "$INC/.cleanrepo"   # lets the listener remove the copy if this script is killed
  # The model only ever sees a copy of the (already redacted) incident files, never the real checkout path.
  mkdir -p "$CLEANREPO/.incident"; cp -R "$INC/evidence" "$CLEANREPO/.incident/evidence"
  cp "$INC/alert.json" "$INC/evidence-manifest.json" "$CLEANREPO/.incident/" 2>/dev/null
  MINC="$CLEANREPO/.incident"
  audit "$INC" config "LLM runs on clean git-archive copy in a temp dir (no untracked files, no .env); incident copy at .incident/"
  PROMPT="$(sed -e "s|{{INCIDENT_DIR}}|$MINC|g" -e "s|{{INCIDENT_ID}}|$ID|g" -e "s|{{ROUTE}}|$ROUTE|g" "$IR_DIR/responder-task.md")"
fi

case "$R" in
  claude) audit "$INC" exec "claude -p (tools: Read,Grep,Glob; cwd=repo)"
          (cd "$CLEANREPO" && run_limited "${CLEAN[@]}" claude -p "$PROMPT" --restricted --tools "Read,Grep,Glob" --strict-mcp-config --allowedTools "Read,Grep,Glob" --disallowedTools "Bash" "Edit" "Write" "WebFetch" "Read(//**/.env)" "Read(//**/.env.*)" "Read(//**/*.pem)" "Read(~/**)" --add-dir "$MINC" < /dev/null) > "$RAW" 2>"$INC/responder-stderr.txt" ;;
  codex)  audit "$INC" exec "codex exec --sandbox read-only"
          (cd "$CLEANREPO" && run_limited "${CLEAN[@]}" codex exec --sandbox read-only "$PROMPT" < /dev/null) > "$RAW" 2>"$INC/responder-stderr.txt" ;;
  stub)   audit "$INC" exec "deterministic stub responder"
          python3 -I - "$INC" "$ID" > "$RAW" <<'PY'
import json, sys, glob, os
inc, iid = sys.argv[1:3]
def load(n):
    try: return json.load(open(os.path.join(inc, "evidence", n)))
    except Exception: return {}
# Evidence-based: flag really set on the running app container AND error ratio high.
flag = load("app_runtime.json").get("fault_endpoint_500", "")
try: ratio = float(load("prom_error_ratio.json")["data"]["result"][0]["value"][1])
except Exception: ratio = 0.0
fault = bool(flag) and ratio >= 0.05
out = {"incident_id": iid,
  "summary": "Elevated 5xx ratio on the alerted route.",
  "root_cause_hypothesis": ("Fault-injection flag FAULT_ENDPOINT_500=%s is set on the running app container and the error ratio is %.2f." % (flag, ratio)) if fault else "Unknown; no active fault flag with a high error ratio in evidence.",
  "confidence": 0.9 if fault else 0.3,
  "evidence_refs": ["app_runtime.json", "prom_error_ratio.json", "loki_errors.json"],
  "proposed_action": ({"type": "config_change", "action_id": "fault_off", "params": {}, "files": []} if fault
                      else {"type": "escalate", "action_id": "none", "params": {}, "files": []}),
  "risk": "Low: recreates only the app container with the fault flag cleared.",
  "needs_human": not fault}
print(json.dumps(out))
PY
          ;;
  *) echo "unknown RESPONDER=$R" >&2; exit 2 ;;
esac
audit "$INC" responder_done "rc=$? bytes=$(wc -c < "$RAW" | tr -d ' ')"

# Extract the first JSON object from the raw output (tolerates fences / stray prose); redact before saving.
python3 -I - "$RAW" "$INC/response.json" <<'PY' || audit "$INC" responder_parse_error "no JSON object found"
import json, sys
t = open(sys.argv[1]).read(); d = json.JSONDecoder()
for i, c in enumerate(t):
    if c == "{":
        try:
            o, _ = d.raw_decode(t[i:]); json.dump(o, open(sys.argv[2], "w"), indent=2); sys.exit(0)
        except ValueError: pass
sys.exit(1)
PY
if [ -f "$INC/response.json" ]; then
  # redact before anything reads it; on ANY redact/jq failure drop the file and escalate (never keep unredacted output)
  if redact < "$INC/response.json" > "$INC/.response.red" 2>/dev/null && jq -e . "$INC/.response.red" >/dev/null 2>&1; then
    mv "$INC/.response.red" "$INC/response.json"
  else
    rm -f "$INC/.response.red" "$INC/response.json"; audit "$INC" redact_error "response.json could not be redacted; dropped" error
    jq -n --arg id "$ID" '{incident_id:$id,why_escalated:["responder output could not be redacted safely; dropped"]}' > "$INC/escalation-packet.json"
    echo "redaction failed: escalated" >&2; exit 1
  fi
fi

NOCLAIM=(); { [ "${DRY_RUN:-0}" = 1 ] || [ "${AUTO_EXECUTE:-0}" != 1 ]; } && NOCLAIM=(--no-claim)   # dry-run must not consume the one-action lock
python3 -I "$IR_DIR/policy-check.py" "$INC" ${NOCLAIM[@]+"${NOCLAIM[@]}"} > "$INC/.decision.out"; PRC=$?
case "$PRC" in
  0) ;;
  3) echo "escalated: $INC/escalation-packet.json"; exit 0 ;;
  *) audit "$INC" policy_error "policy-check.py exited rc=$PRC; not acting" error
     jq -n --arg id "$ID" '{incident_id:$id,why_escalated:["policy check failed internally; no action taken"]}' > "$INC/escalation-packet.json"
     echo "policy error rc=$PRC: escalated"; exit 1 ;;
esac
# Only what policy-check.py built is executed: action_id/params/argv/env from decision.json (never any model text).
CMD="$(jq -r .command "$INC/decision.json")"; AID="$(jq -r .action_id "$INC/decision.json")"; APARAMS="$(jq -c .params "$INC/decision.json")"
if [ "${DRY_RUN:-0}" = 1 ] || [ "${AUTO_EXECUTE:-}" != 1 ]; then   # AUTO_EXECUTE must be exactly the string 1 ("true", "01", " 1", empty do not execute)
  audit "$INC" action_skipped "allowed but not executed (DRY_RUN=${DRY_RUN:-0} AUTO_EXECUTE=${AUTO_EXECUTE:-0}): action_id=$AID params=$APARAMS command=$CMD"
  echo "allowed (not executed): $CMD"; exit 0
fi
# Read action.lock ONCE (one python3 call, status checked) and refuse unless it is a regular non-symlink file whose argv AND env
# equal the structures in decision.json (not the display string). Output: NUL-separated fields, written to a file so the exit
# status is not lost: AID, PARAMS(json), COMMAND, N, argv x N, env KEY=VALUE x rest. Any failure -> audit line, nothing runs.
LOCK="$INC/action.lock"; LOCKOUT="$INC/.lock.read"; rm -f "$LOCKOUT"
LREASON="$(python3 -I - "$LOCK" "$INC/decision.json" 2>&1 > "$LOCKOUT" <<'PY'
import json, os, re, stat, sys
lock, dec = sys.argv[1:3]
def die(m): sys.stderr.write(m); sys.exit(1)
try:
    if stat.S_ISLNK(os.lstat(lock).st_mode): die("action.lock is a symlink")
    fd = os.open(lock, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
except FileNotFoundError: die("no action.lock for an allowed decision")
except OSError as e: die("action.lock cannot be opened (%s)" % type(e).__name__)
try:
    if not stat.S_ISREG(os.fstat(fd).st_mode): die("action.lock is not a regular file")
    with os.fdopen(fd) as f: lk = json.loads(f.read())
    with open(dec) as f: dc = json.loads(f.read())
except (OSError, ValueError) as e: die("action.lock or decision.json unreadable (%s)" % type(e).__name__)
if not isinstance(lk, dict) or not isinstance(dc, dict): die("action.lock or decision.json is not an object")
if dc.get("decision") != "allow": die("decision.json is not an allow")
argv, env = lk.get("argv"), lk.get("env")
if not (isinstance(argv, list) and argv and all(isinstance(x, str) for x in argv) and argv[0]): die("action.lock argv is empty or not a list of strings")
if not (isinstance(env, dict) and all(isinstance(k, str) and re.fullmatch(r"[A-Z_][A-Z0-9_]*", k) and isinstance(v, str) for k, v in env.items())): die("action.lock env is not a map of NAME to string")
if lk.get("argv") != dc.get("argv") or lk.get("env") != dc.get("env") or lk.get("action_id") != dc.get("action_id"): die("action.lock argv/env/action_id differ from decision.json")
parts = [str(lk.get("action_id")), json.dumps(lk.get("params")), str(lk.get("command")), str(len(argv))] + argv + ["%s=%s" % kv for kv in env.items()]
if any("\0" in x for x in parts): die("NUL byte in action.lock")
sys.stdout.write("\0".join(parts) + "\0")
PY
)"; LRC=$?
if [ "$LRC" -ne 0 ]; then
  rm -f "$LOCKOUT"; audit "$INC" action_skipped "refusing to run (fail closed): ${LREASON:-action.lock could not be read}" error; echo "refused: action.lock check failed"; exit 1
fi
LF=(); while IFS= read -r -d '' l; do LF+=("$l"); done < "$LOCKOUT"; rm -f "$LOCKOUT"
AID="${LF[0]}"; APARAMS="${LF[1]}"; CMD="${LF[2]}"; NARGV="${LF[3]}"
[[ "$NARGV" =~ ^[0-9]+$ ]] && [ "$NARGV" -gt 0 ] && [ "${#LF[@]}" -ge $((4 + NARGV)) ] || { audit "$INC" action_skipped "action.lock fields could not be parsed: refusing to run (fail closed)" error; echo "refused: empty argv"; exit 1; }
ARGV=("${LF[@]:4:NARGV}"); ENVSET=(); [ "${#LF[@]}" -gt $((4 + NARGV)) ] && ENVSET=("${LF[@]:$((4 + NARGV))}")
[ -n "$CSTR" ] || { audit "$INC" action_skipped "COMPOSE_FILES not set: refusing to run (fail closed)"; echo "refused: COMPOSE_FILES not set"; exit 1; }
audit "$INC" exec "action_id=$AID params=$APARAMS command=$CMD"
PASS=(); for n in ${ACTION_ENV_PASSTHROUGH:-POSTGRES_USER POSTGRES_PASSWORD POSTGRES_DB JWT_SECRET GRAFANA_ADMIN_PASSWORD APP_PORT APP_VERSION APP_COMMIT COOKIE_SECURE REGISTER_MAX_PER_IP COMPOSE_ENV_FILES}; do
  [[ "$n" =~ ^[A-Z_][A-Z0-9_]*$ ]] && [ -n "${!n:-}" ] && PASS+=("$n=${!n}"); done
audit "$INC" action_env "passthrough names: $(for p in ${PASS[@]+"${PASS[@]}"}; do printf '%s ' "${p%%=*}"; done) compose=$CSTR"
( cd "$REPO" && env -i PATH="$PATH" HOME="$HOME" ${DOCKER_HOST:+DOCKER_HOST="$DOCKER_HOST"} ${PASS[@]+"${PASS[@]}"} ${ENVSET[@]+"${ENVSET[@]}"} "${ARGV[@]}" ) >> "$INC/action-output.txt" 2>&1
ARC=$?; audit "$INC" action_result "rc=$ARC"
"$IR_DIR/runbooks/verify-recovery.sh" "$ROUTE" "$ID"; VRC=$?   # warm-up is measured from here (action time)
audit "$INC" verify_result "rc=$VRC"
if [ "$VRC" -ne 0 ]; then
  jq -n --arg id "$ID" '{incident_id:$id,why_escalated:["automatic action did not recover the service"]}' > "$INC/escalation-packet.json"
  audit "$INC" escalation_packet "post-action verification failed"
  exit 1
fi
echo "recovered"
