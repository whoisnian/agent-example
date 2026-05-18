-- 0001_init_outbox.up
-- Creates the outbox table per docs/ARCHITECTURE.md §4.2.
CREATE TABLE IF NOT EXISTS outbox (
  id             BIGSERIAL PRIMARY KEY,
  aggregate      TEXT        NOT NULL,
  aggregate_id   UUID        NOT NULL,
  topic          TEXT        NOT NULL,
  payload        JSONB       NOT NULL,
  status         TEXT        NOT NULL DEFAULT 'pending',
  attempts       INT         NOT NULL DEFAULT 0,
  next_retry_at  TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Supports the relayer's pending-row scan (status='pending' AND next_retry_at <= now()).
CREATE INDEX IF NOT EXISTS outbox_status_next_retry_at_idx
  ON outbox (status, next_retry_at);
