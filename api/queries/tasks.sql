-- name: CreateTask :one
-- Insert a new task and return the row. The caller is responsible for ID
-- generation and for choosing the initial `status` (typically 'pending').
INSERT INTO tasks (
    id, tenant_id, user_id, title, task_type, status, current_version
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetTaskByID :one
SELECT *
FROM tasks
WHERE id = $1;

-- name: ListTasks :many
-- Owner-scoped listing with an optional status filter. Pass NULL for `status`
-- to skip the filter. Newest tasks first; pagination via LIMIT/OFFSET.
SELECT *
FROM tasks
WHERE tenant_id = $1
  AND user_id = $2
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4;

-- name: LockTaskRow :one
-- Acquires a row-level lock on a task and returns its id/status/current_version.
-- Used by the iterate path so concurrent requests against the same task
-- serialise behind one another inside the transaction.
SELECT id, status, current_version
FROM tasks
WHERE id = $1
FOR UPDATE;

-- name: LockTaskForControl :one
-- Acquires a row-level lock AND verifies ownership in one round-trip
-- (add-task-control-api). Concurrent control requests for the same task
-- serialise on this lock; the second handler observes the first's tx
-- outcome before reading task.status. Owner predicate is inline so
-- unknown OR unowned tasks return no rows — the caller maps
-- pgx.ErrNoRows to ErrTaskNotFound for the identical 404 regardless of
-- cause (mirrors task-read-api / task-cost-api). `task_type` is selected
-- so the rollback `branch` path (add-task-rollback-api) gets everything it
-- needs for createActiveVersion from this one owner-scoped lock, without an
-- unscoped GetTaskByID re-read; the control caller ignores the column.
SELECT id, status, current_version, task_type
FROM tasks
WHERE id = $1 AND tenant_id = $2 AND user_id = $3
FOR UPDATE;

-- name: UpdateTaskCurrentVersion :exec
-- Points `tasks.current_version` at the new version and stamps the task back
-- to `pending`. Called by the iterate transaction after `createActiveVersion`
-- succeeds.
UPDATE tasks
SET status = 'pending',
    current_version = $2,
    updated_at = now()
WHERE id = $1;

-- name: SwitchTaskCurrentVersion :exec
-- Rollback `switch` mode (add-task-rollback-api): repoints `current_version`
-- at a historical (terminal) version WITHOUT touching `tasks.status` —
-- task-event-ingest stays the sole run-driven writer of status. Contrast with
-- UpdateTaskCurrentVersion, which seeds status='pending' for a new run.
UPDATE tasks
SET current_version = $2,
    updated_at = now()
WHERE id = $1;

-- name: CountTasks :one
-- Total count of the caller's tasks for offset pagination, using the same
-- owner + optional-status predicate as ListTasks. Pass NULL for `status` to
-- skip the filter.
SELECT COUNT(*)::bigint AS total
FROM tasks
WHERE tenant_id = $1
  AND user_id = $2
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- name: UpdateTaskTitle :execrows
-- Semantic title write driven by a worker `kind=title` event
-- (add-semantic-task-title). Last-write-wins by design, no terminal guard —
-- a fast run may finish before its title event is consumed. Sanitation and
-- truncation happen in the Domain Service (ApplyGeneratedTitle); this is not
-- a state-machine transition.
UPDATE tasks
SET title = $2,
    updated_at = now()
WHERE id = $1;

-- name: UpdateTaskStatus :execrows
-- Event-ingest state-machine CAS (add-event-ingest-status-sync). Only the
-- task's current (active) version may drive tasks.status, so the update is
-- gated on current_version = $3 — a stale event for a superseded version is
-- a no-op. Same terminal + real-transition guards as UpdateVersionStatus.
UPDATE tasks
SET status = $2,
    updated_at = now()
WHERE id = $1
  AND current_version = $3
  AND status NOT IN ('succeeded', 'failed', 'cancelled')
  AND status IS DISTINCT FROM $2;
