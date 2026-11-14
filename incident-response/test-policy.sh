#!/usr/bin/env bash
# test-policy.sh : positive and negative tests for policy-check.py (no stack needed).
set -uo pipefail
D="$(cd "$(dirname "$0")" && pwd)"
T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
pass=0; fail=0
export GLOBAL_COOLDOWN_SECONDS=0   # cooldown has its own tests below
export COMPOSE_FILES="docker-compose.yml:observability/compose.yaml" COMPOSE_PROJECT_NAME=mgobs
CA="-p mgobs -f docker-compose.yml -f observability/compose.yaml"
mk() { # name command type confidence needs_human
  mkdir -p "$T/$1"
  jq -n --arg id "$1" --arg c "$2" --arg t "$3" --argjson conf "$4" --argjson nh "${5:-false}" \
   '{incident_id:$id,summary:"s",root_cause_hypothesis:"h",confidence:$conf,evidence_refs:["prom_error_ratio.json"],
     proposed_action:{type:$t,command:$c,files:[]},risk:"low",needs_human:$nh}' > "$T/$1/response.json"
}
expect() { # name expected-exit
  python3 -I "$D/policy-check.py" "$T/$1" > "$T/$1/out" 2>&1; rc=$?
  if [ $rc -eq "$2" ]; then pass=$((pass+1)); echo "ok   $1 (exit $rc)"; else fail=$((fail+1)); echo "FAIL $1 (exit $rc, want $2)"; cat "$T/$1/out"; fi
}
mk allow-restart        "docker compose $CA restart app" restart 0.9;                                       expect allow-restart 0
mk allow-faultoff       "FAULT_ENDPOINT_500= docker compose $CA up -d --no-deps app" config_change 0.85;    expect allow-faultoff 0
mk deny-rollback        "APP_IMAGE=mg-app:v1.2.3 docker compose $CA up -d --no-deps app" rollback 0.9;  expect deny-rollback 3   # rollback removed from allowlist
mk deny-no-compose-args "docker compose restart app" restart 0.9;                                       expect deny-no-compose-args 3  # template needs -p/-f
mk deny-other-stack     "docker compose -p other -f docker-compose.yml restart app" restart 0.9;        expect deny-other-stack 3
mk deny-ws              " docker compose $CA restart app" restart 0.9;                                  expect deny-ws 3
( unset COMPOSE_FILES COMPOSE_FILE; mk fail-closed "docker compose $CA restart app" restart 0.9; python3 -I "$D/policy-check.py" "$T/fail-closed" >/dev/null 2>&1; [ $? -eq 3 ] ) && { pass=$((pass+1)); echo "ok   fail-closed without COMPOSE_FILES"; } || { fail=$((fail+1)); echo "FAIL fail-closed"; }
mk deny-db-write        "docker compose exec db psql -c 'DELETE FROM repos'" restart 0.99;              expect deny-db-write 3
mk deny-secrets         "cat .env" config_change 0.99;                                                  expect deny-secrets 3
mk deny-force-push      "git push --force origin main" rollback 0.99;                                   expect deny-force-push 3
mk deny-prod-deploy     "kubectl rollout restart deploy/app" restart 0.99;                              expect deny-prod-deploy 3
mk deny-metachar        "docker compose $CA restart app; rm -rf /" restart 0.99;                            expect deny-metachar 3
mk deny-suffix          "docker compose $CA restart app --timeout 1" restart 0.99;                          expect deny-suffix 3
mk deny-type-mismatch   "docker compose $CA restart app" rollback 0.99;                                     expect deny-type-mismatch 3
mk low-confidence       "docker compose $CA restart app" restart 0.5;                                       expect low-confidence 3
mk needs-human          "docker compose $CA restart app" restart 0.95 true;                                 expect needs-human 3
mk propose-only         "" code_fix_pr 0.95;                                                            expect propose-only 3
# invalid JSON / schema violations
mkdir -p "$T/invalid-json"; echo '{not json' > "$T/invalid-json/response.json";                          expect invalid-json 3
mkdir -p "$T/missing-field"; echo '{"incident_id":"x"}' > "$T/missing-field/response.json";             expect missing-field 3
mk bad-confidence "docker compose $CA restart app" restart 1.7;                                             expect bad-confidence 3
mk bad-type "docker compose $CA restart app" nuke 0.9;                                                      expect bad-type 3
mkdir -p "$T/no-file";                                                                                  expect no-file 3
# max one automatic action: second run on the same incident must escalate
mk once "docker compose $CA restart app" restart 0.9; expect once 0; expect once 3
# global cooldown across incidents (separate dir so .last_action is isolated)
CD="$T/cd"; mkdir -p "$CD/a" "$CD/b" "$CD/c"
for i in a b c; do jq -n --arg id "$i" --arg c "docker compose $CA restart app" '{incident_id:$id,summary:"s",root_cause_hypothesis:"h",confidence:0.9,evidence_refs:["x"],proposed_action:{type:"restart",command:$c,files:[]},risk:"low",needs_human:false}' > "$CD/$i/response.json"; done
cdrun() { GLOBAL_COOLDOWN_SECONDS="$2" python3 -I "$D/policy-check.py" "$CD/$1" "${@:3}" >/dev/null 2>&1; echo $?; }
[ "$(cdrun a 300)" = 0 ] && [ -f "$CD/.last_action" ] && { pass=$((pass+1)); echo "ok   cooldown: first action allowed + .last_action stamped"; } || { fail=$((fail+1)); echo "FAIL cooldown first"; }
[ "$(cdrun b 300)" = 3 ] && grep -q 'global action cooldown' "$CD/b/decision.json" && [ ! -f "$CD/b/action.lock" ] && { pass=$((pass+1)); echo "ok   cooldown: second incident escalated, no lock left"; } || { fail=$((fail+1)); echo "FAIL cooldown second"; }
[ "$(cdrun b 300 --no-claim)" = 3 ] && { pass=$((pass+1)); echo "ok   cooldown: dry-run also reports it"; } || { fail=$((fail+1)); echo "FAIL cooldown dry-run"; }
[ "$(cdrun b 0)" = 0 ] && { pass=$((pass+1)); echo "ok   cooldown: 0 disables"; } || { fail=$((fail+1)); echo "FAIL cooldown off"; }
echo "1 x" > "$CD/.last_action"   # ancient timestamp: cooldown over
[ "$(cdrun c 300)" = 0 ] && { pass=$((pass+1)); echo "ok   cooldown: expires"; } || { fail=$((fail+1)); echo "FAIL cooldown expiry"; }
for bad in nan inf -inf NaN; do   # non-finite timestamp = no previous action (nan used to crash, inf locked forever)
  echo "$bad x" > "$CD/.last_action"; rm -f "$CD/c/action.lock" "$CD/c/decision.json"
  [ "$(cdrun c 300 --no-claim)" = 0 ] && { pass=$((pass+1)); echo "ok   cooldown: .last_action '$bad' treated as free"; } || { fail=$((fail+1)); echo "FAIL cooldown non-finite $bad"; }
done
# unreadable .last_action fails closed (only "not found" is free); skipped as root, which can read mode 000
if [ "$(id -u)" != 0 ]; then
  rm -f "$CD/c/action.lock" "$CD/c/decision.json"; echo "$(date +%s) x" > "$CD/.last_action"; chmod 000 "$CD/.last_action"
  [ "$(cdrun c 300)" = 3 ] && [ ! -f "$CD/c/action.lock" ] && grep -q 'global action cooldown' "$CD/c/decision.json" && { pass=$((pass+1)); echo "ok   cooldown: unreadable .last_action fails closed"; } || { fail=$((fail+1)); echo "FAIL cooldown unreadable"; }
  chmod 600 "$CD/.last_action"
fi
# future-dated stamp: the reported wait equals the enforced wait (ts - now + cooldown), not the bare cooldown
rm -f "$CD/c/action.lock" "$CD/c/decision.json"; echo "$(( $(date +%s) + 3600 )) x" > "$CD/.last_action"
[ "$(cdrun c 300 --no-claim)" = 3 ] && W="$(jq -r '.reasons[0]' "$CD/c/decision.json" | sed -n 's/.*(\([0-9]*\)s left).*/\1/p')" && [ "${W:-0}" -ge 3890 ] && [ "$W" -le 3900 ] && { pass=$((pass+1)); echo "ok   cooldown: future stamp reports the real wait ($W s)"; } || { fail=$((fail+1)); echo "FAIL cooldown future stamp (W=${W:-})"; }
# a crash inside policy-check must exit 1 (not 0/3) and leave an audit line
mkdir -p "$T/crash"; echo '{}' > "$T/crash/response.json"; echo '{broken' > "$T/crash/bad-policy.json"
python3 -I "$D/policy-check.py" "$T/crash" --policy "$T/crash/bad-policy.json" >/dev/null 2>&1; rc=$?
[ $rc -eq 1 ] && grep -q policy_error "$T/crash/audit.jsonl" && { pass=$((pass+1)); echo "ok   policy crash exits 1 + audited"; } || { fail=$((fail+1)); echo "FAIL policy crash (rc=$rc)"; }
# escalation emits a packet and audit lines with the required fields
[ -f "$T/low-confidence/escalation-packet.json" ] && { pass=$((pass+1)); echo "ok   escalation packet written"; } || { fail=$((fail+1)); echo "FAIL no escalation packet"; }
jq -e 'select(.ts and .kind and .detail and .decision)' "$T/low-confidence/audit.jsonl" >/dev/null && { pass=$((pass+1)); echo "ok   audit.jsonl fields"; } || { fail=$((fail+1)); echo "FAIL audit fields"; }
echo "policy tests: $pass passed, $fail failed"; [ $fail -eq 0 ]
