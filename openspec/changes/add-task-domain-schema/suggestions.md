# Review Suggestions — `add-task-domain-schema`

Reviewed against: `proposal.md`, `design.md`, `tasks.md`, `specs/task-data-model/spec.md`, `specs/task-cost-data-model/spec.md`; cross-checked with `docs/ARCHITECTURE.md §4`, `api/` scaffold state, `api/sqlc.yaml`, `api/Makefile`, `.github/workflows/api-ci.yml`, `.github/workflows/worker-ci.yml`.

Overall: well-scoped, well-aligned with ARCHITECTURE, defers state-machine concerns correctly, and pays down the scaffold's testcontainers debt. The items below should be addressed before `/opsx:apply`.

---

## Blocking issues

### 1. `proposal.md` "no new Go modules" contradicts `tasks.md` introducing `testcontainers-go`

`proposal.md:54` says:

> **Dependencies**: no new Go modules; sqlc generates against the existing `pgx/v5` driver pinned in `sqlc.yaml`.

But `tasks.md:35` introduces `github.com/testcontainers/testcontainers-go/modules/postgres` for the integration tests. That is a new module.

**Fix**: update the proposal's impact statement to acknowledge the testcontainers dependency (it is the right tool — just be honest about adding it).

### 2. Nightly CI schedule will not fire as written

`tasks.md:44` says the new `integration-tests` job "runs on `main` + nightly via `schedule:` trigger". But `.github/workflows/api-ci.yml` currently has no `schedule:` entry in `on:`, and the task only mentions adding a job, not the trigger.

**Fix**: the task must add a `schedule:` entry to `on:` in `api-ci.yml` *as well as* the `if: github.ref == 'refs/heads/main' || github.event_name == 'schedule'` gate on the new job. Suggested cron: `'0 6 * * *'` (06:00 UTC, matching common nightly cadences).

> Side note: `.github/workflows/worker-ci.yml` has the same latent bug — its `integration-tests` job's `if:` references `schedule` but the workflow's `on:` block has no `schedule:`. Out of scope for this change but worth filing as a follow-up.

### 3. Mutex regression test (5.3) is ambiguous and may pass for the wrong reason

`tasks.md:37`:

> Add mutex regression test: two goroutines concurrently `INSERT INTO task_versions` for the same `task_id` with `status='pending'`; exactly one succeeds, the other surfaces unique-violation (SQLSTATE `23505`) with constraint name `one_active_version_per_task`.

If both inserts use the same `version_no`, they will collide on `UNIQUE (task_id, version_no)` first and the test "passes" without ever exercising the partial unique index. The spec scenario at `specs/task-data-model/spec.md:33-35` is then untested.

**Fix**: tighten the task text to:

> Seed an inactive version (e.g., `version_no=1`, `status='succeeded'`). Then two goroutines concurrently `INSERT INTO task_versions` for the same `task_id` with **different `version_no` values** (`2` and `3`) and `status='pending'`. Exactly one MUST succeed; the other MUST fail with SQLSTATE `23505` naming `one_active_version_per_task` (not the `(task_id, version_no)` UNIQUE).

Asserting the constraint **name** in the test catches the wrong-constraint trap automatically — keep that part.

---

## Inconsistencies to fix

### 4. Integration test filename mismatch between design and tasks

- `design.md:82`: `api/internal/infrastructure/persistence/persistence_integration_test.go`
- `tasks.md:35`: `api/internal/infrastructure/persistence/migrations_integration_test.go`

Pick one. `migrations_integration_test.go` is more accurate to what 5.1–5.6 actually test; update `design.md:82` to match.

### 5. `task_runs.status` enum drifts from `docs/ARCHITECTURE.md`

`specs/task-data-model/spec.md:43` lists `task_runs.status` as `{queued, running, paused, cancelling, cancelled, succeeded, failed}`. `docs/ARCHITECTURE.md:345` omits `cancelling`. Both shapes are defensible — `cancelling` mirrors `task_versions.status` and is useful for the cancel handshake — but per `AGENTS.md §1` ("conflicts with ARCHITECTURE must be resolved via doc update or explicit deviation in design"), the drift needs an owner.

**Fix**: either (a) add a one-line note in `design.md` (new D-item) explaining the refinement, or (b) update `docs/ARCHITECTURE.md §4.2` in the same change. Option (a) is lighter and matches the proposal's "schema-only" scope.

### 6. `task-cost-data-model/spec.md` scenario title contradicts its body

`specs/task-cost-data-model/spec.md:33-35`:

> #### Scenario: Pricing reference may dangle
> - **WHEN** a `cost_events` row references a `pricing_id` whose row is later deleted
> - **THEN** PostgreSQL MUST raise FK violation (SQLSTATE `23503`) at the moment of `pricing` `DELETE`...

The body asserts the FK **prevents** dangling. The title says it "may dangle". Rename to e.g. **"Pricing rows are protected from delete while referenced"** so the title reflects the asserted behavior.

---

## Worth considering (non-blocking)

### 7. Planner-prescriptive scenarios are brittle

- `specs/task-data-model/spec.md:65-67` — asserts PostgreSQL "MUST use the `(task_id, id)` index".
- `specs/task-cost-data-model/spec.md:47-49` — asserts "using the `(task_id)` index".

The planner is free to pick a sequential scan on small tables or different plans after future schema changes. Index **existence** is already verified by `tasks.md:35` ("every UNIQUE / FK / CHECK is in place"); the scenario should only assert ordering, completeness, and correctness — not the chosen plan.

**Suggested rewrites**:
- Drop "MUST use the `(task_id, id)` index" → keep "results MUST be monotonically ordered by `id`".
- Drop "using the `(task_id)` index" → keep "MUST return the scalar sum across all versions of that task".

### 8. Missing rationale in `design.md` for FK omissions

`task_events`, `cost_events`, and `task_costs` all carry `task_id` / `version_id` / `run_id` columns without FKs. This is intentional (hot append paths, avoid cross-table cascade contention) and matches ARCHITECTURE, but the design.md decisions don't say so explicitly.

**Fix**: add a short **D12** documenting the deliberate FK omission on append-only event tables. This pre-empts the obvious "why no FKs?" review question and lets future hardening proposals revisit the decision against an explicit rationale.

### 9. Outbox migration round-trip test could land here cheaply

`tasks.md:59-60` correctly notes that scaffold debt 5.4 (binary lifecycle) and 6.6 (outbox migration round-trip) are out of scope. But since this proposal **introduces** the testcontainers harness, adding a ~10-line outbox up/down round-trip closes 6.6 entirely.

**Suggested**: add a new task `5.7 Add outbox migration round-trip test (closes scaffold debt 6.6)` — small, additive, no scope creep, and cleans the slate before feature proposals start landing.

### 10. D11 should name the proposal that owns the deferred UPSERT

`design.md:90-92` (D11) defers `task_costs` writes but doesn't say who picks them up. Adding the cross-link makes the deferral fully traceable:

> ...The Cost Service proposal **(`add-task-cost-api`)** will add them.

Same treatment in D1 / proposal Out-of-scope for the 409 envelope mapping (owned by `add-task-create-api`).

---

## What's good (keep)

- **D1 mutex design**: DB-level partial unique index over a generated stored column is the right call; the rejection of app-layer alternatives is well-argued.
- **D8 CREATE+READ-only sqlc scope**: avoids speculative UPDATE queries that would rot before they're called.
- **D7 two-file migration split**: keeps cost concerns independently rollbackable.
- **D10 FK-less `tenant_id`/`user_id`**: avoids blocking on auth/multi-tenant proposals.
- **Open questions**: properly punted with named future owners (`task_type` enum, soft-delete, materialised cost view, trace context).
- **Spec scenarios**: concrete and testable — SQLSTATE codes and (where appropriate) constraint names.
- **Acceptance 8.6**: explicit about what is *not* being addressed; keeps scope honest.

---

## Summary

| # | Severity | Area | Action |
|---|----------|------|--------|
| 1 | Blocking | proposal.md | Acknowledge `testcontainers-go` in Dependencies |
| 2 | Blocking | tasks.md / api-ci.yml | Add `schedule:` to `on:` and `if:` gate |
| 3 | Blocking | tasks.md 5.3 | Tighten test: different `version_no`, assert constraint name |
| 4 | Fix | design.md vs tasks.md | Unify integration test filename |
| 5 | Fix | spec vs ARCHITECTURE | Resolve `task_runs.status` enum drift (D-item or doc update) |
| 6 | Fix | cost spec | Rename "Pricing reference may dangle" scenario |
| 7 | Consider | both specs | Drop planner-prescriptive index assertions |
| 8 | Consider | design.md | Add D12 documenting FK omissions on event tables |
| 9 | Consider | tasks.md | Add outbox round-trip test (closes scaffold debt 6.6) |
| 10 | Consider | design.md | Cross-link deferrals to owning proposals |

After 1–6 are addressed, this is ready for `/opsx:apply`.
