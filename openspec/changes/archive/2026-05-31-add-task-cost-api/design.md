## Context

`add-cost-service` made `task_costs` non-empty for the first time. `add-task-read-api` already exposes an **embedded** cost summary on the list / detail / tree responses (via `numericToDecimalString` and the `CostSummary` struct), but there are no standalone cost endpoints — neither task/version detail, owner rollup, nor the pricing table. The architecture (§5.1) names four endpoints; this change ships all four.

Underlying data model (already in place from `add-cost-data-model` / `add-cost-service`):

- `task_costs(version_id PK, task_id, input_tokens, output_tokens, cached_tokens, tool_calls, wall_time_ms, compute_seconds, amount_usd NUMERIC(18,8), updated_at)` — UPSERT-maintained by Cost Service.
- `cost_events(id, task_id, version_id, run_id, seq, kind, resource_name, ...quantities, amount_usd, pricing_id, occurred_at, created_at)` — append-only history.
- `pricing(id, resource_kind, resource_name, unit, unit_price_usd, effective_at, expires_at)` — windowed price book.

The read API (`task-read-api`) already established conventions this change inherits verbatim: unified envelope `{code, message, data, trace_id}`; owner identity = `(tenant_id, user_id)`; ownership mismatch → 404 (never 403); `amount_usd` is a decimal string at scale 8 (`"0.06750000"`) via `numericToDecimalString`; defaults / clamps live in the HTTP layer, not the domain.

## Goals / Non-Goals

**Goals**

1. Four GET endpoints that match `ARCHITECTURE.md §5.1`: `/tasks/{id}/cost`, `/versions/{id}/cost`, `/me/cost`, `/pricing`.
2. Owner-scoped 404 on the two id-bearing endpoints.
3. `amount_usd` rendered as a decimal string with exactly 8 fractional digits, never a JSON number.
4. `/me/cost` time filter against `cost_events.occurred_at` (the event-time column), not `task_costs.updated_at`. Without `group_by` the response is a single totals object; with `group_by` an ordered array of `{key, totals}`.
5. `/pricing` returns only currently-effective rows (`effective_at <= now() AND (expires_at IS NULL OR expires_at > now())`) — no time-machine view in MVP.

**Non-Goals**

- WebSocket push of cost deltas (`add-realtime-gateway`).
- Pricing CRUD (admin/ops surface; Post-MVP).
- Currency conversion (USD-only; `amount_usd` is the only money column).
- Per-tenant pricing tiers; the `pricing` table has no tenant column today.
- Pagination on `/me/cost` group_by responses — the cardinality is bounded (≤ ~30 days, a few task_types, a few models). If it ever isn't, we add a `?limit=&offset=` slice later without changing the envelope.
- Backfill / recompute endpoints (Post-MVP; the Cost Service is the only writer).

## Decisions

### D1: One handler set, mirroring TaskReadHandlers

Add `TaskCostHandlers` in `interfaces/http/` paralleling the existing `TaskReadHandlers`. It holds the `application/task.CostReadService` (new), the logger, and the dev tenant/user. Its `Register(r *gin.RouterGroup)` mounts the four GETs. `ServerDeps` gains a `TaskCostHandlers` field and `NewEngine` mounts the handler set if non-nil — same pattern as `TaskReadHandlers`.

**Alternative considered**: bolt the cost endpoints onto `TaskReadHandlers`. **Rejected** because the read service is already big and the cost surface has its own DTOs / query shape — keeping them separate makes the unit-test surface smaller per handler.

### D2: Reuse the read-API CostSummary shape; add CostDetail for the new endpoints

The `add-task-read-api` `CostSummary` (`amount_usd` string + token / call / wall_time integers) is exactly what `total` / `by_version[]` rows need. Reuse it verbatim. Add the following DTOs:

- `TaskCostDetail{TaskID, Total CostSummary, ByVersion []VersionCostBreakdown}` for `/tasks/{id}/cost`.
- `VersionCostDetail{VersionID, TaskID, VersionNo, Cost CostSummary, UpdatedAt}` for `/versions/{id}/cost`.
- `/me/cost` uses **two distinct DTOs** (S8): `OwnerCostTotal{Total CostSummary}` and `OwnerCostGrouped{GroupBy string, Items []OwnerCostGroup}`. The application layer returns whichever the request asked for; the HTTP handler renders it directly as `data`. This makes the discriminated JSON shape ("`{total: ...}`" vs "`{group_by: ..., items: [...]}`" — no nulls, no empty strings) trivial — no `omitempty` gymnastics, and clients can branch on the presence of the `group_by` key.
- `OwnerCostGroup{Key string, Totals CostSummary}` is the per-bucket row.
- `PricingEntry{ID, ResourceKind, ResourceName, Unit, UnitPriceUSD, EffectiveAt, ExpiresAt *time.Time}` and `PricingList{Items []PricingEntry}` for `/pricing`. `UnitPriceUSD` is a decimal string at the column's scale (8 fractional digits) for consistency with `amount_usd`.

`VersionCostBreakdown` carries `{VersionID, VersionNo, CreatedAt, Cost CostSummary}` so the front-end can render the breakdown chronologically without a second query.

### D3: Owner-scope via the existing `Owner` type from task-read-api

`domain/task.Owner{TenantID, UserID uuid.UUID}` (introduced by `add-task-read-api`) is the identity unit. The cost read service takes an `Owner` value and uses the same `owns(...)` predicate (validates `tenantID.Valid && userID.Valid && bytes match`). For `/tasks/{id}/cost` and `/versions/{id}/cost`, ownership is resolved by joining `tasks` (or `task_versions → tasks`) to the supplied owner — a mismatch returns the existing domain sentinels `ErrTaskNotFound` / `ErrVersionNotFound` (introduced by `add-task-read-api`, no new error type), which the HTTP layer's `MapError` already renders as `404` with envelope `code = "task_not_found"` / `"version_not_found"`. Never 403, never reveal existence.

`/me/cost` and `/pricing` don't need an id-level ownership check — the former is inherently caller-scoped, the latter is owner-agnostic.

### D4: SQL surface — five new queries, all sqlc-generated

- `GetTaskCostWithOwner :one` — drives FROM `tasks` (not `task_costs`) and LEFT JOINs `task_costs` so an owned task with NO versions / NO settled events still returns a single row with all-zero aggregates. The ownership predicate sits in the WHERE; an unknown / unowned `task_id` returns zero rows → service maps to `ErrTaskNotFound`:

  ```sql
  SELECT t.id AS task_id,
         COALESCE(SUM(tc.input_tokens), 0)::bigint    AS input_tokens,
         COALESCE(SUM(tc.output_tokens), 0)::bigint   AS output_tokens,
         COALESCE(SUM(tc.cached_tokens), 0)::bigint   AS cached_tokens,
         COALESCE(SUM(tc.tool_calls), 0)::int         AS tool_calls,
         COALESCE(SUM(tc.wall_time_ms), 0)::bigint    AS wall_time_ms,
         COALESCE(SUM(tc.compute_seconds), 0)::bigint AS compute_seconds,
         COALESCE(SUM(tc.amount_usd), 0)::numeric     AS amount_usd
  FROM tasks t
  LEFT JOIN task_costs tc ON tc.task_id = t.id
  WHERE t.id = $1 AND t.tenant_id = $2 AND t.user_id = $3
  GROUP BY t.id;
  ```

  This corrects the obvious pgSQL gotcha: an aggregate query without GROUP BY always returns 1 row with `COALESCE(SUM…)` zeros — so it can't *also* do the ownership check itself; bolting `JOIN tasks` onto the existing `GetTaskCost` would have wrongly returned 0 rows for an owned-but-empty task.

- `ListVersionCostsForTask :many` — drives FROM `task_versions` (LEFT JOIN to `task_costs` on `version_id`) ordered by `version_no ASC`. LEFT JOIN so versions with no `task_costs` row yet still appear (mapper zero-fills). Owner predicate goes through `tasks`:

  ```sql
  SELECT v.id AS version_id, v.version_no, v.created_at,
         tc.input_tokens, tc.output_tokens, tc.cached_tokens,
         tc.tool_calls, tc.wall_time_ms, tc.compute_seconds, tc.amount_usd
  FROM task_versions v
  JOIN tasks t ON t.id = v.task_id
  LEFT JOIN task_costs tc ON tc.version_id = v.id
  WHERE v.task_id = $1 AND t.tenant_id = $2 AND t.user_id = $3
  ORDER BY v.version_no ASC;
  ```

- `GetVersionCostWithOwner :one` — drives FROM `task_versions`, JOINs `tasks` for the owner predicate, LEFT JOINs `task_costs`. Returns version row fields + cost columns (NULL when no settled events) + `task_id`. An unknown / unowned `version_id` returns zero rows → `ErrVersionNotFound`.

- `SumOwnerCosts :one` / `GroupOwnerCosts :many` — caller-scoped rollups, both off `cost_events JOIN tasks`. Per-column SQL (mirrors the cost-ingest per-kind aggregate mapping so `/me/cost`'s shape matches `task_costs` for everything except the side effects of the per-kind gating — see S6 discussion):

  ```sql
  SUM(ce.input_tokens)                                                              AS input_tokens
  SUM(ce.output_tokens)                                                             AS output_tokens
  SUM(ce.cached_tokens)                                                             AS cached_tokens
  SUM(COALESCE(ce.calls, 1)) FILTER (WHERE ce.kind = 'tool')                        AS tool_calls
  SUM(COALESCE(ce.duration_ms, 0)) FILTER (WHERE ce.kind IN ('llm','tool'))         AS wall_time_ms
  SUM(ce.amount_usd)                                                                AS amount_usd
  ```

  `GroupOwnerCosts` adds a `group_by text` parameter routed via `CASE` into the key expression (`date_trunc('day', ce.occurred_at, 'UTC')` formatted with `to_char` — explicit UTC pinning rather than session-timezone-dependent `date_trunc('day', X)`). Returns `(key text, ...same sums)` rows ordered by `key ASC`. Time filter is rebound per query so NULL `from` / `to` matches all rows. The owner predicate (`t.tenant_id = $X AND t.user_id = $Y`) is inner-joined: events whose `task_id` orphan-references a non-existent task are silently excluded — by design (defense in depth; orphans cannot be attributed to a caller). The cost-ingest task_id-immutability check is the upstream guard that prevents orphans in steady state.

- `ListCurrentPricing :many` — `WHERE effective_at <= now() AND (expires_at IS NULL OR expires_at > now()) ORDER BY resource_kind, resource_name, unit`.

The `cost_events`-side aggregation for `/me/cost` is the load-bearing choice — `task_costs` has no event-time column, so a time-filtered query needs `cost_events` no matter what. **One source, one shape**: always sum `cost_events` for `/me/cost` so the no-filter and filtered shapes share an SQL skeleton. The per-column FILTER clauses mirror the cost-ingest mapping so the resulting `CostSummary` is shape-compatible with what `task_costs` would produce. The one column where the two diverge under MVP semantics is `compute_seconds`: `task_costs.compute_seconds = floor(SUM(duration_ms)/1000)` for compute events, while `/me/cost` does not surface compute_seconds at all (it's not on `CostSummary`). `amount_usd` is bit-for-bit reconcilable between the two sources because the Cost Service is the sole writer of both `cost_events.amount_usd` and `task_costs.amount_usd` and writes them in the same transaction.

### D5: `/me/cost` group_by parameterisation

The handler accepts `group_by ∈ {day, task_type, model}`. An empty `?group_by=` MUST be treated as **absent** (matches the sibling `task_reads.go` convention where `c.Query("status") == ""` is the no-filter case). Any non-empty value outside the three names is rejected with **400 invalid_input** naming the field.

Window handling (S7):

- When `group_by` is **absent**, no implicit window — `from` / `to` are pure passthroughs (NULL means open).
- When `group_by` is **present** and `from` is absent, the handler defaults `to = now()` and `from = to - 30d`. The 30-day default matches the typical "what did I spend recently" view.
- When `group_by` is present, the handler enforces `to - from ≤ 366d`. A larger window → 400 `invalid_input` on `to`. This is a cardinality guard against `group_by=day` requests that fan out into thousands of buckets in one response (cheap DoS shape).

The SQL takes `group_by text` and resolves the grouping expression inline via a `CASE`. UTC is pinned explicitly via the Postgres 16+ three-arg `date_trunc` form so day boundaries are deterministic regardless of the connection's session timezone:

```sql
SELECT
    CASE sqlc.arg('group_by')::text
        WHEN 'day'       THEN to_char(date_trunc('day', ce.occurred_at, 'UTC'), 'YYYY-MM-DD')
        WHEN 'task_type' THEN t.task_type
        WHEN 'model'     THEN CASE WHEN ce.kind = 'llm' THEN ce.resource_name ELSE 'other' END
    END                                                                       AS key,
    SUM(ce.input_tokens)                                                      AS input_tokens,
    SUM(ce.output_tokens)                                                     AS output_tokens,
    SUM(ce.cached_tokens)                                                     AS cached_tokens,
    SUM(COALESCE(ce.calls, 1)) FILTER (WHERE ce.kind = 'tool')                AS tool_calls,
    SUM(COALESCE(ce.duration_ms, 0)) FILTER (WHERE ce.kind IN ('llm','tool')) AS wall_time_ms,
    SUM(ce.amount_usd)                                                        AS amount_usd
FROM cost_events ce
JOIN tasks t ON t.id = ce.task_id
WHERE t.tenant_id = $1 AND t.user_id = $2
  AND (sqlc.narg('from')::timestamptz IS NULL OR ce.occurred_at >= sqlc.narg('from'))
  AND (sqlc.narg('to')::timestamptz   IS NULL OR ce.occurred_at <  sqlc.narg('to'))
GROUP BY key
ORDER BY key ASC;
```

The `to` predicate is right-exclusive so chained `[from1, to1) ∪ [to1, to2)` slices are non-overlapping. `day` emits a stable ISO string the JSON-rendering side passes through verbatim. `model` collapses non-llm rows into a single `"other"` bucket since `task_type` / `tool_name` are dimensional siblings, not model identities.

**sqlc verification caveat (S5)**: No existing query in this codebase uses `sqlc.arg(...)` inside a `CASE` discriminator. Before merging the apply commit, run `make sqlc` and inspect the generated `GroupOwnerCostsRow` types — if sqlc infers the `key` column as `interface{}` (because Postgres reports it as `text` from a CASE branch with multiple typed expressions, even though all three branches here return text), fall back to three separate queries (`GroupOwnerCostsByDay` / `…ByTaskType` / `…ByModel`). The runtime cost of three near-identical queries is negligible relative to the lookup; the only reason to prefer one is the code-bloat factor, which only pays off if sqlc generates clean types.

**Alternative considered**: emit three separate sqlc queries (one per group_by). **Rejected** for code-bloat reasons, conditional on the sqlc verification above.

### D6: Empty-state semantics

- `/tasks/{id}/cost`: known owned task, no versions / no events yet → `total` is the zero `CostSummary` (`amount_usd = "0.00000000"`), `by_version` is the empty array (NOT `null`, matching the read-API list shape). Unknown / unowned → 404.
- `/versions/{id}/cost`: known owned version, no settled events yet → same shape with zero costs (the LEFT JOIN produces nulls; the mapper zero-fills). Unknown / unowned → 404.
- `/me/cost`: empty result set → `data = {total: zeroCost()}` (no group_by) or `data = {group_by, items: []}`.
- `/pricing`: empty (no rows yet) → `data = {items: []}`.

### D7: Pricing — `unit_price_usd` as decimal string

`pricing.unit_price_usd` is `NUMERIC(18, 8)` — the same column shape as `amount_usd`. Render via `numericToDecimalString` so a consumer that compares prices across endpoints sees the exact same wire format. JSON field name is `unit_price_usd` to match the column.

`effective_at` and `expires_at` are RFC3339 timestamps; `expires_at` is `*time.Time` so a `NULL` becomes `null` in JSON.

### D8: Caching and rate

Out of scope. None of these endpoints carry private data the read-side hasn't already exposed. We do NOT set `Cache-Control` headers; if a downstream proxy wants to cache responses, that's a deployment concern, not a contract concern. Same posture as `task-read-api`.

### D9: Tracing & logging

Every handler call logs at INFO with `trace_id`, `task_id` / `version_id` where applicable, plus `endpoint` / `outcome`. Errors at the application layer return typed errors (`ErrNotFound`, `ErrInvalidInput`) the HTTP layer renders via the shared `handleError` helper from `task_reads.go` (5xx → ERROR level, 4xx → WARN). No new metrics — these endpoints are owner-bound reads with cheap queries; if latency becomes an issue we add a histogram in a follow-up.

### D10: Pagination — none, but room to grow

None of the four endpoints paginates today. `/tasks/{id}/cost` is bounded by version count (typically ≤ 10); `/versions/{id}/cost` is a single row; `/pricing` has < 100 rows in foreseeable MVP; `/me/cost` is bounded by group cardinality + the 366-day window cap from D5 (max 366 day-buckets, or ≤ tens of task_type / model buckets). If pressure ever materialises we'd add `?limit=&offset=` in a follow-up — easy because the response is already an items array. Document the absence so a client doesn't infer pagination support.

### D11: Time filter uses `cost_events.occurred_at`, not `created_at`

`/me/cost`'s `from` / `to` filter against `cost_events.occurred_at` (worker-reported event time), not `cost_events.created_at` (settle time). Otherwise a recently-backfilled event would appear in "today's" bucket even though the work happened last week — confusing for users reading the dashboard. The audit trail for "when did we learn about this cost" lives in `created_at` and can be exposed via a separate Post-MVP endpoint if anyone ever needs it.

### D12: `/pricing` is "now" only — no time-machine view

`/pricing` returns the rows in force at `now()`. We deliberately don't accept a `?at=<timestamp>` query: the historical-pricing reconstruction story lives in `cost_events.pricing_id` (each settled row records which pricing UUID it was scored against), so an auditor can join `cost_events JOIN pricing` to see how a cost was computed at any point in time. The "preview my cost" front-end use case only ever needs "now". Adding `?at=` would expand the surface without solving a real problem.

### D13: Task-cost detail's `by_version` includes versions with no `task_costs` row

`/tasks/{id}/cost.by_version` is built off the LEFT JOIN in `ListVersionCostsForTask` (D4), so a version whose worker run hasn't started yet — and therefore has no `task_costs` row from the Cost Service to UPSERT into — still appears with `cost = zeroCost()`. Skipping such versions would force the front-end to make a second call to `task-read-api` just to discover "is there a version-2 yet?", which is exactly the round-trip this endpoint was designed to avoid.

## Risks / Trade-offs

- **[Risk]** `/me/cost` sums `cost_events` directly, which is slower than reading `task_costs` for the no-filter case → **Mitigation**: the existing `cost_events_task_occurred_idx` covers the WHERE predicate; the GROUP BY runs on the same scan; benchmark before optimising. If a year of events for a heavy user gets slow we add a materialised view.
- **[Risk]** `unit_price_usd` rendered as decimal string differs from how some front-ends consume "price" (often a float) → **Mitigation**: web client already has the decimal-string helper from the cost-summary work; consistent shape across cost + pricing is the explicit choice.
- **[Risk]** `/pricing` returns the entire active table — could grow into hundreds of rows if many models / tools get seeded → **Mitigation**: out of MVP scope; if we hit > 200 rows we add `?kind=` filter. Documented in D10.
- **[Risk]** A `group_by=model` query over tool / compute events labels them all as `"other"`, which is a lossy bucket → **Mitigation**: explicitly documented in the spec scenario; the alternative (mixing model names with tool names) was rejected because the dimensions aren't comparable.
- **[Trade-off]** No metrics. `task-read-api` also has none and the endpoints are identically shaped — we'd rather not add per-handler counters until there's a saturation problem to investigate.

## Migration Plan

No migrations. The endpoints come online when the API process restarts with the new code.

Rollback: revert the binary. No data implications — these are pure reads.

## Open Questions

(None — D11 / D12 / D13 cover what was previously deferred to this section.)
