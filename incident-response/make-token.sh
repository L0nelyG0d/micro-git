#!/usr/bin/env bash
# make-token.sh [file] : create the shared bearer token for POST /alert (default incident-response/.alert-token, mode 600).
# Refuses to overwrite an existing file (delete it to rotate). The same file is mounted into Alertmanager as
# http_config.authorization.credentials_file. Never print or commit it (.gitignore covers it).
set -euo pipefail
F="${1:-${ALERT_TOKEN_FILE:-$(cd "$(dirname "$0")" && pwd)/.alert-token}}"
# Existing file: keep the content (no rotation) but REPAIR mode and, on Linux as root, ownership, so re-running fixes a token
# that Alertmanager or the listener cannot read.
if [ -e "$F" ]; then echo "exists: $F (delete it to rotate; mode/ownership re-checked)"; else
  ( umask 077; openssl rand -hex 32 > "$F" )
  echo "created $F"
fi
# Re-run: refuse a symlink before any chmod/chown (chmod would follow it and change the mode of its target).
if [ -L "$F" ]; then echo "refused: $F is a symlink; replace it with a regular file (delete it to rotate)" >&2; exit 1; fi
chmod 600 "$F"
# Linux bind mounts keep host ownership: the Alertmanager container runs as uid 65534 (nobody) and cannot read a 600 file
# owned by someone else. Root can hand it over; a normal user is told how (Docker Desktop on macOS needs nothing).
# Caveat: once owned by 65534 the listener must run as that uid or root; a listener started as a normal user cannot read the
# file and logs a "config" audit line (token file unreadable) instead of silently rejecting every alert.
# UNVERIFIED on a real Linux host: the chown branch is only exercised with stubbed uname/id/chown in test-incident.sh.
if [ "$(uname -s)" = Linux ]; then
  if [ "$(id -u)" = 0 ]; then chown 65534:65534 "$F" && echo "chown 65534 (nobody) so Alertmanager can read it; mode 600"
  else echo "Linux: Alertmanager runs as nobody (65534). If it cannot read the token run: sudo chown 65534 '$F'  (keep mode 600)"; fi
fi
