# task-event-ingest Specification

## Purpose
TBD - created by archiving change add-event-ingest-status-sync. Update Purpose after archive.
## Requirements
### Requirement: Consume the task events stream

The API SHALL run a consumer subscribed to the `q.task.events` queue (bound `event.#` on the `task.events` topic exchange). The consumer SHALL decode each message body as the worker `TaskEvent` envelope: `{task_id, version_id, run_id, seq, kind, payload, ts}`. Multiple API replicas MAY each run a consumer competing on the shared queue; correctness MUST NOT depend on a single consumer instance.

#### Scenario: Consumer subscribes at startup

- **WHEN** the API process starts and the RabbitMQ connection is established
- **THEN** a consumer is registered on `q.task.events` with a bounded prefetch
- **AND** a consumer-connected gauge reports `1`

#### Scenario: Malformed message is dead-lettered, not requeued

- **WHEN** a delivery body cannot be decoded into the `TaskEvent` envelope (invalid JSON or missing required field)
- **THEN** the consumer rejects the message without requeue (`nack`, requeue=false) so it routes to the DLQ
- **AND** a malformed/DLQ counter is incremented
- **AND** the consumer continues processing subsequent deliveries

#### Scenario: Transient processing failure is requeued

- **WHEN** decoding succeeds but the DB transaction fails with a transient error (connection loss, serialization failure, deadlock, or deadline exceeded)
- **THEN** the consumer nacks with requeue=true so the message is redelivered
- **AND** no partial state is committed (the event row and the status transition share one transaction)

#### Scenario: Permanent processing failure is dead-lettered, not requeued

- **WHEN** decoding succeeds but the DB transaction fails with a non-retryable error (e.g. a constraint violation, SQLSTATE class 23)
- **THEN** the consumer rejects the message without requeue so it routes to the DLQ and does not loop
- **AND** an unclassifiable error defaults to non-retryable (DLQ) rather than infinite requeue

### Requirement: Idempotent event persistence

For every successfully decoded event, the API SHALL insert a row into `task_events` keyed on `(run_id, seq)`, dropping duplicates silently. Persistence and any state-machine transition triggered by the event MUST occur in a single DB transaction, and the message MUST be acked only after that transaction commits.

#### Scenario: First delivery persists the event

- **WHEN** an event with a `(run_id, seq)` not seen before is processed
- **THEN** a new `task_events` row is inserted with the envelope's `task_id`, `version_id`, `run_id`, `seq`, `kind`, and `payload`
- **AND** the message is acked after the transaction commits

#### Scenario: Redelivered event is a no-op

- **WHEN** an event with a `(run_id, seq)` that already exists is processed
- **THEN** no duplicate `task_events` row is created (insert is `ON CONFLICT (run_id, seq) DO NOTHING`)
- **AND** the message is acked

#### Scenario: Ack only follows commit

- **WHEN** the transaction that inserts the event and applies its transition cannot commit
- **THEN** the message is not acked
- **AND** the event is eligible for redelivery

### Requirement: Status events drive the version and task state machine

A `kind=status` event whose `payload.status` is a recognised lifecycle status SHALL transition `task_versions.status` for the event's `version_id` to the mapped status, applied through a Domain Service state-machine method rather than an ad-hoc UPDATE. When the event's `version_id` equals the owning task's `current_version`, the API SHALL also transition `tasks.status` to the mapped task status in the same transaction. The version status is mapped 1:1 (`running`→`running`, `succeeded`→`succeeded`, `failed`→`failed`, `queued`→`queued`); the task status uses the task lifecycle set (version `queued`→task `pending`, otherwise 1:1).

#### Scenario: Running event marks version and task running

- **WHEN** a `kind=status` event with `payload.status="running"` arrives for the task's current version
- **THEN** that `task_versions.status` becomes `running`
- **AND** the owning `tasks.status` becomes `running`

#### Scenario: Succeeded event marks version and task succeeded

- **WHEN** a `kind=status` event with `payload.status="succeeded"` arrives for the task's current version
- **THEN** that `task_versions.status` becomes `succeeded`
- **AND** the owning `tasks.status` becomes `succeeded`
- **AND** the version leaves the active set: the generated `is_active` column flips to false automatically and the `one_active_version_per_task` slot frees (a side effect of setting `status`, not a write the consumer makes)

#### Scenario: Event for a non-current version does not move the task

- **WHEN** a `kind=status` event arrives for a `version_id` that is not the owning task's `current_version`
- **THEN** that `task_versions.status` is updated per the mapping and guard
- **AND** `tasks.status` is left unchanged

#### Scenario: Unrecognised status payload is persisted but does not transition

- **WHEN** a `kind=status` event carries a `payload.status` that is not a known lifecycle status
- **THEN** the event row is still persisted
- **AND** no version or task status change is applied

### Requirement: Error events are treated as failure transitions

A `kind=error` event SHALL transition the event's `version_id` to `failed` (and the owning task to `failed` when that version is `current_version`), using the same state-machine method and terminal guard as a `status` event. This compensates for the worker error path, which emits `kind=error` without a trailing `status:failed`.

#### Scenario: Error event fails the version and task

- **WHEN** a `kind=error` event arrives for the task's current version that is not yet terminal
- **THEN** that `task_versions.status` becomes `failed`
- **AND** the owning `tasks.status` becomes `failed`
- **AND** the error `payload` (e.g. `{code, message}`) is persisted in the `task_events` row

### Requirement: Terminal states are never overwritten

The API SHALL apply every status/error transition with a monotonic guard so that a version or task already in a terminal state (`succeeded`, `failed`, `cancelled`) is never moved by a later or out-of-order event. The guard MUST be enforced in the SQL `WHERE` clause (compare-and-set), not only in application code, so concurrent or duplicated deliveries cannot regress a terminal row.

#### Scenario: Late running event after success is ignored

- **WHEN** a `kind=status` `running` event arrives for a version already in `succeeded`
- **THEN** the `task_versions.status` remains `succeeded`
- **AND** the owning `tasks.status` is unchanged
- **AND** the event row is still persisted (audit trail preserved)

#### Scenario: Duplicate terminal event is a no-op for state

- **WHEN** a terminal event (`succeeded`/`failed`) is redelivered for an already-terminal version
- **THEN** the status row is unchanged and the message is acked

### Requirement: Observability for ingestion

The API SHALL expose metrics covering ingestion volume and state transitions: an events-ingested counter labelled by `kind`, a status-transition counter, and a malformed/DLQ counter, plus the consumer-connected gauge. Each processed event SHALL be logged with structured fields including `task_id`, `version_id`, `run_id`, `seq`, and `kind`.

#### Scenario: Ingest increments counters

- **WHEN** a status event is successfully persisted and applied
- **THEN** the events-ingested counter for `kind="status"` increases
- **AND** the status-transition counter increases when a transition was applied

#### Scenario: Structured log per event

- **WHEN** an event is processed
- **THEN** a structured log line is emitted carrying `task_id`, `version_id`, `run_id`, `seq`, and `kind`
