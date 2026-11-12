#!/bin/bash
# Runs ON the EC2 instance (sent by the deploy-aws workflow through SSM Run Command).
# Usage: remote-deploy.sh <stack-name> <region> <image-uri:tag> <schema.sql>
# Never prints the JWT secret. Idempotent: safe to re-run.
set -euo pipefail

STACK="$1"; REGION="$2"; IMAGE="$3"; SCHEMA="$4"
REGISTRY="${IMAGE%%/*}"

# Wait for first-boot user data (Docker, Postgres, Caddy). Fails if cloud-init errored.
cloud-init status --wait >/dev/null || { echo "cloud-init failed on first boot" >&2; cloud-init status --long >&2 || true; exit 1; }
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

docker rm -f mg-app >/dev/null 2>&1 || true
docker run -d --name mg-app --restart unless-stopped --network mg \
  --env-file /opt/mg/app.env "$IMAGE" >/dev/null
sleep 3
docker ps --filter name=mg-app --filter status=running --format 'mg-app running: {{.Image}}' | grep -q running \
  || { docker logs --tail 30 mg-app >&2; exit 1; }
docker ps --format '{{.Names}} {{.Status}}'
