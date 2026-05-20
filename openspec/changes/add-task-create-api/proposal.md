# Proposal — add-task-create-api

## Why

The `task-data-model` schema is in place but no HTTP endpoint creates active versions yet, so neither the web frontend nor the worker has an end-to-end trigger to exercise the platform. This change lands the first two write endpoints — `POST /api/v1/tasks` and `POST /api/v1/tasks/{task_id}/iterate` — together because they share one Domain Service flow, one transaction shape, and one 409 envelope. Shipping them together is the smallest unit that produces a real execute message on the MQ, which in turn unblocks the worker dispatcher and the web TaskCreate page.

## What Changes

- Add HTTP route `POST /api/v1/tasks` that, in a single PostgreSQL transaction, inserts a row into `tasks` (status `pending`, `current_version=v1.id`), a row into `task_versions` (parent NULL, version_no 1, status `pending`), a row into `task_runs` (attempt_no 1, status `queued`), and a row into `outbox` whose topic encodes `execute.<task_type>.<lane>`. Returns `201` with `{task_id, version_id, version_no, status}` inside the unified envelope.
- Add HTTP route `POST /api/v1/tasks/{task_id}/iterate` that resolves the base version (request `base_version_id`, else `tasks.current_version`), inserts a new `task_versions` row whose `parent_id` equals the base and whose `status` is `pending`, inserts a new `task_runs` row, inserts the matching `outbox` row, and updates `tasks.current_version` to the new version — all in one transaction. Returns `201` with `{version_id, version_no, status}`.
- Translate the `one_active_version_per_task` unique-violation (SQLSTATE `23505`, constraint name `one_active_version_per_task`) into a `409 Conflict` envelope with `code = "active_version_exists"` and `data = { active_version_id, active_version_status }`. The application MAY do an upfront `SELECT … FOR UPDATE` on the `tasks` row to short-circuit with a friendlier message, but the DB index remains the source of truth.
- Introduce a new sqlc query `InsertOutbox` (under `api/queries/outbox.sql`) so the create / iterate transactions can write the outbox row through the typed query layer rather than hand-rolled `pgx`.
- Introduce a new Domain Service package `internal/domain/task/` exposing `CreateTask(ctx, CreateInput) (CreateOutput, error)` and `IterateTask(ctx, taskID, IterateInput) (IterateOutput, error)`. The service owns ID generation (UUIDv7), `idempotency_key` derivation (`run_id` UUID literal per architecture §5.3), and execute-message payload assembly. Handlers stay thin: parse → call service → render.
- Add the corresponding application use cases under `internal/application/task/` and HTTP handlers under `internal/interfaces/http/tasks.go`, wired into the existing Gin router started by `api-bootstrap`.
- Add new business error codes `invalid_input`, `task_not_found`, `version_not_found`, and `active_version_exists` to the error catalog and map them to HTTP `400 / 404 / 404 / 409` respectively. All other write failures fall back to `internal_error` per the `api-bootstrap` envelope contract.
- Add contract-level tests (testcontainers-postgres + httptest) covering: happy path for create; happy path for iterate; 409 race for two concurrent iterate calls against the same `task_id`; 404 for missing `task_id` / `base_version_id`; 400 for missing required fields.

## Capabilities

### New Capabilities
- `task-write-api`: HTTP write endpoints that create new active versions of a task. Owns the request / response contract, the create-and-iterate transactional flow, the SQLSTATE-to-`409` translation, and the execute outbox payload shape. Scope is strictly endpoints that produce new active versions; control signals and rollback live in their own future capabilities.

### Modified Capabilities
- (none — `task-data-model` columns and indexes are unchanged; `api-bootstrap` envelope shape is reused without modification; `api-messaging` Publisher / Outbox Relayer are reused; `api-persistence` sqlc rule gains one new query but the rule itself does not change)

## Impact

- **Code**
  - Append `InsertOutbox :one`, plus the iterate-flow lookups (`GetActiveVersionByTask`, `GetVersionByTaskAndID`, `LockTaskRow`, `MaxVersionNoForTask`, `UpdateTaskCurrentVersion`) to the existing query files under `api/queries/`. No new query file is created; counts and exact list are tracked in `tasks.md` Section 1.
  - `api/internal/infrastructure/persistence/sqlc/` — regenerated `*.sql.go` files for tasks / task_versions / outbox.
  - `api/internal/domain/task/` — new package: `errors.go`, `status.go`, `ports.go` (Clock / IDGenerator interfaces), `service.go`, payload builder, validation helpers.
  - `api/internal/application/task/` — new package wiring repos + outbox via the Domain Service.
  - `api/internal/interfaces/http/tasks.go` — two new handlers + DTOs.
  - `api/internal/interfaces/http/server.go` — register the two routes under `/api/v1`.
  - `api/internal/interfaces/http/errors.go` — extend with `active_version_exists`, `task_not_found`, `version_not_found`, `invalid_input`.
- **Public contract**
  - `POST /api/v1/tasks` — new.
  - `POST /api/v1/tasks/{task_id}/iterate` — new.
  - MQ routing key `execute.<task_type>.<lane>` — new (only the routing key & payload shape; per-lane queues are still declared lazily by workers per `api-messaging`).
  - `cost.exchange` / `task.events` exchanges — untouched.
- **Dependencies**: no new Go modules; uses `github.com/google/uuid` already pulled in. No DB schema changes.
- **Configuration**: introduce env `DEFAULT_LANE` (default `default`) used when `params.lane` is absent on create.
- **Observability**: emit metric counters `tasks_created_total{task_type}`, `tasks_iterated_total{outcome="success|conflict"}` and log fields `task_id`, `version_id`, `run_id` on both success and 409 paths.
