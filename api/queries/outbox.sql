-- name: ScanPendingOutbox :many
-- Returns up to $1 pending rows whose next_retry_at has elapsed (or is unset),
-- ordered by id for stable replay. Used by the Outbox Relayer scan step.
SELECT id, aggregate, aggregate_id, topic, payload, status, attempts, next_retry_at, created_at
FROM outbox
WHERE status = 'pending'
  AND (next_retry_at IS NULL OR next_retry_at <= now())
ORDER BY id
LIMIT $1;

-- name: MarkOutboxSent :exec
-- Marks an outbox row as successfully published.
UPDATE outbox
SET status = 'sent',
    attempts = attempts + 1
WHERE id = $1;

-- name: IncrementOutboxAttempt :exec
-- Records a publish failure: bumps attempts and schedules a retry at next_retry_at.
UPDATE outbox
SET attempts = attempts + 1,
    next_retry_at = $2
WHERE id = $1;

-- name: MarkOutboxFailed :exec
-- Moves an outbox row to terminal failed state after exhausting retries.
UPDATE outbox
SET status = 'failed',
    attempts = attempts + 1
WHERE id = $1;

-- name: CountOutboxPending :one
-- Used by the outbox_pending gauge.
SELECT count(*) FROM outbox WHERE status = 'pending';
