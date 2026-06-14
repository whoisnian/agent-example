## ADDED Requirements

### Requirement: Delete Task Endpoint

The API SHALL expose `DELETE /api/v1/tasks/{task_id}` that soft-deletes one task the caller owns. The operation MUST be owner-scoped, idempotent at the visibility level, and MUST go through a Domain Service state-machine method (no raw `UPDATE` in the handler). A successful first delete MUST set `tasks.deleted_at = now()` (and `updated_at = now()`) and respond HTTP `200` with the unified envelope. The endpoint MUST NOT delete `task_versions`, `task_runs`, `artifacts`, `cost_events`, or any OSS object — soft delete only hides the task; the underlying rows are retained for audit and cost-settlement integrity.

#### Scenario: Owner soft-deletes a task

- **WHEN** the owner `DELETE`s `/api/v1/tasks/{task_id}` for a task with no active version
- **THEN** the response MUST be HTTP `200` with the unified envelope, `tasks.deleted_at` MUST be set, and the task's `task_versions` / `cost_events` rows MUST remain present in the database

#### Scenario: Deleting an active task is rejected

- **WHEN** the owner `DELETE`s a task that has an `is_active` version (pending/queued/running/paused/cancelling)
- **THEN** the response MUST be HTTP `409` with `code = "active_version_exists"` and `data` carrying `active_version_id` and `active_version_status` (the same conflict shape as iterate/rollback), and `tasks.deleted_at` MUST remain `NULL`

#### Scenario: Delete is idempotent and does not leak existence

- **WHEN** the caller `DELETE`s a task id that does not exist, is owned by another tenant/user, or is already soft-deleted
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"` (indistinguishable across the three cases); for an already-deleted task the original `deleted_at` timestamp MUST be unchanged (the guarded `WHERE deleted_at IS NULL` update affects 0 rows)

#### Scenario: Soft delete uses a state-machine method, not raw UPDATE

- **WHEN** the delete is processed
- **THEN** the status flip MUST be performed via the Domain Service method (owner check → active check → guarded soft-delete update in one transaction), MUST NOT be a raw `UPDATE` issued from the HTTP handler, and MUST emit a structured log carrying `trace_id` and `task_id`
