# ADR 0001: Backend package split

Status: Accepted (2026-11-14): option A

Issue: #191

## Context

The `backend/` root is one `package main`: 29 production files (6,243 lines) and 41 white-box test files (10,046 lines). By layer: startup and config 3 files (398 lines), HTTP plumbing 5 (979), endpoints with their rules 9 (1,325), stores 9 (2,884), observability 2 (602), static serving 1 (55). `gitobj`, `linediff` and `treebuild` are already clean packages; `cmd/mg` and `cmd/migrate-sqlite-to-pg` do not import the root package. The boundaries hold through the `Store` interface (24 methods). The server entry point `.` is referenced in 9 places: the Makefile (7), `scripts/smoke-sqlite.sh` and the Dockerfile.

## Options

- A: do nothing; document the layers. Cost none, risk none.
- B: extract `internal/store` only: export the store types and constructors, split the store-matrix tests. Cost medium; risk of breaking white-box tests.
- C: full split into `internal/httpapi`, `internal/store`, `internal/obs`, `cmd/server`. Touches every build entry point and the three deploy paths (compose, Render, AWS). Cost and risk high.

## Recommendation

A now; B only in a feature freeze.

## Decision

Option A. No package split now; the layers are documented in `_docs/team/architect.md`. Option B may be reopened in a feature freeze with a new issue. Nothing to apply.

## Consequences

The layering rules stay conventions checked in review, not by the compiler.

## Preconditions (for B or C later)

`make test-all` green on all three stores; one package per change; Dockerfile, Makefile and smoke script updated in the same change; E2E and the deploy smoke green.
