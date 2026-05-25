-- name: CreateTaskRun :one
-- Insert a new run. The unique idempotency_key lets workers issue
-- INSERT ... ON CONFLICT (idempotency_key) DO NOTHING RETURNING id for the
-- four-branch claim from worker-messaging spec (see add-task-create-api).
INSERT INTO task_runs (
    id, version_id, attempt_no, worker_run_id, status, idempotency_key
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: GetTaskRunByID :one
SELECT *
FROM task_runs
WHERE id = $1;

-- name: GetRunByIdempotencyKey :one
SELECT *
FROM task_runs
WHERE idempotency_key = $1;

-- name: ListRunsByVersion :many
-- All runs for a version, oldest attempt first, for the version-detail view
-- (retry history). The (version_id, attempt_no) unique constraint backs the
-- ordering.
SELECT *
FROM task_runs
WHERE version_id = $1
ORDER BY attempt_no ASC;
