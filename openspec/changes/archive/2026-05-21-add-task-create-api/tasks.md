# Tasks — add-task-create-api

## 1. Query layer (sqlc)

- [x] 1.1 Append `InsertOutbox :one` to existing `api/queries/outbox.sql` returning the inserted row (id + status + created_at), with parameters for `aggregate`, `aggregate_id`, `topic`, `payload`
- [x] 1.2 Add a `GetActiveVersionByTask` query to `api/queries/task_versions.sql` returning at most one row where `task_id = $1 AND is_active`, ordered by `version_no DESC LIMIT 1`
- [x] 1.3 Add `GetVersionByTaskAndID` query to `api/queries/task_versions.sql`: `SELECT * FROM task_versions WHERE id = $1 AND task_id = $2` — used by iterate to validate that `base_version_id` belongs to the path `task_id` atomically
- [x] 1.4 Add `LockTaskRow` query to `api/queries/tasks.sql` that does `SELECT id, status, current_version FROM tasks WHERE id = $1 FOR UPDATE`
- [x] 1.5 Add `MaxVersionNoForTask` query to `api/queries/task_versions.sql` returning `COALESCE(MAX(version_no), 0)` for a task
- [x] 1.6 Add `UpdateTaskCurrentVersion` query to `api/queries/tasks.sql` setting `status='pending', current_version=$2, updated_at=now()`
- [x] 1.7 Run `make sqlc`, commit regenerated `*.sql.go` files; verify `go build ./...` and `go vet ./...` pass

## 2. Domain layer (`internal/domain/task/`)

- [x] 2.1 Create `errors.go` defining `ErrTaskNotFound`, `ErrVersionNotFound`, `ErrActiveVersionExists` (carrying `ActiveVersionID` + `ActiveVersionStatus`), `ErrInvalidInput` (carrying field name + reason)
- [x] 2.2 Create `status.go` defining the active-status set `{pending, queued, running, paused, cancelling}` as a typed helper (the single source of truth referenced by D2 / D9 / D11.5)
- [x] 2.3 Create `ports.go` declaring two injection ports — `type Clock interface { Now() time.Time }` and `type IDGenerator interface { NewV7() (uuid.UUID, error) }` — and ship default implementations (`SystemClock{}` calling `time.Now()`; `UUIDv7Gen{}` calling `uuid.NewV7()`). The `Service` struct depends on the interfaces, not concrete types, so unit tests (Task 7.1) can inject fakes that return deterministic timestamps and ids
- [x] 2.4 Create `service.go` with `Service` struct (deps: `*pgxpool.Pool`, `*sqlc.Queries`, `Clock`, `IDGenerator`, `defaultLane string`, `defaultDeadline time.Duration`)
- [x] 2.5 Implement `(s *Service) CreateTask(ctx, CreateInput) (CreateOutput, error)` — opens tx, mints UUIDv7 ids via `IDGenerator`, inserts `tasks` (status `pending`, `current_version=v1.id`) + `task_versions` (parent_id `NULL`, version_no `1`, status `pending`) + `task_runs` (`attempt_no = 1`, status `queued`, `idempotency_key = run_id::text`) + `outbox`, commits
- [x] 2.6 Implement `(s *Service) IterateTask(ctx, taskID, IterateInput) (IterateOutput, error)` — opens tx, `LockTaskRow`, app-level mutex pre-check via `GetActiveVersionByTask`, base resolution (explicit `base_version_id` validated via `GetVersionByTaskAndID`, else `tasks.current_version`), inserts new `task_versions` (status `pending`) + `task_runs` (`attempt_no = 1`, `idempotency_key = run_id::text`) + `outbox`, `UpdateTaskCurrentVersion`, commits
- [x] 2.7 Implement shared private `createActiveVersion` helper that the iterate path calls and that the create path also calls after the initial `tasks` insert; both call sites pass an already-open `pgx.Tx`
- [x] 2.8 Implement SQLSTATE `23505` detection with savepoint pattern per design D2 Step 5: wrap the `INSERT INTO task_versions` in `SAVEPOINT sp_insert_version`; on conflict-with-constraint `one_active_version_per_task` execute `ROLLBACK TO SAVEPOINT sp_insert_version`, query `GetActiveVersionByTask`, then `ROLLBACK` the outer tx and return `ErrActiveVersionExists{ActiveVersionID, ActiveVersionStatus}`
- [x] 2.9 Implement validation helpers: `title` (1..200), `task_type` (regex `^[a-z][a-z0-9-]{0,63}$`), `prompt` (1..16384), `lane` (`^[a-z0-9-]{1,32}$`; absent/`null` triggers env fallback per D6), `params` JSON ≤ 32 KiB
- [x] 2.10 Unit-test the validation helpers (no DB needed) covering empty / oversize / pattern mismatch / valid inputs
- [x] 2.11 Add the canonical execute payload builder (Architecture §5.3 shape) with explicit fields: `msg_id` from `IDGenerator`, `idempotency_key = run_id::text`, `attempt_no = 1`, `parent_version_id` (string UUID or JSON `null`), `parent_artifact_root` reading the base row's `task_versions.artifact_root` (JSON `null` when the column is `NULL`; per design D8 the worker treats `null` as empty workspace), `deadline_ts = Clock.Now() + defaultDeadline` in Unix seconds

## 3. Application layer (`internal/application/task/`)

- [x] 3.1 Create thin command structs `CreateTaskCommand` / `IterateTaskCommand` that the HTTP layer translates request bodies into
- [x] 3.2 Wire the Application layer to call `domain/task.Service`; no business logic here beyond translation

## 4. HTTP interface (`internal/interfaces/http/tasks.go`)

- [x] 4.1 Define DTO structs `CreateTaskRequest` / `CreateTaskResponse` / `IterateTaskRequest` / `IterateTaskResponse` with `binding` tags matching the validation rules
- [x] 4.2 Implement `POST /api/v1/tasks` handler: parse → application → 201 success or mapped error envelope
- [x] 4.3 Implement `POST /api/v1/tasks/{task_id}/iterate` handler: same shape; reject invalid `task_id` UUID parse as `400 invalid_input`
- [x] 4.4 Extend `internal/interfaces/http/errors.go` with error codes `invalid_input`, `task_not_found`, `version_not_found`, `active_version_exists` and the domain-error → HTTP-status mapping
- [x] 4.5 Register the two routes inside `internal/interfaces/http/server.go` under the `/api/v1` group
- [x] 4.6 Ensure handler middleware emits the standard log fields `task_id`, `version_id`, `request_id`, `trace_id` (extend the existing slog handler context if necessary)

## 5. Observability

- [x] 5.1 Declare Prometheus counters `tasks_created_total{task_type}` and `tasks_iterated_total{outcome}` in `internal/infrastructure/observability/`
- [x] 5.2 Increment the counters at handler exit (success or terminal error)
- [x] 5.3 Verify spans appear with the route templates `POST /api/v1/tasks` and `POST /api/v1/tasks/{task_id}/iterate` (no code change expected — confirm the existing OTEL middleware names them correctly)

## 6. Configuration

- [x] 6.1 Add `DEFAULT_LANE` (default `default`) and `DEFAULT_TASK_DEADLINE` (default `60m`) to `internal/infrastructure/config/`
- [x] 6.2 Document the two env vars in `api/README.md`

## 7. Tests

- [x] 7.1 Domain-level unit tests for `CreateTask` happy path — pure-unit coverage is the validation suite in `validation_test.go` (input rejection paths). The CreateTask happy path itself is exercised end-to-end by 7.4 below because `createActiveVersion`'s savepoint + SQLSTATE semantics are not faithfully reproducible with a mocked Querier interface
- [x] 7.2 Domain-level unit tests for `IterateTask` — covered by the integration scenarios 7.5–7.8 (happy path, app-level mutex, DB-level SQLSTATE 23505, base resolution via current_version, base resolution via explicit `base_version_id`, `task_not_found`, `version_not_found`); deliberately consolidated into the integration suite for the same reason as 7.1
- [x] 7.3 Add testcontainers-postgres integration test (`//go:build integration`) under `api/internal/interfaces/http/` that boots the migrations and the HTTP server in-process via `httptest.Server`
- [x] 7.4 Integration test: `POST /api/v1/tasks` → assert 201, envelope shape, DB rows present in all four tables, outbox topic = `execute.<task_type>.<lane>`
- [x] 7.5 Integration test: `POST /iterate` happy path on a terminal task — assert version_no increments, `tasks.current_version` updated, outbox `parent_version_id` / `parent_artifact_root` populated
- [x] 7.6 Integration test: two concurrent `POST /iterate` calls — exactly one 201, one 409 with `active_version_exists`, exactly one new active row in `task_versions`, AND assert `response_409.data.active_version_id == response_201.data.version_id` (covers the savepoint-rollback read path from design D2 Step 5)
- [x] 7.7 Integration test: `POST /iterate` against task whose current version is `running` — 409 short-circuited at app layer (no insert attempted)
- [x] 7.8 Integration test: 404 cases (unknown task, base_version_id from a different task)
- [x] 7.9 Integration test: 400 cases (missing title, oversized params, invalid `task_type` pattern)
- [x] 7.10 Integration test: metrics — after a successful create, scrape `GET /metrics` and assert `tasks_created_total{task_type=...} == 1`

## 8. Wiring and final checks

- [x] 8.1 Wire the new `domain/task.Service` and `application/task` into the main composition root (`cmd/api/main.go` or wherever the server is constructed)
- [x] 8.2 Run `go vet ./...`, `golangci-lint run ./...`, `go test -race -count=1 ./...`
- [x] 8.3 Run `make test-integration` locally (requires a working Docker daemon for testcontainers-postgres; same prerequisite already documented in `api/README.md` "集成测试") and verify all new tests pass
- [x] 8.4 Update `api/README.md` with a short "Task write endpoints" section pointing at the spec
- [x] 8.5 ~~Append the session's user prompt to `docs/HISTORY.md`~~ — retired per user feedback (2026-05-21): HISTORY.md is now manually maintained; this step removed from future task templates
- [x] 8.6 Final review of `openspec/changes/add-task-create-api/` against the spec scenarios and tick remaining boxes
