Title: e2e-test: repo-create-list-delete
Labels: e2e-test

## Goal

A Playwright end-to-end test covers the `repo-create-list-delete` feature through the real browser UI. Spec file: `e2e/tests/repos.spec.ts`.

## Acceptance criteria

- [ ] create a repository from the home page
- [ ] the list shows own public and private repositories with badges
- [ ] delete a repository from settings

## Constraints

- Playwright (`@playwright/test`), runs from `e2e/` against the app started by the e2e job in CI (`E2E_BASE_URL`, default http://localhost:8080)
- Each test signs up its own user and creates its own repo; no real credentials are committed
- Known risk: `POST /auth/register` is rate limited (10 per IP per 15 min), so a full run can hit 429; see `_docs/questions.md`
