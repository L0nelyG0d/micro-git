#!/usr/bin/env bash
# One-time AWS bootstrap for the GitHub Actions deploy (job deploy-aws).
# Creates, in order, idempotently: GitHub OIDC provider, policy mg-instance-boundary,
# role cfn-deploy-<stack> (+ inline policy), role github-deploy-<stack> (+ inline policy).
# Policy JSON lives in deploy/aws/policies/*.json (placeholders <ACCOUNT_ID>, <STACK_NAME>, <REPO>).
#
# Default is --dry-run: read-only lookups run, every mutating command is only printed.
# Run it yourself with your own credentials (`aws login` or --profile). Never run against
# an account you do not own. No secrets are read, generated or printed here.
set -euo pipefail

usage() {
  cat <<USAGE
Usage: $0 [--dry-run | --apply] [--profile NAME] [--region REGION] [--stack-name NAME] [--repo OWNER/REPO]
  --dry-run     print the planned aws commands, change nothing (default)
  --apply       create/update the resources after typed confirmation
  --profile     AWS CLI profile
  --region      AWS region (default: profile/env default, else us-east-1)
  --stack-name  CloudFormation stack name (default: mg)
  --repo        GitHub repository allowed to deploy (default: L0nelyG0d/micro-git)
USAGE
}

MODE=dry-run
PROFILE=""
REGION=""
STACK="mg"
REPO="L0nelyG0d/micro-git"

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) MODE=dry-run ;;
    --apply) MODE=apply ;;
    --profile) PROFILE="${2:?--profile needs a value}"; shift ;;
    --region) REGION="${2:?--region needs a value}"; shift ;;
    --stack-name) STACK="${2:?--stack-name needs a value}"; shift ;;
    --repo) REPO="${2:?--repo needs a value}"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

if ! [[ "$STACK" =~ ^[a-z][a-z0-9-]{0,19}$ ]]; then
  echo "error: --stack-name must be lowercase letters, digits and '-' (max 20, start with a letter)" >&2; exit 2
fi
if ! [[ "$REPO" =~ ^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$ ]]; then
  echo "error: --repo must look like OWNER/REPO" >&2; exit 2
fi

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POL="$HERE/policies"

AWS=(aws)
[ -n "$PROFILE" ] && AWS+=(--profile "$PROFILE")
[ -n "$REGION" ] && AWS+=(--region "$REGION")

if ! ACCOUNT="$("${AWS[@]}" sts get-caller-identity --query Account --output text 2>/dev/null)" \
   || ! [[ "$ACCOUNT" =~ ^[0-9]{12}$ ]]; then
  echo "error: 'aws sts get-caller-identity' failed. You have no usable AWS session." >&2
  echo "hint: run 'aws login' (or 'aws sso login --profile NAME', then pass --profile NAME), then retry." >&2
  exit 1
fi

PART=aws
OIDC_ARN="arn:$PART:iam::$ACCOUNT:oidc-provider/token.actions.githubusercontent.com"
BOUNDARY=mg-instance-boundary
BOUNDARY_ARN="arn:$PART:iam::$ACCOUNT:policy/$BOUNDARY"
CFN_ROLE="cfn-deploy-$STACK"
GH_ROLE="github-deploy-$STACK"
CFN_ROLE_ARN="arn:$PART:iam::$ACCOUNT:role/$CFN_ROLE"
GH_ROLE_ARN="arn:$PART:iam::$ACCOUNT:role/$GH_ROLE"

echo "Mode:      $MODE"
echo "Account:   $ACCOUNT"
echo "Profile:   ${PROFILE:-<default>}"
echo "Region:    ${REGION:-<cli default>}"
echo "Stack:     $STACK"
echo "Repo:      $REPO (branch main only)"
echo

if [ "$MODE" = apply ]; then
  echo "This will create or update IAM resources in account $ACCOUNT."
  printf 'Type the account id (%s) to continue: ' "$ACCOUNT"
  read -r answer || answer=""
  if [ "$answer" != "$ACCOUNT" ]; then
    echo "Confirmation did not match. Nothing was changed." >&2
    exit 1
  fi
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
for f in "$POL"/*.json; do
  sed -e "s|<ACCOUNT_ID>|$ACCOUNT|g" -e "s|<STACK_NAME>|$STACK|g" -e "s|<REPO>|$REPO|g" "$f" > "$TMP/$(basename "$f")"
done

# Read-only call: always executed.
ro() { "${AWS[@]}" "$@"; }
# Mutating call: printed in dry-run, executed with --apply.
mut() {
  if [ "$MODE" = apply ]; then
    echo "+ aws $*"
    "${AWS[@]}" "$@" >/dev/null
  else
    printf '[dry-run] aws'; printf ' %q' "$@"; printf '\n'
  fi
}

echo "1/5 GitHub OIDC provider"
if ro iam get-open-id-connect-provider --open-id-connect-provider-arn "$OIDC_ARN" >/dev/null 2>&1; then
  echo "  exists, skipping: $OIDC_ARN"
else
  mut iam create-open-id-connect-provider --url https://token.actions.githubusercontent.com --client-id-list sts.amazonaws.com
fi

echo "2/5 Policy $BOUNDARY"
if ro iam get-policy --policy-arn "$BOUNDARY_ARN" >/dev/null 2>&1; then
  echo "  exists, left unchanged (compare with deploy/aws/policies/$BOUNDARY.json; its secret path must cover /$STACK/jwt-secret)"
else
  mut iam create-policy --policy-name "$BOUNDARY" --policy-document "file://$TMP/$BOUNDARY.json"
fi

ensure_role() { # name trust-file policy-file
  local name="$1" trust="$2" pol="$3"
  if ro iam get-role --role-name "$name" >/dev/null 2>&1; then
    echo "  role $name exists: refreshing trust policy"
    mut iam update-assume-role-policy --role-name "$name" --policy-document "file://$TMP/$trust.json"
  else
    mut iam create-role --role-name "$name" --assume-role-policy-document "file://$TMP/$trust.json"
  fi
  mut iam put-role-policy --role-name "$name" --policy-name "$name" --policy-document "file://$TMP/$pol.json"
}

echo "3/5 + 4/5 Role $CFN_ROLE (CloudFormation service role) and its policy"
ensure_role "$CFN_ROLE" cfn-deploy-trust cfn-deploy-policy

echo "5/5 Role $GH_ROLE (GitHub OIDC role) and its policy"
ensure_role "$GH_ROLE" github-deploy-trust github-deploy-policy

echo
if [ "$MODE" = apply ]; then
  echo "Done."
else
  echo "Dry run only, nothing was changed. Re-run with --apply to execute."
fi
cat <<NEXT

Next, run these yourself (nothing below was executed). Values are ARNs, not secrets.

  gh variable set AWS_ROLE_ARN --repo $REPO --body $GH_ROLE_ARN
  gh variable set AWS_CFN_ROLE_ARN --repo $REPO --body $CFN_ROLE_ARN
  openssl rand -hex 32 | gh secret set AWS_JWT_SECRET --repo $REPO
NEXT
[ "$STACK" != mg ] && echo "  gh variable set AWS_STACK_NAME --repo $REPO --body $STACK"
[ -n "$REGION" ] && echo "  gh variable set AWS_REGION --repo $REPO --body $REGION"
echo
echo "Then push to main (or re-run the Deploy workflow). Use a NEW stack name if a hand-made stack exists."
exit 0
