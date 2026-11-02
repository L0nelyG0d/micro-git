Title: CI workflow: run backend, frontend, e2e, integration tests
Labels: ci

## Goal

A GitHub Actions workflow runs every test layer on each push and pull request so a broken change cannot reach `main` unnoticed.

## Acceptance criteria

- [ ] Backend job runs `go test -p 1 -race ./...` against a Postgres service container
- [ ] Backend job also runs the suite with `TEST_STORE=sqlite` and `TEST_STORE=memory`
- [ ] Frontend job runs `npm test` and the production build in `frontend/`
- [ ] E2E job runs the Playwright tests against the app started in CI
- [ ] Integration smoke (`make smoke-sqlite`) runs in CI
- [ ] Workflow fails the run when any job fails; status is visible on the PR
- [ ] No repository secrets are required for the test jobs

## Out of scope

- Deployment (see the CD issue)

## Constraints

- Extends the existing `.github/workflows/ci.yml` (gofmt, vet, openapi lint, secret scan stay in place)
- Never point CI at the Neon production database
