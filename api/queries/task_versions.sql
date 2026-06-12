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

-- name: GetActiveVersionByTask :one
-- Returns the (at most one) active version row for a task; used by the
-- task-write-api 409 path to enrich the response envelope after the savepoint
-- detects a mutex hit. The unique partial index `one_active_version_per_task`
-- guarantees at most one match.
SELECT *
FROM task_versions
WHERE task_id = $1 AND is_active
ORDER BY version_no DESC
LIMIT 1;

-- name: GetVersionByTaskAndID :one
-- Look up a specific version inside a task. Used to validate that an
-- iterate request's optional `base_version_id` belongs to the path task_id
-- atomically, without round-tripping a separate ownership check.
SELECT *
FROM task_versions
WHERE id = $1 AND task_id = $2;

-- name: MaxVersionNoForTask :one
-- Highest version_no observed for a task, 0 when none exist. Used by iterate
-- to assign the new version_no (max + 1).
SELECT COALESCE(MAX(version_no), 0)::int AS max_version_no
FROM task_versions
WHERE task_id = $1;

-- name: GetVersionOwnerTaskID :one
-- Narrow lookup returning just the version's owning task_id. Used by the
-- Cost Service to verify a worker-supplied (task_id, version_id) before
-- the task_costs UPSERT, enforcing task-cost-data-model §"Task Costs
-- task_id is Immutable Per version_id". Returns no rows if version_id is
-- unknown (caller treats as DLQ-permanent).
SELECT task_id
FROM task_versions
WHERE id = $1;

-- name: UpdateVersionStatus :execrows
-- Event-ingest state-machine CAS (add-event-ingest-status-sync). The WHERE
-- clause carries two guards so the update is safe under at-least-once,
-- out-of-order delivery:
--   * terminal guard: a version already in a terminal state is never moved;
--   * real-transition guard (IS DISTINCT FROM): a redelivered same-status
--     event affects 0 rows, so the caller's transition metric is accurate.
-- Setting a terminal status flips the generated is_active column to false
-- automatically, freeing the one_active_version_per_task index slot.
UPDATE task_versions
SET status = $2
WHERE id = $1
  AND status NOT IN ('succeeded', 'failed', 'cancelled')
  AND status IS DISTINCT FROM $2;

-- name: UpdateVersionSummary :execrows
-- Run-result summary write driven by a worker `kind=summary` event
-- (refactor-task-conversation-continuity). Last-write-wins by design, no
-- terminal guard — the summary event races the trailing status event at run
-- end. Sanitation and truncation happen in the Domain Service
-- (ApplyVersionSummary); this is not a state-machine transition.
UPDATE task_versions
SET summary = $2
WHERE id = $1;

-- name: GetVersionChainEntry :one
-- Narrow per-hop lookup for walking the parent chain from a base version up
-- to the root (history assembly, task-conversation-history). The walk loop
-- and its depth bound live in the Domain Service: chain depth is capped at a
-- small constant, so bounded PK point-reads inside the iterate transaction
-- beat a recursive CTE that sqlc's analyzer cannot type.
SELECT id, parent_id, version_no, prompt, summary, status
FROM task_versions
WHERE id = $1;
