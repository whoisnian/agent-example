## MODIFIED Requirements

### Requirement: Tasks Table

The schema SHALL define a `tasks` table representing a user's task intent. Each row MUST carry: `id UUID PRIMARY KEY`, `tenant_id UUID NOT NULL`, `user_id UUID NOT NULL`, `title TEXT NOT NULL`, `task_type TEXT NOT NULL` (e.g., `code-gen`, `research`), `status TEXT NOT NULL`, `current_version UUID` (nullable, points at `task_versions.id`), `deleted_at TIMESTAMPTZ` (nullable, default `NULL`; non-NULL means the task is soft-deleted), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`, `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`. A `CHECK` constraint MUST restrict `status` to `{pending, running, paused, cancelled, succeeded, failed}` (soft deletion is expressed by `deleted_at`, NOT by a `deleted` status value — the enum is unchanged). To support listing of live tasks, a **partial** composite index `(tenant_id, user_id, status) WHERE deleted_at IS NULL` MUST exist; this replaces the prior full composite index on `(tenant_id, user_id, status)` (the migration drops the full index and creates the partial one), since every owner-scoped listing query filters `deleted_at IS NULL`.

#### Scenario: Required columns enforced
- **WHEN** an `INSERT INTO tasks` omits `tenant_id`, `user_id`, `title`, `task_type`, or `status`
- **THEN** PostgreSQL MUST raise `NOT NULL` violation (SQLSTATE `23502`)

#### Scenario: Invalid status rejected
- **WHEN** an `INSERT INTO tasks` supplies `status = 'unknown'`
- **THEN** PostgreSQL MUST raise the CHECK violation (SQLSTATE `23514`)

#### Scenario: deleted_at defaults to NULL (task is live)
- **WHEN** a `tasks` row is inserted without a `deleted_at` value
- **THEN** the insert MUST succeed AND the row's `deleted_at` MUST be `NULL`

#### Scenario: Soft delete sets the timestamp without touching status
- **WHEN** a task is soft-deleted
- **THEN** `deleted_at` MUST be set to a non-NULL timestamp AND `status` MUST be unchanged (no `deleted` status value exists)

#### Scenario: Live-listing is served by the partial index
- **WHEN** the listing query selects the caller's live tasks (it filters `deleted_at IS NULL`)
- **THEN** it MUST be served by the partial index `(tenant_id, user_id, status) WHERE deleted_at IS NULL`, and the prior full `(tenant_id, user_id, status)` index MUST NOT remain (it is replaced, not duplicated)
