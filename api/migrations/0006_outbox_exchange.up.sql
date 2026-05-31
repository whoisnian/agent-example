-- 0006_outbox_exchange: add the per-row exchange column so the Outbox
-- Relayer can route each row to its destination exchange (not a hard-coded
-- constant).
--
-- Capability: api-persistence (MODIFIED by add-task-control-api).
--
-- The DEFAULT 'task.exchange' is what every previously-written row was
-- implicitly routed to (the old relayer's constant), so the migration is
-- safe for existing rows — they retain their effective routing. New
-- callers MUST always supply `exchange` explicitly when inserting; the
-- default exists only to handle this schema-evolution case.
--
-- `IF NOT EXISTS` makes a re-up after a rollback a no-op — see the
-- companion .down.sql which is intentionally a no-op (forward-only
-- schema evolution; the column is harmless to leave in place, and
-- dropping it after `task.control` rows have been written would
-- silently re-route them on subsequent re-up). Reviewer S6.

BEGIN;

ALTER TABLE outbox ADD COLUMN IF NOT EXISTS exchange TEXT NOT NULL DEFAULT 'task.exchange';

COMMIT;
