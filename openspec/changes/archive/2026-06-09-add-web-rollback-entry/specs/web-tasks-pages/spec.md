## ADDED Requirements

### Requirement: Rollback Action With Mode Selection And UI Task-Level Mutex

The Task Detail page SHALL expose a per-version Rollback action on each non-current version row of the version tree that submits `POST /api/v1/tasks/{id}/rollback` via a mutation, supporting both modes:
- **`branch`** — re-execute from the target version (optional `prompt`; an empty prompt is valid and the backend auto-fills it). Success is `201` with `{version_id, version_no, status}`.
- **`switch`** — repoint `current_version` at the target only, with no run. Success is `200` with `{current_version_id, version_no, status}`.

While `task.status` is in an active state (`pending`, `running`, `paused`, `cancelling`) **both** rollback modes MUST be disabled with a reason indicating the task is busy — the backend requires a non-active task for both modes, not only branch. The `switch` option MUST additionally be disabled (advisory) on a target row whose `is_active` is true, because the backend rejects a switch to a non-terminal version. The current version row MUST NOT offer a rollback action.

The backend remains the source of truth. The rollback mutation MUST opt out of the global error toast and have the page handle outcomes inline (mirroring the Iterate and Control actions); it MUST NOT optimistically mutate `task.status`. A `409 active_version_exists` response MUST surface a message naming the active version (from `data.active_version_id` / `data.active_version_status`); a `409 invalid_state` (switch to a non-terminal target) MUST surface a warning message. Neither 409 MUST be retried. On settle (success or conflict) the mutation MUST invalidate the task + versions queries.

#### Scenario: Rollback disabled on all versions while task active

- **WHEN** the loaded task's status is `running` (or any active status)
- **THEN** no version row offers an enabled rollback action (branch or switch) and a reason is shown
- **AND** no rollback request can be sent from the UI

#### Scenario: Branch rollback from a terminal task

- **WHEN** the task's status is terminal and the user picks `branch` on a non-current version row
- **THEN** the page issues `POST /api/v1/tasks/{id}/rollback` with `{target_version_id, mode:"branch"}` (plus the optional prompt when provided)
- **AND** on the `201` success it invalidates the task + versions queries

#### Scenario: Branch with empty prompt is accepted

- **WHEN** the user picks `branch` on a non-current version row and leaves the prompt empty
- **THEN** the request is still sent with `mode:"branch"` and the empty prompt, and the `201` is handled normally (the backend auto-fills the prompt)

#### Scenario: Switch rollback repoints the current version

- **WHEN** the task's status is terminal and the user picks `switch` on a terminal non-current version row
- **THEN** the page issues `POST /api/v1/tasks/{id}/rollback` with `{target_version_id, mode:"switch"}`
- **AND** on the `200` success it invalidates the task + versions queries so the "current" marker moves to the target on refetch

#### Scenario: Switch disabled on a non-terminal target

- **WHEN** a non-current version row has `is_active` true
- **THEN** that row's `switch` option is disabled with a reason, while `branch` remains governed only by the task-level mutex

#### Scenario: 409 active_version_exists is surfaced, not retried

- **WHEN** a rollback submission races the backend and receives `409 active_version_exists`
- **THEN** a message naming the active version is shown, the task + versions queries are refetched, and the request is not retried

#### Scenario: 409 invalid_state on switch is surfaced as a warning

- **WHEN** a `switch` submission races and the target is no longer terminal, returning `409 invalid_state`
- **THEN** a warning message is shown, the queries are refetched, and the request is not retried
