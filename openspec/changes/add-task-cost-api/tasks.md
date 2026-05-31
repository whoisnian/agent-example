## 1. SQL & sqlc surface

- [ ] 1.1 Add `GetTaskCostWithOwner :one` to `api/queries/task_costs.sql` — drives FROM `tasks` LEFT JOIN `task_costs` with `WHERE tasks.id = $1 AND tasks.tenant_id = $2 AND tasks.user_id = $3` and `GROUP BY tasks.id`. Owned-but-empty task returns 1 row with COALESCE'd zero aggregates; unknown / unowned returns no rows (service maps `pgx.ErrNoRows` to `ErrTaskNotFound`).
- [ ] 1.2 Add `ListVersionCostsForTask :many` driving FROM `task_versions JOIN tasks` (owner predicate) LEFT JOIN `task_costs` on `version_id`; `ORDER BY version_no ASC`. Returns `version_id, version_no, created_at, <cost columns nullable>`. Versions with no cost row yet still appear; mapper zero-fills.
- [ ] 1.3 Add `GetVersionCostWithOwner :one` joining `task_versions JOIN tasks LEFT JOIN task_costs`. Returns version row fields + nullable cost columns + `task_id`. Unknown / unowned returns no rows (service maps `pgx.ErrNoRows` to `ErrVersionNotFound`).
- [ ] 1.4 Add `SumOwnerCosts :one` summing `cost_events` joined to `tasks` for `(tenant_id, user_id)` with optional `from` (sqlc.narg) and right-exclusive `to` (sqlc.narg). Per-column SQL mirrors the cost-ingest mapping: `tool_calls = SUM(COALESCE(calls,1)) FILTER (WHERE kind='tool')`, `wall_time_ms = SUM(COALESCE(duration_ms,0)) FILTER (WHERE kind IN ('llm','tool'))`, token columns are plain SUM (NULL contributes 0), `amount_usd = SUM(amount_usd)`.
- [ ] 1.5 Add `GroupOwnerCosts :many` — same joins + filters + per-column FILTER mapping as 1.4 plus a `group_by text` parameter; SQL uses `CASE sqlc.arg('group_by')::text WHEN ...` with the **UTC-pinned** `to_char(date_trunc('day', occurred_at, 'UTC'), 'YYYY-MM-DD')` for the day bucket. `GROUP BY key ORDER BY key ASC`. After generation, verify the sqlc-emitted Go type for `key` is `string` (not `interface{}`); if it isn't, fall back to three separate queries per design D5.
- [ ] 1.6 Add `ListCurrentPricing :many` to `api/queries/pricing.sql` — `WHERE effective_at <= now() AND (expires_at IS NULL OR expires_at > now()) ORDER BY resource_kind, resource_name, unit`.
- [ ] 1.7 Run `make sqlc` and commit regenerated `pricing.sql.go`, `task_costs.sql.go`, `querier.go`. Inspect the new `GroupOwnerCostsRow` shape per 1.5 — pivot to three queries if the `key` column comes back untyped.

## 2. Domain layer (DTOs + read service)

- [ ] 2.1 Add `api/internal/domain/task/cost_read_dtos.go` with `TaskCostDetail`, `VersionCostBreakdown`, `VersionCostDetail`, **two distinct DTOs** for `/me/cost` (`OwnerCostTotal{Total CostSummary}` for the no-group_by branch; `OwnerCostGrouped{GroupBy string, Items []OwnerCostGroup}` for the grouped branch — per design D2/S8, the application layer returns whichever the request asked for), `OwnerCostGroup{Key, Totals}`, `PricingEntry`, `PricingList`. Reuse `CostSummary` and `numericToDecimalString` from `read_dtos.go`.
- [ ] 2.2 Add `api/internal/domain/task/cost_read_service.go` with `CostReadService{Queries *sqlc.Queries}` and methods `GetTaskCost`, `GetVersionCost`, `GetOwnerCostTotal` (no group_by branch), `GetOwnerCostGrouped` (group_by branch), `ListPricing`. Each id-bearing method validates Owner via the existing `Owner.owns(...)` predicate; maps `pgx.ErrNoRows` / unowned rows to the existing sentinels `ErrTaskNotFound` (task path) / `ErrVersionNotFound` (version path) — no new error type. Zero-fills missing aggregates.
- [ ] 2.3 Add input-validation helpers: `parseGroupBy(string) (string, error)` accepting only `day`/`task_type`/`model` and treating empty as "absent"; `parseTimeWindow(from, to *time.Time, requireFromWhenGrouped bool) error` rejecting `from >= to` and applying the 366-day cap (caller passes `true` for the grouped branch); `defaultWindow(groupedNoFrom bool) (from, to time.Time)` producing the `to=now()` / `from=to-30d` defaults for the grouped-no-bounds case.
- [ ] 2.4 Unit-test the validation helpers (empty group_by → "" sentinel, invalid → error; `from >= to` rejected; 366d cap enforced only when grouped; default window math). Plus DTO zero-state defaults (no DB needed).
- [ ] 2.5 Extend the existing `numericToDecimalString` unit tests in `read_dtos_test.go` (or a fresh `cost_read_dtos_test.go`) with explicit pricing-shape cases: `0.015` → `"0.01500000"`, `0.0001` → `"0.00010000"`, and a negative test (`-0.5` → `"-0.50000000"`) — locks the `unit_price_usd` rendering contract for the `/pricing` endpoint.

## 3. Application layer

- [ ] 3.1 Add `api/internal/application/task/cost_queries.go` exposing `CostReadService` that folds the caller's `(tenantID, userID uuid.UUID)` into `domain.Owner` and delegates to the domain service (same idiom as `application/task.ReadService`).

## 4. HTTP layer

- [ ] 4.1 Add `api/internal/interfaces/http/task_cost_reads.go` with `TaskCostHandlers{App, Logger, DevTenantID, DevUserID}` and `Register(r *gin.RouterGroup)` mounting the four GETs.
- [ ] 4.2 Handler implementations: parse path UUIDs via the existing `parseUUIDParam`; parse `from` / `to` as `time.Time` from RFC3339, return 400 with field name on malformed; validate `group_by`; call the app layer; render via the unified envelope. Errors flow through the shared `handleError` from `task_reads.go`.
- [ ] 4.3 Extend `interfaces/http/server.go::ServerDeps` with `TaskCostHandlers *TaskCostHandlers`; mount in the v1 group if non-nil (parallel to `TaskReadHandlers`).
- [ ] 4.4 Wire in `cmd/api/main.go`: `appCostReadSvc := apptask.NewCostReadService(taskdomain.NewCostReadService(queries))`; construct `TaskCostHandlers` with the dev tenant/user; pass to `ServerDeps`.

## 5. Tests

- [ ] 5.1 Add `api/internal/interfaces/http/task_cost_reads_test.go` — handler-level tests with a fake `CostReadService`: each endpoint's happy path, 400s on invalid `group_by` / `from` / `to` / window > 366d, 404 on `ErrTaskNotFound` / `ErrVersionNotFound`, owner-isolation outcome (the fake service returns `ErrTaskNotFound` for the unowned case; the handler MUST surface `code = "task_not_found"`), empty `?group_by=` treated as absent, default window applied for `?group_by=day` with no `from` / `to`. Plus an explicit owner-agnostic assertion for `/pricing`: two callers with distinct `(tenant_id, user_id)` MUST receive byte-identical responses (use the handler-level harness rather than dev-mode middleware).
- [ ] 5.2 Add `api/internal/interfaces/http/task_cost_reads_integration_test.go` (`//go:build integration`) using the existing test fixture from `tasks_integration_test.go`. Cover: `/tasks/{id}/cost` happy + zero by_version + owned-but-empty task → 200 with zero total (per S1) + 404 (other tenant, different user, unknown id, malformed UUID); `/versions/{id}/cost` happy + no-cost-row case + 404; `/me/cost` no-filter total + group_by × 3 + time-window correctness (right-exclusive `to`, UTC day-bucket boundary — assert that an event at `2026-05-30T23:30:00Z` lands in `key="2026-05-30"`, not `2026-05-31`, regardless of session timezone) + 400 invalid `group_by` + 400 `from >= to` + 400 window > 366d + cross-owner isolation; `/pricing` returns seed rows + ordering + decimal-string `unit_price_usd` + owner-agnostic (two callers).

## 6. Documentation

- [ ] 6.1 Update `api/README.md` adding a new `## 任务成本端点（task-cost-api）` section: enumerate the 4 endpoints, query-param semantics, decimal-string convention, 404 on unowned, link to the spec.
- [ ] 6.2 Add a one-line pointer in `docs/ARCHITECTURE.md §5.1` (next to the existing endpoint rows) noting that the cost endpoints are implemented under capability `task-cost-api`.
