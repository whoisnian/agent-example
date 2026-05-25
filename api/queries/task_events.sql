-- name: InsertTaskEvent :exec
-- Idempotent on (run_id, seq). Duplicate inserts from the Realtime Gateway
-- on Worker retries are silently dropped.
INSERT INTO task_events (
    task_id, version_id, run_id, seq, kind, payload
) VALUES (
    $1, $2, $3, $4, $5, $6
)
ON CONFLICT (run_id, seq) DO NOTHING;

-- name: ListEventsAfter :many
-- Pagination for WS gap-fill and HTTP replay: caller passes the highest
-- event id it has already observed. Results are monotonically ordered by
-- id; the (task_id, id) index supports the predicate. Pagination via LIMIT.
SELECT *
FROM task_events
WHERE task_id = $1
  AND id > $2
ORDER BY id ASC
LIMIT $3;

-- name: ListVersionEventsAfter :many
-- Version-scoped event backfill for the HTTP replay endpoint. Anchors on the
-- existing (task_id, id) index (task_id equality + id range) and filters
-- version_id as a residual predicate, so no new index is required. The caller
-- passes the task_id resolved from the version plus the highest id seen.
SELECT *
FROM task_events
WHERE task_id = $1
  AND version_id = $2
  AND id > $3
ORDER BY id ASC
LIMIT $4;
