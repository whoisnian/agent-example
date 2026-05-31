-- 0006_outbox_exchange (down): intentional no-op.
--
-- Forward-only schema evolution (api-persistence spec; reviewer S6):
-- dropping the `exchange` column after some rows have been written with
-- `exchange = 'task.control'` would silently re-route those rows to
-- `'task.exchange'` on a subsequent re-up — worse than leaving the
-- harmless NOT NULL DEFAULT column in place.
--
-- `schema_migrations.version` still decrements on `migrate -1`, so a
-- subsequent `migrate up` re-runs this migration's up.sql, which is
-- itself a no-op (ADD COLUMN IF NOT EXISTS). Operators who genuinely
-- need to delete the column must do so out-of-band, after auditing
-- pending rows.

-- intentionally empty
SELECT 1;
