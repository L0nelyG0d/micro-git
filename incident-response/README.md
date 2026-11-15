# incident-response

Principle (Zoomcamp module 4): **the model may reason; the system must observe, authorize, verify, remember.**

    Alertmanager --POST /alert--> listener.py :9099
      1 observe    collect-evidence.sh <id> <route>   read-only, allowlisted, redacted, size-capped
      2 reason     run-responder.sh                   `claude -p` / `codex exec` headless read-only, else deterministic stub
      3 authorize  policy-check.py                    schema validation + autonomy-policy.yaml
      4 act        one allowlisted command, exact template match, no shell metacharacters
      5 verify     runbooks/verify-recovery.sh <route>   error ratio < 1% and /health 200, N consecutive polls
      6 remember   incidents/<id>/audit.jsonl         every command and policy decision
    anything not allowed / low confidence / invalid output / failed verify -> escalation packet for a human

## Security of the webhook (read first)
- `POST /alert` needs `Authorization: Bearer <token>` (constant-time compare) and `Content-Type: application/json`; anything else is 401/415 and audited to `incidents/listener-audit.jsonl`. No token file = every alert rejected.
- Create the token once: `incident-response/make-token.sh` (`openssl rand -hex 32`, mode 600, file `incident-response/.alert-token`, git-ignored via `incident-response/.gitignore`). Alertmanager reads the same file (`http_config.authorization.credentials_file`, mounted by `observability/compose.yaml`; run make-token.sh BEFORE `docker compose up`). Override the path with `ALERT_TOKEN_FILE`.
- Bind the listener to the docker bridge address, NOT `0.0.0.0` (e.g. `LISTEN_HOST=172.17.0.1` on Linux; on Docker Desktop `host.docker.internal` is already reachable via the host, so the default `127.0.0.1` or the VM gateway is enough). The listener runs at most `MAX_PIPELINES` (2) incidents at once, dedupes repeats with a bounded (1000) cache, and audits non-integer Content-Length and non-object alerts/labels. `route=all` is escalated, never automated.
- The model responder is `claude` only by default (`RESPONDER=auto` never picks `codex`; `RESPONDER=codex` is explicit and prints a warning because its read-only sandbox can still read files outside the clean copy, including `~/` and `.env`). It runs `claude -p --restricted --tools Read,Grep,Glob --strict-mcp-config` with a scrubbed env, stdin from /dev/null, in a temp dir that holds a clean `git archive HEAD` copy (untracked files such as `.env` are absent) plus a copy of the redacted incident files at `.incident/` (the untracked `incident-response/` folder itself is NOT in the copy; the model only sees the evidence); the prompt and `--add-dir` use that copy, never the real checkout path. Absolute deny rules: `Read(//**/.env)`, `Read(//**/.env.*)`, `Read(//**/*.pem)`, `Read(~/**)`; Bash/Edit/Write/WebFetch denied. The run aborts with an escalation packet if git tracks any `.env*` file other than `*.example`. `response.json` is redacted before it is saved. Not yet exercised against a real model in a drill.
- Compose targeting is fail closed: set `COMPOSE_FILES` (colon list) and optionally `COMPOSE_PROJECT_NAME`; they are expanded into the `-f`/`-p` flags of the allowlisted templates. `rollback` is not in the allowlist (root compose hardcodes `mg-app:local`).
- Global action cooldown: `policy-check.py` stamps `incidents/.last_action` (under flock) when an automatic action is claimed; any other incident is escalated until `global_cooldown_seconds` (policy, default 300; env `GLOBAL_COOLDOWN_SECONDS`, 0 = off) has passed, so flapping alerts or a listener restart cannot loop restarts.
- Timeouts: `run_limited` uses `timeout`, else `gtimeout`, else a python3 process-group fallback (exit 124), so stock macOS is limited too.
- Token file: `make-token.sh` keeps mode 600; on Linux as root it chowns the file to 65534 (Alertmanager runs as nobody), otherwise it prints the `sudo chown 65534` hint. The listener warns at start if the file is group/other accessible.
- Claude deny-rule grammar (`Read(//**/.env)`, `Read(//**/.env.*)`, `Read(//**/*.pem)`, `Read(~/**)`) was checked against Claude Code 2.1.323: with these `--disallowedTools` a real run was denied for `.env` and `sub/.env.local` and allowed for a normal file. Re-checked on 2026-11-15 on the current CLI, still 2.1.323 (same result: `.env` and `sub/.env.local` DENIED, a plain file READ). Re-check after every CLI upgrade and update this line with the version.
- `verify-recovery.sh`: ratio is `(5xx rate or 0)/total rate`, only polls after a 75 s warm-up count, and `PROBE_URL` (+ optional `PROBE_COOKIE_JAR`) sends probe requests to the route during verification.

## Files
- `collect-evidence.sh <id> <route>`: Prometheus (`PROM_URL`, default http://localhost:9090), Loki query_range (limit 50, token/cookie/password redaction), Tempo search (limit 5), `git log -5`, `/health`, and `app_runtime` (read-only `docker inspect --format` filters that print ONLY the value of `FAULT_ENDPOINT_500` and the image tag of the running app container; no other env var is read, so no secrets). `APP_URL` defaults to `http://localhost:${APP_PORT:-8080}` (follows `APP_PORT`; set `APP_URL` to override). Each source fails soft and is recorded in `evidence-manifest.json`. `EVIDENCE_FIXTURE=<dir>` swaps the network for canned files.
- `run-responder.sh <id> <route>`: `RESPONDER=auto|claude|codex|stub`. LLM CLIs run with a scrubbed env (no prod creds) and read-only tools. **Dry-run is the default**: without `AUTO_EXECUTE=1` an allowed action is only logged (`action_skipped`), nothing changes. `AUTO_EXECUTE=1` must be set explicitly to run it; `DRY_RUN=1` never does. The stub responder proposes the allowlisted `fault-flag-off` action only when `app_runtime.json` shows `FAULT_ENDPOINT_500` set and the error ratio is >= 0.05 (confidence 0.9); otherwise it escalates. The action runs with a scrubbed env (the stack is selected by the `-p`/`-f` flags inside the command, built from `COMPOSE_FILES` / `COMPOSE_PROJECT_NAME`) plus the names in `ACTION_ENV_PASSTHROUGH` (default: the docker-compose.yml interpolation vars POSTGRES_*, JWT_SECRET, GRAFANA_ADMIN_PASSWORD, APP_PORT, APP_VERSION, APP_COMMIT, COOKIE_SECURE, REGISTER_MAX_PER_IP, COMPOSE_ENV_FILES; only names are audited, never values).
- `responder-task.md` prompt; `response.schema.json` required output shape.
- `autonomy-policy.yaml` JSON-compatible YAML (parsed with stdlib `json`). Allowed (with `{{COMPOSE_ARGS}}` = `-p $COMPOSE_PROJECT_NAME -f <each of $COMPOSE_FILES>`): `docker compose {{COMPOSE_ARGS}} restart app`; `FAULT_ENDPOINT_500= docker compose {{COMPOSE_ARGS}} up -d --no-deps app`. Denied: DB writes, secrets/.env, force push/git push, prod deploy tooling, shell metacharacters, anything unlisted. Confidence >= 0.7; max 1 automatic action per incident (atomic `action.lock`).
- `policy-check.py <incident-dir> [--no-claim]` exit 0 allow, 3 escalate. Small stdlib schema validator; invalid output always escalates.
- `listener.py` stdlib `http.server`; env `LISTEN_HOST` (127.0.0.1), `LISTEN_PORT` (9099), `AUTO_EXECUTE` (**0** = dry-run, log the planned action and change nothing; `1` = execute the allowlisted action). The effective value is written to `audit.jsonl` (`config` line) at incident start. Matches `observability/alertmanager.yml` (`host.docker.internal:9099/alert`).
- `lib.sh` shared helpers (audit, validation, compose args, `run_limited`). `redact.py` (called by `redact()` with a 4 MiB input cap and a 20 s limit): JSON input is walked (any secret-looking key such as password/pass/pwd/passphrase/secret/token/api_key/authorization/session/cookie/credential/signature/private_key has its whole value, nested objects and arrays included, replaced; JSON embedded in log-line strings is walked too), other text goes through case-insensitive regex rules (KEY=value with double/single/escaped quotes, Authorization/Proxy-Authorization/Cookie header values, Bearer/Basic, URI userinfo incl. `redis://:pw/x@host`, `?key= ?sig= ?signature= ?token=`, PEM private keys, JWTs). Valid JSON stays valid. If redaction or jq fails, the evidence file is replaced by `{"raw":"[REDACTED-ERROR]"}` (source marked failed) and `response.json` is dropped with an escalation: an unredacted file is never kept. Limits: names longer than 40 characters around a keyword, and secrets in free prose without a key (`the password is hunter2`), are not caught. `alert.json`, `responder-raw.txt` and `responder-stderr.txt` are stored unredacted locally (git-ignored; tracked in #160). Tests: `test-redact.sh`.
- Incident ids are `inc-<UTC ts>-<fingerprint>-<random hex>`; directories are created exclusively with mode 0700. `valid_id` needs an alphanumeric first character; `COMPOSE_FILES` entries may not be empty or start with `-`.

## Run
    python3 incident-response/listener.py                       # dry-run (default): plans, never executes
    AUTO_EXECUTE=1 python3 incident-response/listener.py        # explicit opt-in: runs the allowlisted action, then verifies
    incident-response/collect-evidence.sh drill-1 '/repos/{owner}/{repo}'
    RESPONDER=stub incident-response/run-responder.sh drill-1 '/repos/{owner}/{repo}'

Live drill on a non-default stack (compose project `mgobs`, app on host port 18080):

    export POSTGRES_USER=... POSTGRES_PASSWORD=... POSTGRES_DB=... JWT_SECRET=... APP_PORT=18080 COMPOSE_PROJECT_NAME=mgobs APP_VERSION=drill-2
    export COMPOSE_FILES=docker-compose.yml:observability/compose.yaml:observability/app-telemetry.yaml COMPOSE_ENV_FILES=/dev/null GRAFANA_ADMIN_PASSWORD=...
    incident-response/make-token.sh                                   # before `up`
    FAULT_ENDPOINT_500='/repos/{owner}/{repo}' docker compose -p $COMPOSE_PROJECT_NAME -f docker-compose.yml -f observability/compose.yaml -f observability/app-telemetry.yaml up -d --no-deps app   # inject; drive GET /api/repos/<u>/<r> traffic
    PROBE_URL=http://localhost:18080/api/repos/<u>/<r> PROBE_COOKIE_JAR=<jar> AUTO_EXECUTE=1 RESPONDER=stub LISTEN_HOST=<bridge/host address> python3 -I incident-response/listener.py   # same env as above

The allowlisted template (`FAULT_ENDPOINT_500= docker compose {{COMPOSE_ARGS}} up -d --no-deps app`) is matched exactly; which stack it hits is decided only by the env (`COMPOSE_FILES`, `COMPOSE_PROJECT_NAME`) the operator starts the listener with, not by the model.

Needs `bash`, `jq`, `curl`, `python3`. Trigger a real incident: `FAULT_ENDPOINT_500='/repos/{owner}/{repo}' docker compose up -d --no-deps app`.

## Tests
    incident-response/test-incident.sh --dry-run   # no stack: fixtures, stub, listener e2e, escalation path
    incident-response/test-redact.sh               # redaction cases, JSON validity
    incident-response/test-policy.sh               # allow cases + negatives: denied cmd, low confidence, invalid JSON, once-only
    incident-response/test-incident.sh             # live: mutates the local stack (needs COMPOSE_FILES / COMPOSE_PROJECT_NAME)

## Gotchas
- Evidence is untrusted data (log lines may carry prompt injection); the policy, not the model, decides.
- Escalation file is `escalation-packet.json` from the policy stage; `escalation.json` from the listener when the pipeline itself fails.
- There is no automatic rollback action: the root compose file hardcodes `mg-app:local`, so it was removed from the allowlist.
