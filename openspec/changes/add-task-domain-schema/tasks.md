## 1. Task-Domain Migration

- [ ] 1.1 Author `api/migrations/0002_init_task_domain.up.sql` creating, in order: `tasks` (with CHECK on status enum + composite index `(tenant_id, user_id, status)`), `task_versions` (incl. `is_active` generated stored column + `UNIQUE (task_id, version_no)` + index `(task_id, parent_id)`), `task_runs` (incl. UNIQUE on `idempotency_key`, UNIQUE on `(version_id, attempt_no)`, index on `(status, last_heartbeat)`), `task_events` (UNIQUE `(run_id, seq)` + index `(task_id, id)`), `task_checkpoints` (UNIQUE `(run_id, step_seq)`), `artifacts`
- [ ] 1.2 Author the matching `api/migrations/0002_init_task_domain.down.sql` (DROP in reverse FK order; no `CASCADE` beyond FK-dependents we own)
- [ ] 1.3 Add the unique partial index `CREATE UNIQUE INDEX one_active_version_per_task ON task_versions (task_id) WHERE is_active` at the end of the `up` file (placed after `task_versions` itself so it can reference the generated column)
- [ ] 1.4 Confirm `CHECK` constraints on `tasks.status` and `task_versions.status` match the enums declared in the spec
- [ ] 1.5 Verify the migration applies cleanly: `make migrate-up` against a fresh local postgres returns successfully; `\d+ task_versions` in psql shows `is_active` as `GENERATED ALWAYS AS ... STORED`

## 2. Cost-Domain Migration

- [ ] 2.1 Author `api/migrations/0003_init_cost_domain.up.sql` creating `pricing` (UNIQUE `(resource_kind, resource_name, unit, effective_at)` + CHECK on `expires_at > effective_at` when not null), `cost_events` (UNIQUE `(run_id, seq)` + indexes on `(task_id, occurred_at)` and `(version_id)`, FK on `pricing_id`), `task_costs` (primary key on `version_id`, FK to `task_versions`, index on `(task_id)`)
- [ ] 2.2 Author the matching `.down.sql`
- [ ] 2.3 Verify `make migrate-up` runs both migrations in order from a clean DB; verify `make migrate-down` rolls them back in reverse order

## 3. sqlc Configuration

- [ ] 3.1 Confirm `api/sqlc.yaml` points at `queries/` and emits into `internal/infrastructure/persistence/sqlc/` (already configured by the api scaffold)
- [ ] 3.2 Confirm the schema source list includes the new migration files (sqlc 1.27+ picks up everything in `migrations/` by default; verify in `make sqlc` output)

## 4. sqlc Query Files (CREATE + READ only)

- [ ] 4.1 `api/queries/tasks.sql`: `CreateTask` (`:one`), `GetTaskByID` (`:one`), `ListTasks` (`:many`, with optional status filter + pagination)
- [ ] 4.2 `api/queries/task_versions.sql`: `CreateTaskVersion` (`:one`), `GetTaskVersionByID` (`:one`), `ListVersionsByTask` (`:many`, ordered by `version_no`)
- [ ] 4.3 `api/queries/task_runs.sql`: `CreateTaskRun` (`:one`), `GetTaskRunByID` (`:one`), `GetRunByIdempotencyKey` (`:one`)
- [ ] 4.4 `api/queries/task_events.sql`: `InsertTaskEvent` (`:exec`, `ON CONFLICT (run_id, seq) DO NOTHING`), `ListEventsAfter` (`:many`, by `task_id` + `id > $`)
- [ ] 4.5 `api/queries/task_checkpoints.sql`: `SelectLatestCheckpoint` (`:one`, ordered by `step_seq DESC LIMIT 1`)
- [ ] 4.6 `api/queries/artifacts.sql`: `ListArtifactsByVersion` (`:many`)
- [ ] 4.7 `api/queries/pricing.sql`: `GetEffectivePricing` (`:one`, the `effective_at <= $now AND (expires_at IS NULL OR expires_at > $now)` query)
- [ ] 4.8 `api/queries/task_costs.sql`: `GetVersionCost` (`:one`), `GetTaskCost` (`:one`, returns `SUM(amount_usd), SUM(input_tokens), ...` grouped by `task_id`)
- [ ] 4.9 Run `make sqlc`; commit the generated `*.sql.go` files under `internal/infrastructure/persistence/sqlc/`
- [ ] 4.10 Run `go build ./...` after generation; fix any sqlc-emitted type names that collide with existing types in `internal/`

## 5. Integration Tests (pays down api-scaffold debt)

- [ ] 5.1 Add `api/internal/infrastructure/persistence/migrations_integration_test.go` (build tag `//go:build integration`) using `testcontainers-go` postgres module; fixture starts postgres:18.4, runs the migration tool against it, asserts: every table from both specs exists, every UNIQUE / FK / CHECK is in place (`pg_constraint`), `is_active` is a generated column (`pg_attribute.attgenerated = 's'`)
- [ ] 5.2 Add round-trip test: `up → down → up` is idempotent; `schema_migrations.dirty = false` afterwards
- [ ] 5.3 Add mutex regression test: seed an inactive version (e.g. `version_no=1`, `status='succeeded'`), then have two goroutines concurrently `INSERT INTO task_versions` for the same `task_id` with **different `version_no` values** (`2` and `3`) and `status='pending'`. Exactly one MUST succeed; the other MUST fail with SQLSTATE `23505` whose constraint name is **`one_active_version_per_task`** (asserting the constraint name catches the failure mode where `(task_id, version_no)` UNIQUE accidentally fires first)
- [ ] 5.4 Add active-set transition test: insert an active version, transition it to `succeeded`, insert another active version for the same `task_id` — second insert succeeds
- [ ] 5.5 Add idempotency tests: `(run_id, seq)` collision for `task_events`; `(run_id, seq)` collision for `cost_events`; `(run_id, step_seq)` collision for `task_checkpoints`; all surface SQLSTATE `23505` with the expected constraint name
- [ ] 5.6 Add pricing test: two pricing rows with identical `(resource_kind, resource_name, unit, effective_at)` → unique violation; pricing row with `expires_at <= effective_at` → check violation
- [ ] 5.7 Add outbox migration round-trip test (closes scaffold debt 6.6 since we now have the testcontainers harness): apply `0001` up → down → up against a fresh PostgreSQL 18.4; assert `outbox` table + `(status, next_retry_at)` index present after each `up`, absent after `down`; `schema_migrations.dirty = false` throughout

## 6. CI

- [ ] 6.1 Update `.github/workflows/api-ci.yml`:
  - add a `schedule:` entry to the workflow's top-level `on:` block (suggested cron `'0 6 * * *'` = 06:00 UTC nightly)
  - add a second job `integration-tests` that depends on `vet-lint-test-build` and is gated by `if: github.ref == 'refs/heads/main' || github.event_name == 'schedule'`
  - the job sets up Docker (ubuntu-latest provides it out of the box), pins Go 1.26 (same as the existing job), and runs `make test-integration`
- [ ] 6.2 Verify `make test-integration` exists in `api/Makefile` (added by the api scaffold) — no change needed if it already runs `go test -tags=integration -race -count=1 ./...`
- [ ] 6.3 (Out of scope — file as follow-up) `.github/workflows/worker-ci.yml` carries the same latent bug (its `integration-tests` job's `if:` references `schedule` but `on:` has no `schedule:`). Capture as a TODO note when opening the PR for this change so it gets a dedicated `chore(ci):` fix

## 7. Documentation

- [ ] 7.1 Update `api/README.md` to mention the new tables under "目录结构" / "关键不变量"; explicitly note "互斥由 DB 唯一部分索引 `one_active_version_per_task` 兜底"; mention `make test-integration` requires Docker
- [ ] 7.2 No changes needed in `docs/ARCHITECTURE.md` (the schema this proposal lands is already documented there)

## 8. Acceptance

- [ ] 8.1 `make migrate-up` against a fresh local postgres applies migrations `0001 → 0002 → 0003` cleanly
- [ ] 8.2 `make migrate-down` rolls all three back without residue (verify with `\dt` showing only `schema_migrations`)
- [ ] 8.3 `make sqlc` regenerates without changes after a fresh checkout; `go build ./...` and `go vet ./...` clean
- [ ] 8.4 `make test` (unit lane) stays green
- [ ] 8.5 `make test-integration` (locally with Docker) green; every spec scenario across both spec files has a corresponding integration test passing
- [ ] 8.6 The api-scaffold acceptance items previously deferred (5.4 binary lifecycle, 6.6 outbox migration round-trip) are NOT addressed here — those remain explicitly out of scope; only schema-domain integration tests land
