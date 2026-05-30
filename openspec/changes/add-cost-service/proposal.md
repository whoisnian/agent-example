## Why

The Worker is already emitting `cost.<kind>` events to RabbitMQ (`cost.exchange`, queue `q.cost.events`), and the data model (`pricing` / `cost_events` / `task_costs`) is in place. But nothing on the API side consumes those events — so `task_costs` is permanently empty and the read endpoints (`add-task-read-api`) render `amount_usd` as `"0.00000000"` for every task. This change adds the **Cost Service**: the queue consumer that prices each cost event, inserts a `cost_events` row, and UPSERTs the per-version aggregate. It completes the "成本统计" MVP goal end-to-end and unblocks the upcoming `add-task-cost-api` read endpoints.

## What Changes

- Add an API-side queue consumer subscribed to `q.cost.events` that, for each delivery: decodes the worker `CostEvent` envelope, verifies `task_id` against `task_versions`, resolves all effective pricing rows at `occurred_at`, computes `amount_usd`, writes `cost_events` and UPSERTs `task_costs` inside a single transaction.
- Add pricing-resolution logic that combines per-unit rows: for `llm` events, sum `(input_tokens, output_tokens, cached_tokens)` × matching `per_1k_*` pricing rows; for `tool` events, `calls × per_call + duration_ms/1000 × per_second`; for `compute` events, `duration_ms/1000 × per_second`.
- Add migration `0004_cost_events_kind_unique.{up,down}.sql` re-keying the cost-events idempotency boundary from `UNIQUE (run_id, seq)` to `UNIQUE (run_id, kind, seq)` — required because the worker allocates `seq` per `(run_id, kind)`, so the legacy two-column key would silently collide `cost.llm seq=1` with `cost.tool seq=1` for the same run.
- Add `INSERT cost_events ... ON CONFLICT (run_id, kind, seq) DO NOTHING RETURNING ...` and gate the `task_costs` UPSERT on the insert actually happening (idempotency under at-least-once delivery).
- Add migration `0005_seed_pricing.{up,down}.sql` loading MVP-default `pricing` rows (the two models in worker `model_by_key` defaults + `claude-haiku-4-5` as defensive pre-seed, × `per_1k_input_tokens` + `per_1k_output_tokens`; plus `oss_fs per_call`; plus a `worker per_second`). The `.down.sql` uses an `AND NOT EXISTS (... cost_events ...)` predicate so rollback is FK-safe in any environment that has already settled events against the seed rows.
- Add new sqlc queries: `InsertCostEvent :one` (ON CONFLICT DO NOTHING RETURNING), `UpsertVersionCost :exec`, `ListEffectivePricings :many` (a single row-set scan per occurred_at + kind, narrower than per-unit `GetEffectivePricing` to avoid 3 round-trips per LLM event).
- Add metrics following the codebase's existing no-prefix convention: `cost_events_consumed_total{kind}`, `cost_events_settled_total{kind, result}`, `cost_pricing_missing_total{kind, resource}`, `cost_amount_settled_usd_total`, `cost_event_settle_duration_seconds`, `cost_consumer_connected` (gauge, **per-queue** — does not share the existing `event_consumer_connected` gauge).
- Refactor `messaging.NewConsumer` to accept the connection-state gauge at construction time so each consumer owns its own gauge (otherwise wiring two consumers would make `event_consumer_connected` lie).
- Wire the new consumer into `cmd/api/main.go` alongside the existing event-ingest consumer; emit a `cost_pricing_coverage` INFO log at startup listing the active `(kind, resource_name)` pairs in `pricing` (guardrail against `resource_name` typos producing silent zero amounts).
- Spec changes:
  - New capability `task-cost-ingest` for the consumer + settlement contract (subscription, settlement math, idempotency on the new triple, missing-pricing handling, per-kind aggregate mapping, `task_id` immutability check, DLQ/requeue rules, metrics).
  - Modify `task-cost-data-model`:
    - MODIFY the "Cost Events Table with Per-Run Monotonic Sequence" requirement to declare `(run_id, kind, seq)` uniqueness with the `0004_cost_events_kind_unique` migration.
    - ADD a "Pricing Reference Convention for Multi-Unit Cost Events" requirement (the dominant-unit rule for `pricing_id`).
    - ADD a "Task Costs `task_id` is Immutable Per `version_id`" requirement.
    - ADD a "Default Pricing Seed" requirement covering the 0005 seed migration and the FK-safe down predicate.

## Capabilities

### New Capabilities

- `task-cost-ingest`: API-side consumer of worker `cost.*` MQ events; resolves `pricing`, writes `cost_events`, UPSERTs `task_costs`. Idempotent on `(run_id, seq)`.

### Modified Capabilities

- `task-cost-data-model`: clarify the `pricing_id` selection rule on multi-unit cost_events rows (dominant unit, or NULL if no pricing matched at `occurred_at`); add a "missing pricing is non-fatal" requirement so the cost_events row persists with `amount_usd = 0` and the consumer logs WARN + bumps a metric.

## Impact

- New code: `api/internal/application/cost/` (settler), `api/internal/domain/cost/` (pricing + amount math), `api/internal/infrastructure/messaging/cost_ingest.go` (DeliveryHandler), `api/queries/cost_events.sql` + `pricing.sql` additions.
- New migrations: `api/migrations/0004_cost_events_kind_unique.{up,down}.sql` and `api/migrations/0005_seed_pricing.{up,down}.sql`.
- Touches `cmd/api/main.go` (start the second consumer, emit `cost_pricing_coverage` startup log), `infrastructure/observability/metrics.go` (new metrics + per-queue connection gauge), `infrastructure/messaging/consumer.go` (`NewConsumer` accepts a connection-state gauge), `api/queries/task_costs.sql` (UPSERT statement).
- No worker changes — the worker side already emits the events this consumer reads.
- No API HTTP surface changes — read endpoints will start returning non-zero values automatically once events flow through.
- Unblocks `add-task-cost-api` (the `/tasks/{id}/cost` etc. read endpoints can ship once `task_costs` is being populated).
- No new external dependencies; reuses existing `amqp091-go`, `pgx/v5`, `slog`, prometheus instrumentation.
