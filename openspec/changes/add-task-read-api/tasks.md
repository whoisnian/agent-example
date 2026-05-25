## 1. SQL queries (no migration)

- [ ] 1.1 Append `CountTasks :one` to `api/queries/tasks.sql` with the same `tenant_id`/`user_id`/optional-`status` predicate as `ListTasks`, returning `COUNT(*)::bigint`.
- [ ] 1.2 Append `ListTaskCostsByTasks :many` to `api/queries/task_costs.sql`: `SUM(...) ... WHERE task_id = ANY($1::uuid[]) GROUP BY task_id`, returning one cost-summary row per task id (zeros via COALESCE).
- [ ] 1.3 Append `ListVersionCostsByTask :many` to `api/queries/task_costs.sql`: `SELECT * FROM task_costs WHERE task_id = $1` — one batched fetch for the whole version tree (design D4/S2), avoiding the per-node N+1.
- [ ] 1.4 Append `ListRunsByVersion :many` to `api/queries/task_runs.sql`: `WHERE version_id = $1 ORDER BY attempt_no ASC`.
- [ ] 1.5 Append `ListVersionEventsAfter :many` to `api/queries/task_events.sql`: `WHERE task_id = $1 AND version_id = $2 AND id > $3 ORDER BY id ASC LIMIT $4` (rides the existing `(task_id, id)` index per design D7).
- [ ] 1.6 Regenerate sqlc (`make sqlc` or `sqlc generate`) and confirm the new methods appear in `internal/infrastructure/persistence/sqlc`.

## 2. Domain read service

- [ ] 2.1 Add `api/internal/domain/task/read_service.go` with `ReadService{ Queries *sqlc.Queries }` and a constructor; define the single `Owner{ TenantID, UserID uuid.UUID }` value type used by every read method (design D1/S8).
- [ ] 2.2 Define read DTOs (`TaskSummary`, `TaskDetail`, `VersionNode`, `VersionDetail`, `RunSummary`, `EventItem`, `CostSummary`) plus mapping helpers: a `task_costs`-row → all-zero-capable `CostSummary` (D4/D5); `numericToDecimalString(pgtype.Numeric) string` rendering `amount_usd` at full 8-dp scale with invalid/NULL → `"0.00000000"` (S1); `is_active` `*bool` → bool (`nil → false`, S12); `EventItem` exposing `{id, version_id, run_id(nullable), seq, kind, payload(json.RawMessage), created_at}` and `params`/run `error` as raw JSON (`json.RawMessage`, S4/S5). Tree node DTO omits `prompt`/`params`; version-detail DTO includes them.
- [ ] 2.3 Implement `ListTasks(ctx, owner, page, pageSize, status)` → `(items, total)`: clamp page (min 1) / page_size ([1,100]), call `ListTasks` + `CountTasks`, batch-fetch costs via `ListTaskCostsByTasks`, zero-fill missing.
- [ ] 2.4 Implement `GetTask(ctx, owner, taskID)`: load task, enforce ownership via `pgtype.UUID`→`uuid.UUID` + `.Valid` comparison on both tenant_id AND user_id (→ `ErrTaskNotFound` when missing or not owned, S9), load current-version summary (nil-safe) and task cost.
- [ ] 2.5 Implement `ListVersions(ctx, owner, taskID)`: ownership check via task, `ListVersionsByTask`, attach per-version cost via the batched `ListVersionCostsByTask` mapped by `version_id` (no per-node query, S2).
- [ ] 2.6 Implement `GetVersion(ctx, owner, versionID)`: load version → owning task → ownership check (→ `ErrVersionNotFound`; a version whose owning task cannot be loaded also maps to `version_not_found`, never 500, S10), load runs via `ListRunsByVersion`, attach version cost via `GetVersionCost`.
- [ ] 2.7 Implement `ListVersionEvents(ctx, owner, versionID, afterID, limit)`: resolve+own the version, clamp limit, call `ListVersionEventsAfter`, compute `next_after_id`.

## 3. Application layer

- [ ] 3.1 Add `api/internal/application/task/queries.go` with thin read methods forwarding to `ReadService`, constructing the domain `Owner` from the caller identity (no second identity type, S8).
- [ ] 3.2 Wire the `ReadService` into `apptask.Service` (or a sibling read service) without disturbing the existing command methods.

## 4. HTTP interface

- [ ] 4.1 Add `api/internal/interfaces/http/task_reads.go` with `TaskReadHandlers{ App, Logger, DevTenantID, DevUserID }` and `Register(r *gin.RouterGroup)` mounting the five GET routes.
- [ ] 4.2 Implement parsing/validation helpers: `page`/`page_size`/`after_id`/`limit` reject non-integers with `400 invalid_input` (naming the field) but clamp out-of-range values per D3/D7; `status` validated against the six task statuses → `400` on anything else (S6/S7); malformed `task_id`/`version_id` UUIDs → `400 invalid_input`.
- [ ] 4.3 Implement the five handlers, rendering `200` via the `OK` helper and mapping `ErrTaskNotFound`/`ErrVersionNotFound` to `404` through `MapError`; add `slog` lines carrying `trace_id`/`task_id`/`version_id` on not-found/error outcomes.
- [ ] 4.4 Define the response DTOs (envelope `data` shapes) for the five endpoints.

## 5. Wiring

- [ ] 5.1 Extend `ServerDeps` in `server.go` with an optional `*TaskReadHandlers` and mount it on the same `/api/v1` group as the write handlers.
- [ ] 5.2 Construct the read service + handlers in `cmd/api/main.go`, reusing the dev tenant/user identity already parsed for the write path.

## 6. Tests

- [ ] 6.1 Unit-test the pure helpers (no DB): pagination/cursor clamp + non-integer→400, `status` validity, UUID parse, `numericToDecimalString` (zero, fractional `0.62`→`"0.62000000"`, large/18-dp), and `is_active` `*bool` deref.
- [ ] 6.2 Integration tests (`//go:build integration`, testcontainers postgres) covering: list pagination + `page=0` clamp + invalid `status`/`page_size`→400 + owner isolation including **same-tenant/different-user → 404** (S9); task detail with/without current_version; version tree ordering + parent_id null + no-N+1 cost; version detail runs ordering + empty runs + nullable `error`; event backfill after_id/next_after_id + version scoping + **null `run_id`** event (S4); 404-on-unowned for task and version; 400 on malformed id; cost `amount_usd` as `"0.00000000"` when absent and `"0.62000000"` when present (S1).
- [ ] 6.3 Run `go vet`, `golangci-lint run` (both build tags), `go test -race ./...`, and `make test-integration`; confirm all pass.

## 7. Docs & validation

- [ ] 7.1 Update `api/README.md` with the five read endpoints and their query params.
- [ ] 7.2 Run `openspec validate add-task-read-api --strict` and fix any reported issues.
