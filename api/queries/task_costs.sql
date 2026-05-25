-- name: GetVersionCost :one
-- One row per version. Returns no rows if the Cost Service hasn't yet
-- upserted anything for the version (treat as "0 across the board").
SELECT *
FROM task_costs
WHERE version_id = $1;

-- name: GetTaskCost :one
-- Task-level totals across all versions. Returns a single row with
-- aggregated values (zeros when no rows match — COALESCE keeps the API
-- caller from special-casing NULLs).
SELECT
    sqlc.arg('task_id')::uuid                                                       AS task_id,
    COALESCE(SUM(input_tokens), 0)::bigint                                          AS input_tokens,
    COALESCE(SUM(output_tokens), 0)::bigint                                         AS output_tokens,
    COALESCE(SUM(cached_tokens), 0)::bigint                                         AS cached_tokens,
    COALESCE(SUM(tool_calls), 0)::int                                               AS tool_calls,
    COALESCE(SUM(wall_time_ms), 0)::bigint                                          AS wall_time_ms,
    COALESCE(SUM(compute_seconds), 0)::bigint                                       AS compute_seconds,
    COALESCE(SUM(amount_usd), 0)::numeric                                           AS amount_usd
FROM task_costs
WHERE task_id = sqlc.arg('task_id');

-- name: ListTaskCostsByTasks :many
-- Batched per-task cost totals for the task list, avoiding an N+1 of
-- GetTaskCost per row. Returns one row per task_id that has any task_costs
-- rows; tasks absent from the result are zero-filled by the caller.
SELECT
    task_id,
    COALESCE(SUM(input_tokens), 0)::bigint     AS input_tokens,
    COALESCE(SUM(output_tokens), 0)::bigint    AS output_tokens,
    COALESCE(SUM(cached_tokens), 0)::bigint     AS cached_tokens,
    COALESCE(SUM(tool_calls), 0)::int           AS tool_calls,
    COALESCE(SUM(wall_time_ms), 0)::bigint       AS wall_time_ms,
    COALESCE(SUM(compute_seconds), 0)::bigint    AS compute_seconds,
    COALESCE(SUM(amount_usd), 0)::numeric        AS amount_usd
FROM task_costs
WHERE task_id = ANY(sqlc.arg('task_ids')::uuid[])
GROUP BY task_id;

-- name: ListVersionCostsByTask :many
-- All per-version cost rows for a task, fetched once for the whole version
-- tree (avoids a per-node GetVersionCost N+1). Versions without a row are
-- zero-filled by the caller. Covered by the task_costs (task_id) index.
SELECT *
FROM task_costs
WHERE task_id = $1;
