# ADR 0002: Go module path and go.mod location

Status: Accepted (2026-11-14): keep

Issue: #192

## Context

`go.mod` lives in `backend/` with module path `github.com/L0nelyG0d/micro-git/backend`. 29 Go files import packages under that path. CI and E2E read the Go version with `go-version-file: backend/go.mod`; the Dockerfile, Makefile and scripts run Go from `backend/`.

## Options

- A: keep. Cost none, risk none.
- B: move `go.mod` to the repository root and rename the module. Every import line, the CI `go-version-file`, the Dockerfile build stage, the Makefile and the scripts change in one go. Cost medium; risk of a broken build on one of the deploy paths.

## Recommendation

A. Nothing is broken, and B gains nothing a user can see.

## Decision

Keep the module path and location. Nothing to apply.

## Consequences

Go commands keep running from `backend/`.

## Preconditions (for B later)

All three deploy paths and both workflows changed and verified in one change.
