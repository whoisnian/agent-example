-- 0005_seed_pricing (down): remove seeded pricing rows, preserving any that
-- have been referenced by cost_events.
--
-- The `cost_events.pricing_id` FK has no ON DELETE clause (defaults to
-- RESTRICT) — per the task-cost-data-model invariant that historical cost
-- records must stay attributable. So a naive DELETE WHERE id IN (...) would
-- fail with SQLSTATE 23503 in any environment that has settled events.
--
-- The NOT EXISTS predicate makes rollback partial-by-design: on a fresh DB
-- with no cost_events, every seed row is removed; in a live env, rows
-- referenced by historical events stay in place (preserving immutability).
-- This matches the task-cost-data-model §"Default Pricing Seed" scenario
-- "Seed down preserves rows referenced by cost_events".

BEGIN;

DELETE FROM pricing
WHERE id IN (
    '133ab3ce-f1c4-44e7-8916-c0b397d56a6c', -- opus input
    '1270ab6a-6196-44e1-b47f-a2489d8feec7', -- opus output
    '5967f314-49a5-4102-8f29-bac5139c6e79', -- sonnet input
    '0fd096b7-e210-4037-9038-9e3cdc489809', -- sonnet output
    '783fde1d-6f71-4201-82a5-038e2c88d2b6', -- haiku input
    '70489dbe-a72e-4a92-837f-6bd3e1406108', -- haiku output
    '30cc3aa1-c634-4e4e-8afd-ee5e999ec4a2', -- oss_fs per_call
    '63fa9884-8d56-427f-b65f-996d1a8f9b7c'  -- worker per_second
)
AND NOT EXISTS (
    SELECT 1 FROM cost_events WHERE cost_events.pricing_id = pricing.id
);

COMMIT;
