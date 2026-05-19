-- Reverse 0002_init_task_domain.
-- Drop order = reverse FK order. No CASCADE beyond FK-dependents we own.

BEGIN;

DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS task_checkpoints;
DROP TABLE IF EXISTS task_events;
DROP TABLE IF EXISTS task_runs;

-- one_active_version_per_task index is dropped automatically with task_versions.
DROP TABLE IF EXISTS task_versions;
DROP TABLE IF EXISTS tasks;

COMMIT;
