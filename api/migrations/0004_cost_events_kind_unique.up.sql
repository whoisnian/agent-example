-- 0004_cost_events_kind_unique: re-key the cost-events idempotency boundary
-- from UNIQUE (run_id, seq) to UNIQUE (run_id, kind, seq).
--
-- Capability: task-cost-data-model (MODIFIED by add-cost-service).
-- Rationale: the Worker's CostEventPublisher allocates seq from a
-- per-(run_id, kind) counter (worker-messaging §"Cost Event Publisher":
-- "per-run-per-kind monotonic"). The original 0003-era index would silently
-- collide cost.llm seq=1 with cost.tool seq=1 for the same run, dropping
-- every kind-after-the-first as ON CONFLICT DO NOTHING. The Cost Service
-- consumer requires the kind-aware boundary before going live.

BEGIN;

DROP INDEX IF EXISTS cost_events_run_seq_key;
CREATE UNIQUE INDEX cost_events_run_kind_seq_key
    ON cost_events (run_id, kind, seq);

COMMIT;
