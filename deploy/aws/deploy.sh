#!/usr/bin/env bash
# shellcheck disable=SC2016  # backticks in single quotes are JMESPath literals, not shell
# One-command AWS deploy of micro-git to stack micro-git-prod in ap-southeast-2, without
# GitHub OIDC (an organization SCP denies oidc-provider/* in this account).
#
#   deploy/aws/deploy.sh                       dry-run (default): read-only checks, prints the plan
#   deploy/aws/deploy.sh --apply --account ID  does it: bootstrap, secret, stack, image, rollout, smoke
#
# Steps (each one idempotent, so re-running is the update path):
#   1 account guard + Free plan check   2 preflight.sh --no-oidc   3 bootstrap.sh --no-oidc
#   4 JWT secret in SSM (SecureString, generated once, never printed)
#   5 stack via change set (refuses to replace/remove AppInstance or the data disk)   6 image build + push to ECR
#   7 remote-deploy.sh through SSM Run Command with APP_COMMIT=<sha> (and the data volume id, if any)
#   8 smoke: /health X-Build == sha, / 200, /api/repos 200, /api/auth/me 401
#
# Never touches the hand-made stack `mg`. Region and stack name are fixed on purpose.
set -euo pipefail

readonly REGION=ap-southeast-2
readonly STACK=micro-git-prod

usage() {
  cat <<USAGE
Usage: $0 [--dry-run | --apply] [--account ID] [--ref GIT-REF] [--profile NAME] [--rotate-jwt] [--ci]
          [--data-volume on|off]
       $0 --check-change-set FILE
  --dry-run     read-only checks and the plan (default)
  --apply       create/update everything; needs --account (or MG_AWS_ACCOUNT, or a typed answer on a terminal)
  --account     expected AWS account id; aborts if the session is another account
  --ref         commit to deploy (default HEAD); the image is built from exactly that commit (git archive)
  --profile     AWS CLI profile
  --rotate-jwt  replace the JWT secret in SSM (logs every user out)
  --ci          GitHub Actions mode: skip preflight and bootstrap (the CI user has no IAM rights),
                Free plan check is informational only
  --data-volume set the stack parameter EnableDataVolume (on: create and attach the data disk,
                remote-deploy.sh mounts it at /opt/mg/data). Without the flag the stack keeps its
                current value (default off for a new stack). off is refused once the disk exists.
  --check-change-set FILE
                offline: read the JSON of 'aws cloudformation describe-change-set' from FILE, print
                why it would replace or remove a protected resource, exit 1 if it would (no AWS call)
Region $REGION and stack $STACK are fixed.
USAGE
}

MODE=dry-run; CHECK_CS=""; WANT_ACCOUNT="${MG_AWS_ACCOUNT:-}"; REF=HEAD; PROFILE=""; ROTATE_JWT=""; CI_MODE=""; DATA_VOLUME_PARAM=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) MODE=dry-run ;;
    --apply) MODE=apply ;;
    --account) WANT_ACCOUNT="${2:?--account needs a value}"; shift ;;
    --ref) REF="${2:?--ref needs a value}"; shift ;;
    --profile) PROFILE="${2:?--profile needs a value}"; shift ;;
    --rotate-jwt) ROTATE_JWT=1 ;;
    --ci) CI_MODE=1 ;;
    --check-change-set) CHECK_CS="${2:?--check-change-set needs a file}"; shift ;;
    --data-volume)
      case "${2:-}" in on) DATA_VOLUME_PARAM=true ;; off) DATA_VOLUME_PARAM=false ;;
        *) echo "--data-volume needs on or off" >&2; exit 2 ;; esac
      shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

# Resources a stack update must never replace or remove: the instance (Postgres starts on its root
# disk) and the data disk with its attachment (Postgres after the move). Replacement "Conditional"
# counts too: for AppInstance that is what any UserData edit produces (#207).
readonly PROTECTED="AppInstance DataVolume DataVolumeAttachment"
# Reads describe-change-set JSON from $1. Prints the protected logical ids the change set would remove
# or replace (space separated, empty if none) on stdout, and the reasons on stderr.
protected_changes() { # change-set.json
  local bad
  # Some jq builds exit 0 on a parse error: require an object with a Changes array explicitly.
  [ "$(jq -r 'if type == "object" and (.Changes | type) == "array" then "ok" else empty end' "$1" 2>/dev/null)" = ok ] \
    || return 2
  bad="$(jq -r --arg p "$PROTECTED" '($p | split(" ")) as $prot | [(.Changes // [])[].ResourceChange
    | select(. != null) | select(.LogicalResourceId as $id | $prot | index($id))
    | select(.Action == "Remove" or .Replacement == "True" or .Replacement == "Conditional")
    | .LogicalResourceId] | unique | join(" ")' "$1")" || return 2
  if [ -n "$bad" ]; then
    jq -r --arg b "$bad" '($b | split(" ")) as $ids | (.Changes // [])[].ResourceChange | select(. != null)
      | select(.LogicalResourceId as $id | $ids | index($id))
      | "  \(.LogicalResourceId): Action \(.Action), Replacement \(.Replacement // "-")",
        (.Details[]? | "    \(.Target.Attribute)/\(.Target.Name // "-"): RequiresRecreation \(.Target.RequiresRecreation // "-"), \(.ChangeSource // "-") \(.CausingEntity // "")")' \
      "$1" >&2 || true
  fi
  printf '%s\n' "$bad"
}
if [ -n "$CHECK_CS" ]; then
  bad="$(protected_changes "$CHECK_CS")" || { echo "error: cannot read $CHECK_CS as change-set JSON" >&2; exit 2; }
  if [ -n "$bad" ]; then echo "UNSAFE: the change set would replace or remove $bad"; exit 1; fi
  echo "safe: no protected resource ($PROTECTED) is replaced or removed"; exit 0
fi

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(git -C "$HERE" rev-parse --show-toplevel)"
export AWS_PAGER=""
AWS=(aws --region "$REGION")
[ -n "$PROFILE" ] && AWS+=(--profile "$PROFILE")
PFX=()
[ -n "$PROFILE" ] && PFX=(--profile "$PROFILE")

step() { echo; echo "== $*"; }
die() { echo "error: $*" >&2; exit 1; }
plan() { printf '[dry-run]'; printf ' %q' "$@"; printf '\n'; }
dry() { [ "$MODE" != apply ]; }

# One run at a time (a git hook and a scheduled run could overlap).
LOCK="${TMPDIR:-/tmp}/mg-deploy-$STACK.lock"
# Per machine only: a local --apply can still overlap the CI job (that one is serialized by the
# workflow's concurrency group). Dry-runs are read-only and take no lock.
WORK="$(mktemp -d)"
if [ "$MODE" = apply ]; then
  mkdir "$LOCK" 2>/dev/null || { rm -rf "$WORK"; die "another deploy of $STACK is running (lock $LOCK; remove it if that run is dead)"; }
  trap 'rm -rf "$LOCK" "$WORK"' EXIT
else
  trap 'rm -rf "$WORK"' EXIT
fi

SHA="$(git -C "$ROOT" rev-parse --verify "$REF^{commit}")" || die "unknown git ref $REF"
SHA7="${SHA:0:7}"

step "1/8 Account, plan, commit"
ACCOUNT="$("${AWS[@]}" sts get-caller-identity --query Account --output text 2>/dev/null)" \
  || die "no usable AWS session: run 'aws login' (or pass --profile) and retry"
[[ "$ACCOUNT" =~ ^[0-9]{12}$ ]] || die "unexpected account id"
echo "account $ACCOUNT, region $REGION, stack $STACK, mode $MODE"
if [ -n "$WANT_ACCOUNT" ] && [ "$WANT_ACCOUNT" != "$ACCOUNT" ]; then
  die "session account $ACCOUNT is not the expected $WANT_ACCOUNT; nothing was changed"
fi
if [ "$MODE" = apply ] && [ -z "$WANT_ACCOUNT" ]; then
  [ -t 0 ] || die "--apply without a terminal needs --account $ACCOUNT (or MG_AWS_ACCOUNT)"
  printf 'Type the account id (%s) to deploy: ' "$ACCOUNT"; read -r ans || ans=""
  [ "$ans" = "$ACCOUNT" ] || die "confirmation did not match; nothing was changed"
fi
[ "$STACK" != mg ] || die "refusing to touch the hand-made stack mg"

plan_state="$(aws ${PFX[@]+"${PFX[@]}"} freetier get-account-plan-state --region us-east-1 \
  --query '[accountPlanType, accountPlanStatus, accountPlanRemainingCredits.amount]' --output text 2>/dev/null || true)"
if [ -n "$plan_state" ]; then
  echo "account plan: $plan_state (type, status, remaining credits USD)"
  if [ "$(echo "$plan_state" | awk '{print $1}')" != FREE ]; then
    if [ -n "$CI_MODE" ]; then echo "WARNING: account is not on the Free plan" >&2
    else die "account is not on the Free plan ($plan_state): stopping, a deploy could cost money"; fi
  fi
elif [ -n "$CI_MODE" ]; then
  echo "note: Free plan state not readable with these credentials (CI user); not checked"
else
  die "cannot read the Free plan state (aws freetier get-account-plan-state); stopping"
fi

echo "commit $SHA ($REF)"
if [ "$REF" = HEAD ] && ! git -C "$ROOT" diff --quiet HEAD -- 2>/dev/null; then
  echo "note: uncommitted changes are NOT deployed; the image is built from commit $SHA7 only"
fi

if [ -z "$CI_MODE" ]; then
  step "2/8 Preflight (read-only)"
  "$HERE/preflight.sh" ${PFX[@]+"${PFX[@]}"} --region "$REGION" --stack-name "$STACK" --no-oidc \
    || die "preflight failed; fix the [FAIL] lines above"

  step "3/8 Bootstrap: policy mg-instance-boundary, role cfn-deploy-$STACK (no OIDC)"
  if dry; then
    "$HERE/bootstrap.sh" ${PFX[@]+"${PFX[@]}"} --region "$REGION" --stack-name "$STACK" --no-oidc | sed -n '/^[0-9]\/5/,/^Dry run/p'
  else
    "$HERE/bootstrap.sh" ${PFX[@]+"${PFX[@]}"} --apply --confirm-account "$ACCOUNT" --region "$REGION" --stack-name "$STACK" --no-oidc \
      | sed -n '/^[0-9]\/5/,/^Done/p'
  fi
else
  step "2/8 + 3/8 Preflight and bootstrap: skipped (--ci)"
fi
CFN_ROLE_ARN="arn:aws:iam::$ACCOUNT:role/cfn-deploy-$STACK"

step "4/8 JWT secret /$STACK/jwt-secret (SecureString)"
PARAM="/$STACK/jwt-secret"
have="$("${AWS[@]}" ssm describe-parameters --parameter-filters "Key=Name,Option=Equals,Values=$PARAM" \
  --query 'Parameters[0].Type' --output text)"
if [ "$have" = SecureString ] && [ -z "$ROTATE_JWT" ]; then
  echo "exists (SecureString), kept; --rotate-jwt replaces it"
elif [ "$have" != None ] && [ "$have" != SecureString ]; then
  die "$PARAM exists with type $have, expected SecureString; fix it by hand"
elif dry; then
  echo "[dry-run] openssl rand -hex 32 | aws ssm put-parameter --name $PARAM --type SecureString --overwrite --value file:///dev/stdin (value never printed)"
else
  # Generated and piped straight in: never in argv, a file, a variable or the output.
  openssl rand -hex 32 | tr -d '\n' | "${AWS[@]}" ssm put-parameter --name "$PARAM" --type SecureString \
    --overwrite --value file:///dev/stdin --query Version --output text | sed 's/^/stored, parameter version /'
fi

step "5/8 Stack $STACK (change set through role cfn-deploy-$STACK)"
stack_status() { # NONE only when the stack does not exist; any other error (denied, throttled) stops
  local out
  if out="$("${AWS[@]}" cloudformation describe-stacks --stack-name "$STACK" --query 'Stacks[0].StackStatus' --output text 2>&1)"; then
    echo "$out"
  elif printf '%s' "$out" | grep -q 'does not exist'; then
    echo NONE
  else
    echo "describe-stacks failed: $(printf '%s' "$out" | head -n1)" >&2; return 1
  fi
}
st="$(stack_status)" || die "cannot read the stack status"
echo "current status: $st"
case "$st" in
  ROLLBACK_COMPLETE|REVIEW_IN_PROGRESS)
    # A failed first create (AppInstance is deleted on a failed create) or an abandoned change set: nothing to keep.
    if [ -n "$CI_MODE" ]; then die "stack is $st; run deploy/aws/deploy.sh --apply locally (it recreates it)"; fi
    if dry; then plan aws cloudformation delete-stack --stack-name "$STACK" --role-arn "$CFN_ROLE_ARN"; st=NONE
    else
      echo "stack is $st (failed first create): deleting it so it can be created again"
      "${AWS[@]}" cloudformation delete-stack --stack-name "$STACK" --role-arn "$CFN_ROLE_ARN"
      "${AWS[@]}" cloudformation wait stack-delete-complete --stack-name "$STACK"
      st=NONE
    fi ;;
  NONE|CREATE_COMPLETE|UPDATE_COMPLETE|UPDATE_ROLLBACK_COMPLETE) ;;
  *) die "stack is $st; wait for it to settle or fix it by hand, then re-run" ;;
esac

if [ "$st" = NONE ]; then
  existed=false
  ami="$("${AWS[@]}" ssm get-parameter --name /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64 \
    --query Parameter.Value --output text)"
  echo "new stack: latest Amazon Linux 2023 AMI $ami"
else
  existed=true
  iid="$("${AWS[@]}" cloudformation describe-stack-resource --stack-name "$STACK" --logical-resource-id AppInstance \
    --query StackResourceDetail.PhysicalResourceId --output text)"
  ami="$("${AWS[@]}" ec2 describe-instances --instance-ids "$iid" --query 'Reservations[0].Instances[0].ImageId' --output text)"
  echo "existing stack: keeping the instance's AMI $ami (a new AMI would replace the instance and wipe Postgres)"
fi
[[ "$ami" =~ ^ami-[0-9a-f]+$ ]] || die "unexpected AMI id '$ami'"

"${AWS[@]}" cloudformation validate-template --template-body "file://$HERE/main.yaml" >/dev/null
echo "template valid"
# Parameters not given here keep their current stack value (aws cloudformation deploy), so a CI run
# without --data-volume never switches the data disk on or off.
OVERRIDES=("LatestAmiId=$ami")
[ -n "$DATA_VOLUME_PARAM" ] && OVERRIDES+=("EnableDataVolume=$DATA_VOLUME_PARAM")
if dry; then
  plan aws cloudformation deploy --template-file deploy/aws/main.yaml --stack-name "$STACK" --role-arn "$CFN_ROLE_ARN" \
    --capabilities CAPABILITY_IAM --parameter-overrides "${OVERRIDES[@]}" --no-execute-changeset --no-fail-on-empty-changeset
  echo "[dry-run] then: refuse if $PROTECTED would be removed or replaced (True or Conditional), execute-change-set, wait stack-$([ $existed = true ] && echo update || echo create)-complete"
else
  rc=0
  "${AWS[@]}" cloudformation deploy --template-file "$HERE/main.yaml" --stack-name "$STACK" \
    --role-arn "$CFN_ROLE_ARN" --capabilities CAPABILITY_IAM --parameter-overrides "${OVERRIDES[@]}" \
    --no-execute-changeset --no-fail-on-empty-changeset > "$WORK/cs.out" 2>&1 || rc=$?
  cat "$WORK/cs.out"
  arn="$(grep -oE 'arn:aws[^ ]*:changeSet/[^ ]+' "$WORK/cs.out" | head -n1 || true)"
  if [ "$rc" -eq 0 ] && [ -z "$arn" ] && grep -q 'No changes to deploy' "$WORK/cs.out"; then
    echo "no stack changes"
    "${AWS[@]}" cloudformation list-change-sets --stack-name "$STACK" \
      --query 'Summaries[?Status==`FAILED`].ChangeSetId' --output text | tr '\t' '\n' | while read -r id; do
        [ -n "$id" ] && [ "$id" != None ] && "${AWS[@]}" cloudformation delete-change-set --change-set-name "$id" || true
      done
  elif [ "$rc" -ne 0 ] || [ -z "$arn" ]; then
    "${AWS[@]}" cloudformation list-change-sets --stack-name "$STACK" \
      --query 'Summaries[?Status==`FAILED`].[ChangeSetName,StatusReason]' --output text 2>/dev/null || true
    die "creating the change set failed (exit $rc)"
  else
    "${AWS[@]}" cloudformation describe-change-set --change-set-name "$arn" --output json > "$WORK/cs.json"
    # Prints why (property, RequiresRecreation, what caused it) on stderr, so the owner can review by hand.
    bad="$(protected_changes "$WORK/cs.json")" || die "cannot parse the change set"
    if [ -n "$bad" ]; then
      "${AWS[@]}" cloudformation delete-change-set --change-set-name "$arn" || true
      die "the change set would replace or remove $bad (Postgres lives on it); aborted, review by hand"
    fi
    echo "change set is safe ($PROTECTED kept where present); executing"
    "${AWS[@]}" cloudformation execute-change-set --change-set-name "$arn"
    if ! "${AWS[@]}" cloudformation wait "stack-$([ $existed = true ] && echo update || echo create)-complete" --stack-name "$STACK"; then
      echo "stack operation failed; failed events:" >&2
      "${AWS[@]}" cloudformation describe-stack-events --stack-name "$STACK" \
        --query 'StackEvents[?contains(ResourceStatus, `FAILED`)].[LogicalResourceId,ResourceStatus,ResourceStatusReason]' \
        --output text | head -n 15 >&2
      die "stack is $(stack_status)"
    fi
    echo "stack is $(stack_status)"
  fi
  # Server-side guard as well (the client-side check above can be skipped by anyone holding
  # ExecuteChangeSet): the stack policy denies replacing or deleting the protected resources. Local
  # runs only; the CI user has no SetStackPolicy right.
  if [ -z "$CI_MODE" ]; then
    "${AWS[@]}" cloudformation set-stack-policy --stack-name "$STACK" --stack-policy-body '{"Statement":[
      {"Effect":"Deny","Action":["Update:Replace","Update:Delete"],"Principal":"*","Resource":[
        "LogicalResourceId/AppInstance","LogicalResourceId/DataVolume","LogicalResourceId/DataVolumeAttachment"]},
      {"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}]}'
    echo "stack policy: $PROTECTED cannot be replaced or deleted by a stack update"
  fi
fi

if dry && [ "$st" = NONE ]; then
  step "6/8 - 8/8 (planned; the stack does not exist yet)"
  echo "[dry-run] git archive $SHA7 | docker build --platform linux/amd64 -t <account>.dkr.ecr.$REGION.amazonaws.com/$STACK-app:$SHA7 -; docker push"
  echo "[dry-run] ssm send-command AWS-RunShellScript: remote-deploy.sh $STACK $REGION <repo>:$SHA7 schema.sql $SHA"
  echo "[dry-run] smoke: /health X-Build == $SHA, / 200, /api/repos 200, /api/auth/me 401"
  echo; echo "Dry run only, nothing was changed. Re-run with --apply --account $ACCOUNT."
  exit 0
fi

out() { "${AWS[@]}" cloudformation describe-stacks --stack-name "$STACK" \
  --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue | [0]" --output text; }
INSTANCE_ID="$(out InstanceId)"; REPO_URI="$(out EcrRepositoryUri)"; APP_URL="$(out AppUrl)"
DATA_VOLUME_ID="$(out DataVolumeId)"   # "None" unless EnableDataVolume=true
if [ "$DATA_VOLUME_ID" = None ] || [ -z "$DATA_VOLUME_ID" ]; then DATA_VOLUME_ID=""
elif ! [[ "$DATA_VOLUME_ID" =~ ^vol-[0-9a-f]+$ ]]; then die "unexpected DataVolumeId '$DATA_VOLUME_ID'"; fi
echo "instance $INSTANCE_ID, repository $REPO_URI, url $APP_URL, data volume ${DATA_VOLUME_ID:-none}"

step "6/8 Image $REPO_URI:$SHA7"
if "${AWS[@]}" ecr describe-images --repository-name "$STACK-app" --image-ids "imageTag=$SHA7" >/dev/null 2>&1; then
  echo "already in ECR, not rebuilt"
elif dry; then
  echo "[dry-run] git archive $SHA7 | docker build --platform linux/amd64 -t $REPO_URI:$SHA7 -t $REPO_URI:latest -; docker push both"
else
  docker info >/dev/null 2>&1 || die "Docker is not running (start Docker Desktop)"
  "${AWS[@]}" ecr get-login-password | docker login --username AWS --password-stdin "${REPO_URI%%/*}" >/dev/null
  # Build context = exactly the commit (no uncommitted files, no git-ignored secrets).
  git -C "$ROOT" archive --format=tar "$SHA" \
    | docker build --platform linux/amd64 -t "$REPO_URI:$SHA7" -t "$REPO_URI:latest" -
  docker push "$REPO_URI:$SHA7"
  docker push "$REPO_URI:latest"
fi

step "7/8 Roll out on $INSTANCE_ID (SSM Run Command, APP_COMMIT=$SHA)"
if dry; then
  echo "[dry-run] ssm send-command AWS-RunShellScript: remote-deploy.sh $STACK $REGION $REPO_URI:$SHA7 schema.sql $SHA ${DATA_VOLUME_ID}"
else
  ping=""
  for _ in $(seq 1 40); do   # a new instance needs a few minutes to register with SSM
    ping="$("${AWS[@]}" ssm describe-instance-information --filters "Key=InstanceIds,Values=$INSTANCE_ID" \
      --query 'InstanceInformationList[0].PingStatus' --output text 2>/dev/null || true)"
    [ "$ping" = Online ] && break
    sleep 15
  done
  [ "$ping" = Online ] || die "instance $INSTANCE_ID is not Online in SSM (status ${ping:-none})"
  # The instance script belongs to this deploy tool (it must understand APP_COMMIT); the schema
  # belongs to the deployed commit, like the image.
  script="$(base64 < "$HERE/remote-deploy.sh" | tr -d '\n')"
  schema="$(git -C "$ROOT" show "$SHA:backend/db/schema.sql" | base64 | tr -d '\n')"
  cmd="echo $script | base64 -d > /tmp/remote-deploy.sh; echo $schema | base64 -d > /tmp/schema.sql; bash /tmp/remote-deploy.sh '$STACK' '$REGION' '$REPO_URI:$SHA7' /tmp/schema.sql '$SHA' '$DATA_VOLUME_ID'"
  params="$(jq -n --arg c "$cmd" '{commands: [$c], executionTimeout: ["900"]}')"
  cid="$("${AWS[@]}" ssm send-command --instance-ids "$INSTANCE_ID" --document-name AWS-RunShellScript \
    --comment "micro-git deploy $SHA7" --parameters "$params" --timeout-seconds 900 --query Command.CommandId --output text)"
  status=Pending
  for _ in $(seq 1 90); do
    status="$("${AWS[@]}" ssm get-command-invocation --command-id "$cid" --instance-id "$INSTANCE_ID" \
      --query Status --output text 2>/dev/null || echo Pending)"
    case "$status" in Pending|InProgress|Delayed) sleep 10 ;; *) break ;; esac
  done
  "${AWS[@]}" ssm get-command-invocation --command-id "$cid" --instance-id "$INSTANCE_ID" \
    --query '[StandardOutputContent,StandardErrorContent]' --output text || true
  [ "$status" = Success ] || die "remote deploy finished with status $status"
fi

step "8/8 Smoke checks on $APP_URL"
if dry; then
  echo "[dry-run] /health X-Build == $SHA, / 200, /api/repos 200, /api/auth/me 401"
  echo; echo "Dry run only, nothing was changed. Re-run with --apply --account $ACCOUNT."
  exit 0
fi
base="${APP_URL%/}"
probe() { # path -> "code build"
  local h
  h="$(curl -sS -D - -o /dev/null --connect-timeout 10 --max-time 30 "$base$1" 2>/dev/null || true)"
  printf '%s %s\n' "$(printf '%s' "$h" | head -n1 | awk '{print $2}')" \
    "$(printf '%s' "$h" | tr -d '\r' | awk -F': ' 'tolower($1)=="x-build"{print $2}')"
}
ok=""
for _ in $(seq 1 30); do
  read -r code build <<<"$(probe /health)"
  [ "$code" = 200 ] && [ "$build" = "$SHA" ] && { ok=1; break; }
  sleep 10
done
[ -n "$ok" ] || die "/health did not report X-Build $SHA within 5 minutes (last: HTTP ${code:-none}, X-Build ${build:-none})"
fail=0
for check in "/ 200" "/api/repos 200" "/api/auth/me 401"; do
  # shellcheck disable=SC2086
  set -- $check
  read -r code build <<<"$(probe "$1")"
  if [ "$code" = "$2" ] && [ "$build" = "$SHA" ]; then r=ok; else r=FAIL; fail=1; fi
  printf '  %-14s HTTP %s (want %s), X-Build %s  %s\n' "$1" "${code:-none}" "$2" "${build:-none}" "$r"
done
[ "$fail" = 0 ] || die "smoke checks failed"
echo
echo "Deployed $SHA to $STACK: $APP_URL"
