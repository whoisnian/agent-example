## ADDED Requirements

### Requirement: Task Cost Detail Endpoint

The API SHALL expose `GET /api/v1/tasks/{task_id}/cost` returning the task's aggregate cost plus a per-version breakdown. The response MUST be HTTP `200` with the unified envelope `{code, message, data, trace_id}` where `data = {task_id, total, by_version}`:

- `total` is the same `CostSummary` shape introduced by `task-read-api`: `{amount_usd, input_tokens, output_tokens, cached_tokens, tool_calls, wall_time_ms}`; `amount_usd` is a decimal string at scale 8 (e.g. `"0.06750000"`), all other fields are JSON numbers.
- `by_version` is an array of `{version_id, version_no, created_at, cost}` ordered by `version_no` ascending. Each `cost` is the same `CostSummary` shape; a version with no settled events yet MUST still appear with the all-zero `CostSummary` (LEFT JOIN, zero-fill — never `null`).

The endpoint MUST be scoped to the caller's `(tenant_id, user_id)`. A `task_id` that does not exist, OR exists but belongs to a different owner, MUST return HTTP `404` with envelope `code = "task_not_found"` — never `403`, never reveal existence (mirrors `task-read-api` §"Owner-Scoped Reads Hide Unowned Resources").

#### Scenario: Detail of an owned task with two versions
- **GIVEN** an owned task with `version_no = 1` (`amount_usd = 1.10`) and `version_no = 2` (`amount_usd = 0.62`)
- **WHEN** the caller `GET /api/v1/tasks/{id}/cost`
- **THEN** the response MUST be HTTP `200` with `data.total.amount_usd = "1.72000000"`, `data.by_version` MUST have two entries ordered by `version_no` ascending, and each `cost.amount_usd` MUST be the decimal-string form of its row

#### Scenario: Owned task with no versions yet
- **GIVEN** an owned task with no `task_versions` rows
- **WHEN** the caller `GET /api/v1/tasks/{id}/cost`
- **THEN** the response MUST be HTTP `200` with `data.total = zeroCost()` and `data.by_version = []` (the empty array, NOT `null`)

#### Scenario: Version exists but has no settled events
- **GIVEN** an owned task with a version that has no `task_costs` row yet (e.g., its run is still queued)
- **WHEN** the caller `GET /api/v1/tasks/{id}/cost`
- **THEN** the version MUST appear in `data.by_version` with `cost = zeroCost()` (LEFT JOIN preserves the version row)

#### Scenario: Unowned or unknown task returns 404
- **GIVEN** a `task_id` that either does not exist OR belongs to a different owner
- **WHEN** the caller `GET /api/v1/tasks/{id}/cost`
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"` (never `403`, never differentiate the two cases)

#### Scenario: Malformed task_id returns 400
- **WHEN** the `{task_id}` path segment is not a valid UUID
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` naming the offending path field

### Requirement: Version Cost Detail Endpoint

The API SHALL expose `GET /api/v1/versions/{version_id}/cost` returning a single version's cost plus the owning `task_id` for deep-linking. The response MUST be HTTP `200` with `data = {version_id, task_id, version_no, cost, updated_at}` where `cost` is the same `CostSummary` shape as the task-detail endpoint and `updated_at` is the `task_costs.updated_at` timestamp (`null` when no settled events yet).

A version that does not exist OR whose owning task belongs to a different owner MUST return HTTP `404` with envelope `code = "version_not_found"`. The ownership probe is `task_versions.task_id → tasks` joined on `(tenant_id, user_id)`.

#### Scenario: Owned version cost
- **WHEN** the caller `GET /api/v1/versions/{id}/cost` for an owned version with at least one settled event
- **THEN** the response MUST be HTTP `200` with `data.cost.amount_usd` equal to the decimal-string form of `task_costs.amount_usd` and `data.task_id` equal to the owning task

#### Scenario: Version with no settled events
- **GIVEN** an owned version with no `task_costs` row yet
- **WHEN** the caller `GET /api/v1/versions/{id}/cost`
- **THEN** the response MUST be HTTP `200` with `data.cost = zeroCost()` and `data.updated_at = null`

#### Scenario: Unowned or unknown version returns 404
- **WHEN** the `version_id` does not exist OR is owned by a different user
- **THEN** the response MUST be HTTP `404` with `code = "version_not_found"`

### Requirement: Caller-Scoped Cost Rollup Endpoint

The API SHALL expose `GET /api/v1/me/cost` returning the caller's total cost with an optional time window and an optional grouping. Query params:

- `from` (RFC3339 timestamp, optional) — inclusive lower bound on `cost_events.occurred_at`.
- `to` (RFC3339 timestamp, optional) — strictly-exclusive upper bound; MUST be `> from` when both are present.
- `group_by` (optional, one of `day` / `task_type` / `model`) — when present, the response is an array of `{key, totals}`; when absent, the response is a single `{total}` object. An empty query value (e.g., `?group_by=`) MUST be treated as absent (matches `task-read-api`'s `status` filter convention).

Default window: when `group_by` is present AND `from` is absent, the server MUST default `from = to - 30d` (with `to = now()` when `to` is also absent). When `group_by` is absent the window is open by default (no implicit bounds), matching "show me everything I ever spent" semantics.

Window cap: when `group_by` is present, the window `to - from` MUST be ≤ `366 days`; a larger range MUST be rejected with HTTP `400 invalid_input` naming `to`. The cap is a soft cardinality guard against a `group_by=day` request that fans out into thousands of buckets in one response.

Without `group_by`, `data = {total}` where `total` is the `CostSummary` shape; an empty result set MUST emit `data.total = zeroCost()` (the all-zero summary), NOT 404.

With `group_by`, `data = {group_by, items}` where `items` is an array of `{key, totals}` ordered by `key` ascending. Each `totals` is the `CostSummary` shape. An empty result MUST emit `data.items = []` (the empty array, NOT `null`).

The two response shapes are discriminated: when `group_by` is absent the response payload MUST contain ONLY a `total` key (no `group_by`, no `items`); when present, the payload MUST contain ONLY `group_by` + `items` (no `total`). Clients can branch on the presence of the `group_by` key.

Grouping semantics:

- `day`: bucket by `date_trunc('day', occurred_at, 'UTC')` (the Postgres 16+ three-arg form pins the timezone explicitly, so the bucket boundary is deterministic regardless of the connection's session `TimeZone`); emit the `key` as the ISO date string `YYYY-MM-DD`.
- `task_type`: bucket by `tasks.task_type`.
- `model`: bucket by `cost_events.resource_name` for rows whose `kind = 'llm'`; non-llm rows MUST collapse into a single `"other"` bucket so the totals's `amount_usd` sum stays reconcilable with the no-group_by `amount_usd`.

`CostSummary`'s non-amount columns on `/me/cost` are aggregated directly off `cost_events` per the cost-ingest per-kind mapping (kept in sync so users don't see double-counts under the `model` collapse):

- `input_tokens` / `output_tokens` / `cached_tokens` = `SUM(<col>)` (NULL contributes 0)
- `tool_calls` = `SUM(COALESCE(calls, 1)) FILTER (WHERE kind = 'tool')`
- `wall_time_ms` = `SUM(COALESCE(duration_ms, 0)) FILTER (WHERE kind IN ('llm', 'tool'))`

The `amount_usd` sum is unfiltered (every kind contributes — that's the whole point of the bottom line).

The query MUST be scoped to the caller's `(tenant_id, user_id)` via `cost_events.task_id → tasks` join (an event whose owning task isn't the caller's MUST NOT contribute to any bucket). The time filter applies to `cost_events.occurred_at` (the event-time column), not `task_costs.updated_at` — settle-time would corrupt buckets when a backfill lands.

Invalid `group_by` (any non-empty value outside the three names), invalid `from` / `to` (non-RFC3339), `from >= to`, or `to - from > 366d` when `group_by` is present MUST return HTTP `400` with `code = "invalid_input"` naming the offending field.

#### Scenario: Total without grouping or filter
- **GIVEN** the caller owns two tasks with combined `amount_usd = 5.43`
- **WHEN** the caller `GET /api/v1/me/cost`
- **THEN** the response MUST be HTTP `200` with `data.total.amount_usd = "5.43000000"`

#### Scenario: Empty result returns zero, not 404
- **GIVEN** a caller with no tasks (or with tasks but no settled events)
- **WHEN** the caller `GET /api/v1/me/cost`
- **THEN** the response MUST be HTTP `200` with `data.total = zeroCost()`

#### Scenario: Group by day with time window
- **GIVEN** the caller has settled `cost_events` rows on `2026-05-29` and `2026-05-30`
- **WHEN** the caller `GET /api/v1/me/cost?from=2026-05-29T00:00:00Z&to=2026-05-31T00:00:00Z&group_by=day`
- **THEN** `data.items` MUST contain two entries with keys `"2026-05-29"` and `"2026-05-30"` in ascending order, AND `data.group_by = "day"`

#### Scenario: Group by model collapses non-llm into "other"
- **GIVEN** the caller has settled events with `kind=llm, resource_name="claude-opus-4-7"` and `kind=tool, resource_name="oss_fs"`
- **WHEN** the caller `GET /api/v1/me/cost?group_by=model`
- **THEN** `data.items` MUST contain `{key: "claude-opus-4-7", totals: ...}` and `{key: "other", totals: ...}`; the sum of items' `amount_usd` MUST equal the no-group_by total exactly

#### Scenario: Right-exclusive `to` predicate
- **GIVEN** a `cost_events` row with `occurred_at = 2026-05-30T00:00:00Z`
- **WHEN** the caller queries `from=2026-05-29T00:00:00Z&to=2026-05-30T00:00:00Z`
- **THEN** that row MUST NOT contribute (the `to` predicate is strict: `< to`, not `<=`); the same row MUST contribute to a `from=2026-05-30&to=2026-05-31` slice

#### Scenario: Owner isolation
- **GIVEN** another user owns a task with `amount_usd = 100.00`
- **WHEN** the caller `GET /api/v1/me/cost`
- **THEN** the response MUST NOT include any contribution from the other user's task

#### Scenario: Invalid group_by returns 400
- **WHEN** the caller `GET /api/v1/me/cost?group_by=hour`
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` naming the `group_by` field

#### Scenario: Empty group_by is treated as absent
- **WHEN** the caller `GET /api/v1/me/cost?group_by=` (empty value)
- **THEN** the handler MUST NOT 400; the response MUST be the no-group_by shape (`data.total`)

#### Scenario: from greater-or-equal than to returns 400
- **WHEN** the caller `GET /api/v1/me/cost?from=2026-05-30T00:00:00Z&to=2026-05-29T00:00:00Z`
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` naming the `to` field

#### Scenario: Window cap exceeded for grouped query returns 400
- **WHEN** the caller `GET /api/v1/me/cost?group_by=day&from=2025-01-01T00:00:00Z&to=2026-05-31T00:00:00Z` (> 366 days)
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` naming the `to` field

#### Scenario: Default window applies for grouped query without `from`
- **WHEN** the caller `GET /api/v1/me/cost?group_by=day` with neither `from` nor `to`
- **THEN** the handler MUST default `to = now()` and `from = to - 30d`, returning at most 31 buckets (no 400)

#### Scenario: Malformed timestamp returns 400
- **WHEN** the caller `GET /api/v1/me/cost?from=yesterday`
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` naming the `from` field

### Requirement: Effective Pricing List Endpoint

The API SHALL expose `GET /api/v1/pricing` returning every pricing row currently in force. The response MUST be HTTP `200` with `data = {items}` where each item is `{id, resource_kind, resource_name, unit, unit_price_usd, effective_at, expires_at}`, ordered by `(resource_kind, resource_name, unit)` ascending. `unit_price_usd` MUST be a decimal string at scale 8 (the column's scale, same convention as `amount_usd`); `effective_at` is RFC3339; `expires_at` is RFC3339 or `null`.

"Currently in force" means `effective_at <= now() AND (expires_at IS NULL OR expires_at > now())` — the same window predicate as `ListEffectivePricings`. The endpoint is owner-agnostic: every authenticated caller MUST receive the same response body (no per-tenant filtering). The capability MUST NOT expose write verbs (`POST` / `PUT` / `PATCH` / `DELETE`) on the `/pricing` path; pricing mutation is out of scope and lives outside the API surface (cf. AGENTS.md §6 red line: "变更价格通过新增带 effective_at 的行" — not via runtime API).

Negative `unit_price_usd` values are not constrained by the DB schema and not specially handled by the renderer; if one ever exists in the table it MUST render with a leading `-` (no clamp to zero). The schema invariant "no negative prices in production" is operational, not API-enforced.

An empty pricing table MUST return `data.items = []` (HTTP `200`, NOT 404).

#### Scenario: Pricing list returns seeded rows
- **GIVEN** migrations are applied (so the seed from `0005_seed_pricing` is present)
- **WHEN** any caller `GET /api/v1/pricing`
- **THEN** the response MUST be HTTP `200` with `data.items` containing at least one entry per `(resource_kind, resource_name, unit)` that has a row whose effective window covers `now()`

#### Scenario: Expired rows are excluded
- **GIVEN** a `pricing` row with `expires_at < now()`
- **WHEN** the caller `GET /api/v1/pricing`
- **THEN** the response MUST NOT contain that row

#### Scenario: Decimal-string unit_price_usd
- **WHEN** a seeded row has `unit_price_usd = 0.015` (numeric form)
- **THEN** the response item's `unit_price_usd` MUST be the string `"0.01500000"` (scale 8)

#### Scenario: Owner-agnostic — same response for every caller
- **GIVEN** two callers with distinct `(tenant_id, user_id)` identities
- **WHEN** both `GET /api/v1/pricing` at the same moment
- **THEN** the response bodies MUST be byte-identical (no tenant filtering)

### Requirement: Owner-Scoped Reads Hide Unowned Resources

Cost-detail endpoints MUST treat "unknown id" and "unowned id" identically — both return HTTP `404` with the unified envelope (`code = "task_not_found"` for `/tasks/{id}/cost`, `code = "version_not_found"` for `/versions/{id}/cost`). No `403 forbidden`, no list-leaking via differential response, no different error code. This MUST mirror `task-read-api` §"Owner-Scoped Reads Hide Unowned Resources".

The owner identity is `(tenant_id, user_id)` resolved from the request (the MVP dev-mode middleware fills these from env). A row counts as "owned" iff `tasks.tenant_id = $tenant AND tasks.user_id = $user`; for versions, ownership is resolved through `task_versions.task_id → tasks`.

#### Scenario: Same tenant, different user, returns 404
- **GIVEN** a task owned by `(tenant_id=T, user_id=U1)`
- **WHEN** caller `(T, U2)` `GET /api/v1/tasks/{id}/cost`
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"` (NOT `403`, even though the tenant matches)

#### Scenario: Different tenant returns 404
- **GIVEN** a task owned by `(tenant_id=T1, user_id=U)`
- **WHEN** caller `(T2, U)` `GET /api/v1/versions/{id}/cost` for one of that task's versions
- **THEN** the response MUST be HTTP `404` with `code = "version_not_found"`

### Requirement: Amounts Are Decimal Strings at Scale 8

Every monetary value on every response from this capability — `amount_usd` on `CostSummary`, `unit_price_usd` on `PricingEntry` — MUST be rendered as a decimal string with exactly `8` fractional digits via the same `numericToDecimalString` helper that `task-read-api` uses. Values MUST NOT be rendered as JSON numbers under any circumstance, including zero (`"0.00000000"`, not `0`). Invalid / NaN / infinite numeric values MUST degrade to `"0.00000000"` so a read never fails on cost data.

#### Scenario: Zero amount renders as scale-8 string
- **WHEN** any cost field on any response would be zero
- **THEN** it MUST render as `"0.00000000"`

#### Scenario: Eight-decimal value preserved
- **WHEN** `task_costs.amount_usd = 0.06750000`
- **THEN** the response's `amount_usd` MUST be the string `"0.06750000"`
