#!/usr/bin/env bash
# Read-only checks before deploy/aws/bootstrap.sh. Changes nothing, prints no secrets.
# Usage: preflight.sh [--profile NAME] [--region REGION] [--stack-name NAME] [--repo OWNER/REPO]
set -uo pipefail

PROFILE=""; REGION=""; STACK="mg"; REPO="L0nelyG0d/micro-git"
while [ $# -gt 0 ]; do
  case "$1" in
    --profile) PROFILE="${2:?--profile needs a value}"; shift ;;
    --region) REGION="${2:?--region needs a value}"; shift ;;
    --stack-name) STACK="${2:?--stack-name needs a value}"; shift ;;
    --repo) REPO="${2:?--repo needs a value}"; shift ;;
    -h|--help) sed -n 2,3p "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

AWS=(aws)
[ -n "$PROFILE" ] && AWS+=(--profile "$PROFILE")
[ -n "$REGION" ] && AWS+=(--region "$REGION")

FAIL=0; WARN=0
ok()   { echo "[ok]   $*"; }
warn() { echo "[warn] $*"; WARN=$((WARN+1)); }
bad()  { echo "[FAIL] $*"; FAIL=$((FAIL+1)); }

if ! ACCOUNT="$("${AWS[@]}" sts get-caller-identity --query Account --output text 2>/dev/null)" \
   || ! [[ "$ACCOUNT" =~ ^[0-9]{12}$ ]]; then
  echo "[FAIL] 'aws sts get-caller-identity' failed. Run 'aws login' (or pass --profile NAME) and retry." >&2
  exit 1
fi
ok "identity: account $ACCOUNT"

R="$("${AWS[@]}" configure get region 2>/dev/null || true)"
R="${REGION:-${AWS_REGION:-${AWS_DEFAULT_REGION:-${R:-}}}}"
if [ -z "$R" ]; then bad "no region: pass --region"; R=""; else ok "region: $R"; fi
[ -n "$R" ] && AWS+=(--region "$R")

if [ -n "$R" ]; then
  vpc="$("${AWS[@]}" ec2 describe-vpcs --filters Name=isDefault,Values=true --query 'Vpcs[0].VpcId' --output text 2>/dev/null || true)"
  if [ -n "$vpc" ] && [ "$vpc" != None ]; then ok "default VPC in $R: $vpc"; else bad "no default VPC in $R (the template needs one)"; fi

  offered="$("${AWS[@]}" ec2 describe-instance-type-offerings --location-type region \
    --filters Name=instance-type,Values=t3.micro,t2.micro --query 'InstanceTypeOfferings[].InstanceType' --output text 2>/dev/null || true)"
  if [ -n "$offered" ]; then ok "instance types offered in $R: $(echo "$offered" | tr -s '\t\n' ' ')"; else bad "neither t3.micro nor t2.micro offered in $R"; fi

  st="$("${AWS[@]}" cloudformation describe-stacks --stack-name "$STACK" --query 'Stacks[0].StackStatus' --output text 2>/dev/null || true)"
  if [ -n "$st" ] && [ "$st" != None ]; then
    warn "stack '$STACK' already exists in $R ($st). If it is the hand-made one, use a NEW name: --stack-name <new> and AWS_STACK_NAME"
  else
    ok "no stack named '$STACK' in $R"
  fi
fi

chk_policy() {
  if "${AWS[@]}" iam get-policy --policy-arn "arn:aws:iam::$ACCOUNT:policy/$1" >/dev/null 2>&1; then
    ok "policy $1 exists (bootstrap leaves it unchanged)"; else ok "policy $1 missing (bootstrap will create it)"; fi
}
chk_role() {
  if "${AWS[@]}" iam get-role --role-name "$1" >/dev/null 2>&1; then
    ok "role $1 exists (bootstrap refreshes its trust and policy)"; else ok "role $1 missing (bootstrap will create it)"; fi
}
if "${AWS[@]}" iam get-open-id-connect-provider \
   --open-id-connect-provider-arn "arn:aws:iam::$ACCOUNT:oidc-provider/token.actions.githubusercontent.com" >/dev/null 2>&1; then
  ok "GitHub OIDC provider exists"; else ok "GitHub OIDC provider missing (bootstrap will create it)"; fi
chk_policy mg-instance-boundary
chk_role "cfn-deploy-$STACK"
chk_role "github-deploy-$STACK"

if command -v gh >/dev/null 2>&1; then
  vars="$(gh variable list --repo "$REPO" 2>/dev/null | cut -f1)"
  secs="$(gh secret list --repo "$REPO" 2>/dev/null | cut -f1)"
  if [ -z "$vars$secs" ]; then
    warn "could not list GitHub variables/secrets for $REPO (gh not logged in, or none set)"
  fi
  for n in AWS_ROLE_ARN AWS_CFN_ROLE_ARN AWS_REGION AWS_STACK_NAME; do
    if printf '%s\n' "$vars" | grep -qx "$n"; then ok "variable $n is set"; else warn "variable $n not set"; fi
  done
  if printf '%s\n' "$secs" | grep -qx AWS_JWT_SECRET; then ok "secret AWS_JWT_SECRET is set"; else warn "secret AWS_JWT_SECRET not set"; fi
else
  warn "gh not installed: skipped GitHub variable/secret check"
fi

echo
echo "preflight: $FAIL failure(s), $WARN warning(s)"
[ "$FAIL" -eq 0 ]
