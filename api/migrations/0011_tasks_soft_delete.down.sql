DROP INDEX IF EXISTS tasks_tenant_user_status_live_idx;
CREATE INDEX tasks_tenant_user_status_idx ON tasks (tenant_id, user_id, status);
ALTER TABLE tasks DROP COLUMN IF EXISTS deleted_at;
