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

-- name: UpdateTaskCurrentVersion :exec
-- Points `tasks.current_version` at the new version and stamps the task back
-- to `pending`. Called by the iterate transaction after `createActiveVersion`
-- succeeds.
UPDATE tasks
SET status = 'pending',
    current_version = $2,
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
