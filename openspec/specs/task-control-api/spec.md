# task-control-api Specification

## Purpose
TBD - created by archiving change add-task-control-api. Update Purpose after archive.

## Requirements

### Requirement: Control Endpoint

The API SHALL expose `POST /api/v1/tasks/{task_id}/control` accepting a JSON body `{action, reason?}` where `action ∈ {"pause", "resume", "cancel"}` and `reason` is an optional free-form string capped at 200 characters (matches the existing `task.title` validation cap). The response on success MUST be HTTP `202` with envelope `data = {accepted: true, action, task_id, effective}` where `effective ∈ {"queued", "best_effort"}` discriminates between "an active `task_runs` row was resolved so the worker will receive this" (`"queued"`) and "no active run, the broker may drop the message" (`"best_effort"`, i.e. pre-claim cancel). The 202 indicates the request was durably written to `outbox`; the actual state change happens asynchronously when the worker reads the control message and emits the corresponding status events through `task-event-ingest`. The 202 body MUST NOT include the outbox row's id — that's an internal detail.

The endpoint MUST be scoped to the caller's `(tenant_id, user_id)`. A `task_id` that does not exist OR belongs to a different owner MUST return HTTP `404` with envelope `code = "task_not_found"` — never `403`, never differentiate the two cases (mirrors `task-read-api`).

The endpoint MUST NOT directly update `tasks.status`, `task_versions.status`, or `task_runs.status`. The sole writer of those columns is `task-event-ingest`, driven by the worker's status-event stream after the worker acts on the control signal.

#### Scenario: Accepted control returns 202 and writes one outbox row
- **GIVEN** an owned task in `running` status with an active `task_runs` row
- **WHEN** the caller `POST /api/v1/tasks/{id}/control` with `{action: "pause"}`
- **THEN** the response MUST be HTTP `202` with `data.accepted = true`, `data.action = "pause"`, `data.task_id = {id}`, and `data.effective = "queued"` (active run resolved); AND exactly one new row in the `outbox` table MUST exist for that task with `exchange = "task.control"` and `topic = "task.{id}"`; AND the 202 body MUST NOT include any `outbox_id` field

#### Scenario: Reason is preserved in the outbox payload
- **WHEN** the caller `POST /api/v1/tasks/{id}/control` with `{action: "cancel", reason: "user changed mind"}`
- **THEN** the resulting `outbox.payload` MUST contain `"reason": "user changed mind"` verbatim

#### Scenario: Owned task with no active run accepts as best_effort
- **GIVEN** an owned task in `pending` status with NO `task_runs` row yet (queued in execute exchange, never claimed)
- **WHEN** the caller `POST /api/v1/tasks/{id}/control` with `{action: "cancel"}`
- **THEN** the response MUST be HTTP `202` with `data.effective = "best_effort"`, AND the outbox payload's `run_id` MUST be `null` (the API documented this is best-effort: with no worker bound, the broker may drop the message — see proposal Non-Goals)

#### Scenario: Duplicate accepted controls both produce outbox rows
- **GIVEN** an owned task in `running` status with an active run
- **WHEN** the caller `POST`s `{action: "pause"}` twice within the same `tasks.status='running'` window (e.g., user double-clicks)
- **THEN** both responses MUST be HTTP `202`, AND exactly two new outbox rows MUST exist; the API does NOT dedupe — the worker is responsible for in-flight deduplication on its in-memory pause flag

#### Scenario: Unowned or unknown task returns 404
- **GIVEN** a `task_id` that either does not exist OR belongs to a different owner
- **WHEN** the caller `POST /api/v1/tasks/{id}/control` with any valid action
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"`; no outbox row MUST be written

### Requirement: State-Machine Preconditions

The API SHALL reject control requests that don't make sense against the task's current `tasks.status`, with HTTP `409` envelope `code = "invalid_state"`. The preconditions:

- `pause` is accepted only when `tasks.status ∈ {pending, running}` — pausing a paused / terminal task is `409`.
- `resume` is accepted only when `tasks.status = paused` — resuming a non-paused task is `409`.
- `cancel` is accepted only when `tasks.status ∉ {cancelled, succeeded, failed}` — cancelling a terminal task is `409`.

The state read happens inside a `SELECT … FOR UPDATE` lock on the `tasks` row so concurrent control requests serialise. The state guards are advisory — they prevent obvious-mistake messages from flooding MQ — and are not a safety invariant; the worker is the authority on whether a control message applies at the moment of arrival.

A 409 response's `message` field MUST include the current `tasks.status` so the front-end can show an actionable error.

#### Scenario: Pause-when-paused returns 409
- **GIVEN** an owned task in `paused` status
- **WHEN** the caller `POST /api/v1/tasks/{id}/control` with `{action: "pause"}`
- **THEN** the response MUST be HTTP `409` with `code = "invalid_state"` and the message mentioning `"paused"`; no outbox row MUST be written

#### Scenario: Resume-when-running returns 409
- **GIVEN** an owned task in `running` status
- **WHEN** the caller `POST /api/v1/tasks/{id}/control` with `{action: "resume"}`
- **THEN** the response MUST be HTTP `409` with `code = "invalid_state"`; no outbox row MUST be written

#### Scenario: Cancel-terminal returns 409
- **GIVEN** an owned task in `succeeded` status
- **WHEN** the caller `POST /api/v1/tasks/{id}/control` with `{action: "cancel"}`
- **THEN** the response MUST be HTTP `409` with `code = "invalid_state"`; no outbox row MUST be written

#### Scenario: Concurrent cancel requests serialise on the task row
- **GIVEN** an owned task in `running` status
- **WHEN** two `POST /api/v1/tasks/{id}/control` with `{action: "cancel"}` arrive in parallel
- **THEN** both responses MUST be HTTP `202`, AND both outbox rows MUST exist; the second handler MUST have observed the task row only after the first's transaction committed (`SELECT ... FOR UPDATE` serialises them); both rows MUST carry identical `run_id` payloads (no race in resolving the current run)

### Requirement: Request Validation

The API SHALL validate request bodies before doing any DB work and return HTTP `400` with `code = "invalid_input"` naming the offending field on any of:

- `action` is absent or not one of `{"pause", "resume", "cancel"}`.
- `reason` exceeds 200 characters (after trimming trailing whitespace) — matches the existing `task.title` validation cap for consistency.
- The body is not valid JSON.
- The `{task_id}` path segment is not a valid UUID.

#### Scenario: Missing action returns 400
- **WHEN** the caller posts `{}` to the control endpoint
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` and `data.field = "action"`

#### Scenario: Invalid action returns 400
- **WHEN** the caller posts `{"action": "kill"}`
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` and `data.field = "action"`

#### Scenario: Reason overflow returns 400
- **WHEN** the caller posts `{"action": "cancel", "reason": "<201-char string>"}`
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` and `data.field = "reason"`

#### Scenario: Malformed task_id returns 400
- **WHEN** the `{task_id}` path segment is not a valid UUID
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` and `data.field = "task_id"`

### Requirement: Outbox Payload Shape

The control outbox row's `payload` SHALL be a JSON object containing exactly these fields:

- `task_id` (UUID string)
- `version_id` (UUID string, or `null` when the task has no `current_version`)
- `run_id` (UUID string, or `null` when no `task_runs` row exists for the current version)
- `action` (`"pause"` / `"resume"` / `"cancel"`)
- `reason` (string, possibly empty — never absent)
- `issued_at` (RFC3339 timestamp, API process clock)

`run_id` resolves to the **latest attempt's** `task_runs.id` for the task's `current_version`, ordered by `attempt_no DESC` — possibly a terminal run (e.g., `succeeded`). The API does NOT pre-filter by run status; the worker is the authoritative filter on "is this run currently running in my process". The relayer publishes this payload to exchange `task.control` with routing key `task.{task_id}`.

#### Scenario: Payload carries the active run_id
- **GIVEN** an owned task with `current_version = V` and a single `task_runs` row `(version_id = V, attempt_no = 1)` with `id = R`
- **WHEN** the caller `POST /api/v1/tasks/{id}/control` with `{action: "cancel"}`
- **THEN** the resulting `outbox.payload.run_id` MUST equal `R` and `outbox.payload.version_id` MUST equal `V`

#### Scenario: Payload run_id is null pre-claim
- **GIVEN** an owned task with `current_version = V` but no `task_runs` row yet
- **WHEN** the caller `POST /api/v1/tasks/{id}/control` with `{action: "cancel"}`
- **THEN** the resulting `outbox.payload.run_id` MUST be `null`; `version_id` MUST be `V`

### Requirement: Observability

The API SHALL emit `task_control_requests_total{action, outcome}` (Counter) where `action ∈ {pause, resume, cancel}` and `outcome ∈ {accepted, conflict, not_found, invalid}`. Every request MUST increment exactly one cell of this metric, including malformed requests (those increment `outcome="invalid"` with `action="unknown"` when the action couldn't be parsed).

Every handler call MUST log an INFO line containing `task_id`, `action`, `outcome`, `trace_id`, and (when accepted) `outbox_id`. 4xx logs at INFO; 5xx logs at ERROR with the error string.

#### Scenario: Accepted control bumps counters
- **WHEN** a `POST /api/v1/tasks/{id}/control` with `{action: "pause"}` returns 202
- **THEN** `task_control_requests_total{action="pause", outcome="accepted"}` MUST increment by 1

#### Scenario: Malformed action bumps the `unknown` action label
- **WHEN** a `POST /api/v1/tasks/{id}/control` with `{"action": "kill"}` returns 400
- **THEN** `task_control_requests_total{action="unknown", outcome="invalid"}` MUST increment by 1 (the `unknown` action label value is reserved for the unparseable-action case, paired only with `outcome="invalid"`)
