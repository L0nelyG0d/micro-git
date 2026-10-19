#!/usr/bin/env bash
# Integration smoke (issue #113): the real frontend dev server (Vite /api proxy,
# real HttpApiService, VITE_USE_MOCK unset) in front of the real backend on a
# throwaway SQLite file. Everything goes through the Vite port, exactly like the
# browser does. Free ports, temp dir and a random JWT_SECRET; nothing is printed
# that is secret and everything is removed on exit.
#
#   make smoke-sqlite            (or: scripts/smoke-sqlite.sh)
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/mg-smoke.XXXXXX")"
JAR="$TMP/jar"
pass=0; fail=0
bpid=; fpid=

cleanup() {
  for p in $fpid $bpid; do
    [ -n "$p" ] && { pkill -TERM -P "$p" 2>/dev/null; kill -TERM "$p" 2>/dev/null; }
  done
  sleep 0.5
  for p in $fpid $bpid; do
    [ -n "$p" ] && { pkill -KILL -P "$p" 2>/dev/null; kill -KILL "$p" 2>/dev/null; }
  done
  rm -rf "$TMP"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

freeport() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'; }
ok()  { pass=$((pass + 1)); echo "  ok   $1"; }
bad() { fail=$((fail + 1)); echo "  FAIL $1 ${2:-}"; }
check() { # check "name" "expected" "actual"
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "(want '$2', got '$3')"; fi
}

for c in curl jq python3 go npx; do
  command -v "$c" >/dev/null || { echo "smoke: $c is required"; exit 2; }
done
[ -d "$ROOT/frontend/node_modules" ] || { echo "smoke: run 'make setup' first (frontend/node_modules missing)"; exit 2; }

BP="$(freeport)"; VP="$(freeport)"
BASE="http://127.0.0.1:$VP"
SECRET="$(python3 -c 'import secrets;print(secrets.token_hex(32))')"
echo "smoke: backend :$BP (sqlite in temp dir), vite :$VP"

(cd "$ROOT/backend" && go build -o "$TMP/mg-backend" .) || { echo "smoke: backend build failed"; exit 2; }
DB_DRIVER=sqlite SQLITE_PATH="$TMP/mg.db" JWT_SECRET="$SECRET" PORT="$BP" \
  "$TMP/mg-backend" >"$TMP/backend.log" 2>&1 & bpid=$!
(cd "$ROOT/frontend" && unset VITE_USE_MOCK && export API_PROXY_TARGET="http://127.0.0.1:$BP" \
  && exec npx --no-install vite --host 127.0.0.1 --port "$VP" --strictPort) >"$TMP/vite.log" 2>&1 & fpid=$!

for _ in $(seq 1 120); do
  [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/health")" = 200 ] && break
  sleep 0.5
done
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/health")" = 200 ] || {
  echo "smoke: stack did not come up"; tail -n 20 "$TMP/backend.log" "$TMP/vite.log"; exit 1; }

# call METHOD PATH [JSON]: sets $code, $body, $hdrs; uses the cookie jar like a browser.
call() {
  local args=(-s -D "$TMP/h" -o "$TMP/b" -w '%{http_code}' -b "$JAR" -c "$JAR" -X "$1" "$BASE/api$2")
  [ -n "${3:-}" ] && args+=(-H 'Content-Type: application/json' -d "$3")
  code="$(curl "${args[@]}")"; body="$(cat "$TMP/b")"; hdrs="$(cat "$TMP/h")"
}

echo "SPA"
code="$(curl -s -o "$TMP/b" -w '%{http_code}' "$BASE/")"
check "GET / is 200" 200 "$code"
grep -q 'id="root"' "$TMP/b" && ok "index.html has the React root" || bad "index.html has the React root"
code="$(curl -s -o "$TMP/b" -w '%{http_code}' -H 'Accept: text/html' "$BASE/someone/some-repo")"
check "deep link falls back to the SPA" 200 "$code"
grep -q 'id="root"' "$TMP/b" && ok "deep link serves index.html" || bad "deep link serves index.html"
curl -s "$BASE/src/services/index.ts" | grep -q 'VITE_USE_MOCK' && ok "frontend service switch present (mock off: env unset)" || bad "service switch"

echo "Auth"
call GET /auth/me; check "me without cookie is 401" 401 "$code"
call POST /auth/register '{"username":"smoke","password":"smoke-pass-123"}'
check "register is 201" 201 "$code"
check "register returns username" smoke "$(echo "$body" | jq -r .username)"
echo "$hdrs" | grep -qi '^set-cookie: session=' && bad "register must not set a cookie" || ok "register sets no cookie (login next)"
call POST /auth/login '{"username":"smoke","password":"smoke-pass-123"}'
check "login is 200" 200 "$code"
echo "$hdrs" | grep -i '^set-cookie: session=' | grep -qi 'HttpOnly' && ok "session cookie is HttpOnly" || bad "session cookie is HttpOnly"
echo "$hdrs" | grep -i '^set-cookie: session=' | grep -qi 'Path=/' && ok "session cookie Path=/" || bad "session cookie Path=/"
echo "$hdrs" | grep -i '^set-cookie: session=' | grep -qi 'Secure' && bad "session cookie must not be Secure over http" || ok "session cookie not Secure over http"
call GET /auth/me; check "me with cookie is 200" 200 "$code"
check "me username" smoke "$(echo "$body" | jq -r .username)"

echo "Repo, commit, browse"
call POST /repos '{"name":"demo","description":"smoke repo"}'
check "create repo is 201" 201 "$code"
check "repo owner" smoke "$(echo "$body" | jq -r .owner)"
check "default branch" main "$(echo "$body" | jq -r .default_branch)"
call POST /repos/smoke/demo/commits '{"branch":"main","expected_tip":"","message":"first commit","changes":[{"path":"README.md","content":"hello\n"},{"path":"src/app.txt","content":"x\n"}]}'
check "first commit is 201" 201 "$code"
oid="$(echo "$body" | jq -r .oid)"
echo "$oid" | grep -Eq '^[0-9a-f]{40}$' && ok "commit oid is 40 hex" || bad "commit oid is 40 hex" "($oid)"
call POST /repos/smoke/demo/commits "{\"branch\":\"main\",\"expected_tip\":\"$oid\",\"message\":\"second\",\"changes\":[{\"path\":\"README.md\",\"content\":\"hello world\\n\"}]}"
check "second commit is 201" 201 "$code"
tip="$(echo "$body" | jq -r .oid)"
call POST /repos/smoke/demo/commits "{\"branch\":\"main\",\"expected_tip\":\"$oid\",\"message\":\"stale\",\"changes\":[{\"path\":\"a\",\"content\":\"b\"}]}"
check "stale tip is 409" 409 "$code"
check "409 carries current_tip" "$tip" "$(echo "$body" | jq -r .current_tip)"

call GET "/repos/smoke/demo/commits"
check "history is 200" 200 "$code"
check "history length" 2 "$(echo "$body" | jq '.commits|length')"
check "history newest first" second "$(echo "$body" | jq -r '.commits[0].message')"
check "history author" smoke "$(echo "$body" | jq -r '.commits[0].author')"
call GET "/repos/smoke/demo/tree/main"; check "tree root is 200" 200 "$code"
check "tree entries" "src README.md" "$(echo "$body" | jq -r '[.entries[].name]|join(" ")')"
call GET "/repos/smoke/demo/blob/main/README.md"; check "blob is 200" 200 "$code"
check "blob content" "hello world" "$(echo "$body" | jq -r .content | tr -d '\n')"
call GET "/repos/smoke/demo/commits/$tip"; check "commit detail is 200" 200 "$code"
call GET "/repos"; check "repo list has the repo" demo "$(echo "$body" | jq -r '.repos[0].name')"

echo "Reload (new request, same cookie jar) and logout"
call GET /auth/me; check "still logged in after reload" smoke "$(echo "$body" | jq -r .username)"
call POST /auth/logout; check "logout is 204" 204 "$code"
call GET /auth/me; check "logged out: me is 401" 401 "$code"
call POST /repos '{"name":"nope"}'; check "write after logout is 401" 401 "$code"
call POST /auth/login '{"username":"smoke","password":"smoke-pass-123"}'; check "login again is 200" 200 "$code"
call GET /auth/me; check "me after login" smoke "$(echo "$body" | jq -r .username)"
call POST /auth/login '{"username":"smoke","password":"wrong-password"}'; check "wrong password is 401" 401 "$code"

echo "Persistence (restart backend on the same SQLite file)"
kill -TERM "$bpid" 2>/dev/null; wait "$bpid" 2>/dev/null
DB_DRIVER=sqlite SQLITE_PATH="$TMP/mg.db" JWT_SECRET="$SECRET" PORT="$BP" \
  "$TMP/mg-backend" >>"$TMP/backend.log" 2>&1 & bpid=$!
for _ in $(seq 1 60); do
  [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/health")" = 200 ] && break; sleep 0.25
done
call GET /auth/me; check "session survives backend restart" smoke "$(echo "$body" | jq -r .username)"
call GET "/repos/smoke/demo/commits"; check "history survives restart" 2 "$(echo "$body" | jq '.commits|length')"

echo "Mock vs real service (same journey, same answer shapes)"
rm -f "$JAR"
if (cd "$ROOT/frontend" && SMOKE_BASE_URL="$BASE" npx --no-install vitest run src/services/parity.live.test.ts >"$TMP/parity.log" 2>&1) && grep -q "1 passed" "$TMP/parity.log"; then
  ok "MockApiService and HttpApiService agree on every step"
else
  bad "mock/real parity"; cat "$TMP/parity.log"
fi

echo
echo "smoke: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
