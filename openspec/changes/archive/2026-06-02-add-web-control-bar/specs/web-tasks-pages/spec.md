## ADDED Requirements

### Requirement: Task Control Bar With State-Machine-Aware Actions

The Task Detail page SHALL render a control bar with `Pause`, `Resume`, and `Cancel` actions that issue `POST /api/v1/tasks/{task_id}/control` with body `{action}` (`action ∈ {pause, resume, cancel}`). Each action's enabled state MUST mirror the `task-control-api` state-machine preconditions against the current `task.status`:

- `Pause` enabled only when `status ∈ {pending, running}`.
- `Resume` enabled only when `status = paused`.
- `Cancel` enabled only when `status ∉ {cancelled, succeeded, failed}` (i.e. not terminal).

A disabled action button MUST carry a human-readable reason (e.g. via `title`) explaining why it is unavailable. This client-side gating is advisory UX; the API remains authoritative, so a `409 invalid_state` returned for an action that the stale client view believed was allowed MUST still be handled (see below) rather than treated as impossible.

The control mutation MUST opt out of the global error toast and have the page handle outcomes inline (mirroring the Iterate Action). The success response is HTTP `202` and MUST NOT be treated as an immediate status change: the page MUST NOT optimistically mutate `task.status`. Instead, the new status arrives through the existing live pipeline — the mutation MUST invalidate the task and versions queries on settle, and the Live Observation pipeline reflects the worker-driven status change when its `status` event arrives.

While a control request is in flight, the page MUST disable all three actions to prevent accidental double submission (the API tolerates duplicates, so this is a UX guard, not a correctness requirement).

#### Scenario: Action enablement follows task status
- **WHEN** the Task Detail page renders a task in `running` status
- **THEN** `Pause` and `Cancel` MUST be enabled and `Resume` MUST be disabled; AND for a task in `paused` status, `Resume` and `Cancel` MUST be enabled and `Pause` disabled; AND for a task in a terminal status (`succeeded` / `failed` / `cancelled`) all three MUST be disabled

#### Scenario: Disabled action exposes a reason
- **WHEN** an action is disabled because the current status does not permit it (e.g. `Resume` on a `running` task)
- **THEN** the disabled button MUST expose a human-readable reason (e.g. a `title` attribute) explaining why it is unavailable

#### Scenario: Pause sends a control request and confirms
- **GIVEN** a Task Detail page for a task in `running` status
- **WHEN** the user clicks `Pause`
- **THEN** the page MUST `POST /api/v1/tasks/{id}/control` with `{action:"pause"}`, MUST show a confirmation toast, and MUST NOT change the displayed `status` until a subsequent status update (live frame or refetch) reports it

#### Scenario: Status updates through the live pipeline, not optimistically
- **GIVEN** a control request that returned `202`
- **WHEN** no status event has yet arrived
- **THEN** the displayed `task.status` MUST remain the pre-request value; AND when the task/versions queries are next invalidated (by the live frame or the mutation's settle) and refetched, the buttons MUST re-derive their enabled state from the new status

#### Scenario: 409 invalid_state is surfaced with the server message
- **GIVEN** a task whose status changed after the page rendered (stale view)
- **WHEN** the user clicks an action and the API returns `409` with `code = "invalid_state"` and a message naming the current status
- **THEN** the page MUST surface a warning carrying that server message, MUST NOT retry it, and MUST NOT leave the action stuck in a pending state

#### Scenario: Unexpected control error surfaces a generic toast, not a retry
- **WHEN** a control request fails with an `ApiError` other than `409 invalid_state` (e.g. an unexpected `400` / `500` / network error)
- **THEN** the page MUST surface a generic error toast, MUST NOT retry the request, and the action buttons MUST return to their status-derived enabled state once the request settles

#### Scenario: Best-effort cancel is flagged as possibly not-yet-effective
- **GIVEN** a task with no active run (pre-claim)
- **WHEN** the user clicks `Cancel` and the API returns `202` with `effective = "best_effort"`
- **THEN** the page MUST inform the user that the cancel may not take effect until the task is claimed

#### Scenario: In-flight control disables the actions
- **WHEN** a control request is in flight
- **THEN** all three action buttons MUST be disabled until the request settles
