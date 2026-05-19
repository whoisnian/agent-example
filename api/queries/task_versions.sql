-- name: CreateTaskVersion :one
-- Insert a new version. The DB enforces task-level mutex: at most one row
-- with `is_active = true` per task_id, via the unique partial index
-- `one_active_version_per_task`. A second concurrent insert with an active
-- status raises SQLSTATE 23505 with that constraint name; callers translate
-- the error to a 409 envelope (owned by add-task-create-api).
INSERT INTO task_versions (
    id, task_id, parent_id, version_no, prompt, params, status, artifact_root
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetTaskVersionByID :one
SELECT *
FROM task_versions
WHERE id = $1;

-- name: ListVersionsByTask :many
-- Returns all versions for a task ordered by version_no ascending so callers
-- can rebuild the tree client-side.
SELECT *
FROM task_versions
WHERE task_id = $1
ORDER BY version_no ASC;
