-- name: GetEffectivePricing :one
-- Returns the pricing row in force at $4 for (resource_kind, resource_name,
-- unit), or no rows if none. Used by the Cost Service when scoring a
-- cost_event at write time.
SELECT *
FROM pricing
WHERE resource_kind = $1
  AND resource_name = $2
  AND unit          = $3
  AND effective_at <= $4
  AND (expires_at IS NULL OR expires_at > $4)
ORDER BY effective_at DESC
LIMIT 1;

-- name: ListEffectivePricings :many
-- Returns every pricing row for (resource_kind, resource_name) whose
-- effective window covers $3 (occurred_at), across all units. The Cost
-- Service picks at most one row per `unit` in Go — the row with the latest
-- effective_at wins on collision. Replaces N per-unit GetEffectivePricing
-- round-trips with a single scan per event.
--
-- Window predicate is right-exclusive on expires_at (a row whose expires_at
-- equals occurred_at exactly does NOT match — matches the spec scenario
-- "Pricing windows abut exactly at occurred_at").
SELECT *
FROM pricing
WHERE resource_kind = $1
  AND resource_name = $2
  AND effective_at <= $3
  AND (expires_at IS NULL OR expires_at > $3)
ORDER BY effective_at DESC;
