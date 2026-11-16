#!/bin/bash
# Runs ON the EC2 instance (sent by deploy/aws/deploy.sh through SSM Run Command, as root).
# Usage: remote-deploy.sh <stack-name> <region> <image-uri:tag> <schema.sql> [app-commit]
# app-commit (git sha, hex) becomes APP_COMMIT in the container, which the server reports as X-Build.
# Never prints the JWT secret. Idempotent: safe to re-run.
#
# Rollback (#182, item 9): before the new mg-app replaces the running one, the running container's
# image and APP_COMMIT are recorded. The new container must answer /health with HTTP 200 (and with
# X-Build equal to app-commit when one is given) within 60 s. If not, its last 30 log lines are
# printed, it is removed, the previous image is started again with its previous APP_COMMIT, and the
# script exits 1 so that deploy.sh fails. Without a previous container (first deploy) it only exits 1.
# The schema is NOT rolled back: schema.sql is additive and idempotent, so the previous image runs
# against the newer schema (it ignores the tables and columns it does not know).
set -euo pipefail

STACK="$1"; REGION="$2"; IMAGE="$3"; SCHEMA="$4"; APP_COMMIT="${5:-}"
if [ -n "$APP_COMMIT" ] && ! [[ "$APP_COMMIT" =~ ^[0-9a-f]{7,40}$ ]]; then
  echo "app-commit must be a hex git sha" >&2; exit 1
fi
REGISTRY="${IMAGE%%/*}"

# Start mg-app from an image (replaces the running one). Same flags for a new image and a rollback.
start_app() { # image [app-commit]
  local commit="${2:-}" extra=()
  [ -n "$commit" ] && extra=(-e "APP_COMMIT=$commit")
  docker rm -f mg-app >/dev/null 2>&1 || true
  docker run -d --name mg-app --restart unless-stopped --network mg \
    --env-file /opt/mg/app.env -e APP_ENV=prod -e TRUSTED_PROXY_HOPS=1 ${extra[@]+"${extra[@]}"} "$1" >/dev/null
}

# One probe of mg-app's /health from inside the container (the image is alpine: busybox wget).
# Prints "<http status> <x-build>"; "none" when the request did not get an answer.
probe_app() {
  local hdrs
  hdrs="$(docker exec mg-app wget -q -S -O /dev/null http://127.0.0.1:8080/health 2>&1 || true)"
  hdrs="$(printf '%s\n' "$hdrs" | tr -d '\r' | sed 's/^[[:space:]]*//')"
  printf '%s %s\n' \
    "$(printf '%s\n' "$hdrs" | awk '/^HTTP\//{c=$2} END{print c}')" \
    "$(printf '%s\n' "$hdrs" | awk -F': ' 'tolower($1)=="x-build"{print $2}' | tail -n1)" \
    | sed 's/ $/ none/; s/^ /none /'
}

# Up to 60 s (30 x 2 s): HTTP 200, and X-Build == $1 when $1 is set. Fails at once if mg-app stopped.
app_healthy() { # [app-commit]
  local commit="${1:-}" i out code="none" build="none"
  for i in $(seq 1 30); do
    if [ -z "$(docker ps -q --filter name=^mg-app$ --filter status=running)" ]; then
      echo "mg-app is not running" >&2; return 1
    fi
    out="$(probe_app)"; code="${out%% *}"; build="${out#* }"
    if [ "$code" = 200 ] && { [ -z "$commit" ] || [ "$build" = "$commit" ]; }; then return 0; fi
    sleep 2
  done
  echo "health check failed: last answer HTTP $code, X-Build $build (want 200${commit:+, X-Build $commit})" >&2
  return 1
}

# Wait for first-boot user data (Docker, Postgres, Caddy). Fails if cloud-init errored.
# Exit 0 = done, 2 = done with recoverable errors (accepted); anything else fails.
rc=0
cloud-init status --wait >/dev/null || rc=$?
if [ "$rc" -ne 0 ] && [ "$rc" -ne 2 ]; then
  echo "cloud-init failed on first boot (exit $rc)" >&2; cloud-init status --long >&2 || true; exit 1
fi
for c in mg-db mg-proxy; do
  docker inspect "$c" >/dev/null 2>&1 || { echo "container $c does not exist (first boot incomplete)" >&2; exit 1; }
done

aws ecr get-login-password --region "$REGION" | docker login --username AWS --password-stdin "$REGISTRY" >/dev/null
docker pull "$IMAGE"

# JWT secret from SSM Parameter Store (instance role), written only to the 600 env file.
umask 077
JWT="$(aws ssm get-parameter --region "$REGION" --name "/$STACK/jwt-secret" --with-decryption --query Parameter.Value --output text)"
if [ "${#JWT}" -lt 32 ]; then echo "jwt-secret parameter missing or shorter than 32 chars" >&2; exit 1; fi
grep -v '^JWT_SECRET=' /opt/mg/app.env > /opt/mg/app.env.new || true
printf 'JWT_SECRET=%s\n' "$JWT" >> /opt/mg/app.env.new
mv /opt/mg/app.env.new /opt/mg/app.env
chmod 600 /opt/mg/app.env
unset JWT

# Postgres must accept connections before the schema is applied.
for _ in $(seq 1 60); do
  docker exec mg-db pg_isready -U mg -d mg >/dev/null 2>&1 && break
  sleep 2
done
docker exec mg-db pg_isready -U mg -d mg >/dev/null
docker exec -i mg-db psql -v ON_ERROR_STOP=1 -q -U mg -d mg < "$SCHEMA"

# Nightly pg_dump (#182, item 13): a script and a systemd timer, rewritten on every run.
install_pgdump() {
  mkdir -p /opt/mg/bin
  cat > /opt/mg/bin/pgdump.sh <<'PGDUMP'
#!/bin/bash
# Nightly logical backup of the mg database (installed by deploy/aws/remote-deploy.sh).
set -euo pipefail
umask 077
dir=/opt/mg/data/backups
mkdir -p "$dir"
out="$dir/mg-$(date -u +%Y%m%dT%H%M%SZ).sql.gz"
if ! docker exec mg-db pg_dump -U mg -d mg | gzip > "$out.part"; then
  rm -f "$out.part"; echo "pg_dump failed" >&2; exit 1
fi
mv "$out.part" "$out"
find "$dir" -maxdepth 1 -name 'mg-*.sql.gz' -mtime +7 -delete
echo "wrote $out"
PGDUMP
  chmod 700 /opt/mg/bin/pgdump.sh
  cat > /etc/systemd/system/mg-pgdump.service <<'UNIT'
[Unit]
Description=Nightly pg_dump of the mg database (gzip, dumps older than 7 days deleted)
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
UMask=0077
ExecStart=/opt/mg/bin/pgdump.sh
UNIT
  cat > /etc/systemd/system/mg-pgdump.timer <<'TIMER'
[Unit]
Description=Run mg-pgdump every night

[Timer]
OnCalendar=*-*-* 03:15:00 UTC
Persistent=true

[Install]
WantedBy=timers.target
TIMER
  systemctl daemon-reload
  systemctl enable --now mg-pgdump.timer >/dev/null
}
install_pgdump

# Record what runs now, so a failed rollout can go back to it (empty on the first deploy).
PREV_IMAGE="$(docker inspect -f '{{.Config.Image}}' mg-app 2>/dev/null || true)"
PREV_COMMIT=""
if [ -n "$PREV_IMAGE" ]; then
  PREV_COMMIT="$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' mg-app 2>/dev/null \
    | sed -n 's/^APP_COMMIT=//p' | head -n1 || true)"
  [[ "$PREV_COMMIT" =~ ^[0-9a-f]{7,40}$ ]] || PREV_COMMIT=""
fi

start_app "$IMAGE" "$APP_COMMIT"
if app_healthy "$APP_COMMIT"; then
  echo "mg-app healthy: $IMAGE${APP_COMMIT:+ (X-Build $APP_COMMIT)}"
  docker ps --format '{{.Names}} {{.Status}}'
  exit 0
fi

echo "new mg-app ($IMAGE) failed its health check; last log lines:" >&2
docker logs --tail 30 mg-app 2>&1 | sed 's/^/  /' >&2 || true
docker rm -f mg-app >/dev/null 2>&1 || true
if [ -z "$PREV_IMAGE" ]; then
  echo "no previous mg-app container (first deploy): nothing to roll back to" >&2
  exit 1
fi
start_app "$PREV_IMAGE" "$PREV_COMMIT"
if app_healthy "$PREV_COMMIT"; then
  echo "rolled back to $PREV_IMAGE${PREV_COMMIT:+ (X-Build $PREV_COMMIT)}" >&2
else
  echo "rolled back to $PREV_IMAGE, but it fails its health check too: check the instance" >&2
fi
exit 1
