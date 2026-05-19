## Why

The api scaffold landed PostgreSQL connectivity and the `outbox` table but no business tables. Every upcoming feature proposal — `add-task-create-api`, `add-task-iterate-api`, `add-task-cost-api`, `add-worker-code-agent`, `add-web-tasks-pages` — needs the same canonical data model: tasks, versions, runs, events, checkpoints, artifacts, plus the cost-side tables. Landing schema first, in its own focused change, prevents each feature proposal from re-litigating column types, FK directions, and index choices.

This proposal is **schema-only**: tables, indexes, generated sqlc code, plus the testcontainers migration tests that the api scaffold deferred. No HTTP handlers, no Domain Service state machines, no worker changes. The DB-level invariants (task-level mutex via unique partial index, idempotency via `(run_id, seq)` uniques, immutable historical pricing) ship here so feature proposals build on a guarantee, not a checklist.

## What Changes

- New PostgreSQL migrations under `api/migrations/`:
  - `0002_init_task_domain.{up,down}.sql` — `tasks`, `task_versions`, `task_runs`, `task_events`, `task_checkpoints`, `artifacts`, plus the `one_active_version_per_task` unique partial index and the `is_active` generated column it depends on.
  - `0003_init_cost_domain.{up,down}.sql` — `pricing`, `cost_events`, `task_costs`.
- `tasks.tenant_id` and `tasks.user_id` are bare `UUID NOT NULL` columns with no FK targets yet (the `tenants` / `users` tables arrive with their own proposals — auth and multi-tenant). MVP uses a sentinel tenant UUID; the column shape is final.
- Add sqlc query files under `api/queries/` for the **CREATE + READ** paths needed by the next batch of business proposals:
  - `queries/tasks.sql` — `CreateTask`, `GetTaskByID`, `ListTasks`
  - `queries/task_versions.sql` — `CreateTaskVersion`, `GetTaskVersionByID`, `ListVersionsByTask`
  - `queries/task_runs.sql` — `CreateTaskRun`, `GetTaskRunByID`
  - `queries/task_events.sql` — `InsertTaskEvent` (idempotent on `(run_id, seq)`), `ListEventsAfter`
  - `queries/task_checkpoints.sql` — `SelectLatestCheckpoint`
  - `queries/artifacts.sql` — `ListArtifactsByVersion`
  - `queries/pricing.sql` — `GetEffectivePricing`
  - `queries/task_costs.sql` — `GetVersionCost`, `GetTaskCost`
- Run `make sqlc` and commit the generated code under `api/internal/infrastructure/persistence/sqlc/`.
- Land the testcontainers-PostgreSQL integration tests previously deferred from `init-api-scaffold`:
  - migrate up → down → up clean, `schema_migrations.dirty = false`
  - every new table has its declared columns + indexes
  - **unique partial index `one_active_version_per_task` rejects concurrent inserts** at the DB level (the load-bearing test for the mutex invariant)
  - duplicate `(run_id, seq)` rejected for `task_events` and `cost_events`
  - duplicate `(run_id, step_seq)` rejected for `task_checkpoints`
- Update `api/queries/outbox.sql` only if sqlc regeneration requires it (it currently does not — outbox stays raw pgx per `api-persistence` D2).

Out of scope for this proposal (deferred to follow-ups):
- HTTP handlers, Domain Services, state machine code — `add-task-create-api`
- API-layer mutex check + 409 envelope mapping — `add-task-create-api` (this proposal ships only the DB constraint that backs it)
- Worker dispatcher / agent implementations — `add-worker-code-agent`
- Frontend pages — `add-web-tasks-pages`
- `tenants` / `users` / `worker_registry` tables — own proposals
- Spec writes / business UPDATE queries — added by the proposal that introduces the state machine

## Capabilities

### New Capabilities

- `task-data-model`: Canonical schema for tasks, versions, runs, events, checkpoints, artifacts; the version-tree invariant; the task-level mutex via DB-enforced unique partial index; per-run idempotency keys.
- `task-cost-data-model`: Pricing-table semantics (no retroactive change via `effective_at`), `cost_events` idempotency keyed on `(run_id, seq)` per kind, `task_costs` 1:1 aggregation per version.

### Modified Capabilities

(none — extends `api-persistence` by following its existing sqlc + migration rules, no requirement changes)

## Impact

- **Code**: `api/migrations/` grows from 1 pair to 3 pairs of SQL files; `api/queries/` adds 8 files; `api/internal/infrastructure/persistence/sqlc/` gains its first generated business code (Querier interface + row types for ~12 queries).
- **Dependencies**: adds `github.com/testcontainers/testcontainers-go/modules/postgres` (test-only) for the integration suite. sqlc generates against the existing `pgx/v5` driver pinned in `sqlc.yaml`.
- **CI**: api-ci's existing unit lane is unaffected. The new integration lane (testcontainers-postgres) is added as a `worker-ci.yml`-style separate job — gated `if: github.ref == 'refs/heads/master'`, skipped on PRs, **no `schedule:` cron**. The worker-ci.yml `integration-tests` job is realigned to the same gate (it referenced `schedule` while having no `schedule:` trigger — a latent dead-code bug).
- **Cross-service contract**: Worker writes to `task_runs.last_heartbeat` / `task_checkpoints` / `artifacts` (per `worker-execution-runtime`) start succeeding once this lands. Worker code itself is unchanged.
- **Downstream**: unblocks `add-task-create-api`, `add-task-iterate-api`, `add-task-cost-api`, `add-worker-code-agent`, `add-web-tasks-pages`.
