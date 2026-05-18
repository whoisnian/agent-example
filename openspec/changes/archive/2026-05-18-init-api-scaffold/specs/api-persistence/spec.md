## ADDED Requirements

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

#### Scenario: Outbox table exists after initial migration
- **WHEN** `api migrate up` runs against an empty database
- **THEN** the `outbox` table MUST exist with all required columns, and the `(status, next_retry_at)` index MUST be present
