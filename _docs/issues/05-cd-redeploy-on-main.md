Title: CD: redeploy on each commit to main
Labels: ci

## Goal

Every commit merged to `main` that passes CI is deployed automatically to the hosting target.

## Acceptance criteria

- [ ] Deploy job runs only on push to `main` and only after the CI jobs succeed
- [ ] Deployment uses a secret configured in GitHub (for example a deploy hook URL or cloud credentials) and no secret is written in the repo
- [ ] The deployed version is traceable to the commit SHA (image tag or build label)
- [ ] A failed deploy leaves the previous version running and marks the run as failed
- [ ] The README documents which GitHub secrets must be created by the user

## Out of scope

- Choosing the hosting provider (see the deploy issues)

## Constraints

- Depends on the CI workflow issue and at least one deploy target issue
- Secret values are never pasted into issues, docs or commits
