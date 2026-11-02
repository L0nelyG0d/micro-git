Title: e2e-test: compare
Labels: e2e-test

## Goal

A Playwright end-to-end test covers the `compare` feature through the real browser UI. Spec file: `e2e/tests/compare.spec.ts`.

## Acceptance criteria

- [ ] compare shows a file added on the feature branch

## Constraints

- Playwright (`@playwright/test`), runs from `e2e/` against the app started by the e2e job in CI (`E2E_BASE_URL`, default http://localhost:8080)
- Each test signs up its own user and creates its own repo; no real credentials are committed
- Known risk: `POST /auth/register` is rate limited (10 per IP per 15 min), so a full run can hit 429; see `_docs/questions.md`
