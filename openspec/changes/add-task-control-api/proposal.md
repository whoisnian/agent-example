## Why

Users can submit tasks (`add-task-create-api`), iterate (`add-task-create-api` iterate flow), and now see cost (`add-task-cost-api`) — but they cannot stop a running task. There is no way to pause for human review, resume, or cancel a runaway agent. The MVP demo loop is unsafe to show real users without a stop button, and the existing topology (`ExchangeControl`, `q.task.control.<worker_id>`) was wired by `add-task-domain-schema` but nothing publishes to it. This change adds the API surface so a user can request pause/resume/cancel — establishing the outbox-driven control pattern that the upcoming `add-worker-control-handling` change will consume on the worker side.

## What Changes

- Add `POST /api/v1/tasks/{task_id}/control` accepting `{action ∈ pause|resume|cancel, reason?}` (`reason` capped at 200 chars to match the existing `title` validation); responds **HTTP 202** with `{accepted, action, task_id, effective}` where `effective ∈ {queued, best_effort}` distinguishes "active run exists so the worker will see this" from "pre-claim, the broker may drop it". Control is asynchronous — the API never flips task/version state; the worker does, via the event stream feeding `task-event-ingest`.
- The API writes a single `outbox` row per request inside a tx that also locks the task and validates ownership + current-state preconditions:
  - **pause** is rejected with `409 invalid_state` unless the task is in `pending` / `running` (note: `queued` is a version-only status; `tasks.status` cannot carry it).
  - **resume** is rejected with `409 invalid_state` unless the task is in `paused`.
  - **cancel** is rejected with `409 invalid_state` if the task is already in a terminal state (`cancelled` / `succeeded` / `failed`).
- New migration `0006_outbox_exchange.{up,down}.sql` adds an `exchange TEXT NOT NULL DEFAULT 'task.exchange'` column to `outbox` so the relayer can route each row to its appropriate exchange. Existing rows backfill to the default; new control rows write `'task.control'`.
- Modify the outbox **Relayer** so per-row `Exchange` overrides the per-instance default exchange. The existing event-ingest path is unaffected (rows continue to default to `task.exchange`).
- Change the `task.control` exchange from `direct` to `topic` so a future worker subscription can wildcard on `task.<task_id>` without requiring the API to know a `worker_id`. Routing-key convention for this change: `task.<task_id>`. Note: a worker subscription model lands in `add-worker-control-handling`; this change just establishes the API-side publisher contract.
- New sqlc queries: `LockTaskForControl :one` (mirror of `LockTaskRow` but returns the task's current `status`); `GetActiveRunIDForTask :one` (resolves the latest `task_runs.id` for the task's current version so the control payload can carry it — nullable if no run has been claimed yet).
- New error subcode `invalid_state` for the 409 path (the existing `MapError` does not currently emit it).
- Add metrics: `task_control_requests_total{action, outcome}` where `outcome ∈ {accepted, conflict, not_found, invalid}`.
- Spec changes:
  - **New** capability `task-control-api` for the endpoint, body shape, state guards, owner-scoped 404, outbox contract, and 202 semantics.
  - **Modify** `api-messaging` so the spec binds "per-row `exchange` overrides the relayer default"; also retypes `task.control` from `direct` to `topic`.
  - **Modify** `api-persistence` so the spec binds the new `outbox.exchange` column.

## Capabilities

### New Capabilities

- `task-control-api`: HTTP endpoint that queues control intents (`pause` / `resume` / `cancel`) to RabbitMQ via the outbox, with state-machine preconditions and owner-scoped 404.

### Modified Capabilities

- `api-messaging`: the Outbox Relayer publishes each row to the exchange named on that row, not a fixed exchange. The `task.control` exchange is `topic` (not `direct`) so workers can wildcard-subscribe by task_id.
- `api-persistence`: the `outbox` table carries an `exchange` column (default `task.exchange`).

## Impact

- New code: `api/internal/application/task/control.go` (command + invariants), `api/internal/domain/task/control_service.go` (transition guards + outbox write inside one tx), `api/internal/interfaces/http/task_control.go` (the HTTP handler).
- New migration: `api/migrations/0006_outbox_exchange.{up,down}.sql`.
- Modify: `api/queries/outbox.sql` (add `exchange` to `InsertOutbox`), `api/queries/tasks.sql` (add `LockTaskForControl`), `api/queries/task_runs.sql` (add `GetActiveRunIDForTask`), `api/internal/infrastructure/messaging/outbox_relayer.go` (read `row.Exchange`), `api/internal/infrastructure/messaging/topology.go` (`task.control` → topic), `api/internal/infrastructure/observability/metrics.go` (new metric), `api/internal/interfaces/http/errors.go` (`invalid_state` subcode + 409 mapping), `api/internal/interfaces/http/server.go` + `cmd/api/main.go` (wire `TaskControlHandlers`).
- No new external dependencies. Worker side is out of scope (`add-worker-control-handling`).
- Unblocks `add-task-rollback-api` (rollback in branch mode reuses the state-machine + outbox pattern this change establishes).
- The `task.control` exchange-type change requires the broker's existing exchange to be deleted + re-declared on the next `DeclareTopology` call. In MVP this is acceptable because no worker is currently bound; documented in the design's Migration Plan.
