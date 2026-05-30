## MODIFIED Requirements

### Requirement: Cost Events Table with Per-Run Monotonic Sequence

The schema SHALL define a `cost_events` table for fine-grained LLM / tool / compute cost records. Each row MUST carry: `id BIGSERIAL PRIMARY KEY`, `task_id UUID NOT NULL`, `version_id UUID NOT NULL`, `run_id UUID NOT NULL`, `seq BIGINT NOT NULL`, `kind TEXT NOT NULL` with values `{llm, tool, compute}`, `resource_name TEXT NOT NULL`, `input_tokens BIGINT`, `output_tokens BIGINT`, `cached_tokens BIGINT`, `calls INT`, `duration_ms BIGINT`, `amount_usd NUMERIC(18,8) NOT NULL`, `pricing_id UUID REFERENCES pricing(id)`, `occurred_at TIMESTAMPTZ NOT NULL`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`.

`(run_id, kind, seq)` MUST be `UNIQUE` (replacing the original `(run_id, seq)` uniqueness from migration 0003): the Worker's `CostEventPublisher` allocates `seq` from a per-`(run_id, kind)` counter — see `worker-messaging` §"Cost Event Publisher" — so `cost.llm seq=1` and `cost.tool seq=1` for the same run are distinct events and MUST both persist. A migration `0004_cost_events_kind_unique.up.sql` SHALL drop the legacy `cost_events_run_seq_key` index and create the three-column unique constraint in its place. Indexes MUST also exist on `(task_id, occurred_at)` (for time-windowed user reporting) and `(version_id)` (for aggregation joins).

#### Scenario: Duplicate cost event rejected
- **WHEN** two `INSERT INTO cost_events` use the same `(run_id, kind, seq)`
- **THEN** PostgreSQL MUST raise unique-violation (SQLSTATE `23505`); idempotent consumers using `ON CONFLICT (run_id, kind, seq) DO NOTHING` MUST observe zero rows affected

#### Scenario: Cross-kind seq=1 events coexist for the same run
- **GIVEN** a single `run_id = R`
- **WHEN** the Worker inserts three events `(R, llm, 1)`, `(R, tool, 1)`, `(R, compute, 1)`
- **THEN** all three INSERTs MUST succeed and three rows MUST be present in `cost_events`

#### Scenario: Pricing rows are protected from delete while referenced
- **WHEN** a `cost_events` row references a `pricing_id` and a `DELETE FROM pricing WHERE id = $that_pricing_id` is issued
- **THEN** PostgreSQL MUST raise FK violation (SQLSTATE `23503`) at the moment of the `pricing` `DELETE`; we deliberately do not cascade — historical cost records must stay attributable

## ADDED Requirements

### Requirement: Pricing Reference Convention for Multi-Unit Cost Events

A `cost_events` row settled against multiple `pricing` rows (e.g., an `llm` event whose `amount_usd` aggregates `per_1k_input_tokens` and `per_1k_output_tokens`) carries a single `pricing_id` FK. The application convention SHALL select the **dominant unit** row, by `kind`:

- `llm` → the `pricing` row for `per_1k_input_tokens` (or `per_1k_output_tokens` if no input-tokens row matched, or `per_1k_cached_tokens` otherwise).
- `tool` → the `pricing` row for `per_call` (or `per_second` if no `per_call` row matched).
- `compute` → the `pricing` row for `per_second`.
- When no pricing row matched any unit → `pricing_id` MUST be `NULL` and `amount_usd` MUST be `0`.

This is a convention, not a DB constraint: forensic queries reconstruct the full unit breakdown from `(occurred_at, kind, resource_name)` against `pricing`. The Cost Service is the sole writer of `cost_events` and the sole enforcer of this convention.

#### Scenario: Dominant pricing_id for LLM input+output
- **GIVEN** an `llm` event settled with both `per_1k_input_tokens` and `per_1k_output_tokens` rows in force
- **WHEN** the `cost_events` row is inserted
- **THEN** `pricing_id` MUST equal the id of the `per_1k_input_tokens` row, AND the `amount_usd` MUST be the sum of both per-unit terms

#### Scenario: NULL pricing_id when nothing matched
- **GIVEN** no `pricing` row exists for the event's `(resource_kind, resource_name)` at `occurred_at`
- **WHEN** the `cost_events` row is inserted
- **THEN** `pricing_id` MUST be `NULL` AND `amount_usd` MUST be `0`

### Requirement: Task Costs `task_id` is Immutable Per `version_id`

The `task_costs.task_id` column SHALL be treated as immutable for a given `version_id`. The Cost Service's UPSERT statement MUST NOT assign `task_id` in its `ON CONFLICT DO UPDATE SET ...` clause (only the aggregate columns + `updated_at` are updated). Additionally, before the UPSERT, the settler MUST verify that the worker-supplied `task_id` matches `SELECT task_id FROM task_versions WHERE id = $version_id`; a mismatch MUST be treated as a permanent error (DLQ), not a silent overwrite.

Rationale: `task_versions.task_id` is the source of truth for which task owns a version. A worker bug or a malicious actor that sends mismatched ids must not be able to migrate a `task_costs` row between tasks.

#### Scenario: Mismatched task_id routes to DLQ
- **GIVEN** `task_versions` row `(id=V, task_id=T0)`
- **WHEN** a `cost.llm` event arrives with `task_id=T1, version_id=V`
- **THEN** the consumer MUST detect the mismatch, `Nack(false, false)` the delivery (DLQ), increment `cost_events_settled_total{result="error"}`, and log ERROR with both ids

#### Scenario: UPSERT does not change task_id on conflict
- **GIVEN** an existing `task_costs` row `(version_id=V, task_id=T0, input_tokens=2000)`
- **WHEN** a subsequent event correctly carrying `(task_id=T0, version_id=V, input_tokens=500)` settles
- **THEN** the resulting row MUST be `(version_id=V, task_id=T0, input_tokens=2500)` — `task_id` was not assigned by the UPSERT (and remains `T0`)

### Requirement: Default Pricing Seed

The schema migration set MUST include a seed migration (`0005_seed_pricing.up.sql` / `.down.sql`) that loads default `pricing` rows for every model the worker may emit in `model_by_key` defaults (today: `claude-opus-4-7` and `claude-sonnet-4-6`) plus `claude-haiku-4-5` (defensive — pre-seeded ahead of the worker config picking it up) and for the built-in `oss_fs` tool, with `effective_at = '2024-01-01T00:00:00Z'` (deliberately back-dated to cover historical events) and `expires_at = NULL`. The migration header MUST cite the price source and date. Price updates SHALL ship as new `pricing` rows with later `effective_at`, never as in-place `UPDATE`s of the seeded rows.

The `.down.sql` MUST be tolerant of references from `cost_events`: it MUST `DELETE FROM pricing WHERE id IN (<hard-coded ids>) AND NOT EXISTS (SELECT 1 FROM cost_events WHERE cost_events.pricing_id = pricing.id)`. Rows already referenced by historical cost events stay in place — by design, to preserve the "historical cost records must stay attributable" invariant (per the existing FK-protection scenario above).

#### Scenario: Seeded prices are queryable after migrate up
- **WHEN** `api migrate up` is executed against a fresh database
- **THEN** at least one `pricing` row MUST exist for `(llm, claude-opus-4-7, per_1k_input_tokens)`, AND `GetEffectivePricing` at `now()` MUST return it

#### Scenario: Seed migration round-trips on a fresh DB
- **WHEN** `api migrate up` then `api migrate down` then `api migrate up` is executed against a fresh database (no consumed cost_events)
- **THEN** the seeded `pricing` rows MUST be present after each up step and absent after the down step, AND `schema_migrations.dirty` MUST be `false`

#### Scenario: Seed down preserves rows referenced by cost_events
- **GIVEN** a `cost_events` row exists with `pricing_id` equal to a seeded row's id
- **WHEN** `api migrate down` is executed for `0005_seed_pricing`
- **THEN** the referenced `pricing` row MUST remain in place (the `AND NOT EXISTS ...` predicate excluded it), AND no FK violation MUST be raised
