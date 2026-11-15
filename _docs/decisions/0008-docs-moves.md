# ADR 0008: Docs moves and role models

Status: Accepted (2026-11-14)

Issue: #199

## Context

After #175, `_docs/` holds process and project docs and `docs/` holds reader-facing guides. Left open:

1. `_docs/README.md` is a reader-facing developer manual. It is referenced by the root `README.md`, `docs/deployment.md`, `docs/testing.md`, `_docs/AGENTS.md`, `_docs/team/architect.md` and `_docs/tasks.md`.
2. `_docs/design-refs/awesome-claude-design/` is a third-party README and LICENSE with no references (added on purpose in 88ef4d9).
3. Old open items in `_docs/questions.md` look answered: lines 88, 90, 99, 104, 105, 106 and 107 (later entries resolve them), and lines 26-33 and 53-63 carry the user's answer inline but are not ticked.
4. Which model the architect and DevOps roles run on.
5. The manual section of `_docs/e2e.md` goes with ADR 0006.

## Options

- Keep everything as is, or apply each item separately. Moving the manual changes about six links; deleting the design refs changes none.

## Recommendation

Move the manual to `docs/developer-guide.md`, delete the design refs, the user ticks the questions.

## Decision

`_docs/README.md` moves to `docs/developer-guide.md`; `_docs/design-refs/` is deleted; the architect runs on Fable, sub-architects and DevOps on Opus. The moves are applied in a separate change after #175 merges. The models are recorded in `_docs/process.md` by #175.

## Consequences

`docs/` holds every reader-facing guide; links to the manual change once.

## Preconditions

Link check clean after the move.
