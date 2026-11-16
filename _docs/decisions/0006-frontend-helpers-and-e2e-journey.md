# ADR 0006: Frontend class helper and the manual e2e journey

Status: Accepted (2026-11-14): option C. Not applied yet (issue #193)

Issue: #193

## Context

- `frontend/src/lib/utils.ts` (the shadcn `cn` helper on `clsx` and `tailwind-merge`) is imported by nothing. The 10 files in `frontend/src/components/ui/` import `cn` from the npm package `cn` instead.
- `cn` and the `radix-ui` meta-package are not in the "Approved packages" list of `_docs/design-system.md` (it lists `clsx`, `tailwind-merge` and the `@radix-ui/*` packages). `clsx` and `tailwind-merge` are used only by the unused file.
- `frontend/components.json` aliases `@/hooks`, a folder that does not exist.
- There is no ESLint config and no `lint` script, so `make lint` runs no frontend lint.
- `_docs/e2e/journey.js` is the manual Playwright-MCP journey from #107; it overlaps the `e2e/` suite.

## Options

- A: do nothing.
- B: delete `lib/utils.ts`, `clsx` and `tailwind-merge`. Risk: `npx shadcn add` writes `@/lib/utils` imports, so new components break.
- C: point the 10 ui files at `@/lib/utils` and drop the `cn` package. Small change, approved packages only.
- D: move or retire `journey.js`.

## Recommendation

The user decides on approving `cn` and `radix-ui`; default C; retire the manual journey when the suite covers it.

## Decision

Option C, applied in branch `issue-193-frontend-cn-helper` (in progress). The manual journey is retired only when the Playwright suite covers it.

## Consequences

The ui files use the shadcn default helper again; one unapproved dependency goes.

## Preconditions

Frontend tests and build green; no visual change.
