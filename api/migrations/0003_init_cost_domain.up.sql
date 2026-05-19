-- 0003_init_cost_domain: pricing, cost_events, task_costs.
--
-- Capability: task-cost-data-model.
-- Key invariants enforced by this migration:
--   * pricing has at most one row per (resource_kind, resource_name, unit,
--     effective_at); historical rows are immutable by convention, enforced
--     by code review.
--   * cost_events are idempotent on (run_id, seq).
--   * task_costs holds at most one row per version; Cost Service is the
--     sole writer (UPSERT) — convention, enforced by review.

BEGIN;

-- ---------------------------------------------------------------------------
-- pricing: model / tool / compute unit prices over time.
-- ---------------------------------------------------------------------------
CREATE TABLE pricing (
    id             UUID PRIMARY KEY,
    resource_kind  TEXT NOT NULL,
    resource_name  TEXT NOT NULL,
    unit           TEXT NOT NULL,
    unit_price_usd NUMERIC(18, 8) NOT NULL,
    effective_at   TIMESTAMPTZ NOT NULL,
    expires_at     TIMESTAMPTZ,
    CONSTRAINT pricing_kind_check CHECK (resource_kind IN ('llm', 'tool', 'compute')),
    CONSTRAINT pricing_window_check CHECK (
        expires_at IS NULL OR expires_at > effective_at
    ),
    CONSTRAINT pricing_unique_effective UNIQUE (
        resource_kind, resource_name, unit, effective_at
    )
);

-- ---------------------------------------------------------------------------
-- cost_events: fine-grained LLM / tool / compute cost records.
-- ---------------------------------------------------------------------------
CREATE TABLE cost_events (
    id            BIGSERIAL PRIMARY KEY,
    task_id       UUID NOT NULL,
    version_id    UUID NOT NULL,
    run_id        UUID NOT NULL,
    seq           BIGINT NOT NULL,
    kind          TEXT NOT NULL,
    resource_name TEXT NOT NULL,
    input_tokens  BIGINT,
    output_tokens BIGINT,
    cached_tokens BIGINT,
    calls         INT,
    duration_ms   BIGINT,
    amount_usd    NUMERIC(18, 8) NOT NULL,
    pricing_id    UUID REFERENCES pricing(id),
    occurred_at   TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT cost_events_kind_check CHECK (kind IN ('llm', 'tool', 'compute'))
);
CREATE UNIQUE INDEX cost_events_run_seq_key ON cost_events (run_id, seq);
CREATE INDEX cost_events_task_occurred_idx ON cost_events (task_id, occurred_at);
CREATE INDEX cost_events_version_idx       ON cost_events (version_id);

-- ---------------------------------------------------------------------------
-- task_costs: one running-aggregate row per version. Cost Service is the
-- sole writer (via UPSERT) — application convention, not DB-enforced.
-- ---------------------------------------------------------------------------
CREATE TABLE task_costs (
    version_id      UUID PRIMARY KEY REFERENCES task_versions(id),
    task_id         UUID NOT NULL,
    input_tokens    BIGINT NOT NULL DEFAULT 0,
    output_tokens   BIGINT NOT NULL DEFAULT 0,
    cached_tokens   BIGINT NOT NULL DEFAULT 0,
    tool_calls      INT    NOT NULL DEFAULT 0,
    wall_time_ms    BIGINT NOT NULL DEFAULT 0,
    compute_seconds BIGINT NOT NULL DEFAULT 0,
    amount_usd      NUMERIC(18, 8) NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX task_costs_task_idx ON task_costs (task_id);

COMMIT;
