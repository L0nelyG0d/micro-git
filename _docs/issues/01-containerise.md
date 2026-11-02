Title: Containerise: single Dockerfile (frontend static + backend)
Labels: deploy

## Goal

One Dockerfile builds the app into a single image: the Go backend serves the API and the built frontend static files, so one container is enough to run the whole product.

## Acceptance criteria

- [ ] `docker build` succeeds from a clean checkout with no local secrets
- [ ] The image runs the backend on a port taken from the `PORT` environment variable
- [ ] The frontend build output is served by the backend at `/` and the API keeps its existing paths
- [ ] Database configuration comes only from `DATABASE_URL` and `JWT_SECRET` env vars; nothing is baked into the image
- [ ] The image runs as a non-root user and has a `HEALTHCHECK` or documented health endpoint
- [ ] A `.dockerignore` excludes `node_modules`, `data/`, `.env` and git metadata

## Out of scope

- Multi-container orchestration (see the Docker Compose issue)
- Hosting-specific configuration (see the deploy issues)

## Constraints

- Stay inside the existing project structure (Go backend in `backend/`, React frontend in `frontend/`)
- Use a multi-stage build (frontend build stage, Go build stage, slim runtime stage)
