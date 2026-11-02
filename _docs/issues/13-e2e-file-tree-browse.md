Title: e2e-test: file-tree-browse
Labels: e2e-test

## Goal

A Playwright end-to-end test covers the `file-tree-browse` feature through the real browser UI. Spec file: `e2e/tests/browse.spec.ts`.

## Acceptance criteria

- [ ] the repository page lists files and folders of the branch
- [ ] open a folder, then a file, and read its content

## Constraints

- Playwright (`@playwright/test`), runs from `e2e/` against the app started by the e2e job in CI (`E2E_BASE_URL`, default http://localhost:8080)
- Each test signs up its own user and creates its own repo; no real credentials are committed
- Known risk: `POST /auth/register` is rate limited (10 per IP per 15 min), so a full run can hit 429; see `_docs/questions.md`
