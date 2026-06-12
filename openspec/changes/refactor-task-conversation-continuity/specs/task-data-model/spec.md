# task-data-model Specification (Delta)

## MODIFIED Requirements

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
