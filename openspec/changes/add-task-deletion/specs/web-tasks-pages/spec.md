## ADDED Requirements

### Requirement: Task Deletion Control

The web client SHALL provide a task deletion control on both the TaskList rows and the TaskDetail header, calling `DELETE /api/v1/tasks/{task_id}`. The control MUST carry stable `data-testid`s and MUST require an explicit confirmation step before issuing the delete (no single-click destructive action). For a task whose `status` is active (`isActiveStatus`), the control MUST be disabled with a reason (mirroring the iterate/rollback busy-disable), since the API rejects deleting an active task. On success the client MUST invalidate the `taskKeys.lists` prefix (so TaskList and the SideNav Recents refresh); a successful delete from TaskDetail MUST additionally navigate back to the task list. An `active_version_exists` error MUST surface a non-destructive warning (e.g. "cancel the active version first"); a `404` (already deleted) MUST be treated as success/no-op, not an error toast.

#### Scenario: Confirmed delete removes the task from the list

- **WHEN** the user activates the delete control on a non-active task row and confirms
- **THEN** the client MUST call `DELETE /api/v1/tasks/{id}`, and on success MUST invalidate the `taskKeys.lists` prefix so the task disappears from TaskList and Recents

#### Scenario: Delete is gated behind confirmation

- **WHEN** the user activates the delete control
- **THEN** a confirmation step MUST appear, and the `DELETE` request MUST NOT be sent until the user confirms

#### Scenario: Active task delete control is disabled with a reason

- **WHEN** a task is in an active status (`isActiveStatus`)
- **THEN** its delete control MUST be disabled and MUST expose the reason (e.g. a title/tooltip), consistent with the iterate/rollback busy-disable

#### Scenario: Deleting from detail navigates back to the list

- **WHEN** the user deletes the currently open task from the TaskDetail header and it succeeds
- **THEN** the client MUST navigate to the task list and the deleted task MUST NOT appear there

#### Scenario: Active-version conflict surfaces a non-destructive warning

- **WHEN** the delete returns HTTP `409 active_version_exists` (it became active between render and click)
- **THEN** the client MUST show a warning (cancel the active version first) and MUST NOT remove the task optimistically
