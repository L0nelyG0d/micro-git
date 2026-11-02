Title: SQLite to PostgreSQL default and migration command
Labels: database

## Goal

PostgreSQL becomes the default database for deployed environments, and a migration command moves existing data from SQLite (`data/mg.db`) into Postgres.

## Acceptance criteria

- [ ] With `DATABASE_URL` set, the backend uses Postgres; SQLite is used only when explicitly selected
- [ ] Schema is applied from `backend/db/schema.sql` (idempotent) on startup or via a documented command
- [ ] A migration command copies users, repositories, branches, and commit data from SQLite to Postgres
- [ ] The migration is repeatable: re-running it does not duplicate rows
- [ ] Row counts before and after the migration are printed and match
- [ ] Documented in the README with the exact command and required env vars (no secret values)

## Out of scope

- Changing the data model

## Constraints

- Pre-approved Go modules only: `gorm.io/gorm`, `gorm.io/driver/postgres`, `github.com/glebarez/sqlite`, `github.com/jackc/pgx/v5`
- Use env vars for `DATABASE_URL` and `TEST_DATABASE_URL`; never commit connection strings
