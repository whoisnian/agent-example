-- 0004_cost_events_kind_unique (down): restore the legacy two-column index.
--
-- For rollback only. The (run_id, seq) form permits cross-kind collisions
-- (see the up migration), but at rollback time the consumer that would
-- exercise such conflicts will have been deployed-back too — so the
-- legacy invariant is acceptable for the rollback window.

BEGIN;

DROP INDEX IF EXISTS cost_events_run_kind_seq_key;
CREATE UNIQUE INDEX cost_events_run_seq_key
    ON cost_events (run_id, seq);

COMMIT;
