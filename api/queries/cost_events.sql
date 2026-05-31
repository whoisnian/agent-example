-- name: InsertCostEvent :one
-- Idempotent insert keyed on (run_id, kind, seq) — the uniqueness boundary
-- after migration 0004_cost_events_kind_unique (matches the Worker's
-- per-(run_id, kind) seq namespace per worker-messaging §"Cost Event
-- Publisher"). Returns the inserted row's quantity / amount fields so the
-- caller can drive the task_costs UPSERT in the same transaction. On
-- duplicate delivery the RETURNING set is empty (caller treats pgx.ErrNoRows
-- as "duplicate, skip aggregate UPSERT").
INSERT INTO cost_events (
    task_id, version_id, run_id, seq, kind, resource_name,
    input_tokens, output_tokens, cached_tokens, calls, duration_ms,
    amount_usd, pricing_id, occurred_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13, $14
)
ON CONFLICT (run_id, kind, seq) DO NOTHING
RETURNING input_tokens, output_tokens, cached_tokens, calls, duration_ms,
          amount_usd, pricing_id;