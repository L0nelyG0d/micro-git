Title: e2e-test: commit-web-editor
Labels: e2e-test

## Goal

A Playwright end-to-end test covers the `commit-web-editor` feature through the real browser UI. Spec file: `e2e/tests/commit.spec.ts`.

## Acceptance criteria

- [ ] commit a new file through the web editor
- [ ] edit an existing file and commit the change
- [ ] delete a file through the editor
- [ ] the editor refuses a commit message over 500 characters

## Constraints

- Playwright (`@playwright/test`), runs from `e2e/` against the app started by the e2e job in CI (`E2E_BASE_URL`, default http://localhost:8080)
- Each test signs up its own user and creates its own repo; no real credentials are committed
- Known risk: `POST /auth/register` is rate limited (10 per IP per 15 min), so a full run can hit 429; see `_docs/questions.md`
