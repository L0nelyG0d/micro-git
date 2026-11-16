# Operations and security report

Status: two live drills recorded (2026-11-12). The responder was a deterministic **stub, not a model**. Drill A: policy allowed the allowlisted fault-off command, it was executed with `AUTO_EXECUTE=1`, and **recovery was verified live**. Drill B: escalation path (no recovery). See section 4 for what is not done.

Related: [`observability/`](../observability/README.md), [`incident-response/`](../incident-response/README.md), [`security-audit/`](../security-audit/README.md), [capability table](../security-audit/capability-table.md).

## 1. Architecture

```
app --/metrics--> collector --> Prometheus --\
app --JSON logs-> filelog --> Loki ----------+--> Grafana
app --OTLP/HTTP-> collector --> Tempo -------/
Prometheus alert rule --> Alertmanager --webhook--> incident-response/listener.py
   --> collect-evidence.sh --> run-responder.sh (stub) --> policy-check.py --> execute | escalate
```

The app (`backend/observe.go`, stdlib only) exposes `/metrics`, writes JSON slog logs with redaction, propagates `traceparent`, and exports spans over OTLP/HTTP JSON. Metrics, logs (via the collector filelog receiver) and traces were verified live in Prometheus, Loki and Tempo. The listener turns a firing alert into an incident directory with an append-only `audit.jsonl`. The responder only proposes; `autonomy-policy.yaml` (confidence threshold 0.7, allow-list of two action ids (`restart_app`, `fault_off`; rollback was removed; the policy builds the command, see below), deny patterns, propose-only types) decides.

Policy gate (#202): the responder model never writes a shell command. Its output names an `action_id` from a fixed allowlist (`restart_app`, `fault_off`, or `none` for propose-only types) plus `params`; `policy-check.py` validates both against `autonomy-policy.yaml` (unknown id, id not allowed for the incident type, unknown or malformed param, or a leftover `command` field all escalate), builds the argv from the policy's template and the operator's `COMPOSE_FILES`/`COMPOSE_PROJECT_NAME`, and `run-responder.sh` executes only that argv. This replaces the earlier exact-match regex on a model-written string, which could never match a real model's output. It was tested offline only; a real-model run with the new contract is still to be done.

Environments (#201): dev runs on Render and receives every green `main` commit; prod runs on AWS and changes only through the manual workflow `Promote to production`. Each instance labels its own telemetry with `APP_ENV` (`dev`, `prod`; `local` by default): field `env` in the JSON logs, label `env` on `build_info`, resource attribute `deployment.environment` on spans. The local stack labels scraped series `env=local`; the dashboard has an `env` variable and the alert rules keep `env`.

## 2. Incident reconstruction (live drills)

Two drills were run. **Drill A (2.1 to 2.6)** is the main incident: the full path alert, evidence, stub proposal, policy allow, **executed** allowlisted command, **verified recovery**. **Drill B (2.7)** is the earlier escalation-path example. Both were deliberate fault injections (`FAULT_ENDPOINT_500=/repos/{owner}/{repo}` via `observability/app-telemetry.yaml`), not real user incidents. Incident id format: `inc-<UTC timestamp>-<alert fingerprint>` for these drills; new incidents get a random 6-hex suffix and a private (0700) directory.

### 2.1 Drill A: deployed version and user impact
- Incident ID: `inc-20261112T145348-93395f6a36fedc71`; artifacts in `incident-response/incidents/inc-20261112T145348-93395f6a36fedc71/`. Stack: compose project `mgobs`, app on host port 18080, `APP_VERSION=drill-2`.
- Deployed version: `build_info{version="drill-2", commit="unknown"}` from Prometheus; image tag `mg-app:local` (`app_runtime.json`, read from the running container).
- Start/end (UTC): alert `startsAt` 2026-11-12T14:34:56.583Z; evidence collected 14:53:48Z; action executed 14:53:50Z; recovery confirmed 14:55:49Z.
- Impact: error ratio on `/repos/{owner}/{repo}` was 1 (100% 5xx) in the 5m window when evidence was collected (`prom_error_ratio.json`). Drill traffic only (one test user, GET `/api/repos/<user>/<repo>`), no real users.

### 2.2 Alert and evidence inspected
- Alert: `HighErrorRatioOnRoute`, critical, route `/repos/{owner}/{repo}`, fingerprint `93395f6a36fedc71`, delivered Prometheus -> Alertmanager -> listener (bearer-token authenticated POST, 202).
- All 10 evidence sources `status=ok`, `failed=[]`: Prometheus (error ratio, p95, rate, `build_info`), Loki, Tempo, `/health` (200; `APP_URL` now follows `APP_PORT`), `git_log`, `deployed_version`, and the new `app_runtime` (docker inspect filtered to only `FAULT_ENDPOINT_500` and the image tag: `FAULT_ENDPOINT_500=/repos/{owner}/{repo}`, image `mg-app:local`).

### 2.3 Model/config and proposed action
- **No LLM was used.** Responder: deterministic stub (`responder=stub`), policy version 1. Whether a real model does as well is untested.
- Stub output (from evidence, not from a canned answer): root cause "Fault-injection flag FAULT_ENDPOINT_500=/repos/{owner}/{repo} is set on the running app container and the error ratio is 1.00."; confidence 0.9; proposed action type `config_change`.

### 2.4 Policy decision and executed command
- Decision: **allow** (policy v1; no reasons). `AUTO_EXECUTE=1` was set explicitly (audited as `config: effective AUTO_EXECUTE=1`); the default is 0 (dry-run).
- Executed command (exact string from `action.lock`, rc=0): `FAULT_ENDPOINT_500= docker compose -p mgobs -f docker-compose.yml -f observability/compose.yaml -f observability/app-telemetry.yaml up -d --no-deps app`. The `-p`/`-f` part comes from `COMPOSE_PROJECT_NAME` / `COMPOSE_FILES` and is part of the allowlisted template, which still matches the whole command exactly.
- Approver: none (automatic, one action per incident).

### 2.5 Recovery verification
- `runbooks/verify-recovery.sh`: ratio `(5xx rate or 0)/total rate` over 1m with probe requests to the route; polls in the first 75 s after the action are not counted. Polls showed ratio 1 until the 1m window cleared, then `ratio=0 health=200`; three consecutive good polls at 14:55:16Z, 14:55:33Z, 14:55:49Z, then `RECOVERED` (`verify_result rc=0`, `incident_done rc=0`). After the action the container's `FAULT_ENDPOINT_500` was empty and the route answered 200.
- Audit trail: `audit.jsonl` has the `policy_decision` (allow), `exec` (command), `action_env` (names only), `action_result rc=0`, the verify lines and `verify_result rc=0`.
- Earlier attempt, same drill: incident `inc-20261112T143842-93395f6a36fedc71` ran the same path but the action failed (rc=1): the scrubbed action environment lacked `GRAFANA_ADMIN_PASSWORD`, which the compose file now requires, so `docker compose` refused to interpolate. Verification kept reporting ratio 1 and I stopped the run by hand (its audit has no `incident_done`). Fix: the variable was added to the default action env passthrough list. It is kept as evidence of the fail-safe behavior (a failed action never reads as recovered).

### 2.6 Related security finding and human disposition
- No Semgrep finding relates to this incident (a deliberate fault flag, not a vulnerability). `related_incident` was not set on any finding.

### 2.7 Drill B (escalation path example)
- Incident ID: `inc-20261112T141757-93395f6a36fedc71`. Earlier run, before the evidence and stub fixes. alert `startsAt` 2026-11-12T14:16:41.583Z, evidence 14:17:57Z, version `drill-1`, error ratio 1.
- `/health` evidence failed (curl rc=7): `APP_URL` defaulted to `:8080` while the app ran on `:18080` (fixed: default follows `APP_PORT`). The stub saw no fault flag, so: confidence 0.3, type `escalate`, `needs_human: true`.
- Policy decision: **escalate** (needs_human; confidence 0.30 < 0.70; `escalate` is propose-only). Executed command: none (the policy escalated, so nothing could run). The effective `AUTO_EXECUTE` was not recorded in that incident's audit (the `config` line was added afterwards), so no value is claimed. Output: `escalation-packet.json`. No recovery verification (the human step was not carried out). It remains a record of the escalation path; it does not demonstrate recovery.

### 2.8 Drill C (real model, issue #176)

**Result of this run: not completed.** The drill was not started: no fault was injected, no model was called by this run, and no alert reached the listener from this run. Cause: the compose stack needs `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `JWT_SECRET` and `GRAFANA_ADMIN_PASSWORD` (`${VAR:?}` in `docker-compose.yml` and `observability/compose.yaml`). The worktree has no `.env` and the agent shell had none of them exported. The secrets were not read from `.env` or from container env, and no placeholder values were used, because a stack with placeholder credentials would not show real recovery. Without them, the fault injection, the `fault_off` action and `docker compose up` all fail at interpolation.

What was done in this run:
- Merged `origin/main` into `issue-176-real-model-drill` (fast-forward to `0fc763f`, Merge #206).
- Stopped an orphan `listener.py` (pid 88934, started 21:15 local, parent launchd, bound to 127.0.0.1:9099 in unknown mode) and the `traffic.sh` loop (pid 88625) that were left from an earlier attempt, so the drill listener could bind 9099.
- Started a drill listener with `RESPONDER=claude AUTO_EXECUTE=1`, `COMPOSE_FILES` / `COMPOSE_PROJECT_NAME=mgobs` set, the probe URL and cookie jar paths set. It received no alert and was stopped.
- Tore down the stack: `docker compose -p mgobs down` run from a directory with no compose file (the three `-f` files fail on interpolation even for `down`). Named volumes were kept (no `-v`). No mgobs container remains.

A real-model incident from the same session (a different listener, started before this run): it ran the claude responder with `AUTO_EXECUTE=1` and a full recovery.
- Incident ID: `inc-20261116T161811-14418a2888c4d605-97706f` (`incident-response/incidents/inc-20261116T161811-14418a2888c4d605-97706f/`). Alert `HighErrorRatioOnRoute`, route `/repos/{owner}/{repo}`, app version `drill-4`.
- Diagnosis: every request to the route returned 500 (error ratio 1.0); `app_runtime.json` showed `FAULT_ENDPOINT_500` set to that route; logs showed about 50 `status=500` entries with `db_statements=0`, so the fault flag, not the database or code, was the cause. Confidence 0.92, `needs_human: false`.
- Proposed action: `action_id=fault_off`, `params={}`. Policy decision: **allow** (no reasons). Executed as the policy-built argv (`FAULT_ENDPOINT_500= docker compose -p mgobs ... up -d --no-deps app`), `action_result rc=0`.
- Verification: `verify_result rc=0`, `incident_done rc=0`, with the warm-up and three consecutive good polls in the audit.
- Provenance caveat: this run did not start that listener or inject that fault, and it did not check the injection command or the responder's prompt and cost. It is recorded here because the audit trail shows the real-model path (`exec claude -p (tools: Read,Grep,Glob; cwd=repo)`) with recovery, and it should be confirmed by the owner before it counts as the drill.

Still open for issue #176: a clean drill run in one shell that exports the stack secrets as the README describes, with the fault injection, alert, model call, policy decision, action and verification all in that run.

## 3. Security audit status

Semgrep run 2026-11-12 (`security-audit/runs/2026-11-12-semgrep.json`, docker `semgrep/semgrep:1.184.0` (pinned in `run-audit.sh`), rule sets p/golang p/typescript p/secrets, scope `backend/` and `frontend/src`; `run-audit.sh` now scans exactly those paths): **6 findings**, all severity medium. Every finding keeps disposition `open` in the run file; the triage below is the orchestrator's **suggested triage, pending human confirmation** (none confirmed by a human; it is stored in `disposition_reason`):

| ID | Rule | Location | Suggested triage (disposition stays `open`) |
| --- | --- | --- | --- |
| SEC-20261112-462d9174 | cookie-missing-httponly | backend/cmd/mg/main.go:89 | false_positive: CLI builds a request cookie; HttpOnly applies to Set-Cookie |
| SEC-20261112-c7b3f056 | cookie-missing-secure | backend/cmd/mg/main.go:89 | accepted_risk: server cookies are HttpOnly + SameSite=Lax, Secure from `COOKIE_SECURE`; must be true in production |
| SEC-20261112-4a8a341f | use-of-sha1 | backend/cmd/mg/sync.go:74 | false_positive: SHA-1 is git's object id format |
| SEC-20261112-e10d06ea | use-of-sha1 | backend/gitobj/gitobj.go:38 | false_positive: same |
| SEC-20261112-7b732c72 | cookie-missing-secure | backend/ratelimit.go:345 | accepted_risk: same as SEC-20261112-c7b3f056 |
| SEC-20261112-5b44560c | cookie-missing-secure | backend/session.go:112 | accepted_risk: same as SEC-20261112-c7b3f056 |

Result: 3 false positives, 3 accepted risks, but the accepted risks rest on `COOKIE_SECURE=true` actually being set in production, which is not verified here. Earlier baseline (`2026-11-12-baseline.json`): go vet clean; npm audit (prod deps) 0 vulnerabilities; grep secret scan 0 hits in tracked files (untracked files not covered).

## 4. Not done / known gaps
- Drills A and B used the deterministic stub responder. The real-model path (`claude -p` in a temp dir holding a clean `git archive HEAD` copy plus a copy of the redacted incident files, absolute deny rules for `.env*`, `*.pem` and `~/`, stdin from /dev/null) ran once in an incident outside this run (section 2.8, pending owner confirmation); the clean drill of issue #176 is still to be done. Before that, it was covered only by a test with a fake `claude` binary that checks the arguments and paths. `codex` is no longer chosen automatically (only with an explicit `RESPONDER=codex` and a warning: its read-only sandbox can still read files outside the copy). Model behavior, prompt, and cost are untested.
- Deferred to #160: global action cooldown across incidents (a flapping alert or a listener restart can repeat restarts); `responder-task.md` alignment with the headless agent's real tools (`code_fix_pr` is propose-only); exporter drop counter, non-2xx handling and shutdown flush; decision whether `ApiService.metrics()` stays.
- Also deferred to #160: macOS fallback when `timeout` is missing for the responder run (it then runs unbounded); token file permissions and the Linux `nobody` reader inside the Alertmanager container; an audit line for the verification probe requests; `alert.json`, `responder-raw.txt` and `responder-stderr.txt` are stored unredacted locally (git-ignored); report/run-file wording nits; note that the model's clean `git archive` copy contains only committed files (the untracked `incident-response/` folder is absent; the model sees the evidence copy `.incident/` only).
- Follow-ups: #167 (deploy/Render nits) and #168 (observability minor findings).
- Recovery was verified live only for Drill A, on a local stack with synthetic traffic and the stub responder. Alertmanager notification state lives in an anonymous volume: re-creating the container keeps it, so a repeat alert is suppressed for `repeat_interval` (1h) unless the volume is removed (`docker compose rm -sfv alertmanager`). That caused a delay in the drill, not an incident-response bug.
- The drill's compose stack needs `GRAFANA_ADMIN_PASSWORD` (and the other compose variables) in the listener's environment; they are forwarded by name to the allowlisted command only.
- Semgrep triage is not human-confirmed; `COOKIE_SECURE=true` in production is unverified.
- Model review pass of code and Snyk Agent Scan: not executed.
