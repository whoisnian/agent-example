# task-cost-data-model Specification

## Purpose
TBD - created by archiving change add-task-domain-schema. Update Purpose after archive.
## Requirements
### Requirement: Pricing Table with Immutable Historical Records

The schema SHALL define a `pricing` table holding unit prices for LLMs, tools, and compute. Each row MUST carry: `id UUID PRIMARY KEY`, `resource_kind TEXT NOT NULL` with values `{llm, tool, compute}`, `resource_name TEXT NOT NULL`, `unit TEXT NOT NULL` (e.g., `per_1k_input_tokens`, `per_1k_output_tokens`, `per_call`, `per_second`), `unit_price_usd NUMERIC(18,8) NOT NULL`, `effective_at TIMESTAMPTZ NOT NULL`, `expires_at TIMESTAMPTZ` (nullable; open-ended row when null).

The composite `(resource_kind, resource_name, unit, effective_at)` MUST be `UNIQUE` so two rows cannot claim to start at the same instant for the same resource+unit. A `CHECK` constraint MUST enforce `expires_at > effective_at` when `expires_at IS NOT NULL`.

Historical pricing MUST never be retroactively modified: a once-published row is treated as immutable by convention; price changes ship as new rows with a later `effective_at`. Database-level enforcement is limited (we accept `UPDATE` as a possibility to fix mistakes), but no application code SHALL emit such an `UPDATE` — the contract is documented and code-reviewed.

#### Scenario: Effective pricing lookup
- **WHEN** a client queries `SELECT * FROM pricing WHERE resource_kind = $1 AND resource_name = $2 AND unit = $3 AND effective_at <= $4 AND (expires_at IS NULL OR expires_at > $4) ORDER BY effective_at DESC LIMIT 1`
- **THEN** PostgreSQL MUST return the row that was in force at the `$4` timestamp, or no rows if none was

#### Scenario: Duplicate effective-window rejected
- **WHEN** two `INSERT INTO pricing` use the same `(resource_kind, resource_name, unit, effective_at)`
- **THEN** PostgreSQL MUST raise unique-violation (SQLSTATE `23505`)

#### Scenario: Invalid expiry rejected
- **WHEN** an `INSERT INTO pricing` provides `effective_at >= expires_at`
- **THEN** PostgreSQL MUST raise the CHECK violation (SQLSTATE `23514`)

### Requirement: Cost Events Table with Per-Run Monotonic Sequence

The schema SHALL define a `cost_events` table for fine-grained LLM / tool / compute cost records. Each row MUST carry: `id BIGSERIAL PRIMARY KEY`, `task_id UUID NOT NULL`, `version_id UUID NOT NULL`, `run_id UUID NOT NULL`, `seq BIGINT NOT NULL`, `kind TEXT NOT NULL` with values `{llm, tool, compute}`, `resource_name TEXT NOT NULL`, `input_tokens BIGINT`, `output_tokens BIGINT`, `cached_tokens BIGINT`, `calls INT`, `duration_ms BIGINT`, `amount_usd NUMERIC(18,8) NOT NULL`, `pricing_id UUID REFERENCES pricing(id)`, `occurred_at TIMESTAMPTZ NOT NULL`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`.

`(run_id, seq)` MUST be `UNIQUE` so duplicate cost-event deliveries are rejected. Indexes MUST exist on `(task_id, occurred_at)` (for time-windowed user reporting) and `(version_id)` (for aggregation joins).

#### Scenario: Duplicate cost event rejected
- **WHEN** two `INSERT INTO cost_events` use the same `(run_id, seq)`
- **THEN** PostgreSQL MUST raise unique-violation (SQLSTATE `23505`); idempotent consumers using `ON CONFLICT (run_id, seq) DO NOTHING` MUST observe zero rows affected

#### Scenario: Pricing rows are protected from delete while referenced
- **WHEN** a `cost_events` row references a `pricing_id` and a `DELETE FROM pricing WHERE id = $that_pricing_id` is issued
- **THEN** PostgreSQL MUST raise FK violation (SQLSTATE `23503`) at the moment of the `pricing` `DELETE`; we deliberately do not cascade — historical cost records must stay attributable

### Requirement: Task Costs Aggregation Table

The schema SHALL define a `task_costs` table maintaining one row per version with running aggregates. Each row MUST carry: `version_id UUID PRIMARY KEY REFERENCES task_versions(id)`, `task_id UUID NOT NULL`, `input_tokens BIGINT NOT NULL DEFAULT 0`, `output_tokens BIGINT NOT NULL DEFAULT 0`, `cached_tokens BIGINT NOT NULL DEFAULT 0`, `tool_calls INT NOT NULL DEFAULT 0`, `wall_time_ms BIGINT NOT NULL DEFAULT 0`, `compute_seconds BIGINT NOT NULL DEFAULT 0`, `amount_usd NUMERIC(18,8) NOT NULL DEFAULT 0`, `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`. An index on `(task_id)` MUST exist for task-level sum queries.

The Cost Service is the **only** authorised writer to this table — populated via `UPSERT` from consumed `cost_events`. Application code outside the Cost Service MUST NOT `INSERT` / `UPDATE` here; the contract is documented and code-reviewed (DB-level GRANTs are deferred to a deployment-hardening proposal).

#### Scenario: One row per version
- **WHEN** two `INSERT INTO task_costs` use the same `version_id`
- **THEN** PostgreSQL MUST raise unique-violation (SQLSTATE `23505`); the Cost Service's `INSERT ... ON CONFLICT (version_id) DO UPDATE` MUST resolve cleanly

#### Scenario: Task-level sum query
- **WHEN** a client queries `SELECT SUM(amount_usd) AS total FROM task_costs WHERE task_id = $1`
- **THEN** PostgreSQL MUST return the scalar sum across all `task_costs` rows whose `task_id` matches (or `NULL` if none exist). (Index existence on `(task_id)` is asserted separately by the structural integration test; the chosen query plan is the planner's prerogative.)

### Requirement: Cost Schema Migrations Apply Cleanly Both Ways

The migration introducing the cost schema (`0003_init_cost_domain.up.sql` / `.down.sql`) MUST apply, roll back, and re-apply against a clean PostgreSQL 18.4 database with no residue, in the same manner as the task-domain migration.

#### Scenario: Up → down → up is idempotent
- **WHEN** `api migrate up` then `api migrate down` then `api migrate up` is executed against a fresh database
- **THEN** the final `schema_migrations.dirty` MUST be `false`, AND every table named in this spec MUST be present with the declared columns, constraints, and indexes

