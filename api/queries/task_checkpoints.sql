-- name: SelectLatestCheckpoint :one
-- The highest-step_seq checkpoint for a run, or zero rows if none. Workers
-- use this on resume to find the resume point.
SELECT *
FROM task_checkpoints
WHERE run_id = $1
ORDER BY step_seq DESC
LIMIT 1;
