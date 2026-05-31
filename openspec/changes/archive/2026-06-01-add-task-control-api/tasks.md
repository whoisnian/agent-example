## 1. Migration

- [x] 1.1 Add `api/migrations/0006_outbox_exchange.up.sql`: `ALTER TABLE outbox ADD COLUMN IF NOT EXISTS exchange TEXT NOT NULL DEFAULT 'task.exchange';` Header explains the per-row routing motivation; `IF NOT EXISTS` makes a re-up after rollback a no-op (S6).
- [x] 1.2 Add `api/migrations/0006_outbox_exchange.down.sql` as a **no-op** (e.g., a single SQL comment): forward-only schema evolution per reviewer S6 — dropping the column after `task.control` rows have been written would silently re-route them on re-up. The header MUST explain the trade-off.
- [x] 1.3 Extend `migrations_integration_test.go` to assert: (a) post-up the `exchange` column exists with `'task.exchange'` default; (b) `down` is a no-op — the column survives and `schema_migrations.version` decrements; (c) up→down→up sequence leaves `schema_migrations.dirty=false` and the column present with no row values changed.

## 2. SQL & sqlc surface

- [x] 2.1 Update `api/queries/outbox.sql` `InsertOutbox` to accept and bind a new `exchange` parameter (5 params total: `aggregate, aggregate_id, topic, payload, exchange`). The only existing caller is `createActiveVersion` in `api/internal/domain/task/service.go` (used by both task-create and task-iterate paths); update it to pass `'task.exchange'` explicitly (S4 — event-ingest never calls `InsertOutbox`).
- [x] 2.2 Add `LockTaskForControl :one` to `api/queries/tasks.sql` — `SELECT id, status, current_version FROM tasks WHERE id=$1 AND tenant_id=$2 AND user_id=$3 FOR UPDATE`. Owner predicate inline; unknown / unowned returns no rows.
- [x] 2.3 Add `GetActiveRunIDForTask :one` to `api/queries/task_runs.sql` — `SELECT r.id FROM task_runs r JOIN tasks t ON t.current_version = r.version_id WHERE t.id = $1 ORDER BY r.attempt_no DESC LIMIT 1`. Returns no rows when there is no active run (pre-claim or current_version is NULL).
- [x] 2.4 Run `make sqlc` and commit regenerated `outbox.sql.go`, `tasks.sql.go`, `task_runs.sql.go`, `querier.go`. Inspect `InsertOutboxParams` to confirm `Exchange string` is added; update every existing caller in one sweep so the project still builds.

## 3. Relayer + topology

- [x] 3.1 Update `api/internal/infrastructure/messaging/outbox_relayer.go`: drop the `exchange` field on `Relayer` (and the `cfg.Exchange` plumbing) so `publishRow` reads `row.Exchange` instead of a constant. Adjust `NewRelayer` signature accordingly.
- [x] 3.2 **Hand-edit** `api/internal/infrastructure/persistence/outbox.go` (this file is the relayer's batched-scan path and is the documented direct-pgx exception — NOT sqlc-generated, reviewer S3): (a) add `Exchange string` to the `OutboxRow` struct; (b) add `exchange` to the SELECT list in `ScanPending`'s SQL; (c) extend the corresponding `rows.Scan(...)` call to bind it. Without all three edits the relayer compiles but `row.Exchange` is the empty string and `Publish(ctx, "", row.Topic, env)` silently drops control messages to the default exchange.
- [x] 3.3 Update `api/internal/infrastructure/messaging/topology.go`: change `task.control` declaration type from `direct` to `topic`. Add a `retypableExchanges` list (today: just `"task.control"`) and pre-delete each before the declare loop runs (idempotent — delete-then-declare a fresh exchange of the new type). Document the carve-out inline.
- [x] 3.4 Update relayer + topology tests for the new shapes. Existing `outbox_relayer_test.go` cases get an explicit `Exchange: "task.exchange"` value on the test rows; topology tests assert `task.control` is `topic` after declaration.
- [x] 3.5 Update `cmd/api/main.go` Relayer construction to drop the (now-absent) exchange config parameter.

## 4. Domain & application layers

- [x] 4.1 Add `api/internal/domain/task/errors.go` sentinel: `var ErrInvalidState = errors.New("invalid state")`. Document that the wrapped message MUST include the current status verbatim so the HTTP layer can pass it through.
- [x] 4.2 Add `api/internal/domain/task/control_service.go` with `ControlService{Pool, Queries, Clock}` and one method `Apply(ctx, owner, taskID, action, reason) (outboxID int64, version, run *uuid.UUID, err error)`. Opens a tx, calls `LockTaskForControl` (owner predicate; `pgx.ErrNoRows` → `ErrTaskNotFound`), validates the action vs `tasks.status` per design D6 (`ErrInvalidState` with the status in the message), resolves `run_id` via `GetActiveRunIDForTask` (NULL when no active run), builds the payload JSON per spec §"Outbox Payload Shape", `InsertOutbox` with `aggregate="task"`, `aggregate_id=taskID`, `topic="task." + taskID`, `exchange="task.control"`, and commits. Returns the outbox id + the resolved version/run ids.
- [x] 4.3 Add `api/internal/application/task/control.go` exposing `ControlService.Apply(ctx, tenantID, userID, taskID, action, reason)`. Folds bare ids into `domain.Owner`; same idiom as `application/task.ReadService`.
- [x] 4.4 Unit-test `ControlService.Apply` against a fake `Queries` + a real / faked `Pool`: each action × current-status matrix (accepted vs ErrInvalidState), unknown task → ErrTaskNotFound, payload shape (run_id null vs populated), outbox row exchange == "task.control", topic == "task.{id}".

## 5. HTTP layer

- [x] 5.1 Add `api/internal/interfaces/http/errors.go` mapping: `errors.Is(err, ErrInvalidState)` → `(http.StatusConflict, "invalid_state", err.Error())`. Add a sentinel scenario to the existing error-mapping test.
- [x] 5.2 Add `api/internal/interfaces/http/task_control.go` with `TaskControlHandlers{App, Logger, Metrics, DevTenantID, DevUserID}` and `Register(r *gin.RouterGroup)` mounting `POST /tasks/:task_id/control`. Parse path UUID, decode body, validate (action set, reason length ≤ 200, JSON shape) → 400 with `data.field` naming the offender; dispatch to the app layer; on `ErrInvalidState` render 409 with `code="invalid_state"` and the current status in `message`; on `ErrTaskNotFound` render 404 `task_not_found`; on success render 202 with `{accepted, action, task_id, effective}` where `effective = "queued"` if the app layer returned a non-nil `run_id`, else `"best_effort"` (S9).
- [x] 5.3 Wire `TaskControlHandlers` into `ServerDeps` + `NewEngine` (same pattern as `TaskCostHandlers`). `cmd/api/main.go` constructs the app/domain ControlService and passes the handler struct in.

## 6. Observability

- [x] 6.1 Add to `observability/metrics.go`: `TaskControlRequestsTotal` (CounterVec by `action, outcome`). Labels are `action ∈ {pause, resume, cancel, unknown}` × `outcome ∈ {accepted, conflict, not_found, invalid}`; the `unknown` action value is reserved for the unparseable-action case (see S15). Register in the `NewMetrics` constructor.
- [x] 6.2 Plumb metric increments through the handler — including the `outcome="invalid"` path for malformed bodies. When the request body's `action` field is missing or not in the accepted set, emit `action="unknown"` (the only `unknown` legal pairing is with `outcome="invalid"`).

## 7. Tests

- [x] 7.1 Handler-level unit tests at `api/internal/interfaces/http/task_control_test.go` covering: 202 on accepted pause/resume/cancel with `effective` discriminator; 400 on missing action / invalid action / overflowing reason (201 chars) / malformed UUID; 404 path via fake service; 409 path via fake `ErrInvalidState` (assert `message` carries the current status verbatim); metric labels asserted per outcome — including `{action="unknown", outcome="invalid"}` for the unparseable-action case (S15).
- [x] 7.2 Integration tests at `api/internal/interfaces/http/task_control_integration_test.go` (`//go:build integration`) extending the existing suite: end-to-end POST → DB outbox row exists with the expected `exchange`/`topic`/`payload` shape; `effective="queued"` when an active run exists, `"best_effort"` when no run is claimed; state-machine 409 per action × status matrix (per spec scenarios — pause-when-paused, resume-when-running, cancel-when-terminal); pre-claim cancel writes outbox with `run_id=null`; owner-isolation 404; duplicate-control: two `pause`s in a row produce two outbox rows (S11); concurrent control requests serialise via the `FOR UPDATE` lock — two cancels in parallel both return 202 and both rows carry identical `run_id` (S14).

## 8. Documentation

- [x] 8.1 Update `api/README.md` adding `## 任务控制端点（task-control-api）`: endpoint, body shape (`action` ∈ {pause, resume, cancel}, optional `reason` ≤ 200 chars), 202 with `effective ∈ {queued, best_effort}` vs 409 (`invalid_state`, current status echoed in `message`) vs 404 (`task_not_found`), idempotency note (worker dedupes; API does not — duplicate pauses produce duplicate outbox rows), the asynchronous-state contract (API writes outbox only; `tasks.status` is event-ingest's job). Link to the spec.
- [x] 8.2 Add a one-line pointer in `docs/ARCHITECTURE.md §5.1` next to the `/control` endpoint row noting it's implemented under capability `task-control-api`.
