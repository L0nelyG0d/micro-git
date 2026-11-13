#!/usr/bin/env bash
# collect-evidence.sh <incident-id> <route>
# Read-only, bounded, repeatable, allowlisted evidence collection.
# Sources: Prometheus, Loki, Tempo (HTTP GET only), git log, docker compose images, /health,
#          app_runtime (docker inspect with --format filters: ONLY the value of FAULT_ENDPOINT_500 and the
#          image tag of the running app container; no other env is ever read, so no secrets).
# Each source fails soft; failures are recorded in evidence-manifest.json.
# Env: PROM_URL LOKI_URL TEMPO_URL APP_URL (default http://localhost:${APP_PORT:-8080}) INCIDENTS_DIR
#      COMPOSE_FILES (colon list) / COMPOSE_PROJECT_NAME select the compose stack for app_runtime and images
#      (unset = those two sources fail soft: recorded as failed, nothing is guessed) LOKI_SELECTOR WINDOW(5m)
#      MAX_BYTES(262144) EVIDENCE_FIXTURE=<dir> (use canned files instead of the network)
set -uo pipefail
. "$(dirname "$0")/lib.sh"
command -v jq >/dev/null || { echo "jq required" >&2; exit 2; }

ID="${1:-}"; ROUTE="${2:-}"
valid_id "$ID" && valid_route "$ROUTE" || { echo "usage: $0 <incident-id> <route>  (ids [A-Za-z0-9._-], route [A-Za-z0-9_/.:{}*-])" >&2; exit 2; }
WINDOW="${WINDOW:-5m}"; MAX_BYTES="${MAX_BYTES:-262144}"
LOKI_SELECTOR="${LOKI_SELECTOR:-}"; [ -n "$LOKI_SELECTOR" ] || LOKI_SELECTOR='{service_name="micro-git"}'
M_REQ="${M_REQ:-http_requests_total}"; M_DUR="${M_DUR:-http_request_duration_seconds_bucket}"

INC="$INCIDENTS_DIR/$ID"; EV="$INC/evidence"; mkdir -p "$EV"
MAN="$EV/.manifest.ndjson"; : > "$MAN"
NOW="$(date +%s)"; START=$((NOW-900))
audit "$INC" collect_start "route=$ROUTE window=$WINDOW fixture=${EVIDENCE_FIXTURE:-none}"

record() { # name query status [note]
  local f="$EV/$1.json" bytes=0 sha=""
  [ -f "$f" ] && { bytes=$(wc -c < "$f" | tr -d ' '); sha=$(sha256 "$f"); }
  jq -nc --arg file "$1.json" --arg query "$2" --arg time "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg sha "$sha" --argjson bytes "$bytes" --arg status "$3" --arg note "${4:-}" \
    '{file:$file,query:$query,time:$time,sha256:$sha,bytes:$bytes,status:$status,note:$note}' >> "$MAN"
  audit "$INC" evidence "$1 status=$3 bytes=$bytes $4"
}

# finish <name> <query> <raw-file> <rc>: redact, cap size, validate JSON, record
finish() {
  local name="$1" q="$2" raw="$3" rc="$4" f="$EV/$1.json" note="" st=ok
  if [ "$rc" -ne 0 ] || [ ! -s "$raw" ]; then rm -f "$f"; record "$name" "$q" failed "source unreachable or empty (rc=$rc)"; return; fi
  # Redact first; on ANY failure drop the content (never keep the unredacted file).
  if redact < "$raw" > "$f.red" 2>/dev/null; then
    head -c "$MAX_BYTES" < "$f.red" > "$f"; rm -f "$f.red"
    if [ "$(wc -c < "$raw")" -gt "$MAX_BYTES" ]; then note="truncated to $MAX_BYTES bytes"; fi
    if ! jq -e . "$f" >/dev/null 2>&1; then
      [ -z "$note" ] && note="not valid JSON"
      if jq -Rs '{raw:.}' "$f" > "$f.tmp" 2>/dev/null; then mv "$f.tmp" "$f"; else rm -f "$f.tmp"; printf '{"raw":"[REDACTED-ERROR]"}' > "$f"; note="$note; wrap failed, content dropped"; fi
    fi
  else
    rm -f "$f.red"; printf '{"raw":"[REDACTED-ERROR]"}' > "$f"; note="redaction failed; content dropped"; st=failed
  fi
  record "$name" "$q" "$st" "$note"
}

get() { # name base-url path query-string-args...  (GET only, 5s timeout)
  local name="$1" base="$2" path="$3"; shift 3
  local raw="$EV/.raw.$name" args=() q
  for kv in "$@"; do args+=(--data-urlencode "$kv"); done
  q="GET $base$path $*"
  if [ -n "${EVIDENCE_FIXTURE:-}" ]; then
    [ -f "$EVIDENCE_FIXTURE/$name.json" ] && { cp "$EVIDENCE_FIXTURE/$name.json" "$raw"; finish "$name" "fixture:$name.json" "$raw" 0; } || { : > "$raw"; finish "$name" "fixture:$name.json" "$raw" 1; }
  else
    curl -sS -G --max-time 5 --max-filesize "$MAX_BYTES" ${args[@]+"${args[@]}"} "$base$path" -o "$raw" 2>"$raw.err"; finish "$name" "$q" "$raw" $?
  fi
  rm -f "$raw" "$raw.err"
}

prom() { get "$1" "$PROM_URL" /api/v1/query "query=$2"; }
prom "prom_error_ratio" "sum(rate(${M_REQ}{route=\"$ROUTE\",status=~\"5..\"}[$WINDOW])) / sum(rate(${M_REQ}{route=\"$ROUTE\"}[$WINDOW]))"
prom "prom_p95_latency" "histogram_quantile(0.95, sum by (le) (rate(${M_DUR}{route=\"$ROUTE\"}[$WINDOW])))"
prom "prom_request_rate" "sum(rate(${M_REQ}{route=\"$ROUTE\"}[$WINDOW]))"
prom "prom_build_info" "build_info"
get loki_errors "$LOKI_URL" /loki/api/v1/query_range \
  "query=${LOKI_SELECTOR} |= \"$ROUTE\" |~ \"(?i)error|panic|status=5..| 5.. \"" "limit=50" "start=${START}000000000" "end=${NOW}000000000" "direction=backward"
get tempo_traces "$TEMPO_URL" /api/search "tags=http.route=$ROUTE" "limit=5" "start=$START" "end=$NOW"
get health "$APP_URL" /health

# app_runtime: read-only allowlisted docker inspect. Go-template filters print only the one env var and the image tag.
if [ -n "${EVIDENCE_FIXTURE:-}" ]; then
  get app_runtime "" ""
else
  compose_args || COMPOSE_ARGS=()
  REPO0="$(git -C "$IR_DIR" rev-parse --show-toplevel 2>/dev/null)"
  CID=""; [ ${#COMPOSE_ARGS[@]} -gt 0 ] && CID="$(cd "${REPO0:-$IR_DIR}" 2>/dev/null && TMO=10 run_limited docker compose "${COMPOSE_ARGS[@]}" ps -q app 2>/dev/null | head -1)"
  if [[ "$CID" =~ ^[0-9a-f]{12,64}$ ]]; then
    FV="$(TMO=10 run_limited docker inspect --format '{{range .Config.Env}}{{if eq (printf "%.19s" .) "FAULT_ENDPOINT_500="}}{{.}}{{end}}{{end}}' "$CID" 2>/dev/null)"; r1=$?
    IT="$(TMO=10 run_limited docker inspect --format '{{.Config.Image}}' "$CID" 2>/dev/null)"; r2=$?
    jq -nc --arg f "${FV#FAULT_ENDPOINT_500=}" --arg i "$IT" --arg c "${CID:0:12}" \
      '{container:$c,image_tag:$i,fault_endpoint_500:$f,fault_flag_active:($f!="")}' > "$EV/.raw.rt"
    finish app_runtime "docker inspect --format (FAULT_ENDPOINT_500 value, image tag) <app container>" "$EV/.raw.rt" $((r1+r2)); rm -f "$EV/.raw.rt"
  else
    : > "$EV/.raw.rt"; finish app_runtime "docker compose ps -q app" "$EV/.raw.rt" 1; rm -f "$EV/.raw.rt"
  fi
fi

# git log (local, read-only) and deployed version
if [ -n "${EVIDENCE_FIXTURE:-}" ]; then
  get git_log "" "" ; get deployed_version "" ""
else
  REPO="$(git -C "$IR_DIR" rev-parse --show-toplevel 2>/dev/null)"
  git -C "${REPO:-$IR_DIR}" log -5 --oneline 2>/dev/null | jq -Rn '[inputs]|{commits:.}' > "$EV/.raw.git"; finish git_log "git log -5 --oneline" "$EV/.raw.git" "${PIPESTATUS[0]}"; rm -f "$EV/.raw.git"
  { git -C "${REPO:-$IR_DIR}" rev-parse HEAD 2>/dev/null | jq -Rn '{git_head:input}'; } > "$EV/.raw.dv"
  compose_args || COMPOSE_ARGS=()
  IMG="$([ ${#COMPOSE_ARGS[@]} -gt 0 ] && cd "${REPO:-$IR_DIR}" 2>/dev/null && TMO=10 run_limited docker compose "${COMPOSE_ARGS[@]}" images app 2>/dev/null | tail -n +2 | head -3 | jq -Rn '[inputs]')"
  jq -c --argjson img "${IMG:-[]}" '.compose_images=$img' "$EV/.raw.dv" > "$EV/.raw.dv2" 2>/dev/null; finish deployed_version "git rev-parse HEAD; docker compose images app" "$EV/.raw.dv2" $?
  rm -f "$EV/.raw.dv" "$EV/.raw.dv2"
fi

jq -s --arg id "$ID" --arg route "$ROUTE" --arg gen "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson cap "$MAX_BYTES" \
  '{incident_id:$id,route:$route,generated_at:$gen,max_bytes_per_file:$cap,sources:.,failed:[.[]|select(.status!="ok")|.file]}' "$MAN" > "$INC/evidence-manifest.json"
cp "$INC/evidence-manifest.json" "$EV/evidence-manifest.json"; rm -f "$MAN"
audit "$INC" collect_done "failed=$(jq -c .failed "$INC/evidence-manifest.json")"
echo "evidence: $INC/evidence-manifest.json"
