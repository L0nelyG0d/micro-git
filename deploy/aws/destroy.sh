#!/usr/bin/env bash
# shellcheck disable=SC2016  # backticks in single quotes are JMESPath literals, not shell
# Tear down what deploy/aws/deploy.sh created, to stop spending Free plan credits.
#
#   deploy/aws/destroy.sh                        dry-run (default): lists what would be deleted
#   deploy/aws/destroy.sh --apply --account ID   deletes it
#
# Default (everything that costs money, plus the CI credential):
#   - EC2 instance(s) tagged aws:cloudformation:stack-name=micro-git-prod (terminated first:
#     AppInstance is DeletionPolicy RetainExceptOnCreate, so a plain delete-stack would leave it running)
#   - stack micro-git-prod (security group, instance role/profile, ECR repo micro-git-prod-app with its images)
#   - IAM user github-deploy-micro-git-prod-user, its access keys, and the GitHub secrets
#     AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY (if gh can reach GitHub)
# With --all also the free leftovers: SSM parameter /micro-git-prod/jwt-secret, role
#   cfn-deploy-micro-git-prod, policy mg-instance-boundary (only if nothing else uses it).
#
# POSTGRES DATA ON THE INSTANCE IS LOST. There are no backups. Never touches the stack `mg`.
set -euo pipefail

readonly REGION=ap-southeast-2
readonly STACK=micro-git-prod
readonly REPO_GH="L0nelyG0d/micro-git"

MODE=dry-run; WANT_ACCOUNT="${MG_AWS_ACCOUNT:-}"; PROFILE=""; ALL=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) MODE=dry-run ;;
    --apply) MODE=apply ;;
    --account) WANT_ACCOUNT="${2:?--account needs a value}"; shift ;;
    --profile) PROFILE="${2:?--profile needs a value}"; shift ;;
    --all) ALL=1 ;;
    -h|--help) sed -n 2,18p "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

export AWS_PAGER=""
AWS=(aws --region "$REGION")
[ -n "$PROFILE" ] && AWS+=(--profile "$PROFILE")
die() { echo "error: $*" >&2; exit 1; }
run() {
  if [ "$MODE" = apply ]; then echo "+ aws $*"; "${AWS[@]}" "$@" >/dev/null
  else printf '[dry-run] aws'; printf ' %q' "$@"; printf '\n'; fi
}
gh_t() { perl -e 'alarm shift; exec @ARGV or exit 127' 60 gh "$@"; }

LOCK="${TMPDIR:-/tmp}/mg-deploy-$STACK.lock"
mkdir "$LOCK" 2>/dev/null || die "a deploy of $STACK is running (lock $LOCK)"
trap 'rm -rf "$LOCK"' EXIT

ACCOUNT="$("${AWS[@]}" sts get-caller-identity --query Account --output text 2>/dev/null)" \
  || die "no usable AWS session: run 'aws login' and retry"
[ -z "$WANT_ACCOUNT" ] || [ "$WANT_ACCOUNT" = "$ACCOUNT" ] || die "session account $ACCOUNT is not $WANT_ACCOUNT"
if [ "$MODE" = apply ] && [ -z "$WANT_ACCOUNT" ]; then
  [ -t 0 ] || die "--apply without a terminal needs --account $ACCOUNT"
  printf 'Deletes %s in %s, account %s, and its Postgres data. Type the account id: ' "$STACK" "$REGION" "$ACCOUNT"
  read -r ans || ans=""; [ "$ans" = "$ACCOUNT" ] || die "confirmation did not match; nothing was changed"
fi
[ "$STACK" != mg ] || die "refusing to touch the hand-made stack mg"

CFN_ROLE="cfn-deploy-$STACK"
CI_USER="github-deploy-$STACK-user"
BOUNDARY_ARN="arn:aws:iam::$ACCOUNT:policy/mg-instance-boundary"
echo "mode $MODE, account $ACCOUNT, region $REGION, stack $STACK${ALL:+, --all}"

echo; echo "== 1. Instances of $STACK (terminated first; Postgres data is lost)"
ids="$("${AWS[@]}" ec2 describe-instances \
  --filters "Name=tag:aws:cloudformation:stack-name,Values=$STACK" \
            "Name=instance-state-name,Values=pending,running,stopping,stopped" \
  --query 'Reservations[].Instances[].InstanceId' --output text)"
if [ -z "$ids" ] || [ "$ids" = None ]; then echo "none"
else
  # shellcheck disable=SC2086
  run ec2 terminate-instances --instance-ids $ids
  # shellcheck disable=SC2086
  [ "$MODE" = apply ] && "${AWS[@]}" ec2 wait instance-terminated --instance-ids $ids && echo "terminated: $ids"
fi

echo; echo "== 2. Stack $STACK"
st="$("${AWS[@]}" cloudformation describe-stacks --stack-name "$STACK" --query 'Stacks[0].StackStatus' --output text 2>/dev/null || echo NONE)"
echo "status: $st"
if [ "$st" != NONE ]; then
  role=()
  "${AWS[@]}" iam get-role --role-name "$CFN_ROLE" >/dev/null 2>&1 && role=(--role-arn "arn:aws:iam::$ACCOUNT:role/$CFN_ROLE")
  run cloudformation delete-stack --stack-name "$STACK" ${role[@]+"${role[@]}"}
  if [ "$MODE" = apply ]; then
    if "${AWS[@]}" cloudformation wait stack-delete-complete --stack-name "$STACK"; then echo "stack deleted"
    else
      "${AWS[@]}" cloudformation describe-stack-events --stack-name "$STACK" \
        --query 'StackEvents[?ResourceStatus==`DELETE_FAILED`].[LogicalResourceId,ResourceStatusReason]' --output text | head -n 10 >&2
      die "stack delete failed; see above (re-run after fixing)"
    fi
  fi
fi
if "${AWS[@]}" ecr describe-repositories --repository-names "$STACK-app" >/dev/null 2>&1; then
  run ecr delete-repository --repository-name "$STACK-app" --force
fi

echo; echo "== 3. CI user $CI_USER and its keys"
if "${AWS[@]}" iam get-user --user-name "$CI_USER" >/dev/null 2>&1; then
  for k in $("${AWS[@]}" iam list-access-keys --user-name "$CI_USER" --query 'AccessKeyMetadata[].AccessKeyId' --output text); do
    run iam delete-access-key --user-name "$CI_USER" --access-key-id "$k"
  done
  for p in $("${AWS[@]}" iam list-user-policies --user-name "$CI_USER" --query 'PolicyNames[]' --output text); do
    run iam delete-user-policy --user-name "$CI_USER" --policy-name "$p"
  done
  run iam delete-user --user-name "$CI_USER"
  for s in AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY; do
    if [ "$MODE" = apply ]; then
      gh_t secret delete "$s" --repo "$REPO_GH" >/dev/null 2>&1 && echo "GitHub secret $s deleted" \
        || echo "note: could not delete GitHub secret $s (already gone, or GitHub unreachable); the key itself is deleted"
    else echo "[dry-run] gh secret delete $s --repo $REPO_GH"; fi
  done
else
  echo "none"
fi

if [ -n "$ALL" ]; then
  echo; echo "== 4. --all: SSM parameter, service role, boundary policy"
  if [ "$("${AWS[@]}" ssm describe-parameters --parameter-filters "Key=Name,Option=Equals,Values=/$STACK/jwt-secret" \
        --query 'length(Parameters)' --output text)" != 0 ]; then
    run ssm delete-parameter --name "/$STACK/jwt-secret"
  fi
  if "${AWS[@]}" iam get-role --role-name "$CFN_ROLE" >/dev/null 2>&1; then
    for p in $("${AWS[@]}" iam list-role-policies --role-name "$CFN_ROLE" --query 'PolicyNames[]' --output text); do
      run iam delete-role-policy --role-name "$CFN_ROLE" --policy-name "$p"
    done
    run iam delete-role --role-name "$CFN_ROLE"
  fi
  if "${AWS[@]}" iam get-policy --policy-arn "$BOUNDARY_ARN" >/dev/null 2>&1; then
    users="$("${AWS[@]}" iam list-entities-for-policy --policy-arn "$BOUNDARY_ARN" \
      --query 'length(PolicyRoles)' --output text)"
    bnd="$("${AWS[@]}" iam list-entities-for-policy --policy-arn "$BOUNDARY_ARN" --policy-usage-filter PermissionsBoundary \
      --query 'length(PolicyRoles)' --output text)"
    if [ "$users" != 0 ] || [ "$bnd" != 0 ]; then
      echo "mg-instance-boundary is still used by $users attached / $bnd boundary role(s): left in place"
    else
      for v in $("${AWS[@]}" iam list-policy-versions --policy-arn "$BOUNDARY_ARN" \
          --query 'Versions[?!IsDefaultVersion].VersionId' --output text); do
        run iam delete-policy-version --policy-arn "$BOUNDARY_ARN" --version-id "$v"
      done
      run iam delete-policy --policy-arn "$BOUNDARY_ARN"
    fi
  fi
fi

echo
if [ "$MODE" = apply ]; then
  left="$("${AWS[@]}" ec2 describe-instances --filters "Name=tag:aws:cloudformation:stack-name,Values=$STACK" \
    "Name=instance-state-name,Values=pending,running,stopping,stopped" --query 'length(Reservations[].Instances[])' --output text)"
  sgs="$("${AWS[@]}" ec2 describe-security-groups --filters "Name=tag:aws:cloudformation:stack-name,Values=$STACK" \
    --query 'length(SecurityGroups)' --output text)"
  echo "Done. Leftovers: $left instance(s), $sgs security group(s) tagged $STACK."
  echo "Redeploy any time with: deploy/aws/deploy.sh --apply --account $ACCOUNT"
else
  echo "Dry run only, nothing was changed. Re-run with --apply --account $ACCOUNT${ALL:+ --all}."
fi
