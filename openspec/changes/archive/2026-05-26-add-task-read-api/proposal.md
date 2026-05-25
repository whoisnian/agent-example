## Why

`add-task-create-api` gave clients a way to *write* tasks and versions, but there is still no way to *read* them back: the web `TaskList`, `TaskDetail`, and `VersionTree` pages (architecture §3) have no data source. This change adds the owner-scoped read side so the frontend can list tasks, open a task, walk the version tree, and replay an execution's event stream after a disconnect — without exposing any cross-tenant data.

## What Changes

- Add five read-only endpoints under `/api/v1` (all owner-scoped, all returning the unified `{code, message, data, trace_id}` envelope):
  - `GET /tasks` — paginated, status-filterable list; each row carries a cost summary.
  - `GET /tasks/{task_id}` — task detail: task fields + current-version summary + cost summary.
  - `GET /tasks/{task_id}/versions` — the version tree (flat array with `parent_id`), each node carrying a cost summary.
  - `GET /versions/{version_id}` — version detail: version fields + its runs + cost summary.
  - `GET /versions/{version_id}/events?after_id=&limit=` — event-stream backfill for WS gap-fill / disconnect recovery.
- Embed a **cost summary** (amount_usd / token counts / wall_time) in list, detail, and version nodes, sourced from the existing `task_costs` table. Values are naturally `0` until the Cost Service is built; the dedicated cost *breakdown* endpoints (`/tasks/{id}/cost`, `/versions/{id}/cost`, `/me/cost`) remain out of scope and belong to `add-task-cost-api`.
- All reads enforce ownership against the dev tenant/user identity (`DevTenantID`/`DevUserID`, same as the write endpoints) and return `404` — never `403` — for rows the caller does not own, so existence is not leaked.
- Add five new sqlc queries (no schema migration): `CountTasks`, `ListTaskCostsByTasks`, `ListVersionCostsByTask`, `ListRunsByVersion`, `ListVersionEventsAfter`. The events query is scoped by `(task_id, version_id)` so it rides the existing `task_events (task_id, id)` index — no new index required.

## Capabilities

### New Capabilities
- `task-read-api`: the owner-scoped HTTP read surface for tasks, versions, runs, and event backfill, including the embedded cost summary contract and the 404-on-not-owned ownership rule.

### Modified Capabilities
<!-- None. task-write-api and task-data-model requirements are unchanged; no new
     migration, no new index, no public write-contract change. -->

## Impact

- **api/queries/**: append `CountTasks` (tasks.sql), `ListTaskCostsByTasks` + `ListVersionCostsByTask` (task_costs.sql), `ListRunsByVersion` (task_runs.sql), `ListVersionEventsAfter` (task_events.sql); regenerate sqlc.
- **api/internal/domain/task/**: a read service (queries-only, no transactions, no Outbox) plus DTO assembly; reuse existing `Status`/ownership helpers.
- **api/internal/application/task/**: read queries (list / get-task / list-versions / get-version / list-events).
- **api/internal/interfaces/http/**: new `task_reads.go` handlers + DTOs + pagination parsing + cursor parsing; register five GET routes alongside the existing write routes.
- **api/internal/infrastructure/observability/**: optional read-path counters (kept minimal; reads are not state transitions).
- **No** changes to the DB schema, the Outbox path, the write endpoints, or the worker.
- Unblocks `add-web-tasks-pages` (frontend) and partially de-risks `add-task-cost-api` (shares the `task_costs` read shape).
