## ADDED Requirements

### Requirement: Artifact-deleted events are persisted without a state transition

A `kind="artifact_deleted"` task event SHALL be ingested like any other event — a `task_events` row inserted with the envelope's `task_id`, `version_id`, `run_id`, `seq`, `kind`, and `payload`, idempotent on `(run_id, seq)` — and SHALL trigger **no** business-table state transition. The Worker has already removed the corresponding `artifacts` row, so the API MUST NOT attempt to delete or mutate any `artifacts`, `task_versions`, or `tasks` row in response to this event. The event MUST be counted under the events-ingested metric labelled by `kind` (`kind="artifact_deleted"`) and logged with the standard structured fields (`task_id`, `version_id`, `run_id`, `seq`, `kind`). An unrecognised or malformed `payload` MUST NOT fail ingestion beyond the existing malformed-message policy — a well-formed envelope with `kind="artifact_deleted"` is always persisted.

#### Scenario: Artifact-deleted event is persisted and transitions nothing

- **WHEN** a `kind="artifact_deleted"` event with payload `{path: "styles.css", version_id}` arrives for a version
- **THEN** a `task_events` row MUST be inserted for it and NO `artifacts`, `task_versions`, or `tasks` row MUST be changed as a result

#### Scenario: Redelivered artifact-deleted event is a no-op

- **WHEN** an `artifact_deleted` event whose `(run_id, seq)` already exists is processed again
- **THEN** ingestion MUST be a no-op for state and MUST NOT insert a duplicate `task_events` row

#### Scenario: Artifact-deleted ingestion is observable

- **WHEN** an `artifact_deleted` event is ingested
- **THEN** the events-ingested counter for `kind="artifact_deleted"` MUST increase and a structured log line carrying `task_id`, `version_id`, `run_id`, `seq`, and `kind` MUST be emitted
