#!/usr/bin/env bash
# Offline checks for deploy/aws/main.yaml and the change-set guard of deploy/aws/deploy.sh (#207).
# No AWS call. Needs bash, awk, jq and sha256sum or shasum; runs cfn-lint too when it is installed.
# Usage: bash deploy/aws/test-template.sh
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TPL="$HERE/main.yaml"
T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
fails=0; n=0
ok()  { n=$((n+1)); echo "ok   $n $1"; }
bad() { n=$((n+1)); fails=$((fails+1)); echo "FAIL $n $1"; }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -d' ' -f1; else shasum -a 256 | cut -d' ' -f1; fi; }

# --- 1. UserData is frozen ---------------------------------------------------------------------
# The UserData block of AppInstance (from "      UserData:" to the next key at 6 spaces or less,
# trailing blank lines dropped) must stay byte-identical to the template that is live on stack
# micro-git-prod (deployed from 5828285). Any edit makes the change set report AppInstance as
# "Replacement: Conditional" (UserData is a conditionalCreateOnly property of AWS::EC2::Instance)
# and deploy.sh aborts the promote. Put instance changes in remote-deploy.sh instead. If a
# replacement is really intended (new stack, data backed up), change this hash in the same commit.
FROZEN_USERDATA_SHA256=3115634041fef0a8fa39d3464cb931b6b0c10ce2cf491967d5b88ee948c11912
userdata_block() {
  awk '/^      UserData:$/ { f = 1; print; next }
       f { match($0, /^ */); if (RLENGTH <= 6 && $0 !~ /^[ \t]*$/) exit
           buf = buf $0 "\n"; if ($0 !~ /^[ \t]*$/) { printf "%s", buf; buf = "" } }' "$1"
}
got="$(userdata_block "$TPL" | sha)"
check "AppInstance UserData is byte-identical to the live template (frozen, #207)" '[ "$got" = "$FROZEN_USERDATA_SHA256" ]'
[ "$got" = "$FROZEN_USERDATA_SHA256" ] || echo "     got $got; see the comment above FROZEN_USERDATA_SHA256"
check "exactly one UserData block" '[ "$(grep -c "^      UserData:$" "$TPL")" -eq 1 ]'

# --- 2. The data disk is optional and off by default ------------------------------------------------
resource_block() { awk -v r="  $1:" '$0 == r { f = 1; print; next } f && /^  [A-Za-z]/ { exit } f && /^[A-Za-z]/ { exit } f' "$TPL"; }
param_block() { awk -v r="  $1:" '$0 == r { f = 1; next } f && /^  [A-Za-z]/ { exit } f && /^[A-Za-z]/ { exit } f' "$TPL"; }
check "parameter EnableDataVolume defaults to false" 'param_block EnableDataVolume | grep -q "Default: \"false\""'
check "condition HasDataVolume = EnableDataVolume == true" 'grep -q "^  HasDataVolume: !Equals \[!Ref EnableDataVolume, \"true\"\]$" "$TPL"'
check "DataVolume has Condition HasDataVolume" 'resource_block DataVolume | grep -q "^    Condition: HasDataVolume$"'
check "DataVolumeAttachment has Condition HasDataVolume" 'resource_block DataVolumeAttachment | grep -q "^    Condition: HasDataVolume$"'
check "output DataVolumeId has Condition HasDataVolume" 'awk "/^Outputs:/{f=1} f" "$TPL" | grep -A1 "^  DataVolumeId:$" | grep -q "Condition: HasDataVolume"'
check "DataVolume keeps a snapshot on delete and replace" 'resource_block DataVolume | grep -q "DeletionPolicy: Snapshot" && resource_block DataVolume | grep -q "UpdateReplacePolicy: Snapshot"'
check "AppInstance keeps RetainExceptOnCreate / Retain" 'resource_block AppInstance | grep -q "DeletionPolicy: RetainExceptOnCreate" && resource_block AppInstance | grep -q "UpdateReplacePolicy: Retain"'
check "AppInstance ImageId comes from LatestAmiId (no SSM-resolved AMI)" 'resource_block AppInstance | grep -q "ImageId: !Ref LatestAmiId$" && ! grep -q "AWS::SSM::Parameter::Value" "$TPL"'
check "nothing references DataVolume outside its condition" '[ "$(grep -c "!Ref DataVolume$" "$TPL")" -eq 2 ]'

# --- 3. deploy.sh guard on saved change sets (deploy.sh --check-change-set, no AWS) ------------------
cs() { printf '{"Changes":[%s]}' "$1" > "$T/cs.json"; }
rc_of() { bash "$HERE/deploy.sh" --check-change-set "$T/cs.json" > "$T/out" 2>&1; echo $?; }
res() { # logical-id action replacement [target-name requires-recreation]
  printf '{"Type":"Resource","ResourceChange":{"Action":"%s","LogicalResourceId":"%s","Replacement":"%s","Details":[{"Target":{"Attribute":"Properties","Name":"%s","RequiresRecreation":"%s"},"ChangeSource":"DirectModification"}]}}' \
    "$2" "$1" "$3" "${4:-X}" "${5:-Never}"
}
# The failing promote (run 43941079090): UserData edited -> AppInstance Replacement Conditional.
cs "$(res AppInstance Modify Conditional UserData Conditionally)"
rc="$(rc_of)"
check "guard: UserData edit (Replacement Conditional) is refused" '[ "$rc" = 1 ] && grep -q "UNSAFE.*AppInstance" "$T/out" && grep -q "UserData: RequiresRecreation Conditionally" "$T/out"'
# The first apply of this branch on the live stack, as expected: role and profile replaced (path),
# instance modified in place (IamInstanceProfile, RequiresRecreation Never).
cs "$(res InstanceRole Modify True Path Always),$(res InstanceProfile Modify True Path Always),$(res AppInstance Modify False IamInstanceProfile Never)"
rc="$(rc_of)"
check "guard: IAM path change with in-place profile swap is allowed" '[ "$rc" = 0 ] && grep -q "^safe" "$T/out"'
cs "$(res AppInstance Modify True ImageId Always)"
check "guard: AMI change (Replacement True) is refused" '[ "$(rc_of)" = 1 ]'
cs "$(res AppInstance Remove "")"
check "guard: removing AppInstance is refused" '[ "$(rc_of)" = 1 ]'
cs "$(res DataVolume Remove ""),$(res DataVolumeAttachment Remove "")"
rc="$(rc_of)"
check "guard: removing the data disk (EnableDataVolume on -> off) is refused" '[ "$rc" = 1 ] && grep -q "UNSAFE.*DataVolume DataVolumeAttachment" "$T/out"'
cs "$(res DataVolume Add ""),$(res DataVolumeAttachment Add "")"
check "guard: adding the data disk is allowed" '[ "$(rc_of)" = 0 ]'
cs "$(res DataVolume Modify False Size Never)"
check "guard: resizing the data disk in place is allowed" '[ "$(rc_of)" = 0 ]'
cs ""
check "guard: empty change set is allowed" '[ "$(rc_of)" = 0 ]'
echo 'not json' > "$T/cs.json"
check "guard: unreadable file is an error (exit 2)" '[ "$(rc_of)" = 2 ]'

# --- 4. Syntax of the deploy scripts, and cfn-lint when available ------------------------------------
for f in deploy.sh destroy.sh bootstrap.sh preflight.sh remote-deploy.sh test-remote-deploy.sh; do
  check "bash -n $f" 'bash -n "$HERE/$f"'
done
if command -v cfn-lint >/dev/null 2>&1; then
  check "cfn-lint main.yaml" 'cfn-lint --regions ap-southeast-2 -- "$TPL" > "$T/lint" 2>&1 || { cat "$T/lint"; false; }'
else
  echo "skip cfn-lint (not installed)"
fi

echo
if [ "$fails" -eq 0 ]; then echo "all $n template checks passed"; else echo "$fails of $n failed"; exit 1; fi
