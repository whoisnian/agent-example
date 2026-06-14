-- add-task-deletion: soft-delete support on tasks.
-- A nullable deleted_at expresses soft deletion (NULL = live). The status
-- CHECK enum is unchanged. The full listing index is replaced by a partial
-- index over live rows, since every owner-scoped listing filters deleted_at.
ALTER TABLE tasks ADD COLUMN deleted_at TIMESTAMPTZ;

DROP INDEX IF EXISTS tasks_tenant_user_status_idx;
CREATE INDEX tasks_tenant_user_status_live_idx
    ON tasks (tenant_id, user_id, status)
    WHERE deleted_at IS NULL;
