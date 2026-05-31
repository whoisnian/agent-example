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

-- name: GetActiveRunIDForTask :one
-- Resolves the latest task_runs.id for the task's current version
-- (add-task-control-api). Returns no rows when current_version is NULL
-- or no attempts have been claimed yet (pre-claim state — the caller
-- writes the outbox row with run_id = null).
--
-- "Latest" = highest attempt_no, NOT a status filter. A terminal run
-- (e.g. succeeded) may surface here; the worker is the authoritative
-- "is this run currently active in my process" filter. Reviewer S10.
SELECT r.id
FROM task_runs r
JOIN tasks t ON t.current_version = r.version_id
WHERE t.id = $1
ORDER BY r.attempt_no DESC
LIMIT 1;
