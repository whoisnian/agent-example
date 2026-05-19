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
