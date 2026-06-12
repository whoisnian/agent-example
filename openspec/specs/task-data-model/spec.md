# task-data-model Specification

## Purpose
TBD - created by archiving change add-task-domain-schema. Update Purpose after archive.
## Requirements
### Requirement: Tasks Table

The schema SHALL define a `tasks` table representing a user's task intent. Each row MUST carry: `id UUID PRIMARY KEY`, `tenant_id UUID NOT NULL`, `user_id UUID NOT NULL`, `title TEXT NOT NULL`, `task_type TEXT NOT NULL` (e.g., `code-gen`, `research`), `status TEXT NOT NULL`, `current_version UUID` (nullable, points at `task_versions.id`), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`, `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`. A `CHECK` constraint MUST restrict `status` to `{pending, running, paused, cancelled, succeeded, failed}`. A composite index `(tenant_id, user_id, status)` MUST exist to support listing.

#### Scenario: Required columns enforced
- **WHEN** an `INSERT INTO tasks` omits `tenant_id`, `user_id`, `title`, `task_type`, or `status`
- **THEN** PostgreSQL MUST raise `NOT NULL` violation (SQLSTATE `23502`)

#### Scenario: Invalid status rejected
- **WHEN** an `INSERT INTO tasks` supplies `status = 'unknown'`
- **THEN** PostgreSQL MUST raise the CHECK violation (SQLSTATE `23514`)

### Requirement: Task Versions Table with Tree Invariant

The schema SHALL define a `task_versions` table forming a versioning tree per task. Each row MUST carry: `id UUID PRIMARY KEY`, `task_id UUID NOT NULL REFERENCES tasks(id)`, `parent_id UUID REFERENCES task_versions(id)` (nullable for the root version), `version_no INT NOT NULL`, `prompt TEXT NOT NULL`, `params JSONB NOT NULL DEFAULT '{}'::jsonb`, `status TEXT NOT NULL` with the same enum as `tasks.status` plus `queued` and `cancelling`, `artifact_root TEXT` (nullable OSS prefix), `summary TEXT` (nullable; the worker-generated run result summary applied via `kind=summary` events, see `task-event-ingest`), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`.

The pair `(task_id, version_no)` MUST be `UNIQUE`. A separate index on `(task_id, parent_id)` MUST exist for tree traversal.

#### Scenario: Duplicate version_no rejected
- **WHEN** two `INSERT`s use the same `(task_id, version_no)` pair
- **THEN** PostgreSQL MUST raise the unique-violation (SQLSTATE `23505`)

#### Scenario: Parent must exist
- **WHEN** an `INSERT` references a `parent_id` that does not exist in `task_versions`
- **THEN** PostgreSQL MUST raise FK violation (SQLSTATE `23503`)

#### Scenario: Summary is nullable and absent by default
- **WHEN** a `task_versions` row is inserted without a `summary` value
- **THEN** the insert MUST succeed AND the row's `summary` MUST be NULL

### Requirement: Task-Level Mutex via Unique Partial Index

`task_versions` MUST include a generated `BOOLEAN` column `is_active GENERATED ALWAYS AS (status IN ('pending','queued','running','paused','cancelling')) STORED`. A `UNIQUE INDEX one_active_version_per_task ON task_versions(task_id) WHERE is_active` MUST exist. The database — not application code — is the single source of truth for the "one active version per task" invariant.

#### Scenario: Concurrent iterate rejected at DB level
- **WHEN** two transactions concurrently `INSERT INTO task_versions` for the same `task_id` with `status='pending'`
- **THEN** at most one MUST commit; the other MUST fail with unique-violation (SQLSTATE `23505`) naming `one_active_version_per_task`

#### Scenario: Active-set transition releases the slot
- **WHEN** an active version transitions to a terminal status (`succeeded`, `failed`, `cancelled`)
- **THEN** the `is_active` generated column MUST automatically become `false`, AND a subsequent `INSERT INTO task_versions` for the same `task_id` with an active status MUST succeed

### Requirement: Task Runs Table

The schema SHALL define a `task_runs` table representing one execution attempt of a version. Each row MUST carry: `id UUID PRIMARY KEY`, `version_id UUID NOT NULL REFERENCES task_versions(id)`, `attempt_no INT NOT NULL`, `worker_run_id UUID` (nullable until claimed), `status TEXT NOT NULL` with values `{queued, running, paused, cancelling, cancelled, succeeded, failed}`, `started_at TIMESTAMPTZ`, `ended_at TIMESTAMPTZ`, `last_heartbeat TIMESTAMPTZ`, `error JSONB` (nullable; shape `{code, message, stack_oss_key}`), `idempotency_key TEXT NOT NULL UNIQUE`.

The pair `(version_id, attempt_no)` MUST be `UNIQUE`. An index on `(status, last_heartbeat)` MUST exist to support the Reaper.

#### Scenario: Idempotency key uniqueness
- **WHEN** two `INSERT INTO task_runs` use the same `idempotency_key`
- **THEN** PostgreSQL MUST raise unique-violation (SQLSTATE `23505`); the second insert with `ON CONFLICT (idempotency_key) DO NOTHING` MUST return zero rows

#### Scenario: Attempt count uniqueness
- **WHEN** two `INSERT`s use the same `(version_id, attempt_no)`
- **THEN** PostgreSQL MUST raise unique-violation (SQLSTATE `23505`)

### Requirement: Task Events Table with Per-Run Monotonic Sequence

The schema SHALL define a `task_events` table for status / log / step / artifact / error events. Each row MUST carry: `id BIGSERIAL PRIMARY KEY`, `task_id UUID NOT NULL`, `version_id UUID NOT NULL`, `run_id UUID`, `seq BIGINT NOT NULL`, `kind TEXT NOT NULL`, `payload JSONB NOT NULL`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`.

`(run_id, seq)` MUST be `UNIQUE` so duplicate event deliveries are rejected at the DB. An index on `(task_id, id)` MUST exist to support replay by chronological event id.

#### Scenario: Replayed event rejected
- **WHEN** an `INSERT INTO task_events` re-uses an existing `(run_id, seq)` pair
- **THEN** PostgreSQL MUST raise unique-violation (SQLSTATE `23505`); idempotent consumers using `ON CONFLICT (run_id, seq) DO NOTHING` MUST observe zero rows affected

#### Scenario: Listing supports replay-after-id
- **WHEN** a client issues `SELECT * FROM task_events WHERE task_id = $1 AND id > $2 ORDER BY id LIMIT $3`
- **THEN** the result set MUST contain every row matching the predicate (no gaps within the requested range up to `LIMIT`), monotonically ordered by `id` ascending. (Index existence is asserted separately by the structural integration test; the chosen query plan is the planner's prerogative.)

### Requirement: Task Checkpoints Table

The schema SHALL define a `task_checkpoints` table for worker checkpoints. Each row MUST carry: `id UUID PRIMARY KEY`, `run_id UUID NOT NULL REFERENCES task_runs(id)`, `step_seq INT NOT NULL`, `step_name TEXT NOT NULL`, `state JSONB NOT NULL`, `oss_key TEXT` (nullable, populated when payload exceeds inline budget), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`. The pair `(run_id, step_seq)` MUST be `UNIQUE`.

#### Scenario: Duplicate step rejected
- **WHEN** two `INSERT INTO task_checkpoints` use the same `(run_id, step_seq)`
- **THEN** PostgreSQL MUST raise unique-violation (SQLSTATE `23505`), and the second insert MUST be the one that fails

### Requirement: Artifacts Table

The schema SHALL define an `artifacts` table for OSS object metadata. Each row MUST carry: `id UUID PRIMARY KEY`, `version_id UUID NOT NULL REFERENCES task_versions(id)`, `kind TEXT NOT NULL` (e.g., `code-bundle`, `report`, `image`, `log`), `oss_key TEXT NOT NULL`, `mime TEXT`, `bytes BIGINT`, `sha256 TEXT`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`.

The table MUST enforce **at most one row per `(version_id, oss_key)`** via a UNIQUE constraint, so artifact recording is idempotent under at-least-once message delivery and under overwrite (a re-recorded or overwritten object updates the single row rather than appending a duplicate). Writers SHALL upsert on this key.

#### Scenario: Artifact rows are addressable by version
- **WHEN** a client queries `SELECT * FROM artifacts WHERE version_id = $1`
- **THEN** PostgreSQL MUST return all rows whose `version_id` matches, with no FK orphans (orphan rejection enforced by `REFERENCES task_versions(id)`)

#### Scenario: One row per object per version
- **WHEN** a writer records the same `(version_id, oss_key)` twice (e.g. a redelivered run re-inheriting a parent artifact, or an overwrite of a produced file)
- **THEN** the table MUST hold exactly one row for that pair (the second write upserts), never two

### Requirement: Migrations Apply Cleanly Both Ways

The migrations introducing this schema (`0002_init_task_domain.up.sql` / `0002_init_task_domain.down.sql`) MUST apply, roll back, and re-apply against a clean PostgreSQL 18.4 database with no residue. After `down`, no table, index, or generated column added by `up` MUST remain.

#### Scenario: Up → down → up is idempotent
- **WHEN** `api migrate up` then `api migrate down` then `api migrate up` is executed against a fresh database
- **THEN** the final `schema_migrations.dirty` MUST be `false`, AND every table named in this spec MUST be present with the declared columns and indexes
