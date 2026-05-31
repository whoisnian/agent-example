-- 0005_seed_pricing: load MVP-default pricing rows.
--
-- Capability: task-cost-data-model §"Default Pricing Seed" (ADDED by add-cost-service).
--
-- Source / date of figures: placeholder values aligned with publicly-available
-- Anthropic API pricing as of 2026-05 (per docs/ARCHITECTURE.md §8.6 and §11
-- discussion of cost domain). MVP scope — updates ship as NEW pricing rows
-- with later effective_at, never as in-place UPDATEs (per the immutability
-- convention bound in the task-cost-data-model spec).
--
-- Seeded resources:
--   * Workers's model_by_key defaults: claude-opus-4-7, claude-sonnet-4-6
--   * Defensive pre-seed (worker config will pick it up later): claude-haiku-4-5
--   * Tool: oss_fs per_call
--   * Compute: worker per_second
--
-- All rows: effective_at = 2024-01-01 (deliberately back-dated to cover any
-- imaginable historical occurred_at), expires_at = NULL.

BEGIN;

INSERT INTO pricing (id, resource_kind, resource_name, unit, unit_price_usd, effective_at, expires_at) VALUES
    -- claude-opus-4-7 (highest-tier model used by code agent)
    ('133ab3ce-f1c4-44e7-8916-c0b397d56a6c', 'llm', 'claude-opus-4-7',   'per_1k_input_tokens',  0.01500000, '2024-01-01T00:00:00Z', NULL),
    ('1270ab6a-6196-44e1-b47f-a2489d8feec7', 'llm', 'claude-opus-4-7',   'per_1k_output_tokens', 0.07500000, '2024-01-01T00:00:00Z', NULL),
    -- claude-sonnet-4-6 (mid-tier, research agent)
    ('5967f314-49a5-4102-8f29-bac5139c6e79', 'llm', 'claude-sonnet-4-6', 'per_1k_input_tokens',  0.00300000, '2024-01-01T00:00:00Z', NULL),
    ('0fd096b7-e210-4037-9038-9e3cdc489809', 'llm', 'claude-sonnet-4-6', 'per_1k_output_tokens', 0.01500000, '2024-01-01T00:00:00Z', NULL),
    -- claude-haiku-4-5 (defensive pre-seed — worker model_by_key may pick it up later)
    ('783fde1d-6f71-4201-82a5-038e2c88d2b6', 'llm', 'claude-haiku-4-5',  'per_1k_input_tokens',  0.00080000, '2024-01-01T00:00:00Z', NULL),
    ('70489dbe-a72e-4a92-837f-6bd3e1406108', 'llm', 'claude-haiku-4-5',  'per_1k_output_tokens', 0.00400000, '2024-01-01T00:00:00Z', NULL),
    -- oss_fs tool (file IO overhead — token-cost-of-overhead figure)
    ('30cc3aa1-c634-4e4e-8afd-ee5e999ec4a2', 'tool', 'oss_fs',           'per_call',             0.00000100, '2024-01-01T00:00:00Z', NULL),
    -- worker compute (small per-second placeholder so the column isn't always zero)
    ('63fa9884-8d56-427f-b65f-996d1a8f9b7c', 'compute', 'worker',        'per_second',           0.00010000, '2024-01-01T00:00:00Z', NULL);

COMMIT;
