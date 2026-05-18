-- 0001_init_outbox.down
DROP INDEX IF EXISTS outbox_status_next_retry_at_idx;
DROP TABLE IF EXISTS outbox;
