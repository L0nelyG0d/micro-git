#!/usr/bin/env bash
# verify-recovery.sh <route> [incident-id]
# Polls Prometheus error ratio and /health for up to VERIFY_MINUTES (default 5).
# Success = VERIFY_OK_POLLS (default 3) consecutive polls with ratio < ERR_THRESHOLD (0.01) and /health 200,
# counting only polls taken after a warm-up of WARMUP_SECONDS (default 75 = 1m rate window + scrape lag) from the start
# of this script (= right after the action), so pre-action 5xx still inside the rate window cannot pass or fail it.
# Probe: if PROBE_URL is set, PROBE_COUNT (5) GETs are sent to it every poll (optional PROBE_COOKIE_JAR for auth) so the
# route has fresh traffic; without it the ratio relies on organic traffic and "no data" counts as NOT recovered.
# Ratio = (5xx rate or 0) / total rate, so a route that now returns only 2xx reads 0, not "no data".
# No data from Prometheus counts as NOT recovered unless ALLOW_NO_DATA=1.
# DRY_RUN=1 skips the network and reports a simulated pass. Exit 0 recovered, 1 not recovered.
set -uo pipefail
. "$(dirname "$0")/../lib.sh"
ROUTE="${1:-}"; ID="${2:-}"
valid_route "$ROUTE" || { echo "usage: $0 <route> [incident-id]" >&2; exit 2; }
MIN="${VERIFY_MINUTES:-5}"; EVERY="${VERIFY_INTERVAL:-15}"; THR="${ERR_THRESHOLD:-0.01}"; NEED="${VERIFY_OK_POLLS:-3}"
M_REQ="${M_REQ:-http_requests_total}"
INC=""; [ -n "$ID" ] && valid_id "$ID" && INC="$INCIDENTS_DIR/$ID"
log() { echo "verify: $*"; [ -n "$INC" ] && audit "$INC" verify "$*"; return 0; }
if [ "${DRY_RUN:-0}" = 1 ]; then log "DRY_RUN simulated recovery for $ROUTE"; exit 0; fi

if [ -n "${PROBE_URL:-}" ]; then
  # The URL as given is NEVER logged or echoed (a rejected value may carry a password; a password may contain "/", "?", "#").
  # Accepted only as http(s)://, logged as scheme://host[:port][/path]: userinfo is dropped up to the LAST "@", then the
  # query and fragment. The host part must be a plain hostname[:port], otherwise the URL is rejected.
  PU=""
  case "$PROBE_URL" in
    http://*|https://*)
      SCHEME="${PROBE_URL%%://*}"; REST="${PROBE_URL#*://}"
      REST="${REST##*@}"            # longest prefix ending in "@" = everything up to the last "@"
      REST="${REST%%[?#]*}"         # drop query and fragment
      if printf '%s' "${REST%%/*}" | grep -Eq '^[A-Za-z0-9.-]+(:[0-9]+)?$'; then PU="$SCHEME://$REST"; fi
      ;;
  esac
  if [ -n "$PU" ]; then
    log "probe enabled: ${PROBE_COUNT:-5} GET per poll to $PU$([ -n "${PROBE_COOKIE_JAR:-}" ] && echo " with a cookie jar")"
  else   # rejected: fixed text only, no probing; the ratio then relies on organic traffic (see header)
    log "PROBE_URL rejected (must be http:// or https://); probing disabled"
    PROBE_URL=""
  fi
fi
Q="(sum(rate(${M_REQ}{route=\"$ROUTE\",status=~\"5..\"}[1m])) or vector(0)) / sum(rate(${M_REQ}{route=\"$ROUTE\"}[1m]))"
WARM="${WARMUP_SECONDS:-75}"; START_TS=$(date +%s)
deadline=$(( START_TS + WARM + MIN*60 )); ok=0
while :; do
  if [ -n "${PROBE_URL:-}" ]; then
    for _ in $(seq 1 "${PROBE_COUNT:-5}"); do curl -s -o /dev/null --max-time 5 ${PROBE_COOKIE_JAR:+-b "$PROBE_COOKIE_JAR"} "$PROBE_URL"; sleep 0.3; done
  fi
  ratio="$(curl -sS -G --max-time 5 --data-urlencode "query=$Q" "$PROM_URL/api/v1/query" 2>/dev/null | jq -r '.data.result[0].value[1] // "nodata"' 2>/dev/null)"
  code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "$APP_URL/health" 2>/dev/null)"
  good=0
  if [ "$code" = 200 ]; then
    case "$ratio" in
      nodata|NaN|"") [ "${ALLOW_NO_DATA:-0}" = 1 ] && good=1 ;;
      *) awk -v r="$ratio" -v t="$THR" 'BEGIN{exit !(r<t)}' && good=1 ;;
    esac
  fi
  if [ $(( $(date +%s) - START_TS )) -lt "$WARM" ]; then good=0; log "warm-up (<${WARM}s after action), poll not counted"; fi
  if [ $good = 1 ]; then ok=$((ok+1)); else ok=0; fi
  log "ratio=${ratio:-?} health=${code:-?} consecutive_ok=$ok/$NEED"
  [ $ok -ge "$NEED" ] && { log "RECOVERED"; exit 0; }
  [ "$(date +%s)" -ge "$deadline" ] && { log "NOT RECOVERED after ${MIN}m"; exit 1; }
  sleep "$EVERY"
done
