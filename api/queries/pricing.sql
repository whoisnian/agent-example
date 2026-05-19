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
