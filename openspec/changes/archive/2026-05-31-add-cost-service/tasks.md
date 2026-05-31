## 1. Migrations

- [x] 1.1 Add `api/migrations/0004_cost_events_kind_unique.up.sql`: `DROP INDEX cost_events_run_seq_key; CREATE UNIQUE INDEX cost_events_run_kind_seq_key ON cost_events (run_id, kind, seq);` (header explains the per-run-per-kind seq contract from `worker-messaging`).
- [x] 1.2 Add `api/migrations/0004_cost_events_kind_unique.down.sql`: restore the legacy `(run_id, seq)` index. Header notes that the restored two-column form is for rollback only.
- [x] 1.3 Add `api/migrations/0005_seed_pricing.up.sql` loading `pricing` rows (with hard-coded UUIDs, `effective_at = '2024-01-01T00:00:00Z'`, `expires_at = NULL`): `claude-opus-4-7` and `claude-sonnet-4-6` (worker `model_by_key` defaults) plus `claude-haiku-4-5` (defensive pre-seed), each with `per_1k_input_tokens` and `per_1k_output_tokens`; `oss_fs` `per_call`; one `worker` `per_second` compute row. Migration header cites the source/date.
- [x] 1.4 Add `api/migrations/0005_seed_pricing.down.sql`: `DELETE FROM pricing WHERE id IN (<hard-coded ids>) AND NOT EXISTS (SELECT 1 FROM cost_events WHERE cost_events.pricing_id = pricing.id);` (FK-safe rollback).
- [x] 1.5 Extend `migrations_integration_test.go` to assert: (a) post-0004-up, `cost_events_run_kind_seq_key` exists and the legacy `cost_events_run_seq_key` does not; (b) post-0005-up, seed rows exist and `GetEffectivePricing` at `now()` returns the opus input-tokens row; (c) full up→down→up round-trip on a fresh DB leaves `schema_migrations.dirty=false` with the expected state at each step; (d) on a DB where a `cost_events` row references a seed pricing id, running `0005_down` preserves that pricing row (no FK violation, predicate skip).

## 2. SQL & sqlc surface

- [x] 2.1 Add `ListEffectivePricings :many` to `api/queries/pricing.sql` (returns all pricing rows for `(resource_kind, resource_name)` whose window covers `$3 = occurred_at`, ordered by `effective_at DESC`). Window predicate uses `effective_at <= $3 AND (expires_at IS NULL OR expires_at > $3)` — right-exclusive on `expires_at`.
- [x] 2.2 Create `api/queries/cost_events.sql` with `InsertCostEvent :one` that uses `INSERT ... ON CONFLICT (run_id, kind, seq) DO NOTHING RETURNING input_tokens, output_tokens, cached_tokens, calls, duration_ms, amount_usd, pricing_id` (caller uses sql.ErrNoRows / pgx.ErrNoRows as the "duplicate" signal).
- [x] 2.3 Add `UpsertVersionCost :exec` to `api/queries/task_costs.sql` performing the column-wise `task_costs.col + EXCLUDED.col` UPSERT keyed on `version_id` (with `updated_at = now()`). The `DO UPDATE SET` list deliberately omits `task_id` (immutability per `task-cost-data-model`).
- [x] 2.4 Add `GetVersionOwnerTaskID :one` to `api/queries/task_versions.sql` (or extend existing): `SELECT task_id FROM task_versions WHERE id = $1` for the cross-check before UPSERT. (May already exist — reuse if so.)
- [x] 2.5 Run `make sqlc-generate` (or equivalent) and commit `querier.go` + `pricing.sql.go` + `cost_events.sql.go` + `task_costs.sql.go` + `task_versions.sql.go` regenerations.

## 3. Domain & application layers

- [x] 3.1 Add `api/internal/domain/cost/inputs.go` with `CostEventInput` (decoded shape from worker `CostEvent` — pgtype.UUID for ids, int64 ptrs for nullable quantities, time.Time for occurred_at) and `SettleResult` (`Kind` ∈ `ok`/`duplicate`/`missing_pricing`/`error_mismatch`, plus `AmountUSD *big.Rat` for metrics).
- [x] 3.2 Add `api/internal/domain/cost/pricer.go` implementing `ComputeAmount(kind, qty, rows) (amount *big.Rat, dominantPricingID *uuid.UUID)` per the spec's per-kind formulas. Apply the dominant-unit rule from `task-cost-data-model` §"Pricing Reference Convention". Treat NULL quantity fields as zero.
- [x] 3.3 Add `api/internal/domain/cost/aggregate.go` implementing `ResolveIncrements(kind, ev)` returning the per-kind `task_costs` column values (NULL→0 coercion, `tool_calls = calls ?? 1` for tool, `compute_seconds = floor(duration_ms/1000)` for compute only) per the spec's "Aggregate Increment Mapping Per Kind" table.
- [x] 3.4 Add `api/internal/application/cost/settler.go` orchestrating: `GetVersionOwnerTaskID` cross-check (mismatch → return `error_mismatch`); `ListEffectivePricings`; `ComputeAmount`; open tx → `InsertCostEvent` → if no row returned → return `duplicate`; else `ResolveIncrements` + `UpsertVersionCost` → commit. Returns `SettleResult`.
- [x] 3.5 Unit-test `pricer.ComputeAmount` exhaustively: each kind, presence/absence of each unit, NULL quantities, multi-window pricing (latest `effective_at` wins, abutting windows at `occurred_at` exactly), dominant-id selection rules, zero-amount-when-no-pricing case. Include sub-second compute (`duration_ms=800` → amount exact via 0.8 × per_second).
- [x] 3.6 Unit-test `ResolveIncrements`: NULL→0 coercion; `tool_calls = 1` when worker sends `calls=NULL`; `compute_seconds=0` for sub-second compute; `wall_time_ms` flows only for llm/tool; cross-kind contamination test (llm event must yield `tool_calls=0`).
- [x] 3.7 Settler unit test — deferred to section 7 integration coverage. Rationale: the settler is pure orchestration glue over `Queries`/`Pool`; every math/mapping branch is already covered by `pricer_test.go` + `aggregate_test.go`, and the existing codebase pattern (`task.Service.IngestEvent`) has no fake-Queries unit test either — the tx flow is exercised via testcontainers in section 7.

## 4. Messaging consumer (DeliveryHandler)

- [x] 4.1 Add `api/internal/infrastructure/messaging/cost_ingest.go` with `CostIngestHandler` implementing `DeliveryHandler` (mirror `event_ingest.go`'s ack/nack pattern); reuse `isRetryable` for transient-vs-permanent classification.
- [x] 4.2 In `Handle`: decode body → if malformed or unknown `kind` → Nack(false,false) → DLQ; else call `Settler.Settle(ctx, in)`; map errors per the "Delivery Settlement Rules" requirement table (including `error_mismatch` → DLQ, `missing_pricing` → ack, `duplicate` → ack, transient → requeue, permanent → DLQ).
- [x] 4.3 Wire trace propagation: read AMQP `traceparent` header, bind it onto the per-delivery `slog.Logger` (matches existing event-ingest convention).
- [x] 4.4 Unit-test the handler with `fakeAck` + a fake `Settler` covering each settle result and each error class (mirrors `event_ingest_test.go` exactly), asserting the exact ack/nack call and the metrics incremented.

## 5. Observability

- [x] 5.1 Add to `api/internal/infrastructure/observability/metrics.go` (no `api_` prefix, matching existing style): `CostEventsConsumedTotal` (CounterVec by `kind`), `CostEventsSettledTotal` (CounterVec by `kind, result`), `CostPricingMissingTotal` (CounterVec by `kind, resource`), `CostAmountSettledUSDTotal` (Counter), `CostEventSettleDurationSeconds` (Histogram with `prometheus.DefBuckets`), `CostConsumerConnected` (Gauge).
- [x] 5.2 Plumb metric increments through `CostIngestHandler.Handle` and `Settler.Settle`. `CostAmountSettledUSDTotal` binds via `(*big.Rat).Float64` (sum-only metric, exact value stays in DB).
- [x] 5.3 Refactor `messaging.NewConsumer` to accept the connection-state gauge (`prometheus.Gauge`) at construction time so each consumer drives its own gauge. Update the existing event-ingest wiring in `cmd/api/main.go` to pass `metrics.EventConsumerConnected`. New cost wiring passes `metrics.CostConsumerConnected`. Migrate existing test fakes to pass `prometheus.NewGauge(...)` or a no-op stand-in.
- [x] 5.4 Implement the `cost_pricing_coverage` INFO log: on consumer startup, after subscribe succeeds, run `SELECT DISTINCT resource_kind, resource_name FROM pricing WHERE effective_at <= now() AND (expires_at IS NULL OR expires_at > now())` and log the result list at INFO. One query at boot; not on the hot path.

## 6. Wiring & lifecycle

- [x] 6.1 In `cmd/api/main.go`, after the event-ingest consumer is started, construct `costSettler := costapp.NewSettler(queries, db)` and `costHandler := messaging.NewCostIngestHandler(costSettler, logger, metrics)`, then start a second consumer goroutine on `messaging.QueueCostEvents` (passing `metrics.CostConsumerConnected` to `NewConsumer`) with prefetch from env `COST_INGEST_PREFETCH` (default 64).
- [x] 6.2 Ensure graceful shutdown waits for in-flight cost settlements before closing the AMQP connection (extend the existing shutdown sequence).
- [x] 6.3 Log a startup line (`cost_ingest_started`) with `prefetch`, `queue`, `consumer_tag`. Emit the `cost_pricing_coverage` log immediately after.

## 7. Tests (mirrors task-event-ingest patterns)

- [x] 7.1 Add `api/internal/infrastructure/messaging/cost_ingest_test.go` (no build tag — pure handler unit test). Uses `fakeAck` and a stub `Settler` to assert ack/nack/metric paths for every row of the "Delivery Settlement Rules" table.
- [x] 7.2 Add `api/internal/infrastructure/persistence/cost_settler_integration_test.go` (`//go:build integration`) using testcontainers `postgres:18.4-alpine`. Drive `costapp.Settler.Settle` directly (no broker container — the broker round-trip is already covered by the topology test). Cover:
  - (a) happy LLM with input+output prices → `cost_events` row inserted with dominant `pricing_id`; `task_costs` row UPSERTed with summed `amount_usd`.
  - (b) tool event with `per_call` only → `wall_time_ms` increments, `tool_calls` increments by `calls=1`, `compute_seconds=0`.
  - (c) compute event with `duration_ms=800` → `amount_usd` exact (`0.8 × per_second`), `compute_seconds += 0`.
  - (d) missing-pricing: assert `pricing_id=NULL`, `amount_usd=0`, token columns still incremented, `cost_pricing_missing_total` bumped.
  - (e) duplicate (R, llm, 1) delivered twice → second is no-op; `task_costs.input_tokens` unchanged after the second.
  - (f) cross-kind same-seq (R, llm, 1) + (R, tool, 1) both persist (new uniqueness boundary).
  - (g) mismatched `task_id` → `error_mismatch` returned, no rows persisted.
  - (h) abutting-windows pricing: `occurred_at` exactly equals an `expires_at` → that row excluded, the successor row used.

## 8. Documentation

- [x] 8.1 Update `api/README.md` with a new `## 成本结算（task-cost-ingest）` section: queue name, consumer prefetch knob (`COST_INGEST_PREFETCH`), DLQ behavior, link to spec. Include the recommended alert rule snippet: `increase(cost_pricing_missing_total[10m]) > 5`.
- [x] 8.2 Add a brief note in `docs/ARCHITECTURE.md` under the Cost Service paragraph pointing at the implemented capability (one line, no spec duplication).
