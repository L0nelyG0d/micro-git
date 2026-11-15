#!/usr/bin/env bash
# shellcheck disable=SC2016  # backticks in single quotes are JMESPath literals, not shell
# Read-only checks before deploy/aws/bootstrap.sh. Changes nothing, prints no secrets.
# Usage: preflight.sh [--profile NAME] [--region REGION] [--stack-name NAME] [--repo OWNER/REPO] [--no-oidc] [--ci-user]
#   --no-oidc  skip the GitHub OIDC provider, the github-deploy role and the GitHub OIDC variables
#              (for deploy/aws/deploy.sh, or accounts where an SCP denies oidc-provider/*)
#   --ci-user  also check the CI IAM user github-deploy-<stack>-user and its GitHub secrets (names only)
set -uo pipefail

PROFILE=""; REGION=""; STACK="mg"; REPO="L0nelyG0d/micro-git"; NO_OIDC=""; CI_USER=""
while [ $# -gt 0 ]; do
  case "$1" in
    --no-oidc) NO_OIDC=1 ;;
    --ci-user) CI_USER=1 ;;
    --profile) PROFILE="${2:?--profile needs a value}"; shift ;;
    --region) REGION="${2:?--region needs a value}"; shift ;;
    --stack-name) STACK="${2:?--stack-name needs a value}"; shift ;;
    --repo) REPO="${2:?--repo needs a value}"; shift ;;
    -h|--help) sed -n 2,6p "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

_clash=
for _role in cfn-deploy github-deploy; do
  case "$_role-" in "$STACK-"*) _clash=1 ;; esac
  case "$STACK" in "$_role"*) _clash=1 ;; esac
done
if [ -n "${_clash:-}" ]; then
  echo "[FAIL] stack name '$STACK' collides with the deploy role names: <stack>-* would match cfn-deploy-* or github-deploy-* (reserved: cfn, github, and anything starting with cfn-deploy or github-deploy)" >&2; exit 2
fi

AWS=(aws)
[ -n "$PROFILE" ] && AWS+=(--profile "$PROFILE")
[ -n "$REGION" ] && AWS+=(--region "$REGION")

FAIL=0; WARN=0; NOTE=0
ok()   { echo "[ok]   $*"; }
note() { echo "[note] $*"; NOTE=$((NOTE+1)); }
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
  if [ -n "$st" ] && [ "$st" != None ] && [ -n "$NO_OIDC" ] && [ "$STACK" != mg ]; then
    ok "stack '$STACK' exists in $R ($st); deploy.sh updates it in place"
  elif [ -n "$st" ] && [ "$st" != None ]; then
    warn "stack '$STACK' already exists in $R ($st). If it is the hand-made one, use a NEW name: --stack-name <new> and AWS_STACK_NAME"
  else
    ok "no stack named '$STACK' in $R"
  fi
fi

# Classify a read-only IAM lookup: prints exists | missing | denied | error:<first line>.
# AccessDenied (for example an organization SCP) is NOT "missing": the object may exist.
lookup() {
  local err rc=0
  err="$("${AWS[@]}" "$@" 2>&1 >/dev/null)" || rc=$?
  if [ "$rc" -eq 0 ]; then echo exists
  elif printf '%s' "$err" | grep -q 'NoSuchEntity'; then echo missing
  elif printf '%s' "$err" | grep -Eq 'AccessDenied|not authorized|explicit deny'; then echo denied
  else echo "error:$(printf '%s' "$err" | head -n1)"; fi
}
report() { # what result exists-msg missing-msg
  case "$2" in
    exists) ok "$1 exists $3" ;;
    missing) ok "$1 missing $4" ;;
    denied) warn "$1: lookup denied (AccessDenied, likely an SCP); state unknown" ;;
    *) warn "$1: lookup failed: ${2#error:}" ;;
  esac
}
chk_policy() { report "policy $1" "$(lookup iam get-policy --policy-arn "arn:aws:iam::$ACCOUNT:policy/$1")" \
  "(bootstrap leaves it unchanged)" "(bootstrap will create it)"; }
chk_role() { report "role $1" "$(lookup iam get-role --role-name "$1")" \
  "(bootstrap refreshes its trust and policy)" "(bootstrap will create it)"; }

if [ -n "$NO_OIDC" ]; then
  note "--no-oidc: skipping the GitHub OIDC provider, role github-deploy-$STACK and the OIDC GitHub variables"
else
  oidc="$(lookup iam get-open-id-connect-provider \
    --open-id-connect-provider-arn "arn:aws:iam::$ACCOUNT:oidc-provider/token.actions.githubusercontent.com")"
  case "$oidc" in
    exists) ok "GitHub OIDC provider exists" ;;
    missing) ok "GitHub OIDC provider missing (bootstrap will create it)" ;;
    denied) note "GitHub OIDC provider: lookup denied (AccessDenied, likely an organization SCP on oidc-provider/*). The OIDC path cannot be used in this account; use deploy/aws/deploy.sh (--no-oidc) instead" ;;
    *) warn "GitHub OIDC provider: lookup failed: ${oidc#error:}" ;;
  esac
fi
chk_policy mg-instance-boundary
chk_role "cfn-deploy-$STACK"
[ -z "$NO_OIDC" ] && chk_role "github-deploy-$STACK"
if [ -n "$CI_USER" ]; then
  u="github-deploy-$STACK-user"
  ustate="$(lookup iam get-user --user-name "$u")"
  report "CI user $u" "$ustate" "(bootstrap --ci-user refreshes its policy)" "(bootstrap --ci-user will create it)"
  if [ "$ustate" = exists ]; then
    nkeys="$("${AWS[@]}" iam list-access-keys --user-name "$u" --query 'length(AccessKeyMetadata[?Status==`Active`])' --output text 2>/dev/null || echo '?')"
    if [ "$nkeys" = 1 ]; then ok "CI user $u has 1 active access key"; else warn "CI user $u has $nkeys active access keys (want exactly 1)"; fi
  fi
fi

# gh may hang when github.com is unreachable: bound every call.
gh_t() { perl -e 'alarm shift; exec @ARGV or exit 127' 20 gh "$@"; }
if [ -n "$NO_OIDC" ] && [ -z "$CI_USER" ]; then
  : # GitHub variables are not used by deploy.sh
elif command -v gh >/dev/null 2>&1; then
  vars="$(gh_t variable list --repo "$REPO" 2>/dev/null | cut -f1)"
  secs="$(gh_t secret list --repo "$REPO" 2>/dev/null | cut -f1)"
  if [ -z "$vars$secs" ]; then
    warn "could not list GitHub variables/secrets for $REPO (gh not logged in, github.com unreachable, or none set)"
  fi
  want_vars="AWS_CFN_ROLE_ARN AWS_REGION AWS_STACK_NAME"
  want_secs=""
  [ -z "$NO_OIDC" ] && { want_vars="AWS_ROLE_ARN $want_vars"; want_secs="AWS_JWT_SECRET"; }
  [ -n "$CI_USER" ] && want_secs="$want_secs AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY"
  for n in $want_vars; do
    if printf '%s\n' "$vars" | grep -qx "$n"; then ok "variable $n is set"; else warn "variable $n not set"; fi
  done
  for n in $want_secs; do
    if printf '%s\n' "$secs" | grep -qx "$n"; then ok "secret $n is set"; else warn "secret $n not set"; fi
  done
else
  warn "gh not installed: skipped GitHub variable/secret check"
fi

echo
echo "preflight: $FAIL failure(s), $WARN warning(s), $NOTE note(s)"
[ "$FAIL" -eq 0 ]
