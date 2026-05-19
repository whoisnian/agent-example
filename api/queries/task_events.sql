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
