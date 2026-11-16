# ADR 0003: Ops and deploy directory moves

Status: Accepted (2026-11-14): no moves

Issue: #195

## Context

Two moves were considered for a tidier top level:

1. Group `observability/`, `incident-response/` and `security-audit/` under `ops/`. On main (04b3b02) these paths appear 100 times in 16 tracked files, 38 of them in shell, Python, YAML and `.gitignore` files. Compose bind mounts are among them: when a mounted file path is missing, Docker silently creates a directory in its place, so a missed reference fails late and confusingly.
2. Move `deploy/render.yaml` into `deploy/render/`. The Blueprint file path is set in the Render dashboard; moving the file disconnects the Blueprint until the dashboard is changed.

## Options

- A: do nothing. Cost none, risk none.
- B: move the three directories under `ops/`. Cost: about 100 edits; risk: broken mounts, scripts and the security-audit link to `incident-response/redact.py`.
- C: move `render.yaml` and update the dashboard. Cost small; risk: a missed dashboard change stops Render deploys.

## Recommendation

A for both.

## Decision

No moves. `observability/`, `incident-response/` and `security-audit/` stay top-level; `deploy/render.yaml` stays. Nothing to apply.

## Consequences

The top level keeps three ops directories; the root `README.md` and `_docs/team/architect.md` describe each.

## Preconditions (for a later move)

A grep-driven list of every reference, compose stacks started and torn down with the new paths, and the Render dashboard updated in the same change.
