## ADDED Requirements

### Requirement: Rollback Endpoint and Modes

The API SHALL expose `POST /api/v1/tasks/{task_id}/rollback` accepting `{target_version_id, mode, prompt?, params?, lane?}` and returning the unified `{code, message, data, trace_id}` envelope. `mode` MUST be either `"branch"` (re-execute from the target historical version) or `"switch"` (repoint `current_version` at the target without executing); any other value MUST return HTTP `400` with `code = "invalid_input"`. A missing or malformed `target_version_id`, a malformed `task_id`, or malformed `params` MUST also return `400 invalid_input`. The route MUST require authentication (it is not public).

#### Scenario: Unknown mode is rejected
- **WHEN** the caller POSTs `mode` other than `"branch"` or `"switch"`
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` and no version, run, or outbox row MUST be written

#### Scenario: Missing target_version_id is rejected
- **WHEN** the caller POSTs a body without a valid `target_version_id`
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"`

### Requirement: Branch Mode Re-executes from a Historical Version

In `branch` mode the API SHALL create a new version whose `parent_id` is `target_version_id`, insert its `task_runs` row, enqueue an `execute` message via the outbox (carrying the target version's `artifact_root` as the parent artifact root, identical to iterate), point `tasks.current_version` at the new version, and seed `tasks.status = "pending"` — all in one transaction. The new `version_no` MUST be the task's current max plus one. When the request `prompt` is empty, the API SHALL substitute an auto-generated prompt naming the target version (e.g. `"rollback to version <n>"`) so the stored prompt is non-empty. On success the response MUST be HTTP `201` with `data = {version_id, version_no, status: "pending"}`.

`branch` is subject to the task-level mutex exactly as iterate: the create is guarded by the `one_active_version_per_task` index, and an attempt while a version is active MUST fail with `409 active_version_exists` (see "Non-Active Precondition").

#### Scenario: Branch from a historical version creates and enqueues a new version
- **GIVEN** a task whose versions are all terminal and a `target_version_id` belonging to that task
- **WHEN** the caller POSTs `mode = "branch"`
- **THEN** the response MUST be HTTP `201` with a new `version_id` whose `version_no` is max+1 and `status = "pending"`, a `task_runs` row MUST exist for it, exactly one `execute` outbox row MUST be enqueued carrying the target's artifact root, and `tasks.current_version` MUST point at the new version

#### Scenario: Branch with an empty prompt auto-fills
- **WHEN** the caller POSTs `mode = "branch"` with an empty or absent `prompt`
- **THEN** the created version's stored `prompt` MUST be a non-empty auto-generated value referencing the target version

### Requirement: Switch Mode Repoints current_version Only

In `switch` mode the API SHALL update **only** `tasks.current_version` (and `updated_at`) to `target_version_id`, in one owner-scoped transaction. It MUST NOT create a new version, MUST NOT insert a `task_runs` row, MUST NOT enqueue any outbox/execute message, and MUST NOT write `tasks.status` (preserving `task-event-ingest` as the sole run-driven writer of `tasks.status`). On success the response MUST be HTTP `200` with `data` echoing the now-current version (`{current_version_id, version_no, status}` where `status` is the task's existing, unmodified status taken from the locked task row).

The `switch` target MUST be a **terminal** version (not active). The API MUST assert this explicitly against the target version's active flag (it MUST NOT rely on the non-active task precondition to imply target terminality); switching to a non-terminal version MUST return HTTP `409` with `code = "invalid_state"`.

Because `switch` deliberately does not write `tasks.status`, `tasks.status` (the last execution outcome) and the status of the version now pointed at by `current_version` MAY legitimately diverge — e.g. `tasks.status = "failed"` while `current_version` points at a `succeeded` version. This is intended: readers MUST treat the divergence as valid, not a defect. The task list/dashboard badge, which exposes only `tasks.status`, therefore reflects the last execution outcome rather than the working base.

#### Scenario: Switch repoints the pointer without side effects
- **GIVEN** a non-active task and a `target_version_id` belonging to it
- **WHEN** the caller POSTs `mode = "switch"`
- **THEN** the response MUST be HTTP `200`, `tasks.current_version` MUST equal `target_version_id`, and NO new version row, NO new `task_runs` row, and NO outbox row MUST be written

#### Scenario: Switch does not write tasks.status
- **GIVEN** a task whose `status` is a terminal value
- **WHEN** the caller POSTs `mode = "switch"` to a different terminal version
- **THEN** `tasks.status` MUST be unchanged by the operation (only `current_version` and `updated_at` change), and `tasks.status` MAY differ from the now-current version's status

#### Scenario: Switch to a non-terminal version is rejected
- **WHEN** the caller POSTs `mode = "switch"` with a `target_version_id` whose version is active (non-terminal)
- **THEN** the response MUST be HTTP `409` with `code = "invalid_state"` and `current_version` MUST be unchanged

### Requirement: Non-Active Precondition

Both rollback modes SHALL require the task to have no active version. If any version of the task is active (`tasks.status` in an active state), the API MUST return HTTP `409` with `code = "active_version_exists"` and a `data` block carrying `{active_version_id, active_version_status}`. For `branch` this is the task-level mutex; for `switch` it is an explicit guard because moving `current_version` while a run's status-sync is gated on it would desync `tasks.status`.

#### Scenario: Rollback while a version is active is rejected
- **GIVEN** a task with an active (e.g. running) version
- **WHEN** the caller POSTs `mode = "branch"` OR `mode = "switch"`
- **THEN** the response MUST be HTTP `409` with `code = "active_version_exists"` and `data.active_version_id` / `data.active_version_status` populated, and no state MUST change

### Requirement: Owner Scoping and Target Validation

The endpoint SHALL resolve the caller's `tenant_id`/`user_id` from the authenticated principal and scope the task lookup to that owner. An unknown OR unowned `task_id` MUST return HTTP `404` with `code = "task_not_found"` (never `403`). A `target_version_id` that does not belong to the path task MUST return HTTP `404` with `code = "version_not_found"`.

#### Scenario: Unowned task is 404
- **WHEN** an authenticated caller POSTs rollback for a `task_id` owned by a different principal
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"`, never `403`

#### Scenario: Target version not in the task is 404
- **WHEN** the caller POSTs a `target_version_id` that exists under a different task or not at all
- **THEN** the response MUST be HTTP `404` with `code = "version_not_found"`
