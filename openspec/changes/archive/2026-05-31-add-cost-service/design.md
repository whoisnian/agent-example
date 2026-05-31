## Context

The Worker already emits `cost.<kind>` messages to RabbitMQ on `cost.exchange` (routing keys `cost.llm` / `cost.tool` / `cost.compute`). On the API side, `topology.go` already declares `q.cost.events` bound to `cost.#`, the `pricing` / `cost_events` / `task_costs` tables exist (migration 0003), and the read API (`add-task-read-api`) already reads from `task_costs`. The missing piece is the **Cost Service**: a queue consumer that prices each event and writes the aggregate. Without it, `task_costs` is empty and every `amount_usd` rendered by the read API is `"0.00000000"`.

The Cost Service is the **sole writer** to `task_costs` (per `task-cost-data-model` spec). Worker is forbidden from touching that table; everything funnels through this consumer.

Worker `CostEvent` body shape (from `worker/core/messages.py`, exposed at `cost.exchange` per `worker-messaging` spec §"Cost Event Publisher"):

```json
{
  "task_id": "...", "version_id": "...", "run_id": "...",
  "seq": 1, "kind": "llm" | "tool" | "compute",
  "resource_name": "claude-opus-4-7" | "oss_fs" | ...,
  "input_tokens": 1234, "output_tokens": 567, "cached_tokens": 89,
  "calls": null, "duration_ms": 4200, "occurred_at": "2026-05-30T..."
}
```

## Goals / Non-Goals

**Goals**

1. Drain `q.cost.events` deterministically: each delivery either acks (settled) or dead-letters / requeues with explicit rules — never silently dropped.
2. Resolve unit prices from `pricing` at the event's `occurred_at` and compute `amount_usd` exactly (NUMERIC, not float).
3. Persist one `cost_events` row per `(run_id, seq)` (idempotent on duplicate delivery) and UPSERT a per-`version_id` aggregate in `task_costs` in the same transaction.
4. Non-fatal handling when pricing is missing — the event still persists with `amount_usd = 0` and `pricing_id = NULL`, plus a WARN log and a metric.
5. Seed MVP-default pricing so cost amounts are non-zero out of the box (no separate "ship pricing" step).

**Non-Goals**

- HTTP cost endpoints (`/tasks/{id}/cost` etc.) — those belong to `add-task-cost-api`.
- Web UI for cost — `add-web-cost-views`.
- Backfill / recompute tooling (e.g., "rerun this version's settlement with new pricing") — Post-MVP.
- Real-time push of cost updates over WS — Post-MVP (`add-realtime-gateway` carries the channel; cost rides on it later).
- Multi-currency / FX conversion — `amount_usd` is the only currency.
- GRANT-based DB enforcement of "Cost Service is the only writer" — convention only, mirroring `task-cost-data-model` §"Task Costs Aggregation Table".

## Decisions

### D1: Separate consumer, shared MQ connection

The Cost Service runs as a **second consumer goroutine** in `cmd/api/main.go`, parallel to the existing event-ingest consumer. Both share one `*amqp.Connection` but each owns its own `*amqp.Channel` (so flow-control on one queue does not stall the other) and its own prefetch budget.

**Alternative considered**: bolt into the event-ingest consumer with a switch on routing key. **Rejected** because the queues are separate (`q.task.events` vs `q.cost.events`), and mixing the two would entangle delivery accounting, metrics labels, and DLQ behavior.

### D2: Per-kind pricing math (contract)

Given an event with `kind`, `resource_name`, and the per-event quantities, the settler computes `amount_usd` as a sum across these formulas:

| kind     | unit                    | quantity × price                                    |
|----------|-------------------------|-----------------------------------------------------|
| `llm`    | `per_1k_input_tokens`   | `input_tokens / 1000.0 × unit_price_usd`           |
| `llm`    | `per_1k_output_tokens`  | `output_tokens / 1000.0 × unit_price_usd`          |
| `llm`    | `per_1k_cached_tokens`  | `cached_tokens / 1000.0 × unit_price_usd` (optional) |
| `tool`   | `per_call`              | `(calls ?? 1) × unit_price_usd`                    |
| `tool`   | `per_second`            | `duration_ms / 1000.0 × unit_price_usd` (optional)  |
| `compute`| `per_second`            | `duration_ms / 1000.0 × unit_price_usd`             |

A unit row that is absent in `pricing` at `occurred_at` contributes **0** (rather than aborting). All math runs over Go `*big.Rat` and bind to `pgtype.Numeric` with scale 8, mirroring the read-side `amountScale=8` convention in `read_dtos.go`.

**Alternative considered**: settle entirely in Postgres via a single SQL statement (CTE that joins `pricing` and aggregates). **Rejected**: harder to unit-test the math, harder to attach idempotency-aware logging, and the Go-side already has the per-event values in hand.

### D3: Single pricing lookup per event (no N+1)

Instead of three `GetEffectivePricing` round-trips for an `llm` event (one per unit), add `ListEffectivePricings :many` that returns **all pricing rows** in force at `(resource_kind, resource_name, occurred_at)`, regardless of `unit`. The settler picks the units it needs in Go. For `llm` events this collapses 3 RTTs to 1.

The lookup is keyed on `(resource_kind, resource_name, occurred_at)` and the in-flight pricing row per `unit` is the one with the latest `effective_at ≤ occurred_at AND (expires_at IS NULL OR expires_at > occurred_at)` — same window predicate as `GetEffectivePricing`.

### D4: `pricing_id` points to the **dominant unit** row

`cost_events.pricing_id` is a single FK, so multi-unit `llm` rows must pick one. The convention is:

- `llm` → the `per_1k_input_tokens` row's id.
- `tool` → the `per_call` row's id (else the `per_second` row's id if only that matched).
- `compute` → the `per_second` row's id.
- If **no** pricing row matched at all → `pricing_id = NULL`.

This is enough for forensic traceability ("which price model was in force"). Auditors who want full breakdown reconstruct it from `occurred_at` + `kind` + `resource_name`. The convention lives in the modified `task-cost-data-model` spec so it is binding.

**Alternative considered**: model `cost_events` 1-to-many `pricing` via a join table. **Rejected** for MVP scope — added complexity for marginal forensic value.

### D5: Missing pricing is non-fatal

If `ListEffectivePricings` returns no rows the consumer:

1. Persists the `cost_events` row with `amount_usd = 0` and `pricing_id = NULL`.
2. Logs WARN with `kind`, `resource_name`, `occurred_at`, `run_id`, `seq`, `trace_id`.
3. Increments `api_cost_pricing_missing_total{kind,resource}`.
4. Acks the delivery (because the event itself is well-formed; rejecting it would DLQ all events for an unknown model and lose token data we'll want once pricing is loaded).

The token counts persist in `cost_events`, so a future backfill tool can recompute. The aggregate (`task_costs`) still UPSERTs with the token/call/duration columns incremented and `amount_usd` unchanged.

### D6: Idempotency via insert-or-skip on `(run_id, kind, seq)`

The worker's `CostEventPublisher` allocates `seq` from a **per-`(run_id, kind)`** counter (`worker/worker/core/run_context.py::next_cost_seq`, confirmed by `worker-messaging` §"Cost Event Publisher": *"per-run-per-kind monotonic"*). Migration 0003's existing `cost_events_run_seq_key UNIQUE (run_id, seq)` does NOT carry `kind` and would silently collide `(R, llm, 1)` with `(R, tool, 1)`. **This change ships migration `0004_cost_events_kind_unique` that drops the legacy index and creates `UNIQUE (run_id, kind, seq)`** — see the `task-cost-data-model` MODIFIED requirement.

The settle transaction (after the new index is in place):

```
BEGIN;
  -- 1. cross-check task ownership (S12: task_costs.task_id immutable per version)
  SELECT task_id FROM task_versions WHERE id = $version_id;
  -- if no row or != event.task_id → ROLLBACK; consumer Nacks→DLQ.

  -- 2. attempt the cost_events insert, idempotent on the new unique boundary
  INSERT INTO cost_events (..., task_id, version_id, run_id, kind, seq, ...)
  VALUES (...)
  ON CONFLICT (run_id, kind, seq) DO NOTHING
  RETURNING input_tokens, output_tokens, cached_tokens, calls, duration_ms, amount_usd;

  -- 3. if RETURNING returned a row, UPSERT the aggregate using the per-kind
  --    mapping in §"Aggregate Increment Mapping" (task-cost-ingest spec):
  INSERT INTO task_costs (version_id, task_id, input_tokens, output_tokens,
                          cached_tokens, tool_calls, wall_time_ms,
                          compute_seconds, amount_usd)
  VALUES ($version_id, $task_id, $in_tok, $out_tok, $cached_tok,
          $tool_calls, $wall_ms, $compute_s, $amount_usd)
  ON CONFLICT (version_id) DO UPDATE SET
    input_tokens     = task_costs.input_tokens     + EXCLUDED.input_tokens,
    output_tokens    = task_costs.output_tokens    + EXCLUDED.output_tokens,
    cached_tokens    = task_costs.cached_tokens    + EXCLUDED.cached_tokens,
    tool_calls       = task_costs.tool_calls       + EXCLUDED.tool_calls,
    wall_time_ms     = task_costs.wall_time_ms     + EXCLUDED.wall_time_ms,
    compute_seconds  = task_costs.compute_seconds  + EXCLUDED.compute_seconds,
    amount_usd       = task_costs.amount_usd       + EXCLUDED.amount_usd,
    updated_at       = now();
  -- NOTE: task_id is deliberately NOT in the DO UPDATE SET list (S12 — immutable).
COMMIT;
```

A duplicate delivery's `INSERT cost_events` is a no-op (zero rows in RETURNING) → the aggregate UPSERT is skipped → no double-counting. On Postgres serialisation failures the consumer requeues (treated as transient, mirroring `event_ingest.isRetryable`).

The settler is responsible for resolving worker-supplied NULLs to `0` per the "Aggregate Increment Mapping Per Kind" requirement before binding the parameters — `task_costs` columns are NOT NULL so NULLs would otherwise abort the INSERT. The `compute_seconds` aggregate is `floor(duration_ms / 1000)` for `compute` events only and `0` for `llm`/`tool` events; consequently a sub-second compute event contributes `0` to `compute_seconds` while contributing exact `amount_usd` (the divergence is intentional and tested).

**Alternative considered**: use advisory locks per `version_id`. **Rejected** — the ON CONFLICT path already serialises through the table's PK lock; advisory locks add complexity for no observed contention win.

### D7: Delivery settlement rules (DLQ vs requeue vs ack)

Mirroring `EventIngestHandler.Handle`:

| Outcome                                | Action                          |
|----------------------------------------|---------------------------------|
| Malformed body / missing required field | `Nack(false, false)` → DLQ      |
| Unknown `kind` (≠ llm/tool/compute)    | `Nack(false, false)` → DLQ      |
| `task_id` mismatch (S12 / spec §"task_id Immutable") | `Nack(false, false)` → DLQ |
| Transient pgx error (`isRetryable`)    | `Nack(false, true)` → requeue   |
| Permanent DB error (CHECK / FK / NOT NULL) | `Nack(false, false)` → DLQ  |
| Pricing missing (no row matched)       | Ack (see D5)                     |
| Success (insert + upsert both ran)     | Ack                              |
| Success (insert was duplicate-noop)    | Ack (idempotency)                |

Reusing the `isRetryable` helper from `event_ingest.go` keeps the retry policy uniform across both consumers.

**FK-violation source clarification.** The `cost_events` table has no FK on `version_id` (only `pricing_id`); the `task_costs.version_id → task_versions(id)` FK is the one that fires when a worker emits a `version_id` that does not exist. The violation surfaces at step 3 of the transaction (the `task_costs` UPSERT), aborting the whole tx — including the `cost_events` insert from step 2. Test assertions and log-line `slog.String("err", ...)` extraction MUST account for this (the offending statement is the UPSERT, not the INSERT).

**Sub-decision (formerly Open Question #2): FK violation on `version_id`.** Routes to DLQ as a permanent error. Backfill / replay tooling is Post-MVP; for the MVP we accept the data loss in exchange for not requeue-storming on a permanent mismatch.

### D8: Seed pricing in migration 0005 (renumbered)

This change set introduces two migrations:

- `0004_cost_events_kind_unique.{up,down}.sql` — drops `cost_events_run_seq_key` and creates `UNIQUE (run_id, kind, seq)` in its place; the down migration restores the original two-column index (acceptable because at rollback time we'd need to re-introduce the legacy collision behavior — that is the cost of rolling back). MUST run before any cost event is ingested.
- `0005_seed_pricing.{up,down}.sql` — loads default pricing rows.

`0005_seed_pricing.up.sql` loads rows for:

- The two models in worker `model_by_key` defaults (`claude-opus-4-7`, `claude-sonnet-4-6`) — both `per_1k_input_tokens` and `per_1k_output_tokens` with placeholder USD figures sourced from `docs/ARCHITECTURE.md` (cite the section in the migration header).
- `claude-haiku-4-5` — defensive seeding ahead of the worker config picking it up (cheap insurance; the worker doesn't emit Haiku events today, so the seeded row is dormant until the model is wired).
- Tool: `oss_fs` `per_call` at a token-cost-of-overhead figure.
- Compute: a single `per_second` placeholder for `worker` instances (small; just so the column isn't always zero).

All seed rows use `effective_at = '2024-01-01T00:00:00Z'` (deliberately far in the past so they cover any imaginable historical `occurred_at`) and `expires_at = NULL`. Future price updates ship as new `pricing` rows with their own `effective_at`, never `UPDATE`s — per the immutability convention in `task-cost-data-model`.

The `.down.sql` MUST use a tolerant predicate:

```sql
DELETE FROM pricing
WHERE id IN (<hard-coded seed ids>)
  AND NOT EXISTS (SELECT 1 FROM cost_events WHERE cost_events.pricing_id = pricing.id);
```

The naive `DELETE WHERE id IN (...)` would fail SQLSTATE 23503 in any environment that has settled even one cost event against a seeded row (because `cost_events.pricing_id REFERENCES pricing(id)` with no `ON DELETE`, defaulting to RESTRICT). The tolerant form lets the migration roll back cleanly in fresh-DB tests AND lets it complete in a live env where some seed rows are already referenced — those rows stay in place, which is exactly the immutability invariant the data-model spec already binds.

### D9: Consumer module layout

```
api/internal/
  application/cost/settler.go         # SettleEvent(in CostEventInput) -> Result
  domain/cost/pricer.go               # computeAmount(kind, qty, rows) (*big.Rat, dominantID)
  domain/cost/inputs.go               # CostEventInput, IngestResult
  infrastructure/messaging/cost_ingest.go  # DeliveryHandler (mirrors event_ingest.go)
  infrastructure/persistence/sqlc/cost_events.sql + pricing.sql additions
```

The settler is the application-layer orchestrator (load pricing → compute → write inside a tx). The HTTP layer remains untouched — no handlers in this change.

### D10: Metrics & logging

Existing `observability/metrics.go` names metrics without an `api_` prefix (`events_ingested_total`, `event_consumer_connected`, etc.). The Cost Service follows the same convention. Add:

- `cost_events_consumed_total{kind}` — counter, every delivery.
- `cost_events_settled_total{kind, result="ok"|"missing_pricing"|"duplicate"|"error"}` — counter.
- `cost_pricing_missing_total{kind,resource}` — counter (subset of above).
- `cost_amount_settled_usd_total` — counter; observe `amount_usd` per ok delivery (sum, not bucket).
- `cost_event_settle_duration_seconds` — histogram, end-to-end per delivery (buckets: `prometheus.DefBuckets`).
- `cost_consumer_connected` — gauge (0/1) **separate from** `event_consumer_connected`. The existing `Consumer.setConnected` writes to a single gauge held in `Metrics`; with two consumers sharing it, one queue's disconnect would be masked by the other's connect. Resolution: refactor `Consumer` to accept the gauge (or a setter) at construction time. Both consumers then drive their own gauge instance — see D11.

All log lines carry `trace_id`, `task_id`, `version_id`, `run_id`, `seq`, `kind` per AGENTS.md §4.1.

A startup `cost_pricing_coverage` INFO log (see the spec requirement) lists which models / tools have at least one currently-effective pricing row — the operational defense against `resource_name` typos that would otherwise produce silent `amount_usd = 0` forever (the only other signal is `cost_pricing_missing_total`, which is not alerted-on by default).

### D11: Trace propagation + per-consumer gauge wiring

The worker publisher already attaches `traceparent` as an AMQP header. The consumer reads that header (if present) and binds it to the settle transaction's slog logger + Prometheus exemplars — same pattern as event-ingest.

For the per-consumer connection gauge: extend `messaging.NewConsumer` to accept either a `prometheus.Gauge` (cleanest for two consumers) or a `gaugeSetter func(bool)` so each consumer owns its connection state. The event-ingest wiring in `cmd/api/main.go` passes `metrics.EventConsumerConnected`; the cost wiring passes the new `metrics.CostConsumerConnected`. Test fakes can pass a no-op setter.

## Risks / Trade-offs

- **[Risk]** Pricing seed migration ships hard-coded USD figures that go stale → **Mitigation**: the figures live in a migration whose header cites the source (date + ARCHITECTURE §). Updates ship as new `pricing` rows, not migrations.
- **[Risk]** A worker burst (e.g., 100 tool calls/s) outpaces consumer prefetch and grows queue depth → **Mitigation**: configurable `prefetch` (default 64) and a settle-duration histogram so we can see saturation. Re-tunable without code changes (env var).
- **[Risk]** Postgres serialisation failures under concurrent UPSERT to the same `version_id` cause repeated requeues with no broker-level cap → **Mitigation**: the per-row PK lock should already serialise enough at MVP scale; we acknowledge that `q.cost.events` (like `q.task.events`) is declared with neither `x-dead-letter-exchange` nor a `x-max-delivery-attempts` policy in `topology.go`, so a pathological loop would in principle requeue forever. Adding a per-message attempt counter (`x-death` header inspection) and a broker DLX policy is tracked as a separate Post-MVP hardening change covering both consumers.
- **[Risk]** A pricing typo (wrong unit price) flows into `amount_usd` forever → **Mitigation**: cost displays show `last_priced_at` (out of scope for this change but landing soon in `add-task-cost-api`); ops can introduce a corrective `pricing` row with a forward-dated `effective_at` and decide whether to backfill.
- **[Risk]** A `resource_name` typo (e.g. trailing space, wrong dash) silently produces `amount_usd = 0` events forever, distinguishable only via `cost_pricing_missing_total` → **Mitigation**: emit `cost_pricing_coverage` INFO at startup listing the active `(kind, resource_name)` rows so operators can sanity-check on every deploy; recommend an alert rule on `increase(cost_pricing_missing_total[10m]) > 5` in the README. Documents the failure mode operationally without changing the consumer's correctness contract.
- **[Risk]** Sub-second `compute` durations contribute exact `amount_usd` but `0` to the `compute_seconds` aggregate (D6's `floor(duration_ms / 1000)`) → **Mitigation**: this divergence is locked into the spec ("Aggregate Increment Mapping") and tested both in unit and integration tests (Task 3.4 / 7.2). Operators reading `task_costs` should treat `compute_seconds` as a coarse counter and `amount_usd` as the precise figure.
- **[Risk]** Cross-kind `seq=1` collisions on the legacy `cost_events_run_seq_key (run_id, seq)` would silently drop events. → **Mitigation**: migration `0004_cost_events_kind_unique` re-keys the constraint to `(run_id, kind, seq)` BEFORE the consumer goes live; spec MODIFIES the data-model requirement; settler uses the new triple. See D6.
- **[Trade-off]** Settling in Go (D2) means the pricing rows travel into the application. Acceptable: the data is tiny (≤ 6 rows per event), unambiguous, and the math is exhaustively unit-testable.

## Migration Plan

1. Apply `0004_cost_events_kind_unique.up.sql` FIRST — re-keys the cost-events unique constraint to `(run_id, kind, seq)` before any consumer goes live. (On a fresh DB this is a no-op since `cost_events` is empty; on an existing DB the legacy index simply hadn't been hit yet because no consumer existed.)
2. Apply `0005_seed_pricing.up.sql` to load default pricing rows.
3. Deploy the consumer with feature behavior: it idles until a `cost.<kind>` message arrives.
4. The Worker continues to emit `cost.<kind>` regardless; once the consumer is up, `task_costs` fills in for new runs.
5. **Rollback (fresh / dev DB)**: stop the consumer goroutine, run `0005_seed_pricing.down.sql`. Seed `pricing` rows are removed; `cost_events` and `task_costs` rows are retained (the down migration only removes seed `pricing` rows, not historical cost data — preserves the immutability convention).
6. **Rollback (env with consumed cost events)**: same as above; seed pricing rows that are already referenced by `cost_events` will be **skipped** by the `NOT EXISTS` predicate in `0005_seed_pricing.down.sql` and stay in place. This is intentional — the FK invariant ("historical cost records stay attributable") binds. After 0005 down completes (partial DELETE), if the operator also needs to roll back 0004, the unique index reverts to `(run_id, seq)`; this would in theory permit future cross-kind collisions, but it is acceptable as a rollback edge because the consumer code that would emit such conflicts will have been deployed-back too.

## Open Questions

1. **Should `task_costs.compute_seconds` accumulate from `llm`/`tool` events' `duration_ms` too, or only from `compute` events?** Current decision in the "Aggregate Increment Mapping" spec requirement: only `compute` events feed it, to match the column name's intent. `llm`/`tool` durations feed `wall_time_ms`. Open: do we ever want a "total billable seconds" view that sums all three? Probably yes, but it is a read-API concern (`add-task-cost-api`), not an ingest one.
2. **Should the Cost Service publish a derived event** (e.g., `cost.settled.<version_id>`) so the realtime gateway can push updates to subscribed clients? Deferred to `add-realtime-gateway`.
