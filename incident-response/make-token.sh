#!/usr/bin/env bash
# make-token.sh [file] : create the shared bearer token for POST /alert (default incident-response/.alert-token, mode 600).
# Refuses to overwrite an existing file (delete it to rotate). The same file is mounted into Alertmanager as
# http_config.authorization.credentials_file. Never print or commit it (.gitignore covers it).
set -euo pipefail
F="${1:-${ALERT_TOKEN_FILE:-$(cd "$(dirname "$0")" && pwd)/.alert-token}}"
[ -e "$F" ] && { echo "exists: $F (delete it to rotate)"; exit 0; }
( umask 077; openssl rand -hex 32 > "$F" )
chmod 600 "$F"; echo "created $F (mode 600)"
