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

-- name: GetTaskCostWithOwner :one
-- Task-level totals scoped to an owner. Drives FROM tasks (not task_costs)
-- and LEFT JOINs the cost rows so an owned-but-empty task — one with no
-- versions / no settled events yet — still returns a 1-row zero-aggregate.
-- An unknown OR unowned task_id returns no rows; the caller maps pgx.ErrNoRows
-- to ErrTaskNotFound (identical 404 regardless of cause). GROUP BY t.id is
-- load-bearing: without it the aggregate would always return 1 row with zero
-- sums regardless of the WHERE — breaking the owner check.
SELECT
    t.id                                                                            AS task_id,
    COALESCE(SUM(tc.input_tokens), 0)::bigint                                       AS input_tokens,
    COALESCE(SUM(tc.output_tokens), 0)::bigint                                      AS output_tokens,
    COALESCE(SUM(tc.cached_tokens), 0)::bigint                                      AS cached_tokens,
    COALESCE(SUM(tc.tool_calls), 0)::int                                            AS tool_calls,
    COALESCE(SUM(tc.wall_time_ms), 0)::bigint                                       AS wall_time_ms,
    COALESCE(SUM(tc.compute_seconds), 0)::bigint                                    AS compute_seconds,
    COALESCE(SUM(tc.amount_usd), 0)::numeric                                        AS amount_usd
FROM tasks t
LEFT JOIN task_costs tc ON tc.task_id = t.id
WHERE t.id = $1 AND t.tenant_id = $2 AND t.user_id = $3
GROUP BY t.id;

-- name: ListVersionCostsForTask :many
-- Per-version breakdown for /tasks/{id}/cost. Drives FROM task_versions
-- (LEFT JOIN task_costs) so a version whose Cost Service has nothing to
-- UPSERT yet still appears in the breakdown with the cost columns as NULL
-- (the caller zero-fills). Ownership predicate goes through tasks. Orders
-- by version_no ASC so the breakdown renders chronologically without a
-- second sort on the client.
SELECT
    v.id                AS version_id,
    v.version_no,
    v.created_at,
    tc.input_tokens,
    tc.output_tokens,
    tc.cached_tokens,
    tc.tool_calls,
    tc.wall_time_ms,
    tc.compute_seconds,
    tc.amount_usd,
    tc.updated_at
FROM task_versions v
JOIN tasks t ON t.id = v.task_id
LEFT JOIN task_costs tc ON tc.version_id = v.id
WHERE v.task_id = $1 AND t.tenant_id = $2 AND t.user_id = $3
ORDER BY v.version_no ASC;

-- name: GetVersionCostWithOwner :one
-- Single-version cost lookup with owner check + owning task_id for
-- deep-linking. Drives FROM task_versions, JOIN tasks for the owner
-- predicate, LEFT JOIN task_costs so versions with no settled events
-- still return a row (cost columns NULL → mapper zero-fills, updated_at
-- NULL becomes null in JSON). Unknown / unowned returns no rows;
-- caller maps to ErrVersionNotFound.
SELECT
    v.id                AS version_id,
    v.task_id,
    v.version_no,
    tc.input_tokens,
    tc.output_tokens,
    tc.cached_tokens,
    tc.tool_calls,
    tc.wall_time_ms,
    tc.compute_seconds,
    tc.amount_usd,
    tc.updated_at
FROM task_versions v
JOIN tasks t ON t.id = v.task_id
LEFT JOIN task_costs tc ON tc.version_id = v.id
WHERE v.id = $1 AND t.tenant_id = $2 AND t.user_id = $3;

-- name: SumOwnerCosts :one
-- Caller-scoped totals for /me/cost (no group_by branch). Aggregates
-- cost_events directly so the time filter applies to occurred_at — settle-
-- time would corrupt buckets when a backfill lands. Per-column FILTER
-- clauses mirror the cost-ingest per-kind mapping so the resulting
-- CostSummary shape matches task_costs' semantics:
--   * tool_calls bumps only for kind='tool' (NULL→1 default per worker contract)
--   * wall_time_ms bumps for kind IN ('llm','tool') (compute events have
--     their own aggregate, but CostSummary doesn't surface compute_seconds)
--   * token columns are plain SUM (NULL contributes 0)
-- The inner JOIN to tasks is the owner gate AND defense-in-depth against
-- orphaned cost_events.task_id rows (no FK on that column).
SELECT
    COALESCE(SUM(ce.input_tokens), 0)::bigint                                       AS input_tokens,
    COALESCE(SUM(ce.output_tokens), 0)::bigint                                      AS output_tokens,
    COALESCE(SUM(ce.cached_tokens), 0)::bigint                                      AS cached_tokens,
    COALESCE(SUM(COALESCE(ce.calls, 1)) FILTER (WHERE ce.kind = 'tool'), 0)::int    AS tool_calls,
    COALESCE(SUM(COALESCE(ce.duration_ms, 0)) FILTER (WHERE ce.kind IN ('llm','tool')), 0)::bigint AS wall_time_ms,
    COALESCE(SUM(ce.amount_usd), 0)::numeric                                        AS amount_usd
FROM cost_events ce
JOIN tasks t ON t.id = ce.task_id
WHERE t.tenant_id = $1
  AND t.user_id   = $2
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR ce.occurred_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::timestamptz   IS NULL OR ce.occurred_at <  sqlc.narg('to_ts'));

-- Caller-scoped grouped rollups for /me/cost. Split into three queries
-- (instead of one with `CASE sqlc.arg('group_by')`) because sqlc infers
-- the CASE-expression result as `interface{}` — design D5 contingency
-- triggered. Each query is otherwise identical in shape to SumOwnerCosts
-- and emits a stable `string` key. ORDER BY key ASC keeps client
-- rendering deterministic without a second sort.

-- name: GroupOwnerCostsByDay :many
-- Day bucket; the PG16+ three-arg date_trunc pins UTC so the boundary
-- doesn't drift with the connection's session TimeZone.
SELECT
    to_char(date_trunc('day', ce.occurred_at, 'UTC'), 'YYYY-MM-DD')::text           AS key,
    COALESCE(SUM(ce.input_tokens), 0)::bigint                                       AS input_tokens,
    COALESCE(SUM(ce.output_tokens), 0)::bigint                                      AS output_tokens,
    COALESCE(SUM(ce.cached_tokens), 0)::bigint                                      AS cached_tokens,
    COALESCE(SUM(COALESCE(ce.calls, 1)) FILTER (WHERE ce.kind = 'tool'), 0)::int    AS tool_calls,
    COALESCE(SUM(COALESCE(ce.duration_ms, 0)) FILTER (WHERE ce.kind IN ('llm','tool')), 0)::bigint AS wall_time_ms,
    COALESCE(SUM(ce.amount_usd), 0)::numeric                                        AS amount_usd
FROM cost_events ce
JOIN tasks t ON t.id = ce.task_id
WHERE t.tenant_id = $1
  AND t.user_id   = $2
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR ce.occurred_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::timestamptz   IS NULL OR ce.occurred_at <  sqlc.narg('to_ts'))
GROUP BY key
ORDER BY key ASC;

-- name: GroupOwnerCostsByTaskType :many
-- Group by tasks.task_type. NULL is impossible (task_type is NOT NULL) so
-- no COALESCE needed.
SELECT
    t.task_type                                                                     AS key,
    COALESCE(SUM(ce.input_tokens), 0)::bigint                                       AS input_tokens,
    COALESCE(SUM(ce.output_tokens), 0)::bigint                                      AS output_tokens,
    COALESCE(SUM(ce.cached_tokens), 0)::bigint                                      AS cached_tokens,
    COALESCE(SUM(COALESCE(ce.calls, 1)) FILTER (WHERE ce.kind = 'tool'), 0)::int    AS tool_calls,
    COALESCE(SUM(COALESCE(ce.duration_ms, 0)) FILTER (WHERE ce.kind IN ('llm','tool')), 0)::bigint AS wall_time_ms,
    COALESCE(SUM(ce.amount_usd), 0)::numeric                                        AS amount_usd
FROM cost_events ce
JOIN tasks t ON t.id = ce.task_id
WHERE t.tenant_id = $1
  AND t.user_id   = $2
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR ce.occurred_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::timestamptz   IS NULL OR ce.occurred_at <  sqlc.narg('to_ts'))
GROUP BY key
ORDER BY key ASC;

-- name: GroupOwnerCostsByModel :many
-- Group by model. Llm events bucket by resource_name; tool/compute events
-- collapse into a single 'other' bucket so the amount_usd sum still
-- reconciles with SumOwnerCosts.
SELECT
    CASE WHEN ce.kind = 'llm' THEN ce.resource_name ELSE 'other' END                AS key,
    COALESCE(SUM(ce.input_tokens), 0)::bigint                                       AS input_tokens,
    COALESCE(SUM(ce.output_tokens), 0)::bigint                                      AS output_tokens,
    COALESCE(SUM(ce.cached_tokens), 0)::bigint                                      AS cached_tokens,
    COALESCE(SUM(COALESCE(ce.calls, 1)) FILTER (WHERE ce.kind = 'tool'), 0)::int    AS tool_calls,
    COALESCE(SUM(COALESCE(ce.duration_ms, 0)) FILTER (WHERE ce.kind IN ('llm','tool')), 0)::bigint AS wall_time_ms,
    COALESCE(SUM(ce.amount_usd), 0)::numeric                                        AS amount_usd
FROM cost_events ce
JOIN tasks t ON t.id = ce.task_id
WHERE t.tenant_id = $1
  AND t.user_id   = $2
  AND (sqlc.narg('from_ts')::timestamptz IS NULL OR ce.occurred_at >= sqlc.narg('from_ts'))
  AND (sqlc.narg('to_ts')::timestamptz   IS NULL OR ce.occurred_at <  sqlc.narg('to_ts'))
GROUP BY key
ORDER BY key ASC;

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

-- name: UpsertVersionCost :exec
-- Sole writer to task_costs (task-cost-data-model §"Task Costs Aggregation
-- Table"). Per-event aggregate increment; caller pre-resolves NULL→0 and
-- per-kind column gating per spec §"Aggregate Increment Mapping Per Kind".
--
-- task_id is deliberately ABSENT from DO UPDATE SET — task-cost-data-model
-- §"Task Costs task_id is Immutable Per version_id" requires that a
-- version_id's task ownership never migrate via the UPSERT. The settler
-- pre-verifies via task_versions and DLQs on mismatch, so by the time we
-- get here the supplied task_id is authoritative for an INSERT but
-- redundant on UPDATE.
INSERT INTO task_costs (
    version_id, task_id,
    input_tokens, output_tokens, cached_tokens,
    tool_calls, wall_time_ms, compute_seconds, amount_usd
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
ON CONFLICT (version_id) DO UPDATE SET
    input_tokens    = task_costs.input_tokens    + EXCLUDED.input_tokens,
    output_tokens   = task_costs.output_tokens   + EXCLUDED.output_tokens,
    cached_tokens   = task_costs.cached_tokens   + EXCLUDED.cached_tokens,
    tool_calls      = task_costs.tool_calls      + EXCLUDED.tool_calls,
    wall_time_ms    = task_costs.wall_time_ms    + EXCLUDED.wall_time_ms,
    compute_seconds = task_costs.compute_seconds + EXCLUDED.compute_seconds,
    amount_usd      = task_costs.amount_usd      + EXCLUDED.amount_usd,
    updated_at      = now();
