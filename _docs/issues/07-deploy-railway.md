Title: Deploy: Railway
Labels: deploy

## Goal

The app runs on Railway: the backend image is deployed as a service and PostgreSQL is a Railway database service.

## Acceptance criteria

- [ ] A documented path deploys the Dockerfile image to Railway
- [ ] Postgres runs as a Railway service and `DATABASE_URL` is referenced through Railway variables, not committed
- [ ] `JWT_SECRET` is set in Railway variables only
- [ ] The public URL serves the frontend and the API
- [ ] The README lists the steps the user performs in the Railway dashboard or CLI

## Out of scope

- Railway-specific features beyond the single service and database

## Constraints

- Depends on the Containerise issue and the SQLite-to-Postgres default
- Note the free-trial or plan limits in the docs
