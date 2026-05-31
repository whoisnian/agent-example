## Why

The Cost Service now populates `task_costs` (`add-cost-service` just shipped), but no HTTP endpoint exposes those numbers. The web UI surfaces cost as `0.00000000` everywhere outside the embedded summaries on `add-task-read-api`'s list / detail / tree responses — and there's no way to see how a task's amount decomposes across its versions, what the caller has spent overall, or which prices are in force. This change adds the four cost endpoints documented in `docs/ARCHITECTURE.md §5.1` (lines 528–531) so the read side of the cost vertical is complete and the front-end can stop hard-coding zero placeholders.

## What Changes

- Add `GET /api/v1/tasks/{task_id}/cost` returning `{task_id, total, by_version[]}` — task-level totals (sum across versions) plus per-version breakdown ordered by `version_no` ascending, each row joined with `task_versions` for `version_no` + `created_at`.
- Add `GET /api/v1/versions/{version_id}/cost` returning the single-version row plus its owning task id so the client can deep-link without an extra round-trip.
- Add `GET /api/v1/me/cost` returning caller-scoped rollups. Query params: `from` (RFC3339, optional), `to` (RFC3339, optional; must be `> from` when both present), `group_by ∈ {day, task_type, model}` (optional). Without `group_by` the response is a single totals object; with it the response is an array of `{key, totals}` pairs ordered by `key`.
- Add `GET /api/v1/pricing` returning the currently-effective pricing rows (`effective_at <= now() AND (expires_at IS NULL OR expires_at > now())`) as a list, sorted by `(resource_kind, resource_name, unit)`. Owner-agnostic (prices are not tenant-scoped in MVP).
- Add new sqlc queries: `GetTaskCostWithOwner :one` (drives FROM `tasks` LEFT JOIN `task_costs` GROUP BY `tasks.id` so an owned-but-empty task still returns a zero-aggregate row); `ListVersionCostsForTask :many` (joins `task_versions LEFT JOIN task_costs` to surface `version_no` + `created_at` per row); `GetVersionCostWithOwner :one` (single-row join through `task_versions → tasks` to resolve ownership without an extra round-trip); `SumOwnerCosts :one` / `GroupOwnerCosts :many` (the `/me/cost` aggregator, parameterised on `group_by`, UTC-pinned via `date_trunc('day', occurred_at, 'UTC')`); `ListCurrentPricing :many`.
- Per-column `/me/cost` SQL mirrors the cost-ingest aggregate mapping — `tool_calls` filtered to `kind='tool'`, `wall_time_ms` filtered to `kind IN ('llm','tool')` — so the resulting `CostSummary` is shape-compatible with what `task_costs` would produce; `amount_usd` reconciles bit-for-bit because the Cost Service writes both sources in one transaction.
- Reuse `amount_usd` as the 8-decimal string (`"0.06750000"`) via `numericToDecimalString` and the read-API's `amountScale` convention — never a JSON number. `unit_price_usd` follows the same rule.
- Owner-scoped 404 on unknown / unowned `task_id` / `version_id` (mirrors `add-task-read-api`: envelope `code = "task_not_found"` / `"version_not_found"`, never 403, never reveal existence). Service maps `pgx.ErrNoRows` to the existing domain sentinels `ErrTaskNotFound` / `ErrVersionNotFound` — no new error type.
- `/me/cost` defaults `to = now()` and `from = to - 30d` when `group_by` is present and no explicit window was supplied; enforces `to - from ≤ 366d` for grouped queries (cardinality guard against thousands of buckets in one response).
- `/pricing` is read-only via this capability and owner-agnostic (every authenticated caller receives the same body); mutation lives outside the API surface per AGENTS.md §6.
- Spec changes:
  - New capability `task-cost-api` for the four endpoints, request/response shapes, ownership rules, and aggregator parameters.

## Capabilities

### New Capabilities

- `task-cost-api`: HTTP read endpoints for cost — task/version detail, caller-scoped rollup with group_by, and the effective pricing list.

### Modified Capabilities

(none — `task-cost-data-model` and `task-cost-ingest` are unaffected; the read endpoints are pure consumers.)

## Impact

- New code: `api/internal/domain/task/cost_read_*.go` (DTOs + read service for the cost-detail and pricing endpoints), `api/internal/application/task/cost_queries.go`, `api/internal/interfaces/http/task_cost_reads.go` (4 GET handlers).
- New SQL: `api/queries/task_costs.sql` (additions for the join + owner-scoped sum/group), `api/queries/pricing.sql` (additions for `ListCurrentPricing`).
- Touches `cmd/api/main.go` to wire the new handler set into the v1 group; `interfaces/http/server.go` `ServerDeps` gains `TaskCostHandlers` (same pattern as `TaskReadHandlers`).
- No migrations, no MQ topology changes, no new dependencies.
- Unblocks `add-web-cost-views` (TaskDetail / VersionTree / CostDashboard call sites have a real source).
- Reuses everything from `add-task-read-api`: unified envelope, owner-scoped 404, decimal-string `amount_usd`, slog field discipline.
