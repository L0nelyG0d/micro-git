#!/usr/bin/env bash
# test-policy.sh : positive and negative tests for policy-check.py (no stack needed).
set -uo pipefail
D="$(cd "$(dirname "$0")" && pwd)"
T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
pass=0; fail=0
export GLOBAL_COOLDOWN_SECONDS=0   # cooldown has its own tests below
export COMPOSE_FILES="docker-compose.yml:observability/compose.yaml" COMPOSE_PROJECT_NAME=mgobs
CA="-p mgobs -f docker-compose.yml -f observability/compose.yaml"
mk() { # name action_id type confidence [needs_human] [params-json]   (the model never supplies a command)
  local EMPTY='{}'; mkdir -p "$T/$1"
  jq -n --arg id "$1" --arg a "$2" --arg t "$3" --argjson conf "$4" --argjson nh "${5:-false}" --argjson p "${6:-$EMPTY}" \
   '{incident_id:$id,summary:"s",root_cause_hypothesis:"h",confidence:$conf,evidence_refs:["prom_error_ratio.json"],
     proposed_action:{type:$t,action_id:$a,params:$p,files:[]},risk:"low",needs_human:$nh}' > "$T/$1/response.json"
}
mkraw() { mkdir -p "$T/$1"; printf '%s\n' "$2" > "$T/$1/response.json"; }   # name raw-json
expect() { # name expected-exit [policy-check args...]
  n="$1"; want="$2"; shift 2
  python3 -I "$D/policy-check.py" "$T/$n" "$@" > "$T/$n/out" 2>&1; rc=$?
  if [ $rc -eq "$want" ]; then pass=$((pass+1)); echo "ok   $n (exit $rc)"; else fail=$((fail+1)); echo "FAIL $n (exit $rc, want $want)"; cat "$T/$n/out"; fi
}
reason() { # name substring : decision.json reasons must mention it, and no command may be built
  if jq -e --arg s "$2" '[.reasons[]|contains($s)]|any' "$T/$1/decision.json" >/dev/null && jq -e '.command==null and .argv==null' "$T/$1/decision.json" >/dev/null; then pass=$((pass+1)); echo "ok   $1: reason '$2', no command built"; else fail=$((fail+1)); echo "FAIL $1: reason '$2' not found"; jq -c .reasons "$T/$1/decision.json"; fi
}
okjq() { # label name jq-filter
  if jq -e "$3" "$T/$2/decision.json" >/dev/null; then pass=$((pass+1)); echo "ok   $1"; else fail=$((fail+1)); echo "FAIL $1"; cat "$T/$2/decision.json"; fi
}
# approved actions: the policy builds the command from the template + system compose args
mk allow-restart  restart_app restart 0.9;                  expect allow-restart 0
okjq "restart_app builds the template with the compose args" allow-restart ".command==\"docker compose $CA restart app\" and .action_id==\"restart_app\" and .params=={} and .argv==[\"docker\",\"compose\",\"-p\",\"mgobs\",\"-f\",\"docker-compose.yml\",\"-f\",\"observability/compose.yaml\",\"restart\",\"app\"]"
mk allow-faultoff fault_off config_change 0.85;             expect allow-faultoff 0
okjq "fault_off builds the template with the compose args" allow-faultoff ".command==\"FAULT_ENDPOINT_500= docker compose $CA up -d --no-deps app\" and .env=={\"FAULT_ENDPOINT_500\":\"\"} and .argv[0:2]==[\"docker\",\"compose\"] and .argv[-4:]==[\"up\",\"-d\",\"--no-deps\",\"app\"]"
# unknown / missing / disallowed ids (replaces deny-rollback: rollback is not an action)
mk deny-rollback-id      rollback rollback 0.9;             expect deny-rollback-id 3;  reason deny-rollback-id "unknown action_id"
mk deny-rollback-type    fault_off rollback 0.9;            expect deny-rollback-type 3; reason deny-rollback-type "not allowed for incident type rollback"
mk deny-unknown-id       drop_database config_change 0.99;  expect deny-unknown-id 3;   reason deny-unknown-id "unknown action_id"
mk deny-id-wrong-type    fault_off restart 0.99;            expect deny-id-wrong-type 3; reason deny-id-wrong-type "not allowed for incident type restart"
mk deny-id-wrong-type2   restart_app config_change 0.99;    expect deny-id-wrong-type2 3; reason deny-id-wrong-type2 "not allowed for incident type config_change"
mk deny-none-executable  none restart 0.99;                 expect deny-none-executable 3
mkraw deny-missing-id '{"incident_id":"x","summary":"s","root_cause_hypothesis":"h","confidence":0.9,"evidence_refs":["a"],"proposed_action":{"type":"restart","params":{},"files":[]},"risk":"r","needs_human":false}'
expect deny-missing-id 3; reason deny-missing-id "missing action_id"
mkraw deny-id-not-string '{"incident_id":"x","summary":"s","root_cause_hypothesis":"h","confidence":0.9,"evidence_refs":["a"],"proposed_action":{"type":"restart","action_id":["restart_app"],"params":{},"files":[]},"risk":"r","needs_human":false}'
expect deny-id-not-string 3
# legacy command field: refused outright, even when the string is the exact allowlisted command (replaces deny-no-compose-args/other-stack/ws/suffix intents: a command string can no longer reach execution)
for c in "docker compose $CA restart app" "FAULT_ENDPOINT_500= docker compose $CA up -d --no-deps app" "docker compose restart app" "docker compose -p other -f docker-compose.yml restart app" " docker compose $CA restart app" "docker compose $CA restart app --timeout 1" "docker compose $CA restart app; rm -rf /"; do
  n="legacy-$(printf '%s' "$c" | cksum | cut -d' ' -f1)"; mk "$n" restart_app restart 0.99
  jq --arg c "$c" '.proposed_action.command=$c' "$T/$n/response.json" > "$T/$n/r2" && mv "$T/$n/r2" "$T/$n/response.json"
  expect "$n" 3; reason "$n" "legacy command field refused"
done
# legacy denied-pattern intents (db write, secrets, force push, prod deploy, metachar) as legacy commands, and as param values below
for c in "docker compose exec db psql -c 'DELETE FROM repos'" "cat .env" "git push --force origin main" "kubectl rollout restart deploy/app"; do
  n="legacy-deny-$(printf '%s' "$c" | cksum | cut -d' ' -f1)"; mkraw "$n" "$(jq -n --arg c "$c" '{incident_id:"x",summary:"s",root_cause_hypothesis:"h",confidence:0.99,evidence_refs:["a"],proposed_action:{type:"config_change",command:$c,files:[]},risk:"r",needs_human:false}')"
  expect "$n" 3; reason "$n" "legacy command field refused"
done
( unset COMPOSE_FILES COMPOSE_FILE; mk fail-closed restart_app restart 0.9; python3 -I "$D/policy-check.py" "$T/fail-closed" >/dev/null 2>&1; [ $? -eq 3 ] && grep -q 'COMPOSE_FILES not set' "$T/fail-closed/decision.json" ) && { pass=$((pass+1)); echo "ok   fail-closed without COMPOSE_FILES"; } || { fail=$((fail+1)); echo "FAIL fail-closed"; }
for bad in ":a.yml" "a.yml:" "a.yml::b.yml" "-f:b.yml" "a.yml:-x" "a b.yml" 'a.yml;rm'; do
  n="badcompose-$(printf '%s' "$bad" | cksum | cut -d' ' -f1)"; mk "$n" restart_app restart 0.9
  ( COMPOSE_FILES="$bad" python3 -I "$D/policy-check.py" "$T/$n" >/dev/null 2>&1; [ $? -eq 3 ] ) && { pass=$((pass+1)); echo "ok   invalid COMPOSE_FILES '$bad' fails closed"; } || { fail=$((fail+1)); echo "FAIL invalid COMPOSE_FILES '$bad'"; }
done
# params: neither shipped action takes any, so every param is unknown
mk deny-extra-param      restart_app restart 0.9 false '{"service":"db"}';   expect deny-extra-param 3; reason deny-extra-param "unknown param"
mk deny-param-metachar   fault_off config_change 0.9 false '{"x":"a;rm -rf /"}'; expect deny-param-metachar 3; reason deny-param-metachar "unknown param"
mkraw deny-params-array '{"incident_id":"x","summary":"s","root_cause_hypothesis":"h","confidence":0.9,"evidence_refs":["a"],"proposed_action":{"type":"restart","action_id":"restart_app","params":["x"],"files":[]},"risk":"r","needs_human":false}'; expect deny-params-array 3
# strict param validation, exercised with a test policy that declares an action with parameters
jq '.allowed_actions.set_level={types:["config_change"],env:{},argv:["docker","compose","{{COMPOSE_ARGS}}","run","{{param:level}}","{{param:name}}"],params:{level:{enum:["info","debug"]},name:{regex:"^[a-z]{1,10}$"}}}' "$D/autonomy-policy.yaml" > "$T/param-policy.json"
pp() { # name params-json want-exit
  mk "$1" set_level config_change 0.9 false "$2"; expect "$1" "$3" --policy "$T/param-policy.json"
}
pp param-ok '{"level":"debug","name":"abc"}' 0
okjq "param value becomes exactly one argv element" param-ok '.argv[-2:]==["debug","abc"] and (.command|endswith(" debug abc"))'
pp param-missing '{"level":"debug"}' 3
pp param-unknown-key '{"level":"debug","name":"abc","extra":"x"}' 3
pp param-not-in-enum '{"level":"trace","name":"abc"}' 3
pp param-regex-miss '{"level":"info","name":"ABC"}' 3
pp param-int '{"level":"info","name":5}' 3
pp param-null '{"level":null,"name":"abc"}' 3
pp param-nested '{"level":{"a":"info"},"name":"abc"}' 3
for v in 'info;id' 'info$(id)' 'info`id`' "info
id" 'info id' 'info|id' 'info&id' 'info>x' 'info\x' "info'x" 'info"x' '-x' '' 'a/../b*' 'info{a,b}'; do
  n="param-meta-$(printf '%s' "$v" | cksum | cut -d' ' -f1)"; mk "$n" set_level config_change 0.9 false "$(jq -nc --arg v "$v" '{level:$v,name:"abc"}')"
  expect "$n" 3 --policy "$T/param-policy.json"
done
jq '.allowed_actions.set_level.params.name={regex:".*"}' "$T/param-policy.json" > "$T/loose-policy.json"   # even a permissive rule cannot admit shell syntax
for v in 'a;b' 'a b' 'a$(id)' 'a`id`' "a
b"; do
  n="param-loose-$(printf '%s' "$v" | cksum | cut -d' ' -f1)"; mk "$n" set_level config_change 0.9 false "$(jq -nc --arg v "$v" '{level:"info",name:$v}')"
  expect "$n" 3 --policy "$T/loose-policy.json"
done
# full match (issue #203 D): a trailing newline or trailing text must not satisfy a pattern/regex rule
mkraw inc-id-newline "$(jq -nc '{incident_id:"x\n",summary:"s",root_cause_hypothesis:"h",confidence:0.9,evidence_refs:["a"],proposed_action:{type:"restart",action_id:"restart_app",params:{},files:[]},risk:"r",needs_human:false}')"
expect inc-id-newline 3; jq -e '.reasons|any(test("incident_id: pattern mismatch"))' "$T/inc-id-newline/decision.json" >/dev/null && { pass=$((pass+1)); echo "ok   incident_id with a trailing newline: pattern mismatch"; } || { fail=$((fail+1)); echo "FAIL incident_id newline reason"; }
mkraw inc-id-ok "$(jq -nc '{incident_id:"x",summary:"s",root_cause_hypothesis:"h",confidence:0.9,evidence_refs:["a"],proposed_action:{type:"restart",action_id:"restart_app",params:{},files:[]},risk:"r",needs_human:false}')"
expect inc-id-ok 0
jq '.allowed_actions.set_level.params.name={regex:"^[a-z]+$"}' "$T/param-policy.json" > "$T/nl-policy.json"
mk param-trailing-newline set_level config_change 0.9 false "$(jq -nc '{level:"info",name:"abc\n"}')"; expect param-trailing-newline 3 --policy "$T/nl-policy.json"
jq '.allowed_actions.set_level.params.name={regex:"[a-z]"}' "$T/param-policy.json" > "$T/prefix-policy.json"   # unanchored: must still be a FULL match
pp2() { mk "$1" set_level config_change 0.9 false "$2"; expect "$1" "$3" --policy "$T/prefix-policy.json"; }
pp2 param-prefix-only '{"level":"info","name":"a1"}' 3
pp2 param-single-char '{"level":"info","name":"a"}' 0
# confidence, needs_human, propose-only (they apply to a valid action too)
mk low-confidence restart_app restart 0.5;                  expect low-confidence 3
mk needs-human    restart_app restart 0.95 true;            expect needs-human 3
mk propose-only   none code_fix_pr 0.95;                    expect propose-only 3
mk propose-only-id restart_app code_fix_pr 0.95;            expect propose-only-id 3
mk propose-escalate none escalate 0.95;                     expect propose-escalate 3
# invalid JSON / schema violations
mkdir -p "$T/invalid-json"; echo '{not json' > "$T/invalid-json/response.json";                          expect invalid-json 3
mkdir -p "$T/missing-field"; echo '{"incident_id":"x"}' > "$T/missing-field/response.json";             expect missing-field 3
mk bad-confidence restart_app restart 1.7;                                             expect bad-confidence 3
mk bad-type restart_app nuke 0.9;                                                      expect bad-type 3
mkdir -p "$T/no-file";                                                                                  expect no-file 3
# confidence gate: anything that is not a finite number >= threshold escalates (NaN used to pass every comparison)
mkc() { # name raw-confidence-token
  mkraw "$1" "{\"incident_id\":\"$1\",\"summary\":\"s\",\"root_cause_hypothesis\":\"h\",\"confidence\":$2,\"evidence_refs\":[\"a\"],\"proposed_action\":{\"type\":\"restart\",\"action_id\":\"restart_app\",\"params\":{},\"files\":[]},\"risk\":\"r\",\"needs_human\":false}"
}
for pair in "nan:NaN" "inf:Infinity" "ninf:-Infinity" "true:true" "str:\"0.9\"" "null:null" "big:1e999" "nbig:-1e999" "neg:-0.5" "above:1.0000001" "below:0.6999999" "arr:[0.9]"; do
  mkc "conf-${pair%%:*}" "${pair#*:}"; expect "conf-${pair%%:*}" 3
done
jq -e '.reasons|any(test("non-finite"))' "$T/conf-nan/decision.json" >/dev/null && jq -e '.reasons|any(test("non-finite"))' "$T/conf-big/decision.json" >/dev/null && { pass=$((pass+1)); echo "ok   NaN/Infinity tokens and 1e999 are reported as non-finite numbers"; } || { fail=$((fail+1)); echo "FAIL non-finite reason"; }
mkdir -p "$T/conf-missing"; echo '{"incident_id":"x","summary":"s","root_cause_hypothesis":"h","evidence_refs":["a"],"proposed_action":{"type":"restart","action_id":"restart_app","params":{},"files":[]},"risk":"r","needs_human":false}' > "$T/conf-missing/response.json"; expect conf-missing 3
mkc conf-exact 0.7;   expect conf-exact 0     # exactly the threshold is allowed
mkc conf-one 1;       expect conf-one 0
mkc conf-justbelow 0.69; expect conf-justbelow 3
mkdir -p "$T/deep"; python3 -c "print('{\"incident_id\":' + '['*5000 + ']'*5000 + '}')" > "$T/deep/response.json"
python3 -I "$D/policy-check.py" "$T/deep" > "$T/deep/out" 2>"$T/deep/err"; rc=$?
[ $rc -eq 3 ] && jq -e '.decision=="escalate"' "$T/deep/decision.json" >/dev/null && ! grep -q Traceback "$T/deep/out" "$T/deep/err" && { pass=$((pass+1)); echo "ok   deeply nested response escalates cleanly"; } || { fail=$((fail+1)); echo "FAIL deep nesting (rc=$rc)"; head -c 300 "$T/deep/err"; }
# hostile action.lock written before the policy step (regular file, then dangling symlink): never claimed, never executed
mk lock-file restart_app restart 0.9; echo '{"action_id":"restart_app","params":{},"argv":["touch","'"$T"'/PWNED"],"env":{},"command":"touch x"}' > "$T/lock-file/action.lock"
expect lock-file 3; reason lock-file "already taken"
[ ! -e "$T/PWNED" ] && grep -q PWNED "$T/lock-file/action.lock" && { pass=$((pass+1)); echo "ok   pre-existing action.lock: escalated, left untouched, nothing run"; } || { fail=$((fail+1)); echo "FAIL pre-existing lock"; }
mk lock-link restart_app restart 0.9; ln -s "$T/PWNED-target" "$T/lock-link/action.lock"
expect lock-link 3
[ ! -e "$T/PWNED-target" ] && [ -L "$T/lock-link/action.lock" ] && { pass=$((pass+1)); echo "ok   action.lock symlink (dangling): escalated, target not created"; } || { fail=$((fail+1)); echo "FAIL lock symlink"; }
# max one automatic action: second run on the same incident must escalate
mk once restart_app restart 0.9; expect once 0; expect once 3
# global cooldown across incidents (separate dir so .last_action is isolated)
CD="$T/cd"; mkdir -p "$CD/a" "$CD/b" "$CD/c"
for i in a b c; do jq -n --arg id "$i" '{incident_id:$id,summary:"s",root_cause_hypothesis:"h",confidence:0.9,evidence_refs:["x"],proposed_action:{type:"restart",action_id:"restart_app",params:{},files:[]},risk:"low",needs_human:false}' > "$CD/$i/response.json"; done
cdrun() { GLOBAL_COOLDOWN_SECONDS="$2" python3 -I "$D/policy-check.py" "$CD/$1" "${@:3}" >/dev/null 2>&1; echo $?; }
[ "$(cdrun a 300)" = 0 ] && [ -f "$CD/.last_action" ] && { pass=$((pass+1)); echo "ok   cooldown: first action allowed + .last_action stamped"; } || { fail=$((fail+1)); echo "FAIL cooldown first"; }
[ "$(cdrun b 300)" = 3 ] && grep -q 'global action cooldown' "$CD/b/decision.json" && [ ! -f "$CD/b/action.lock" ] && { pass=$((pass+1)); echo "ok   cooldown: second incident escalated, no lock left"; } || { fail=$((fail+1)); echo "FAIL cooldown second"; }
[ "$(cdrun b 300 --no-claim)" = 3 ] && { pass=$((pass+1)); echo "ok   cooldown: dry-run also reports it"; } || { fail=$((fail+1)); echo "FAIL cooldown dry-run"; }
[ "$(cdrun b 0)" = 0 ] && { pass=$((pass+1)); echo "ok   cooldown: 0 disables"; } || { fail=$((fail+1)); echo "FAIL cooldown off"; }
echo "1 x" > "$CD/.last_action"   # ancient timestamp: cooldown over
[ "$(cdrun c 300)" = 0 ] && { pass=$((pass+1)); echo "ok   cooldown: expires"; } || { fail=$((fail+1)); echo "FAIL cooldown expiry"; }
for bad in nan inf -inf NaN Infinity garbage; do   # non-finite or non-numeric stamp: cannot prove the cooldown is over -> fail closed
  echo "$bad x" > "$CD/.last_action"; rm -f "$CD/c/action.lock" "$CD/c/decision.json"
  [ "$(cdrun c 300 --no-claim)" = 3 ] && grep -q 'cooldown state unreadable' "$CD/c/decision.json" && { pass=$((pass+1)); echo "ok   cooldown: .last_action '$bad' fails closed (cooldown state unreadable)"; } || { fail=$((fail+1)); echo "FAIL cooldown non-finite $bad"; }
done
# unreadable .last_action fails closed (only "not found" is free); skipped as root, which can read mode 000
if [ "$(id -u)" != 0 ]; then
  rm -f "$CD/c/action.lock" "$CD/c/decision.json"; echo "$(date +%s) x" > "$CD/.last_action"; chmod 000 "$CD/.last_action"
  [ "$(cdrun c 300)" = 3 ] && [ ! -f "$CD/c/action.lock" ] && grep -q 'cooldown state unreadable' "$CD/c/decision.json" && ! grep -q 'left)' "$CD/c/decision.json" && { pass=$((pass+1)); echo "ok   cooldown: unreadable .last_action fails closed with an honest reason"; } || { fail=$((fail+1)); echo "FAIL cooldown unreadable"; }
  chmod 600 "$CD/.last_action"
fi
# future-dated stamp: the reported wait equals the enforced wait (ts - now + cooldown), not the bare cooldown
rm -f "$CD/c/action.lock" "$CD/c/decision.json"; echo "$(( $(date +%s) + 3600 )) x" > "$CD/.last_action"
[ "$(cdrun c 300 --no-claim)" = 3 ] && W="$(jq -r '.reasons[0]' "$CD/c/decision.json" | sed -n 's/.*(\([0-9]*\)s left).*/\1/p')" && [ "${W:-0}" -ge 3890 ] && [ "$W" -le 3900 ] && { pass=$((pass+1)); echo "ok   cooldown: future stamp reports the real wait ($W s)"; } || { fail=$((fail+1)); echo "FAIL cooldown future stamp (W=${W:-})"; }
# malformed policy structure (issue #203 A): exit 1 with a clear message, audited, no allow, no decision.json, no action.lock
badpol() { # name jq-filter-applied-to-the-shipped-policy [want-message-substring]
  mk "bp-$1" restart_app restart 0.9; jq "$2" "$D/autonomy-policy.yaml" > "$T/bp-$1/policy.json"
  python3 -I "$D/policy-check.py" "$T/bp-$1" --policy "$T/bp-$1/policy.json" > "$T/bp-$1/out" 2>&1; rc=$?
  if [ $rc -eq 1 ] && [ ! -e "$T/bp-$1/action.lock" ] && [ ! -e "$T/bp-$1/decision.json" ] && ! grep -q '"decision": "allow"' "$T/bp-$1/out" && grep -q 'policy invalid' "$T/bp-$1/out" && grep -q "${3:-policy invalid}" "$T/bp-$1/out"; then
    pass=$((pass+1)); echo "ok   malformed policy refused: $1"; else fail=$((fail+1)); echo "FAIL malformed policy $1 (rc=$rc)"; cat "$T/bp-$1/out"; fi
}
badpol argv-string        '.allowed_actions.restart_app.argv="docker compose restart app"' "argv must be a non-empty list"
badpol types-string       '.allowed_actions.restart_app.types="restart"' "types must be a non-empty list"
badpol argv-missing       'del(.allowed_actions.restart_app.argv)' "missing argv"
badpol argv-empty         '.allowed_actions.restart_app.argv=[]' "argv must be a non-empty list"
badpol argv-nonstring     '.allowed_actions.restart_app.argv=["docker",5]' "argv must be a non-empty list"
badpol types-empty        '.allowed_actions.restart_app.types=[]' "types must be a non-empty list"
badpol env-nonstring      '.allowed_actions.fault_off.env={"FAULT_ENDPOINT_500":1}' "env must be an object"
badpol params-list        '.allowed_actions.restart_app.params=["x"]' "params must be an object"
badpol param-rule-kind    '.allowed_actions.restart_app.params={"x":{"glob":"*"}}' "rule must be exactly one of"
badpol unknown-action-key '.allowed_actions.restart_app.shell="sh -c x"' "unknown key"
badpol unknown-top-key    '.auto_approve=true' "unknown top-level key"
badpol version-wrong      '.version=99' "unsupported policy version"
badpol version-missing    'del(.version)' "missing top-level key version"
badpol threshold-string   '.confidence_threshold="0.7"' "confidence_threshold must be"
badpol threshold-range    '.confidence_threshold=-1' "confidence_threshold must be"
badpol threshold-bool     '.confidence_threshold=true' "confidence_threshold must be"
badpol cooldown-string    '.global_cooldown_seconds="soon"' "global_cooldown_seconds must be"
badpol cooldown-negative  '.global_cooldown_seconds=-5' "global_cooldown_seconds must be"
badpol actions-list       '.allowed_actions=[]' "allowed_actions must be an object"
badpol template-unknown   '.allowed_actions.restart_app.argv=["docker","{{param:nope}}"]' "template element not understood"
printf '{"version":2,"version":2}' > "$T/dupkey.json"; mk bp-dupkey restart_app restart 0.9
python3 -I "$D/policy-check.py" "$T/bp-dupkey" --policy "$T/dupkey.json" >/dev/null 2>&1; rc=$?
[ $rc -eq 1 ] && [ ! -e "$T/bp-dupkey/action.lock" ] && { pass=$((pass+1)); echo "ok   malformed policy refused: duplicate key"; } || { fail=$((fail+1)); echo "FAIL duplicate key policy (rc=$rc)"; }
printf 'version: 2\nactions: {}\n' > "$T/notjson.yaml"; mk bp-yaml restart_app restart 0.9
python3 -I "$D/policy-check.py" "$T/bp-yaml" --policy "$T/notjson.yaml" >/dev/null 2>&1; rc=$?
[ $rc -eq 1 ] && [ ! -e "$T/bp-yaml/action.lock" ] && { pass=$((pass+1)); echo "ok   non-JSON policy refused (no PyYAML path)"; } || { fail=$((fail+1)); echo "FAIL non-JSON policy (rc=$rc)"; }
# a crash inside policy-check must exit 1 (not 0/3) and leave an audit line
mkdir -p "$T/crash"; echo '{}' > "$T/crash/response.json"; echo '{broken' > "$T/crash/bad-policy.json"
python3 -I "$D/policy-check.py" "$T/crash" --policy "$T/crash/bad-policy.json" >/dev/null 2>&1; rc=$?
[ $rc -eq 1 ] && grep -q policy_error "$T/crash/audit.jsonl" && { pass=$((pass+1)); echo "ok   policy crash exits 1 + audited"; } || { fail=$((fail+1)); echo "FAIL policy crash (rc=$rc)"; }
# concurrent claims: 8 incidents race for the global cooldown; exactly one may win (flock), the rest escalate
CC="$T/cc"; mkdir -p "$CC"
for i in 1 2 3 4 5 6 7 8; do mkdir -p "$CC/i$i"; jq -n --arg id "i$i" '{incident_id:$id,summary:"s",root_cause_hypothesis:"h",confidence:0.9,evidence_refs:["x"],proposed_action:{type:"restart",action_id:"restart_app",params:{},files:[]},risk:"low",needs_human:false}' > "$CC/i$i/response.json"; done
for i in 1 2 3 4 5 6 7 8; do ( GLOBAL_COOLDOWN_SECONDS=300 python3 -I "$D/policy-check.py" "$CC/i$i" >/dev/null 2>&1; echo $? > "$CC/i$i.rc" ) & done; wait
WON=$(cat "$CC"/i?.rc | grep -c '^0$'); ESC=$(cat "$CC"/i?.rc | grep -c '^3$'); LOCKS=$(ls "$CC"/i*/action.lock 2>/dev/null | wc -l | tr -d ' ')
[ "$WON" = 1 ] && [ "$ESC" = 7 ] && [ "$LOCKS" = 1 ] && { pass=$((pass+1)); echo "ok   cooldown: 8 concurrent claims -> 1 allowed, 7 escalated, 1 lock"; } || { fail=$((fail+1)); echo "FAIL concurrent claims (won=$WON esc=$ESC locks=$LOCKS)"; }
# escalation emits a packet and audit lines with the required fields
[ -f "$T/low-confidence/escalation-packet.json" ] && { pass=$((pass+1)); echo "ok   escalation packet written"; } || { fail=$((fail+1)); echo "FAIL no escalation packet"; }
jq -e 'select(.ts and .kind and .detail and .decision)' "$T/low-confidence/audit.jsonl" >/dev/null && { pass=$((pass+1)); echo "ok   audit.jsonl fields"; } || { fail=$((fail+1)); echo "FAIL audit fields"; }
echo "policy tests: $pass passed, $fail failed"; [ $fail -eq 0 ]
