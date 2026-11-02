Title: Docker Compose (app + Postgres)
Labels: deploy

## Goal

A `docker-compose.yml` starts the app container and a PostgreSQL container together, so the full stack runs locally with one command.

## Acceptance criteria

- [ ] `docker compose up --build` starts both services and the app becomes reachable on a documented port
- [ ] The app waits for Postgres to be healthy (`healthcheck` plus `depends_on: condition: service_healthy`)
- [ ] Postgres data is kept in a named volume and survives `docker compose down` (without `-v`)
- [ ] Credentials are read from a git-ignored `.env` file; an `.env.example` with placeholder values is committed
- [ ] No real password, connection string or JWT secret is written into the compose file or committed

## Out of scope

- Production hosting (see the deploy issues)

## Constraints

- Depends on the Containerise issue (single app image)
- Use the Postgres 16 image to match the local test database
