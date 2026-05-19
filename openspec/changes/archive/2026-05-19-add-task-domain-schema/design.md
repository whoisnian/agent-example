## Context

`docs/ARCHITECTURE.md §4` fixes the data model at a logical level. The api-scaffold (archived `2026-05-18-init-api-scaffold`) landed the migration tool, pgxpool, sqlc pipeline, and `outbox` table. This proposal turns the logical model into actual PostgreSQL DDL and generates the first round of sqlc query types so feature proposals can land HTTP and worker code without re-debating column shapes.

Constraints inherited from architecture:
- Task-level mutex must be enforced at the DB (no application-layer-only solution), per the iteration flow in ARCHITECTURE §6.4.
- Worker may only write to `task_runs.last_heartbeat`, `task_checkpoints`, `artifacts` (AGENTS.md §4.2 / worker-execution-runtime spec).
- Cost Service is the sole writer of `cost_events` and `task_costs`; Worker emits events via MQ.
- Outbox stays raw-pgx (api-persistence D2). No business code may bypass sqlc.

Open questions resolved by this proposal:
- "Where do `tenants` / `users` tables live?" — Not here. `tasks.tenant_id` / `tasks.user_id` are bare UUID columns. Auth and multi-tenant proposals add the FK targets.
- "DAG or tree for versions?" — Tree. The schema permits a future DAG with one column change (`parent_id` is nullable today, no merge column yet). MVP keeps it strict-tree.
- "Where does pricing immutability live — DB triggers, app code, or convention?" — Convention + code review. DB triggers are overkill for MVP; we revisit if pricing-tampering becomes a real risk.

## Goals / Non-Goals

**Goals**
- Land the canonical schema for tasks, versions, runs, events, checkpoints, artifacts, pricing, cost_events, task_costs in a single reviewable change.
- Make the task-level mutex a DB-enforced invariant, not an app-layer check; ship a testcontainers integration test that proves it.
- Generate sqlc types for the CREATE + READ paths that the next batch of business proposals will need on day one.
- Pay down the testcontainers-Postgres integration-test debt accrued by the api scaffold (deferred items 5.4 / 6.6).

**Non-Goals**
- HTTP handlers, Domain Services, state machine code — `add-task-create-api` and friends.
- API-layer 409 envelope mapping — also `add-task-create-api`. This proposal ships the constraint that backs it.
- Worker code changes — none needed; the existing helpers in `worker/core/persistence.py` start succeeding once these tables exist.
- `tenants` / `users` / `worker_registry` tables — own proposals.
- Spec writes / UPDATE / state-transition queries — owned by the proposals that introduce the state machine.
- Cost computation logic (price × tokens) — that's Cost Service code in `add-task-cost-api`.

## Decisions

### D1. One DB-level mutex constraint, no app-layer fallback

`task_versions` carries a generated stored column `is_active BOOLEAN GENERATED ALWAYS AS (status IN ('pending','queued','running','paused','cancelling')) STORED` and a `UNIQUE INDEX one_active_version_per_task ON task_versions(task_id) WHERE is_active`. Alternatives considered:

- **App-layer SELECT FOR UPDATE then INSERT**: requires advisory locking or strict transaction isolation; loses the invariant if any rogue path bypasses the check. Rejected.
- **Stateful "active version" pointer on `tasks`**: simpler conceptually but invariant lives in app code. Rejected.
- **Trigger that raises on conflict**: works but is opaque to query plans; harder to debug. Rejected.

Partial unique index gives us O(1) lookups, automatic release on terminal status, zero app-code involvement, and clear error semantics (`23505` with constraint name). The matching 409 envelope mapping is owned by `add-task-create-api`.

### D2. Generated columns over computed UPDATE triggers

`is_active` is a `STORED` generated column rather than a derived column maintained by a trigger. Reasons: PostgreSQL evaluates it inside the row, so the unique partial index can reference it; no trigger to forget; immutable view of the data. The expression is pure (`status IN (...)`) so it's allowed in `STORED`.

### D3. UUID v7 / v4 — caller's choice, not enforced

Schema uses `UUID` everywhere. Generation strategy is caller-side: api will use `uuid` (Google's UUID v4) for now; switching to time-ordered v7 is a one-line change in the caller without DB-level migration. This proposal does not pick.

### D4. `task_runs.idempotency_key` is the consumer's primary dedup tool

`(idempotency_key TEXT NOT NULL UNIQUE)` lets workers issue `INSERT ... ON CONFLICT (idempotency_key) DO NOTHING RETURNING id` for the four-branch claim from `worker-messaging`. This is more ergonomic than per-attempt UNIQUE constraints and matches how the worker scaffold already uses the field.

### D5. JSONB everywhere mutable-shape data lands

`task_versions.params`, `task_runs.error`, `task_events.payload`, `task_checkpoints.state` are all JSONB. Rationale: payload shape evolves per task-type / agent-version, and we don't want schema migrations for every new event kind. We DO normalise the things we query on regularly (timestamps, FK ids, run_id, kind) as proper columns.

### D6. NUMERIC(18,8) for amount_usd

8 decimal places lets us represent fractional-cent token costs without rounding artefacts. NUMERIC over DOUBLE PRECISION because we never want binary float drift on a money value.

### D7. Migration packaging: two files, two concerns

- `0002_init_task_domain.{up,down}.sql` — tasks, versions, runs, events, checkpoints, artifacts, mutex index.
- `0003_init_cost_domain.{up,down}.sql` — pricing, cost_events, task_costs.

Two migrations rather than one to keep cost concerns isolable (e.g., if a deployment needs to roll back cost changes without dropping task tables). Each `down` removes only what its `up` added, with `DROP ... CASCADE` strictly limited to FK-dependent objects we own.

### D8. sqlc query files — CREATE/READ only

We add only the queries the next batch of feature proposals needs to compile and read. State-mutating queries (UPDATE status, SET current_version, etc.) belong with the proposal that introduces the state machine — landing them here would be speculative and rot before they're called.

Files added under `api/queries/`:
- `tasks.sql`, `task_versions.sql`, `task_runs.sql`, `task_events.sql`, `task_checkpoints.sql`, `artifacts.sql`, `pricing.sql`, `task_costs.sql`.

`outbox.sql` stays untouched.

### D9. Integration tests live with this proposal

The api scaffold deferred testcontainers-Postgres integration tests (items 5.4, 6.6). This proposal is the right home: we're adding schema, and integration tests are the only credible way to prove a schema does what it says. Tests live at `api/internal/infrastructure/persistence/migrations_integration_test.go` behind the `//go:build integration` tag so `make test` skips them and `make test-integration` runs them.

CI: api-ci.yml gains a second job `integration-tests` that runs on pushes to `master` only — no `schedule:` cron. PR feedback stays fast; integration regressions surface the moment a feature lands on `master`. (The worker-ci.yml `integration-tests` job is realigned to the same gate in this change; the earlier `event_name == 'schedule'` clause was a latent bug since neither workflow had a `schedule:` trigger.)

### D10. `tenant_id` / `user_id` columns without FKs

`tasks.tenant_id UUID NOT NULL` and `tasks.user_id UUID NOT NULL` are mandatory but FK-less. MVP single-tenant uses a sentinel UUID (`00000000-0000-0000-0000-000000000001` or similar — decided by `add-task-create-api`). When the auth and multi-tenant proposals land, they MAY add `REFERENCES users(id)` / `REFERENCES tenants(id)` constraints via an ALTER. The current shape is final.

### D11. No `RETURNING` queries for COST writes (yet)

Cost Service writes are append-only INSERTs on `cost_events` and UPSERTs on `task_costs`. Until that service has a Go implementation (it's currently described in architecture but not yet a proposal), we don't generate UPDATE queries for `task_costs`. `add-task-cost-api` will add them.

### D12. No FKs on append-only event tables

`task_events.task_id` / `version_id` / `run_id`, `cost_events.task_id` / `version_id` / `run_id`, and `task_costs.task_id` are intentionally NOT `REFERENCES`-bound to their parent rows. Reasons:

- These tables sit on the hot append path; FK checks would cost an index probe per insert against `tasks` / `task_versions` / `task_runs`.
- Cascade semantics on `tasks` / `task_versions` deletion are not what we want — we never delete those rows from app code, and if we ever do for cleanup, we don't want cost / event history vapourised with them.
- The application is the single writer; orphan rows can only arise from bugs we'd notice immediately via `ListEventsAfter` / `GetTaskCost` returning rows that fail to join.

Exceptions where we DO keep FKs:
- `task_versions.parent_id REFERENCES task_versions(id)` — same-table tree integrity, no cascade pressure.
- `task_versions.task_id REFERENCES tasks(id)` — task deletion is a deliberate admin operation; we want it loud.
- `task_runs.version_id REFERENCES task_versions(id)`, `task_checkpoints.run_id REFERENCES task_runs(id)`, `artifacts.version_id REFERENCES task_versions(id)`, `task_costs.version_id REFERENCES task_versions(id)`, `cost_events.pricing_id REFERENCES pricing(id)` — narrow parent-child relationships, low-frequency parent mutations.

A future hardening proposal may revisit this once we have load profiles.

### D13. `task_runs.status` includes `cancelling` (refinement vs ARCHITECTURE)

`docs/ARCHITECTURE.md §4.2` lists `task_runs.status` values as `{queued, running, paused, cancelled, succeeded, failed}`. This proposal adds `cancelling` between `running` and `cancelled`. Rationale:

- The cancel handshake described in ARCHITECTURE §6.3 has a measurable "we received the signal, we're winding down to a clean checkpoint" window. A dedicated status makes that observable from the API side without polling event logs.
- `task_versions.status` already includes `cancelling` in ARCHITECTURE; aligning `task_runs.status` removes a needless asymmetry.
- The CHECK constraint stays narrow either way; adding `cancelling` is a strict superset, so future ARCHITECTURE.md update (or any business proposal that wants to use the state) is non-breaking.

This deviation is documented here in lieu of patching ARCHITECTURE in a schema-only change. A future doc-sweep proposal may fold this back.

## Risks / Trade-offs

- **[Risk] `is_active` generated column breaks if status enum changes** → Mitigation: the enum is enforced via CHECK constraint on `task_versions.status`; any future enum change is a migration that updates the CHECK and (if needed) the `is_active` expression atomically. Documented in the spec.
- **[Risk] Pricing immutability is convention-only** → Mitigation: enforced in code review for now; future hardening proposal can add column-level GRANTs or row-level security rules.
- **[Risk] `task_versions.parent_id` cycle is structurally possible** → Mitigation: parent_id can only reference rows that already exist (FK), and the application sets it once at INSERT and never updates it. The schema permits cycles only via deliberate UPDATE; reviewers reject such code. A future hardening proposal can add a recursive CTE check or trigger if needed.
- **[Risk] BIGSERIAL on `task_events.id` and `cost_events.id` will be hot under load** → Mitigation: BIGSERIAL is fine for MVP throughput (we expect ≤ 100 events/s aggregate). Revisit when we add Citus-style sharding (post-MVP).
- **[Risk] JSONB blobs grow unbounded** → Mitigation: `task_checkpoints.state` already has the inline budget (8 KiB per worker-execution-runtime spec) with overflow going to OSS. `task_events.payload` is bounded by application code (event kind contracts). `task_runs.error` is small by construction.
- **[Risk] Integration tests need Docker, which CI may not provide everywhere** → Mitigation: the api-ci `integration-tests` job runs only on pushes to `master`. PR feedback stays fast; integration regressions surface at merge time. No scheduled / cron execution — deliberate decision to keep CI minutes predictable and to make every integration run causally tied to a code change.

## Migration Plan

Forward path: `make migrate-up` applies `0002` then `0003` in order. Each is wrapped in a single transaction (PostgreSQL `BEGIN/COMMIT` around the file); a failure inside leaves `schema_migrations.dirty=true` and the `down` half can be invoked.

Rollback path:
- `make migrate-down` rolls back the most recent migration. Targeted multi-step rollback uses `api migrate force <version>` then `migrate up` from the target.
- Production safety: rollbacks happen only when the application code that depends on the schema has been rolled back too. The schema's correctness post-rollback is verified by the integration test (`up → down → up`).

No data migration is needed — this proposal lands schema for tables that are currently empty.

## Open Questions

1. **Should `tasks.task_type` be an enum or a free text column?** Currently free text; future proposals introducing specific types (`code-gen`, `research`) may want a CHECK constraint or a separate `task_types` lookup table. Punted to the proposal that introduces the second task type.
2. **Do we want a `tasks.deleted_at` soft-delete column from day one?** Not yet. Hard-delete is fine for MVP; soft-delete can be added later without breaking schema.
3. **Do we materialise the task-level cost as a view, or rely on `SELECT SUM` per request?** Punted to `add-task-cost-api`. Both are valid; depends on hot-path latency targets we don't yet have.
4. **Should `task_events.payload` include trace context (`trace_id`)?** Currently it's a free JSONB field; we'll have the convention "the application includes trace context inline in payload". A future proposal may add a typed column if we find ourselves filtering on it.
