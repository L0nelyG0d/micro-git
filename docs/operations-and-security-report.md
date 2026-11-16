# Operations and security report

Status: three live drills recorded. Drill A (2026-11-12): deterministic **stub**, policy allowed the fault-off command, executed with `AUTO_EXECUTE=1`, **recovery verified live**. Drill B (2026-11-12): escalation path (no recovery). Drill C (2026-11-14 and 2026-11-16): the **real model** (`claude` CLI). Run 4 (2026-11-14, `AUTO_EXECUTE=0`) escalated because the model wrote a command the policy could not allow. The 2026-11-16 run (`AUTO_EXECUTE=1`, action_id contract from #202) executed the policy-built fault-off command and **recovered** (section 2.8). See section 4 for what is not done.

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

### 2.8 Drill C: real model (claude)

Both parts use the same deliberate fault (`FAULT_ENDPOINT_500=/repos/{owner}/{repo}` via `observability/app-telemetry.yaml`) on the compose stack `mgobs`, app on host port 18080. Responder: `RESPONDER=claude` (`claude -p --restricted --tools Read,Grep,Glob`, clean git-archive copy, redacted evidence in `.incident/`).

**(a) Run 4, 2026-11-14 (`AUTO_EXECUTE=0`, escalated).** Incident `inc-20261114T082103-93395f6a36fedc71-ff6b44`, Claude Code 2.1.323, model `claude-sonnet-5-5` (CLI default, not pinned). Model step 19 s, alert to decision 20 s. Tokens: input 8, output 1,965, cache read 44,596, cache write 27,070 (no cost in text mode, subscription). Tools: 1 Glob, 10 Read, 1 Grep, no denied or errored calls; stderr empty.
- Proposal: `config_change`, command `FAULT_ENDPOINT_500= docker compose up -d --no-deps app` (no `-p`/`-f`), confidence 0.9, `needs_human: false`. Policy: **escalate**, "command not in allowlist for type config_change". Nothing executed. An operator later ran the allowlisted command by hand and verified recovery (RECOVERED 06:55:06Z, not in the audit).
- Better than the stub: it cross-checked Loki latency and `db_statements=0` to rule out a database outage, cited 8 evidence sources, and stated the risk. The stub only checks the flag and the error ratio.
- Worse: it wrote a shell command, which the policy cannot allow because the flags come from the system. This is the contract problem #202 fixed (the model now proposes an `action_id`, the policy builds the argv).

**(b) 2026-11-16, `AUTO_EXECUTE=1` (executed, recovered).** Incident `inc-20261116T161811-14418a2888c4d605-97706f` (`incident-response/incidents/inc-20261116T161811-14418a2888c4d605-97706f/`, private, not committed). Alert `HighErrorRatioOnRoute` on `/repos/{owner}/{repo}`, app `APP_VERSION=drill-4`, branch `issue-176-real-model-drill` at `913d035` (main, with the #202 action_id contract and the #200 USER fix).
- Model: `claude` CLI 2.1.323, model not pinned by `run-responder.sh` (the CLI default); the model name and token usage are not recorded in this run's audit or response, so none is claimed.
- Timings (UTC): alert received 16:18:11; responder start 16:18:13; model done 16:18:25 (model step about 12 s); policy decision 16:18:25 (alert to decision about 14 s); action 16:18:26; verify polls from 16:18:28; `RECOVERED` 16:20:25 (about 2 min 14 s alert to recovery).
- Proposal: root cause "`FAULT_ENDPOINT_500` is active for the route" (`app_runtime.json` `fault_flag_active=true`; about 50 `status=500` log lines with `db_statements=0`, so not the database or code), `action_id=fault_off`, `params={}`, confidence 0.92, `needs_human: false`, 5 evidence refs, a stated risk (brief restart blip; a human should look if errors continue).
- Policy: **allow** (no reasons), policy version 2. Executed the policy-built argv, `FAULT_ENDPOINT_500= docker compose -p mgobs -f docker-compose.yml -f observability/compose.yaml -f observability/app-telemetry.yaml up -d --no-deps app`, `action_result rc=0`.
- Verification: 8 polls logged, the first 75 s warm-up not counted, ratio 1 then 0.43 then 0, 3/3 consecutive good polls, `verify_result rc=0`, `incident_done rc=0`.
- Escalation: none.
- Secret check of the incident directory (20 files, redacted and raw): Bearer 0, Authorization 0, cookie 1 and password 2 (only the names of env-passthrough keys and the words "cookie jar" in the audit; no values), DSN or `postgres://` 0; none of the four generated drill credentials and no session cookie value appear in any file.
- Teardown: traffic loop and listener stopped, `docker compose down -v`, no `mgobs` volumes or containers left, scratch credential file deleted.
- Caveat: the first waiting loop of this run filtered on the Drill A fingerprint and did not see this incident, so the wait was repeated; the incident itself was not affected. A second session in the same worktree also committed notes to this section (`1c25b3e`, which called this run an orphan listener and said no alert reached it). Its listener pid 88934 is the one that handled this incident (audit at 16:18 UTC); that note is superseded by the audit trail.

Still open for #176: the QA and reviewer steps, and a run of the same drill that a second person can repeat.

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
- Drills A and B used the deterministic stub responder. The real model (`claude` CLI, `claude -p` in a temp dir with a clean `git archive HEAD` copy and redacted evidence, absolute deny rules for `.env*`, `*.pem` and `~/`, stdin from /dev/null) was exercised in Drill C (section 2.8): once with `AUTO_EXECUTE=0` (escalated) and once with `AUTO_EXECUTE=1` (executed and recovered). Still untested: (1) prompt injection in evidence (Loki and log lines are untrusted; no injected-content drill has been run); (2) `codex` (only with an explicit `RESPONDER=codex` and a warning: its read-only sandbox can still read files outside the copy); (3) Linux and API-key auth (the drills ran on macOS with subscription login, and the keychain login depends on `USER` being passed through, #200); (4) cost under API billing (the CLI reports no cost in text mode; token usage was recorded only for run 4). The model name is not pinned in `run-responder.sh`.
- Deferred to #160: global action cooldown across incidents (a flapping alert or a listener restart can repeat restarts); `responder-task.md` alignment with the headless agent's real tools (`code_fix_pr` is propose-only); exporter drop counter, non-2xx handling and shutdown flush; decision whether `ApiService.metrics()` stays.
- Also deferred to #160: macOS fallback when `timeout` is missing for the responder run (it then runs unbounded); token file permissions and the Linux `nobody` reader inside the Alertmanager container; an audit line for the verification probe requests; `alert.json`, `responder-raw.txt` and `responder-stderr.txt` are stored unredacted locally (git-ignored); report/run-file wording nits; note that the model's clean `git archive` copy contains only committed files (the untracked `incident-response/` folder is absent; the model sees the evidence copy `.incident/` only).
- Follow-ups: #167 (deploy/Render nits) and #168 (observability minor findings).
- Recovery was verified live only for Drill A, on a local stack with synthetic traffic and the stub responder. Alertmanager notification state lives in an anonymous volume: re-creating the container keeps it, so a repeat alert is suppressed for `repeat_interval` (1h) unless the volume is removed (`docker compose rm -sfv alertmanager`). That caused a delay in the drill, not an incident-response bug.
- The drill's compose stack needs `GRAFANA_ADMIN_PASSWORD` (and the other compose variables) in the listener's environment; they are forwarded by name to the allowlisted command only.
- Semgrep triage is not human-confirmed; `COOKIE_SECURE=true` in production is unverified.
- Model review pass of code and Snyk Agent Scan: not executed.
