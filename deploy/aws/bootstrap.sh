#!/usr/bin/env bash
# One-time AWS bootstrap for the AWS deploy (deploy/aws/deploy.sh and the GitHub job deploy-aws).
# Creates, in order, idempotently: GitHub OIDC provider, policy mg-instance-boundary,
# role cfn-deploy-<stack> (+ inline policy), role github-deploy-<stack> (+ inline policy).
#   --no-oidc  skips the OIDC provider and the github-deploy role (accounts where an SCP
#              denies oidc-provider/*; deploy/aws/deploy.sh uses this mode).
#   --ci-user  adds the IAM user github-deploy-<stack>-user (+ inline policy, tag Project=micro-git)
#              for GitHub Actions without OIDC, and, if it has no active key (or --rotate-ci-key),
#              creates one and pipes it straight into the secrets AWS_ACCESS_KEY_ID /
#              AWS_SECRET_ACCESS_KEY of GitHub environment aws-prod (deployable from main only).
#              The key is never printed, logged or written to a file.
# Policy JSON lives in deploy/aws/policies/*.json (placeholders <ACCOUNT_ID>, <STACK_NAME>, <REPO>).
#
# Default is --dry-run: read-only lookups run, every mutating command is only printed.
# Run it yourself with your own credentials (`aws login` or --profile). Never run against
# an account you do not own. No secrets are read or printed here.
set -euo pipefail

usage() {
  cat <<USAGE
Usage: $0 [--dry-run | --apply] [--profile NAME] [--region REGION] [--stack-name NAME] [--repo OWNER/REPO]
          [--no-oidc] [--ci-user [--rotate-ci-key]] [--confirm-account ID]
  --dry-run          print the planned aws commands, change nothing (default)
  --apply            create/update the resources (asks to type the account id unless --confirm-account matches)
  --profile          AWS CLI profile
  --region           AWS region (default: profile/env default, else us-east-1)
  --stack-name       CloudFormation stack name (default: mg)
  --repo             GitHub repository allowed to deploy (default: L0nelyG0d/micro-git)
  --no-oidc          skip the GitHub OIDC provider and the github-deploy-<stack> role
  --ci-user          create/refresh IAM user github-deploy-<stack>-user and its GitHub secrets
  --rotate-ci-key    with --ci-user: new key -> GitHub secrets -> delete the old key
  --confirm-account  expected account id; --apply aborts if the session is another account
USAGE
}

MODE=dry-run
PROFILE=""
REGION=""
STACK="mg"
REPO="L0nelyG0d/micro-git"
NO_OIDC=""
CI_USER=""
ROTATE=""
CONFIRM=""

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) MODE=dry-run ;;
    --apply) MODE=apply ;;
    --profile) PROFILE="${2:?--profile needs a value}"; shift ;;
    --region) REGION="${2:?--region needs a value}"; shift ;;
    --stack-name) STACK="${2:?--stack-name needs a value}"; shift ;;
    --repo) REPO="${2:?--repo needs a value}"; shift ;;
    --no-oidc) NO_OIDC=1 ;;
    --ci-user) CI_USER=1 ;;
    --rotate-ci-key) ROTATE=1 ;;
    --confirm-account) CONFIRM="${2:?--confirm-account needs a value}"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

if [ -n "$ROTATE" ] && [ -z "$CI_USER" ]; then
  echo "error: --rotate-ci-key needs --ci-user" >&2; exit 2
fi
if ! [[ "$STACK" =~ ^[a-z][a-z0-9-]{0,19}$ ]]; then
  echo "error: --stack-name must be lowercase letters, digits and '-' (max 20, start with a letter)" >&2; exit 2
fi
for _role in cfn-deploy github-deploy; do
  # The stack role pattern role/<stack>-* matches a deploy role <role>-<stack2> when "<stack>-" is a prefix of "<role>-"
  # (stack names cfn, github) or when the stack name itself starts with the deploy role prefix.
  case "$_role-" in "$STACK-"*) _clash=1 ;; esac
  case "$STACK" in "$_role"*) _clash=1 ;; esac
done
if [ -n "${_clash:-}" ]; then
  echo "error: --stack-name '$STACK' collides with the deploy role names: the stack role pattern <stack>-* would match cfn-deploy-* or github-deploy-* (reserved: cfn, github, and anything starting with cfn-deploy or github-deploy)" >&2; exit 2
fi
if ! [[ "$REPO" =~ ^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$ ]]; then
  echo "error: --repo must look like OWNER/REPO" >&2; exit 2
fi

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POL="$HERE/policies"

AWS=(aws)
[ -n "$PROFILE" ] && AWS+=(--profile "$PROFILE")
[ -n "$REGION" ] && AWS+=(--region "$REGION")
export AWS_PAGER=""

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
CI_USER_NAME="github-deploy-$STACK-user"
GH_ENV=aws-prod
CFN_ROLE_ARN="arn:$PART:iam::$ACCOUNT:role/$CFN_ROLE"
GH_ROLE_ARN="arn:$PART:iam::$ACCOUNT:role/$GH_ROLE"

echo "Mode:      $MODE"
echo "Account:   $ACCOUNT"
echo "Profile:   ${PROFILE:-<default>}"
echo "Region:    ${REGION:-<cli default>}"
echo "Stack:     $STACK"
echo "Repo:      $REPO (branch main only)"
echo "OIDC:      $([ -n "$NO_OIDC" ] && echo "skipped (--no-oidc)" || echo yes)"
echo "CI user:   $([ -n "$CI_USER" ] && echo "$CI_USER_NAME${ROTATE:+ (rotate key)}" || echo no)"
echo

if [ "$MODE" = apply ]; then
  if [ -n "$CONFIRM" ]; then
    if [ "$CONFIRM" != "$ACCOUNT" ]; then
      echo "--confirm-account $CONFIRM does not match the session account $ACCOUNT. Nothing was changed." >&2
      exit 1
    fi
  else
    echo "This will create or update IAM resources in account $ACCOUNT."
    printf 'Type the account id (%s) to continue: ' "$ACCOUNT"
    read -r answer || answer=""
    if [ "$answer" != "$ACCOUNT" ]; then
      echo "Confirmation did not match. Nothing was changed." >&2
      exit 1
    fi
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
# Does a read-only IAM lookup find the object? 0 = exists, 1 = NoSuchEntity, 2 = anything else (denied, network).
exists() {
  local err
  if err="$(ro "$@" 2>&1 >/dev/null)"; then return 0; fi
  if printf '%s' "$err" | grep -q NoSuchEntity; then return 1; fi
  echo "  lookup failed: $(printf '%s' "$err" | head -n1)" >&2
  return 2
}
# gh can hang when github.com is unreachable: bound every call (no coreutils timeout on macOS).
gh_t() { perl -e 'alarm shift; exec @ARGV or exit 127' 60 gh "$@"; }

if [ -n "$NO_OIDC" ]; then
  echo "1/5 GitHub OIDC provider: skipped (--no-oidc)"
else
  echo "1/5 GitHub OIDC provider"
  rc=0; exists iam get-open-id-connect-provider --open-id-connect-provider-arn "$OIDC_ARN" || rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "  exists, skipping: $OIDC_ARN"
  elif [ "$rc" -eq 1 ]; then
    # No --thumbprint-list: AWS validates the GitHub OIDC certificate chain itself and ignores thumbprints for it.
    mut iam create-open-id-connect-provider --url https://token.actions.githubusercontent.com --client-id-list sts.amazonaws.com
  else
    echo "error: cannot check the OIDC provider (denied by an SCP?). Re-run with --no-oidc and use deploy/aws/deploy.sh." >&2
    exit 1
  fi
fi

echo "2/5 Policy $BOUNDARY"
rc=0; exists iam get-policy --policy-arn "$BOUNDARY_ARN" || rc=$?
if [ "$rc" -eq 0 ]; then
  # Never changed here (its name is account-wide), but drift from the file is reported.
  ver="$(ro iam get-policy --policy-arn "$BOUNDARY_ARN" --query Policy.DefaultVersionId --output text)"
  cur="$(ro iam get-policy-version --policy-arn "$BOUNDARY_ARN" --version-id "$ver" --query PolicyVersion.Document --output json \
    | python3 -c 'import sys,json,urllib.parse as u;d=json.load(sys.stdin);d=json.loads(u.unquote(d)) if isinstance(d,str) else d;print(json.dumps(d,sort_keys=True))' 2>/dev/null || echo)"
  want="$(python3 -c 'import sys,json;print(json.dumps(json.load(open(sys.argv[1])),sort_keys=True))' "$TMP/$BOUNDARY.json")"
  if [ -n "$cur" ] && [ "$cur" = "$want" ]; then
    echo "  exists and matches deploy/aws/policies/$BOUNDARY.json"
  else
    echo "  WARNING: $BOUNDARY exists but differs from deploy/aws/policies/$BOUNDARY.json (or could not be compared)." >&2
    echo "  It is left unchanged; update it on purpose: aws iam create-policy-version --policy-arn $BOUNDARY_ARN --policy-document file://<rendered json> --set-as-default" >&2
  fi
elif [ "$rc" -eq 1 ]; then
  mut iam create-policy --policy-name "$BOUNDARY" --policy-document "file://$TMP/$BOUNDARY.json"
else
  exit 1
fi

ensure_role() { # name trust-file policy-file
  local name="$1" trust="$2" pol="$3" rc=0
  exists iam get-role --role-name "$name" || rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "  role $name exists: refreshing trust policy"
    local cur want
    cur="$(ro iam get-role --role-name "$name" --query 'Role.AssumeRolePolicyDocument' --output json 2>/dev/null | python3 -c 'import sys,json;print(json.dumps(json.load(sys.stdin),sort_keys=True))' 2>/dev/null || echo)"
    want="$(python3 -c 'import sys,json;print(json.dumps(json.load(open(sys.argv[1])),sort_keys=True))' "$TMP/$trust.json" 2>/dev/null || echo)"
    if [ -n "$cur" ] && [ -n "$want" ] && [ "$cur" != "$want" ]; then
      echo "  WARNING: current trust policy of $name differs from deploy/aws/policies/$trust.json; the refresh overwrites any hand edits" >&2
    fi
    mut iam update-assume-role-policy --role-name "$name" --policy-document "file://$TMP/$trust.json"
  elif [ "$rc" -eq 1 ]; then
    mut iam create-role --role-name "$name" --assume-role-policy-document "file://$TMP/$trust.json" \
      --tags Key=Project,Value=micro-git
  else
    exit 1
  fi
  mut iam put-role-policy --role-name "$name" --policy-name "$name" --policy-document "file://$TMP/$pol.json"
}

echo "3/5 + 4/5 Role $CFN_ROLE (CloudFormation service role) and its policy"
ensure_role "$CFN_ROLE" cfn-deploy-trust cfn-deploy-policy

if [ -n "$NO_OIDC" ]; then
  echo "5/5 Role $GH_ROLE: skipped (--no-oidc)"
else
  echo "5/5 Role $GH_ROLE (GitHub OIDC role) and its policy"
  ensure_role "$GH_ROLE" github-deploy-trust github-deploy-policy
fi

# Create one access key and hand it to GitHub without printing it: the JSON from
# create-access-key goes through a pipe into python, which feeds each value to
# `gh secret set --env` on stdin. Prints only "ok <key id>" or "fail <key id>"; the key id
# is an identifier, not the secret, and the caller shows only its last 4 characters.
# The secret value is set first, so a failure after it leaves the old (still valid) id/secret
# pair only if neither was replaced.
new_key_to_github() {
  ro iam create-access-key --user-name "$CI_USER_NAME" --output json | python3 -I -c '
import json, subprocess, sys
k = json.load(sys.stdin)["AccessKey"]
ok = True
for name, val in (("AWS_SECRET_ACCESS_KEY", k["SecretAccessKey"]), ("AWS_ACCESS_KEY_ID", k["AccessKeyId"])):
    r = subprocess.run(["perl", "-e", "alarm 60; exec @ARGV or exit 127", "gh", "secret", "set", name,
                        "--repo", sys.argv[1], "--env", sys.argv[2]],
                       input=val.encode(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if r.returncode != 0:
        ok = False
        break
print(("ok " if ok else "fail ") + k["AccessKeyId"])
' "$REPO" "$GH_ENV"
}

# GitHub environment that holds the key: only branch main may deploy to it, so a workflow
# on another branch cannot read the secrets. The PUT sends only the branch policy; GitHub may
# reset other protection rules (reviewers, wait timer) added by hand, so re-add those after a run.
ensure_github_env() {
  gh_t api -X PUT "repos/$REPO/environments/$GH_ENV" --input - >/dev/null <<'JSON'
{"deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}}
JSON
  if ! gh_t api "repos/$REPO/environments/$GH_ENV/deployment-branch-policies" --jq '.branch_policies[].name' | grep -qx main; then
    gh_t api -X POST "repos/$REPO/environments/$GH_ENV/deployment-branch-policies" -f name=main -f type=branch >/dev/null
  fi
  echo "  GitHub environment $GH_ENV: deployments from branch main only"
}

if [ -n "$CI_USER" ]; then
  echo "CI user $CI_USER_NAME (GitHub Actions without OIDC), its policy and its single access key"
  rc=0; exists iam get-user --user-name "$CI_USER_NAME" || rc=$?
  if [ "$rc" -eq 1 ]; then
    mut iam create-user --user-name "$CI_USER_NAME" --tags Key=Project,Value=micro-git
  elif [ "$rc" -ne 0 ]; then
    exit 1
  else
    echo "  user exists: refreshing its policy"
  fi
  mut iam put-user-policy --user-name "$CI_USER_NAME" --policy-name "$CI_USER_NAME" \
    --policy-document "file://$TMP/ci-user-policy.json"

  old_keys=""
  if [ "$rc" -eq 0 ]; then
    old_keys="$(ro iam list-access-keys --user-name "$CI_USER_NAME" --query 'AccessKeyMetadata[].AccessKeyId' --output text)"
    [ "$old_keys" = None ] && old_keys=""
  fi
  nold="$(printf '%s' "$old_keys" | wc -w | tr -d ' ')"
  if [ "$MODE" = apply ]; then
    gh_t api "repos/$REPO" --jq .full_name >/dev/null 2>&1 \
      || { echo "error: GitHub API unreachable or gh not logged in; no key was created. Retry later." >&2; exit 1; }
    ensure_github_env
  else
    echo "[dry-run] gh api -X PUT repos/$REPO/environments/$GH_ENV (deployment branch policy: main only)"
  fi
  if [ "$nold" -ge 1 ] && [ -z "$ROTATE" ]; then
    echo "  user has $nold access key(s); leaving them (use --rotate-ci-key to replace)"
    [ "$nold" -gt 1 ] && echo "  WARNING: more than one key; run with --rotate-ci-key to get back to exactly one" >&2
  elif [ "$nold" -ge 2 ]; then
    echo "error: $CI_USER_NAME already has 2 keys (the AWS maximum); delete the unused one first" >&2; exit 1
  elif [ "$MODE" != apply ]; then
    echo "[dry-run] aws iam create-access-key --user-name $CI_USER_NAME | gh secret set AWS_SECRET_ACCESS_KEY / AWS_ACCESS_KEY_ID --repo $REPO --env $GH_ENV (stdin; never printed)"
    for k in $old_keys; do echo "[dry-run] aws iam delete-access-key --user-name $CI_USER_NAME --access-key-id ...${k: -4}"; done
  else
    res="$(new_key_to_github)" || true
    new_id="${res#* }"
    if [ "${res%% *}" != ok ]; then
      echo "error: creating the key or storing it in GitHub failed; deleting the key this run created" >&2
      echo "error: the environment secrets may now be a mismatched pair; CI deploys fail until you re-run with --rotate-ci-key" >&2
      if [[ "$new_id" =~ ^A[A-Z0-9]{15,}$ ]]; then
        ro iam delete-access-key --user-name "$CI_USER_NAME" --access-key-id "$new_id" && echo "  deleted new key ...${new_id: -4}" >&2
      else  # python never saw the key: fall back to the list (eventually consistent, so re-check by hand)
        for k in $(ro iam list-access-keys --user-name "$CI_USER_NAME" --query 'AccessKeyMetadata[].AccessKeyId' --output text); do
          case " $old_keys " in *" $k "*) ;; *) ro iam delete-access-key --user-name "$CI_USER_NAME" --access-key-id "$k"; echo "  deleted new key ...${k: -4}" >&2 ;; esac
        done
        echo "  check: aws iam list-access-keys --user-name $CI_USER_NAME" >&2
      fi
      exit 1
    fi
    echo "  new key ...${new_id: -4} stored in environment $GH_ENV secrets AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY"
    for k in $old_keys; do
      [ "$k" = "$new_id" ] && continue
      echo "  deleting old key ...${k: -4}"
      ro iam delete-access-key --user-name "$CI_USER_NAME" --access-key-id "$k"
    done
    unset res new_id
  fi
  if [ "$MODE" = apply ]; then
    var_fail=0
    for kv in "AWS_REGION=${REGION:-}" "AWS_STACK_NAME=$STACK" "AWS_CFN_ROLE_ARN=$CFN_ROLE_ARN" "AWS_ACCOUNT_ID=$ACCOUNT"; do
      [ -n "${kv#*=}" ] || continue
      if gh_t variable set "${kv%%=*}" --repo "$REPO" --body "${kv#*=}" >/dev/null 2>&1; then
        echo "  GitHub variable ${kv%%=*} set"
      else
        echo "  ERROR: could not set GitHub variable ${kv%%=*} (retry: gh variable set ${kv%%=*} --repo $REPO --body ${kv#*=})" >&2
        var_fail=1
      fi
    done
    [ "$var_fail" = 0 ] || { echo "error: GitHub variables incomplete; the deploy-aws-keys job needs them" >&2; exit 1; }
    # Only switch CI on when the key really is in the environment, and no repo-level secret of the
    # same name exists (Actions would fall back to it, outside the main-only environment).
    env_secs="$(gh_t secret list --repo "$REPO" --env "$GH_ENV" 2>/dev/null | cut -f1)"
    repo_secs="$(gh_t secret list --repo "$REPO" 2>/dev/null | cut -f1)"
    mode_ok=1
    for n in AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY; do
      printf '%s\n' "$env_secs" | grep -qx "$n" || { echo "  ERROR: secret $n missing in environment $GH_ENV" >&2; mode_ok=0; }
      if printf '%s\n' "$repo_secs" | grep -qx "$n"; then
        echo "  ERROR: repository-level secret $n exists; delete it (gh secret delete $n --repo $REPO) so only $GH_ENV holds the key" >&2; mode_ok=0
      fi
    done
    [ "$mode_ok" = 1 ] || { echo "error: not switching the CI job on (AWS_DEPLOY_MODE unchanged)" >&2; exit 1; }
    # Set last: this is what switches the deploy-aws-keys job on.
    if gh_t variable set AWS_DEPLOY_MODE --repo "$REPO" --body keys >/dev/null 2>&1; then
      echo "  GitHub variable AWS_DEPLOY_MODE=keys set (deploy-aws-keys job enabled)"
    else
      echo "error: could not set AWS_DEPLOY_MODE (retry: gh variable set AWS_DEPLOY_MODE --repo $REPO --body keys)" >&2; exit 1
    fi
  else
    echo "[dry-run] gh variable set AWS_REGION / AWS_STACK_NAME / AWS_CFN_ROLE_ARN / AWS_ACCOUNT_ID, then AWS_DEPLOY_MODE=keys --repo $REPO"
  fi
fi

echo
if [ "$MODE" = apply ]; then
  echo "Done."
else
  echo "Dry run only, nothing was changed. Re-run with --apply to execute."
fi
if [ -n "$NO_OIDC" ]; then
  echo
  echo "Next: deploy/aws/deploy.sh --apply --account $ACCOUNT (it runs this bootstrap itself)."
  exit 0
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
