# Database migrations with goose

Status: accepted

## Context

The PostgreSQL schema must be versioned and applied through `go run ./cmd/migrate`,
separately from API startup. The repository will use pgx for PostgreSQL access.

## Decision

Use goose as a Go library with the pgx `database/sql` adapter in `cmd/migrate`.
Bundle SQL migrations with `embed.FS` so the built command does not depend on its
working directory. Read the database connection from `DATABASE_URL`.

The command applies pending migrations only. Goose records applied versions and
runs each SQL migration in a transaction by default. Applied migrations are immutable;
schema changes require a new migration. Run one migration command at a time.

## Consequences

The migration command shares Go tooling and the PostgreSQL driver with the project.
The alternative, a separate migrate/migrate image, would add another executable and
would not directly satisfy the existing Go command interface.

Only migrations use `database/sql`; the repository implementation will use `pgxpool`.
The initial table uses a primary key on `code` to enforce uniqueness, `BIGINT` for
clicks to match Go's `int64`, and `TIMESTAMPTZ` for creation time.

Down SQL documents the inverse operation, but the command does not expose destructive
rollback. Recover production schema issues with reviewed forward migrations.

## Reference

- https://pressly.github.io/goose/documentation/provider/
