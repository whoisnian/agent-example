# task-event-ingest Specification (Delta)

## ADDED Requirements

### Requirement: Summary events update the version summary

A `kind=summary` event SHALL update `task_versions.summary` for the event's `version_id` through a dedicated Domain Service method (`ApplyVersionSummary`), never via an ad-hoc UPDATE. The method MUST trim the event's `payload.summary`, truncate it on a rune boundary so that the final string — including the `…` appended when truncation occurs — is at most 2048 bytes, and silently skip the update when the sanitized value is empty or `payload.summary` is missing — the event row is still persisted in that case.

The summary update MUST occur in the same DB transaction as the `task_events` insert; when the `(run_id, seq)` insert is a duplicate no-op, the summary update MUST NOT be applied either. The update MUST NOT be guarded by task or version status: a summary event arriving after the version reached a terminal status still applies (the worker emits it at run end, racing the trailing status event). Summary events do not participate in the version/task state machine.

#### Scenario: Summary event updates the version summary
- **WHEN** a `kind=summary` event with `payload.summary="完成登录页与表单校验"` arrives for a version
- **THEN** the event row is persisted AND `task_versions.summary` for that `version_id` equals `完成登录页与表单校验`, both committed in one transaction

#### Scenario: Summary applies after terminal status
- **WHEN** a `kind=summary` event arrives for a version whose status is already `succeeded`
- **THEN** `task_versions.summary` is still updated AND the version status remains `succeeded`

#### Scenario: Redelivered summary event does not reapply
- **WHEN** a `kind=summary` event with a `(run_id, seq)` that already exists is processed
- **THEN** no duplicate `task_events` row is created AND `task_versions.summary` is left unchanged by this delivery

#### Scenario: Empty or missing payload summary is skipped
- **WHEN** a `kind=summary` event arrives whose `payload.summary` is absent, empty, or trims to empty
- **THEN** the event row is still persisted AND `task_versions.summary` is unchanged
