Title: Deploy: Render (Docker Compose-only path)
Labels: deploy

## Goal

The app runs on Render using the Docker Compose definition as the single source of truth, with Postgres provided by Render.

## Acceptance criteria

- [ ] A documented path deploys the app to Render from the repository using Docker
- [ ] A Render Postgres instance is used through `DATABASE_URL` set in the Render dashboard (not in the repo)
- [ ] `JWT_SECRET` is set in the Render dashboard and is not committed
- [ ] The public URL serves the frontend and the API
- [ ] Redeploy on commit to main is either configured or documented as a manual step
- [ ] The README lists the exact steps the user performs in the Render dashboard

## Out of scope

- Rewriting the Compose file for Render-specific features

## Constraints

- Depends on the Containerise and Docker Compose issues
- Free tier only where the provider allows it; note any limits
