#!/usr/bin/env bash
# make-token.sh [file] : create the shared bearer token for POST /alert (default incident-response/.alert-token, mode 600).
# Refuses to overwrite an existing file (delete it to rotate). The same file is mounted into Alertmanager as
# http_config.authorization.credentials_file. Never print or commit it (.gitignore covers it).
set -euo pipefail
F="${1:-${ALERT_TOKEN_FILE:-$(cd "$(dirname "$0")" && pwd)/.alert-token}}"
[ -e "$F" ] && { echo "exists: $F (delete it to rotate)"; exit 0; }
( umask 077; openssl rand -hex 32 > "$F" )
chmod 600 "$F"; echo "created $F (mode 600)"
# Linux bind mounts keep host ownership: the Alertmanager container runs as uid 65534 (nobody) and cannot read a 600 file
# owned by someone else. Root can hand it over; a normal user is told how (Docker Desktop on macOS needs nothing).
if [ "$(uname -s)" = Linux ]; then
  if [ "$(id -u)" = 0 ]; then chown 65534:65534 "$F" && echo "chown 65534 (nobody) so Alertmanager can read it; mode stays 600"
  else echo "Linux: Alertmanager runs as nobody (65534). If it cannot read the token run: sudo chown 65534 '$F'  (keep mode 600)"; fi
fi
