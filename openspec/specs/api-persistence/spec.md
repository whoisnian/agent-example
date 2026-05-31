# api-persistence Specification

## Purpose
TBD - created by archiving change init-api-scaffold. Update Purpose after archive.
## Requirements
### Requirement: PostgreSQL Connection Pool

The service SHALL use `pgxpool` to connect to PostgreSQL. The pool's `MaxConns`, `MinConns`, `MaxConnLifetime`, and `MaxConnIdleTime` SHALL be configurable via environment variables with safe defaults (`MaxConns=20`, `MinConns=2`, `MaxConnLifetime=30m`, `MaxConnIdleTime=5m`). The pool MUST be created at startup and closed during graceful shutdown.

#### Scenario: Pool config from env
- **WHEN** the service starts with `DB_MAX_CONNS=50`
- **THEN** the active `pgxpool.Config.MaxConns` MUST equal 50

#### Scenario: Pool closes on shutdown
- **WHEN** the process begins graceful shutdown
- **THEN** the pool MUST be closed before exit, and no new acquisitions MUST succeed after shutdown begins

### Requirement: Startup Connectivity Check

At startup, after pool creation, the service SHALL execute `SELECT 1` against PostgreSQL. Failure MUST abort the process with a non-zero exit code and a fatal log entry naming PostgreSQL as the failed dependency.

#### Scenario: DB unreachable at startup
- **WHEN** PostgreSQL is unreachable during startup probe
- **THEN** the process MUST exit non-zero before opening the HTTP listener, and the log MUST contain a fatal entry identifying PostgreSQL

### Requirement: Schema Migrations

Database schema SHALL be managed by `golang-migrate` with versioned SQL files under `api/migrations/`. Migrations MUST be idempotent (each version applied exactly once), strictly ordered, and runnable both:
- automatically at service startup (controlled by `DB_MIGRATE_ON_BOOT=true|false`, default `false` in production, `true` in dev),
- and via an explicit CLI subcommand `api migrate up|down|version`.

Each migration MUST have a paired `up` and `down` file. A failed migration MUST not leave the schema in a half-applied state; on failure the process MUST exit non-zero.

#### Scenario: Migration version recorded
- **WHEN** `api migrate up` is executed and succeeds
- **THEN** the `schema_migrations` table MUST record the new version and `dirty=false`

#### Scenario: Failed migration aborts
- **WHEN** a migration step raises a SQL error mid-application
- **THEN** the migration tooling MUST mark `dirty=true` and the invoking command MUST exit non-zero without proceeding to subsequent versions

### Requirement: Typed Query Layer via sqlc

All persistent data access in production code SHALL go through `sqlc`-generated query code located at `api/internal/infrastructure/persistence/sqlc/`. Hand-written `database/sql` or `pgx` calls in handler/application/domain layers MUST be rejected by lint or code review. The single exception is the Outbox Relayer's batched scan, which MAY use direct `pgx` calls and MUST be reviewed individually.

#### Scenario: sqlc regenerates from query files
- **WHEN** a developer runs `make sqlc`
- **THEN** the generated Go files under `sqlc/` MUST be deterministic given the same `query.sql` and `schema.sql`, and `go build` MUST succeed afterwards

### Requirement: Outbox Table Schema

The initial migration set MUST create the `outbox` table conforming to `docs/ARCHITECTURE.md §4.2` (columns: `id BIGSERIAL`, `aggregate TEXT`, `aggregate_id UUID`, `topic TEXT`, `payload JSONB`, `status TEXT DEFAULT 'pending'`, `attempts INT DEFAULT 0`, `next_retry_at TIMESTAMPTZ`, `created_at TIMESTAMPTZ DEFAULT now()`), plus an index `(status, next_retry_at)` to support relayer scans.

A subsequent migration `0006_outbox_exchange` (shipped by `add-task-control-api`) MUST add an `exchange TEXT NOT NULL DEFAULT 'task.exchange'` column so the Outbox Relayer can route each row to its destination exchange (see `api-messaging` §"Outbox Relayer"). The up migration MUST use `ADD COLUMN IF NOT EXISTS` so a re-run after the down step is a no-op. The default makes the migration safe for existing rows — every previously-written row had been routed to `task.exchange` implicitly by the relayer's old constant.

`outbox.topic` continues to mean "routing key on the row's exchange"; the column's name is preserved for backwards compatibility with already-applied migrations. Callers MUST always supply `exchange` explicitly when inserting new rows; the `DEFAULT` exists only to handle the schema-evolution case.

The `.down.sql` for `0006_outbox_exchange` SHALL be **a no-op** (forward-only schema evolution). Dropping the `exchange` column after `'task.control'` rows have been written would silently re-route those rows to `'task.exchange'` on a subsequent re-up, which is worse than leaving the harmless `NOT NULL DEFAULT` column in place. Operators who genuinely need to delete the column must do so out-of-band; this is a deliberate design trade-off.

#### Scenario: Outbox table exists after initial migration
- **WHEN** `api migrate up` runs against an empty database
- **THEN** the `outbox` table MUST exist with all required columns, and the `(status, next_retry_at)` index MUST be present

#### Scenario: Exchange column exists after migration
- **WHEN** migration `0006_outbox_exchange.up.sql` has been applied
- **THEN** `outbox` MUST have a column `exchange TEXT NOT NULL DEFAULT 'task.exchange'`, AND every existing row's `exchange` value MUST equal `'task.exchange'`

#### Scenario: Down migration is a no-op
- **WHEN** `0006_outbox_exchange.down.sql` is applied (e.g., `migrate -1`)
- **THEN** the `exchange` column MUST still be present, AND no rows MUST have been modified or dropped, AND `schema_migrations.version` MUST decrement so a subsequent `migrate up` re-runs the `0006_outbox_exchange.up.sql` (which is itself a no-op due to `ADD COLUMN IF NOT EXISTS`)

#### Scenario: Up→down→up sequence is idempotent
- **WHEN** `0006_outbox_exchange.up.sql` then `.down.sql` then `.up.sql` is run against a database (fresh or populated)
- **THEN** the final schema MUST have the `exchange` column, AND `schema_migrations.dirty` MUST be `false` at each step, AND no `outbox` row's `exchange` value MUST have changed across the sequence

