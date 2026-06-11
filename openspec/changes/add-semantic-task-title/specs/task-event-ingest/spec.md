## ADDED Requirements

### Requirement: Title events update the task title

A `kind=title` event SHALL update `tasks.title` for the event's `task_id` through a dedicated Domain Service method (`ApplyGeneratedTitle`), never via an ad-hoc UPDATE. The method MUST trim the event's `payload.title`, truncate it on a rune boundary so that the final string — including the `…` appended when truncation occurs — is at most 64 runes AND at most 200 bytes, and silently skip the update when the sanitized value is empty or `payload.title` is missing — the event row is still persisted in that case.

The title update MUST occur in the same DB transaction as the `task_events` insert; when the `(run_id, seq)` insert is a duplicate no-op, the title update MUST NOT be applied either. A title event arriving with a new `(run_id, seq)` — e.g. the worker regenerated after a fresh redelivery that crashed before its first checkpoint — re-applies normally (last-write-wins); the worker-side fresh-run guard makes this rare (see `worker-execution-runtime`). The update MUST NOT be guarded by task status: a title event arriving after the task reached a terminal status still applies (fast runs may finish before the title event is consumed). Title events do not participate in the version/task state machine.

#### Scenario: Title event updates the task title
- **WHEN** a `kind=title` event with `payload.title="重构用户认证模块"` arrives for a task
- **THEN** the event row is persisted AND `tasks.title` for that `task_id` equals `重构用户认证模块`, both committed in one transaction

#### Scenario: Title applies even after the task is terminal
- **WHEN** a `kind=title` event arrives for a task whose status is already `succeeded`
- **THEN** `tasks.title` is still updated AND the task status remains `succeeded`

#### Scenario: Redelivered title event does not reapply
- **WHEN** a `kind=title` event with a `(run_id, seq)` that already exists is processed
- **THEN** no duplicate `task_events` row is created AND `tasks.title` is left unchanged by this delivery

#### Scenario: Empty or missing payload title is skipped
- **WHEN** a `kind=title` event arrives whose `payload.title` is absent, empty, or trims to empty
- **THEN** the event row is still persisted AND `tasks.title` is unchanged

#### Scenario: Oversized payload title is truncated
- **WHEN** a `kind=title` event arrives whose `payload.title` exceeds 64 runes or 200 bytes
- **THEN** the persisted `tasks.title` is truncated on a rune boundary with a trailing `…` such that the final string including the `…` is within 64 runes and 200 bytes, satisfying the application-level title rule (`tasks.title` itself is `TEXT NOT NULL` with no length constraint)
