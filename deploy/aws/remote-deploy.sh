#!/bin/bash
# Runs ON the EC2 instance (sent by deploy/aws/deploy.sh through SSM Run Command, as root).
# Usage: remote-deploy.sh <stack-name> <region> <image-uri:tag> <schema.sql> [app-commit] [data-volume-id]
# app-commit (git sha, hex) becomes APP_COMMIT in the container, which the server reports as X-Build.
# data-volume-id (vol-..., the stack output DataVolumeId, only with EnableDataVolume=true): the data
# disk is formatted if it has no filesystem at all and mounted at /opt/mg/data (#207: done here and not
# in user data, because user data is frozen and runs only on first boot). Postgres is NOT moved onto
# it: that is a one-time owner step (_docs/deploy-runbook.md).
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

# --- Data disk (#207). MG_DEV, MG_DATA_DIR, MG_FSTAB and MG_SLEEP exist only for deploy/aws/test-remote-deploy.sh. ---
DEV="${MG_DEV:-/dev}"
DATA_DIR="${MG_DATA_DIR:-/opt/mg/data}"

is_disk() { if [ -n "${MG_DEV:-}" ]; then [ -e "$1" ]; else [ -b "$1" ]; fi; }

# Prints the block device of EBS volume $1. Nitro (t3): the by-id link carries the volume id without
# the dash. Xen (t2): the attachment's device name /dev/sdf, shown as /dev/xvdf. Never the root disk.
find_data_disk() { # volume-id
  local id="${1#vol-}" link dev root_src root_disk
  root_src="$(findmnt -no SOURCE / 2>/dev/null || true)"
  root_disk="$(lsblk -no PKNAME "$root_src" 2>/dev/null | head -n1 || true)"
  [ -n "$root_disk" ] || root_disk="$(basename "${root_src:-/unknown}")"
  for link in "$DEV/disk/by-id/nvme-Amazon_Elastic_Block_Store_vol$id" \
              "$DEV/disk/by-id/nvme-Amazon_Elastic_Block_Store_vol${id}_1"; do
    [ -e "$link" ] || continue
    dev="$(readlink -f "$link")"
    [ "$(basename "$dev")" = "$root_disk" ] && continue
    echo "$dev"; return 0
  done
  for dev in "$DEV/xvdf" "$DEV/sdf"; do
    is_disk "$dev" || continue
    dev="$(readlink -f "$dev")"
    [ "$(basename "$dev")" = "$root_disk" ] && continue
    echo "$dev"; return 0
  done
  return 1
}

# Formats (only a disk with no filesystem and no partitions) and mounts the data disk at $DATA_DIR.
# Never moves Postgres. Fails (and the deploy with it, before mg-app is touched) on anything unexpected.
prepare_data_disk() { # volume-id
  local vol="$1" dev="" fstype uuid src aside
  for _ in $(seq 1 30); do   # the attachment can still be in progress right after the stack update
    dev="$(find_data_disk "$vol" || true)"
    [ -n "$dev" ] && break
    sleep "${MG_SLEEP:-2}"
  done
  [ -n "$dev" ] || { echo "data volume $vol: no device found (is it attached?)" >&2; return 1; }
  if mountpoint -q "$DATA_DIR"; then
    src="$(readlink -f "$(findmnt -no SOURCE "$DATA_DIR")")"
    [ "$src" = "$dev" ] || { echo "$DATA_DIR is mounted from $src, not from the data volume ($dev)" >&2; return 1; }
    echo "data volume $vol ($dev) already mounted at $DATA_DIR"
    mkdir -p "$DATA_DIR/backups"
    return 0
  fi
  fstype="$(blkid -o value -s TYPE "$dev" 2>/dev/null || true)"
  if [ -z "$fstype" ]; then
    if [ "$(lsblk -no NAME "$dev" 2>/dev/null | wc -l)" -gt 1 ]; then
      echo "data volume $dev has partitions but no filesystem: not formatting it, check by hand" >&2; return 1
    fi
    command -v mkfs.xfs >/dev/null 2>&1 || dnf install -y -q xfsprogs
    mkfs.xfs -q "$dev"
    echo "data volume $vol ($dev): formatted (XFS; it had no filesystem)"
    fstype=xfs
  fi
  [ "$fstype" = xfs ] || { echo "data volume $dev has filesystem $fstype, expected xfs: not mounted" >&2; return 1; }
  uuid="$(blkid -o value -s UUID "$dev")"
  [ -n "$uuid" ] || { echo "data volume $dev: no filesystem UUID" >&2; return 1; }
  # Something already in $DATA_DIR on the root disk (dumps written before the disk existed): refuse if a
  # container uses it, otherwise move it aside and copy the dumps onto the disk after the mount.
  aside=""
  if [ -d "$DATA_DIR" ] && [ -n "$(ls -A "$DATA_DIR")" ]; then
    local ids mounts=""
    ids="$(docker ps -q)" || { echo "docker ps failed" >&2; return 1; }
    if [ -n "$ids" ]; then
      # shellcheck disable=SC2086  # ids are hex container ids, one per word
      mounts="$(docker inspect -f '{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}' $ids)" \
        || { echo "docker inspect failed" >&2; return 1; }
    fi
    if printf '%s\n' "$mounts" | grep -qE "^${DATA_DIR}(/|\$)"; then
      echo "a running container uses $DATA_DIR on the root disk: not mounting over it, check by hand" >&2; return 1
    fi
    aside="$DATA_DIR.root-$(date -u +%Y%m%dT%H%M%SZ)"
    mv "$DATA_DIR" "$aside"
    echo "moved the old $DATA_DIR (root disk) to $aside"
  fi
  mkdir -p "$DATA_DIR"
  # nofail: the instance still boots without the disk (SSM keeps working). x-systemd.*: docker (and so
  # mg-db) waits for the mount and does not start without it, so after the Postgres move it can never
  # initialise an empty cluster on the root disk. Same line as in the runbook.
  grep -q "^UUID=$uuid " "${MG_FSTAB:-/etc/fstab}" || \
    echo "UUID=$uuid $DATA_DIR xfs defaults,nofail,x-systemd.before=docker.service,x-systemd.required-by=docker.service 0 2" >> "${MG_FSTAB:-/etc/fstab}"
  systemctl daemon-reload
  mount "$DATA_DIR"
  mkdir -p "$DATA_DIR/backups"
  if [ -n "$aside" ] && [ -d "$aside/backups" ]; then cp -a "$aside/backups/." "$DATA_DIR/backups/"; fi
  echo "data volume $vol ($dev) mounted at $DATA_DIR"
}
# deploy/aws/test-remote-deploy.sh sources this file for the functions above only.
if [ -n "${MG_REMOTE_DEPLOY_FUNCS_ONLY:-}" ]; then return 0 2>/dev/null || exit 0; fi

STACK="$1"; REGION="$2"; IMAGE="$3"; SCHEMA="$4"; APP_COMMIT="${5:-}"; DATA_VOLUME="${6:-}"
if [ -n "$APP_COMMIT" ] && ! [[ "$APP_COMMIT" =~ ^[0-9a-f]{7,40}$ ]]; then
  echo "app-commit must be a hex git sha" >&2; exit 1
fi
if [ -n "$DATA_VOLUME" ] && ! [[ "$DATA_VOLUME" =~ ^vol-[0-9a-f]{8,17}$ ]]; then
  echo "data-volume-id must look like vol-0123456789abcdef0" >&2; exit 1
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

# Data disk (#207): only when deploy.sh passed the stack output DataVolumeId (EnableDataVolume=true).
if [ -n "$DATA_VOLUME" ]; then
  prepare_data_disk "$DATA_VOLUME"
else
  echo "no data volume (EnableDataVolume=false): Postgres and the dumps stay on the root disk"
fi

# Nightly pg_dump (#182, item 13): a script and a systemd timer, rewritten on every run.
# Writes to /opt/mg/data/backups: the data disk when it is mounted, otherwise a directory on the root disk.
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
