# task-cost-ingest Specification

## Purpose
TBD - created by archiving change add-cost-service. Update Purpose after archive.

## Requirements

### Requirement: Cost Event Queue Subscription

The API SHALL operate a Cost Service consumer subscribed to queue `q.cost.events` (declared by `api-messaging`), running as a goroutine independent of the task-event ingester so flow control on one queue does not stall the other. The consumer MUST share the API's single AMQP connection but own its own channel and prefetch budget, with the latter configurable via env (default `64`).

The consumer MUST inspect the message body's `kind` field for branching (`llm` / `tool` / `compute`); routing keys are advisory only. Each delivery whose body's `kind` is not one of the three values MUST be dead-lettered as a poison message (no requeue).

The Cost Service MUST own a dedicated `cost_consumer_connected` Prometheus gauge (separate from the existing `event_consumer_connected` used by the task-event ingester) so per-queue connection state is observable without one consumer's outage masking the other.

On startup — after subscription succeeds and before processing the first delivery — the Cost Service MUST emit an INFO log `cost_pricing_coverage` listing the distinct `(resource_kind, resource_name)` pairs currently present in `pricing` with at least one row whose effective window includes `now()`. This is a low-cost guardrail against `resource_name` typos that would otherwise produce silent zero-amount events forever; operators can compare the log against the worker's `model_by_key` and tool registry at boot time.

#### Scenario: Consumer subscribes on startup
- **GIVEN** `q.cost.events` is declared and bound to `cost.exchange` per `api-messaging`
- **WHEN** the API process starts
- **THEN** a Cost Service goroutine MUST open a dedicated channel, declare the queue passively, set prefetch to the configured value, begin consuming with `auto-ack = false`, set `cost_consumer_connected = 1` while connected, and log `cost_pricing_coverage` containing the model + tool names found in active pricing rows

#### Scenario: Unknown kind dead-letters
- **WHEN** a delivery arrives with body `{"kind":"unknown",...}` and otherwise valid fields
- **THEN** the consumer MUST `Nack(multiple=false, requeue=false)` the delivery and increment `cost_events_settled_total{kind="unknown",result="error"}`

#### Scenario: Per-queue connection gauge
- **WHEN** the cost consumer's AMQP channel is open and consuming
- **THEN** `cost_consumer_connected` MUST be `1`, AND its value MUST NOT be affected by the task-event consumer's connection state

### Requirement: Cost Event Settlement Math

For each valid delivery, the Cost Service SHALL compute `amount_usd` by summing all applicable per-unit terms in force at the event's `occurred_at`:

- For `kind = 'llm'`:
  - if a pricing row exists for `(llm, resource_name, per_1k_input_tokens)` → `+= (input_tokens / 1000) × unit_price_usd`
  - if a pricing row exists for `(llm, resource_name, per_1k_output_tokens)` → `+= (output_tokens / 1000) × unit_price_usd`
  - if a pricing row exists for `(llm, resource_name, per_1k_cached_tokens)` → `+= (cached_tokens / 1000) × unit_price_usd`
- For `kind = 'tool'`:
  - if a pricing row exists for `(tool, resource_name, per_call)` → `+= (calls ?? 1) × unit_price_usd`
  - if a pricing row exists for `(tool, resource_name, per_second)` → `+= (duration_ms / 1000) × unit_price_usd`
- For `kind = 'compute'`:
  - if a pricing row exists for `(compute, resource_name, per_second)` → `+= (duration_ms / 1000) × unit_price_usd`

A unit row absent in `pricing` at `occurred_at` MUST contribute exactly `0` to `amount_usd` (the term is silently dropped). NULL quantity fields (e.g., `cached_tokens = NULL`) MUST be treated as `0` in their term. The arithmetic MUST be exact rational arithmetic (no float intermediates); the final value MUST be bound as `NUMERIC(18, 8)` matching the column.

#### Scenario: LLM event with input + output prices
- **GIVEN** a `pricing` row exists for `(llm, claude-opus-4-7, per_1k_input_tokens, $3.00)` and `(llm, claude-opus-4-7, per_1k_output_tokens, $15.00)`, both `effective_at <= occurred_at` with no expiry
- **WHEN** an event arrives with `kind=llm`, `resource_name=claude-opus-4-7`, `input_tokens=2000`, `output_tokens=500`
- **THEN** the settler MUST compute `amount_usd = (2000/1000)*3 + (500/1000)*15 = 6 + 7.5 = 13.50000000`

#### Scenario: Tool event with per-call only
- **GIVEN** a `pricing` row exists for `(tool, oss_fs, per_call, $0.0001)` and no `per_second` row
- **WHEN** an event arrives with `kind=tool`, `resource_name=oss_fs`, `calls=3`, `duration_ms=120`
- **THEN** the settler MUST compute `amount_usd = 3 * 0.0001 = 0.00030000` (the per_second term contributes 0)

#### Scenario: NULL quantity treated as zero
- **WHEN** an `llm` event has `cached_tokens=NULL` but a `per_1k_cached_tokens` price exists
- **THEN** the cached-tokens term MUST contribute exactly `0` to `amount_usd`

### Requirement: Effective Pricing Lookup

The Cost Service SHALL resolve pricing with a single query per event, returning all rows for `(resource_kind, resource_name)` whose `effective_at <= occurred_at` AND `(expires_at IS NULL OR expires_at > occurred_at)`, then pick at most one row per `unit` (the row with the latest `effective_at` if multiple windows are open). The settler MUST NOT issue one query per unit.

The window predicate is **right-exclusive**: a row whose `expires_at` equals `occurred_at` exactly does NOT match.

#### Scenario: Two pricing windows for the same unit
- **GIVEN** two `pricing` rows for `(llm, claude-opus-4-7, per_1k_input_tokens)` with `effective_at = 2024-01-01` and `2026-01-01` (both unexpired)
- **WHEN** an event with `occurred_at = 2026-03-01` is settled
- **THEN** the settler MUST use the `2026-01-01` row's `unit_price_usd` (the latest `effective_at <= occurred_at`)

#### Scenario: Pricing windows abut exactly at occurred_at
- **GIVEN** row A `(effective_at=2026-01-01, expires_at=2026-03-01)` and row B `(effective_at=2026-03-01, expires_at=NULL)` for the same `(resource_kind, resource_name, unit)`
- **WHEN** an event with `occurred_at = 2026-03-01T00:00:00Z` is settled
- **THEN** row B's `unit_price_usd` MUST be used (row A's `expires_at` is not `> occurred_at`, so it is excluded)

### Requirement: Aggregate Increment Mapping Per Kind

The Cost Service's `task_costs` UPSERT SHALL resolve each `EXCLUDED.<column>` value per the table below before binding, so a single SQL statement applies uniformly across kinds without cross-kind contamination. Worker-supplied NULLs MUST be coerced to `0` before binding (`task_costs` columns are NOT NULL with default 0).

| kind     | input_tokens | output_tokens | cached_tokens | tool_calls            | wall_time_ms | compute_seconds       |
|----------|--------------|---------------|---------------|-----------------------|--------------|------------------------|
| `llm`    | NULL→0       | NULL→0        | NULL→0        | 0                     | duration_ms (NULL→0) | 0          |
| `tool`   | 0            | 0             | 0             | `calls ?? 1` (count tool invocations regardless of which pricing units matched) | duration_ms (NULL→0) | 0 |
| `compute`| 0            | 0             | 0             | 0                     | 0            | `floor(duration_ms / 1000)` (NULL→0) |

`compute_seconds` integer-truncates sub-second compute durations to `0` — `amount_usd` (computed in exact rational arithmetic) remains correct, but the `compute_seconds` aggregate reflects only whole-second contributions. This is a known divergence; operators reading `task_costs` should treat `compute_seconds` as a coarse counter and `amount_usd` as the precise value.

`tool_calls` increments by 1 (or by the worker-supplied `calls`) on every `tool` event regardless of whether a `per_call` pricing row matched, so the aggregate stays a faithful invocation counter even when pricing is missing.

#### Scenario: Aggregate mapping for a tool event
- **GIVEN** task_costs `(version_id=V, tool_calls=2, wall_time_ms=300)` exists
- **WHEN** a `cost.tool` event arrives with `calls=1`, `duration_ms=150`, `input_tokens=NULL`
- **THEN** the resulting row MUST be `(version_id=V, tool_calls=3, wall_time_ms=450, input_tokens=0, ...)`; the NULL `input_tokens` was coerced to 0 before binding

#### Scenario: Aggregate mapping for a sub-second compute event
- **WHEN** a `cost.compute` event arrives with `duration_ms=800` and a matching `per_second` price of `$0.01`
- **THEN** the `cost_events` row's `amount_usd` MUST be exactly `0.00800000` (`0.8 × 0.01`), AND `task_costs.compute_seconds` MUST increment by `0` (floor(800/1000)), AND `task_costs.amount_usd` MUST increment by `0.00800000`

#### Scenario: LLM event leaves compute_seconds untouched
- **WHEN** a `cost.llm` event arrives with `duration_ms=4200`
- **THEN** `task_costs.wall_time_ms` MUST increment by `4200`, `task_costs.compute_seconds` MUST NOT change, `task_costs.tool_calls` MUST NOT change

### Requirement: Missing Pricing Is Non-Fatal

When no pricing row matches the event at `occurred_at` (across every applicable unit for its `kind`), the Cost Service SHALL persist the `cost_events` row with `amount_usd = 0`, `pricing_id = NULL`, log a WARN containing `kind`, `resource_name`, `run_id`, `seq`, `trace_id`, increment `cost_pricing_missing_total{kind,resource}`, and Ack the delivery. The aggregate `task_costs` row MUST still be UPSERTed with the event's token / call / duration columns incremented per the "Aggregate Increment Mapping" table; `amount_usd` simply does not increase.

Rationale: the token / call data is the raw input to settlement and must be preserved so a future backfill can recompute `amount_usd` when pricing arrives. Dead-lettering would lose it. The startup `cost_pricing_coverage` log (see "Cost Event Queue Subscription") and a recommended alert on `increase(cost_pricing_missing_total[10m]) > 5` are the operational signals that a typo or a genuinely-missing model is in play; the consumer itself stays deterministic.

#### Scenario: Unknown model
- **GIVEN** no `pricing` rows exist for `resource_name = "experimental-model"`
- **WHEN** an `llm` event for `experimental-model` is delivered
- **THEN** the consumer MUST insert `cost_events` with `amount_usd = 0` and `pricing_id = NULL`, UPSERT `task_costs` incrementing tokens but leaving `amount_usd` unchanged, increment `cost_pricing_missing_total{kind="llm",resource="experimental-model"}`, log WARN, and Ack the delivery

### Requirement: Idempotent Settlement Per (run_id, kind, seq)

The Cost Service settle transaction SHALL be idempotent on `(run_id, kind, seq)` (matching the new uniqueness boundary in `task-cost-data-model`). The `cost_events` insert MUST use `ON CONFLICT (run_id, kind, seq) DO NOTHING RETURNING`; if the RETURNING set is empty the consumer MUST skip the `task_costs` UPSERT (otherwise duplicate deliveries would double-count) and Ack the delivery. The insert + upsert MUST run inside the same database transaction.

The `task_costs` UPSERT MUST be `ON CONFLICT (version_id) DO UPDATE SET <aggregate cols> = task_costs.col + EXCLUDED.col, updated_at = now()` for each numeric column **except** `task_id` (which is immutable per version_id — see `task-cost-data-model` §"Task Costs `task_id` is Immutable Per `version_id`"). The Cost Service is the only writer to `task_costs`.

#### Scenario: Duplicate delivery does not double-count
- **GIVEN** a `cost.llm` event with `(run_id, kind, seq) = (R, llm, 1)` has already been settled, producing one `cost_events` row and `task_costs.input_tokens = 2000`
- **WHEN** the same delivery is redelivered (e.g., consumer restart before Ack)
- **THEN** the `INSERT cost_events ... ON CONFLICT (run_id, kind, seq) DO NOTHING` MUST return zero rows, the `task_costs` UPSERT MUST be skipped, the consumer MUST Ack the redelivery, and `task_costs.input_tokens` MUST remain `2000`

#### Scenario: Same seq, different kind is not a duplicate
- **GIVEN** `(R, llm, 1)` has been settled
- **WHEN** a `cost.tool` event with `(R, tool, 1)` arrives
- **THEN** the insert MUST succeed (uniqueness includes `kind`), a second `cost_events` row MUST appear, and `task_costs.tool_calls` MUST increment

### Requirement: Delivery Settlement Rules

The Cost Service SHALL settle each delivery according to this table:

| Outcome                                | Action                                                  |
|----------------------------------------|---------------------------------------------------------|
| Malformed body / missing required field | `Nack(false, false)` → DLQ; bump `cost_events_settled_total{result="error"}` |
| Unknown `kind`                          | `Nack(false, false)` → DLQ; bump `cost_events_settled_total{result="error"}` |
| `task_id` does not match `task_versions.task_id` for the event's `version_id` | `Nack(false, false)` → DLQ; bump `cost_events_settled_total{result="error"}` |
| Transient pgx error (deadlock / serialisation / connection) | `Nack(false, true)` → requeue                          |
| Permanent DB error (CHECK / FK violation / NOT NULL)       | `Nack(false, false)` → DLQ                              |
| Pricing missing                         | Ack; bump `cost_events_settled_total{result="missing_pricing"}` |
| Insert was duplicate-noop               | Ack; bump `cost_events_settled_total{result="duplicate"}`       |
| Insert + upsert succeeded               | Ack; bump `cost_events_settled_total{result="ok"}`              |

Transient vs permanent error classification MUST reuse the `isRetryable` helper from `event_ingest.go` (same classification across both consumers).

#### Scenario: Malformed JSON
- **WHEN** a delivery's body fails JSON parsing
- **THEN** the consumer MUST `Nack(false, false)` it (DLQ), bump `cost_events_settled_total{result="error"}`, and log WARN with the body length (not the body itself, to avoid leaking secrets)

#### Scenario: FK violation routes to DLQ (raised by task_costs UPSERT)
- **GIVEN** an event references a `version_id` that does not exist in `task_versions` (so the `task_costs.version_id` FK fails)
- **WHEN** settlement attempts the `task_costs` UPSERT (the preceding `cost_events` INSERT has no FK on `version_id` and would succeed in isolation, but the entire transaction is aborted by the UPSERT failure)
- **THEN** Postgres MUST raise SQLSTATE `23503` and the consumer MUST `Nack(false, false)` the delivery (DLQ, not requeue); no rows MUST persist in `cost_events` either (atomic rollback)

### Requirement: Trace Propagation

The Cost Service consumer SHALL read the AMQP `traceparent` header (set by the worker's `CostEventPublisher`) when present, bind it as a slog attribute on the settle logger, and propagate it onto any subsequent published events (none in this change, but the contract is set).

#### Scenario: traceparent flows from worker to consumer logs
- **GIVEN** a worker publishes a `cost.llm` with header `traceparent = "00-aaaa..-bbbb..-01"`
- **WHEN** the API consumer settles the delivery
- **THEN** every log line emitted during settlement MUST carry `trace_id = "aaaa.."` (W3C trace-id field) as a structured attribute

### Requirement: Observability Metrics

The Cost Service SHALL register and emit the following Prometheus metrics. Naming follows the codebase's existing convention (no `api_` prefix; cf. `events_ingested_total`):

- `cost_events_consumed_total{kind}` — counter, one increment per delivery received.
- `cost_events_settled_total{kind, result}` — counter, with `result ∈ {ok, missing_pricing, duplicate, error}`.
- `cost_pricing_missing_total{kind, resource}` — counter, one per missing-pricing event.
- `cost_amount_settled_usd_total` — counter, summed `amount_usd` across `result=ok` deliveries (NaN-safe; binds as float64 via `*big.Rat`'s `Float64` for the metric only, the persisted value stays exact NUMERIC).
- `cost_event_settle_duration_seconds` — histogram, end-to-end per delivery (includes pricing query + tx). Bucket set: `prometheus.DefBuckets`.
- `cost_consumer_connected` — gauge (0/1), set on AMQP channel state changes; independent of `event_consumer_connected`.

`cost_events_consumed_total` and `cost_events_settled_total{result="ok"}` MUST agree at steady state minus DLQ and missing-pricing counts.

#### Scenario: Metrics emitted on successful settle
- **WHEN** one `cost.llm` event for `claude-opus-4-7` is settled successfully with `amount_usd = 13.50`
- **THEN** `cost_events_consumed_total{kind="llm"}` MUST increment by 1, `cost_events_settled_total{kind="llm",result="ok"}` MUST increment by 1, `cost_amount_settled_usd_total` MUST increment by `13.50`, and `cost_event_settle_duration_seconds` MUST observe one sample
