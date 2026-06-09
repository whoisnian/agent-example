## Context

Rollback is specified in `docs/ARCHITECTURE.md §6.5` and listed in the `§5` API table, but never built. Everything it needs already exists:

- **Version tree**: `task_versions.parent_id REFERENCES task_versions(id)` (strict tree for MVP), `version_no` unique per task.
- **Task-level mutex**: the partial unique index `one_active_version_per_task` over the generated `is_active` column (`status IN ('pending','queued','running','paused','cancelling')`). So a version is either **active** or **terminal** (`succeeded`/`failed`/`cancelled`) — those two sets exactly partition the 8 statuses.
- **Create-active-version machinery**: `Service.createActiveVersion` (`domain/task/service.go`) does the savepoint-wrapped `task_versions` INSERT (translating the mutex `23505` → `ErrActiveVersionExists`), the `task_runs` INSERT, the `buildExecutePayload`, and the `execute.<type>.<lane>` outbox row. `IterateTask` already calls it with an explicit base version.
- **Owner-scoped lock**: `LockTaskForControl` (`queries/tasks.sql`) returns `id/status/current_version` under a `WHERE id = $1 AND tenant_id = $2 AND user_id = $3` predicate; `ErrTaskNotFound` (→404) on miss. `task-control-api` uses it.
- **Error mapping**: `version_not_found`(404), `active_version_exists`(409, with `data`), `task_not_found`(404), `invalid_input`(400) are all wired in `interfaces/http/errors.go` + the iterate handler.

`branch` mode is therefore "iterate with the base pinned to a chosen historical version"; `switch` mode is a pointer move.

## Goals / Non-Goals

**Goals:**
- `POST /tasks/{id}/rollback` with `branch` and `switch` modes, owner-scoped, reusing the existing mutex/outbox/execute machinery.
- Preserve every existing invariant: the DB mutex is the source of truth; `task-event-ingest` stays the sole **run-driven** writer of `tasks.status`; owner-scoped 404 (never 403).

**Non-Goals:**
- Merge / non-tree DAG semantics; a `--no-execute` flavour of `branch` (the architecture mentions it, but `switch` already covers the no-execute case).
- A dedicated audit table for rollback (`§9`).
- Retro-fixing the iterate endpoint's pre-existing missing owner check (out of scope; not a drive-by).
- Any worker or web change.

## Decisions

### Decision 1 — `branch` reuses `createActiveVersion`; the only new input is the pinned parent

`branch` runs the same transaction as `iterate`:
1. `LockTaskForControl(owner, taskID)` → owner-scoped row lock (`ErrTaskNotFound` → 404). **This query MUST be widened to also return `task_type`** (today it returns only `id/status/current_version`): `createActiveVersion` needs `task_type` for the execute payload and the `execute.<type>.<lane>` topic, and the iterate flow currently sources it from the **unscoped** `GetTaskByID` (`WHERE id = $1`). Widening the single owner-scoped lock means `branch` never issues an unscoped read. `task-control-api`'s existing `Apply` ignores the new column (it reads only `status`/`current_version`), so widening is non-breaking.
2. App-level mutex pre-check: if `IsActive(task.status)`, return `ErrActiveVersionExists` (enriched via `GetActiveVersionByTask`) → 409. (The savepoint `23505` path in `createActiveVersion` remains the belt-and-suspenders source of truth.)
3. Resolve the target: `GetVersionByTaskAndID(target_version_id, taskID)` — confirms the version belongs to the task (else `ErrVersionNotFound` → 404). Its `artifact_root` becomes `parent_artifact_root` for the execute payload.
4. `versionNo = MaxVersionNoForTask + 1`.
5. `createActiveVersion(parentVersionID = target, parentArtifactRoot = target.artifact_root, taskType = locked.task_type, prompt, ...)` → INSERT version (+run +execute outbox).
6. `UpdateTaskCurrentVersion(current_version = new, status = 'pending')` (the existing query).

Response mirrors iterate: `201 {version_id, version_no, status: "pending"}`. The worker consumes the execute message exactly as for iterate — **no worker change**.

- **Alternative considered — extend `IterateTask` with a `rollback` flag:** rejected. Rollback is a distinct endpoint/verb with its own metric, the `switch` mode has no iterate analogue, and overloading iterate would muddy two specs. A sibling `RollbackTask` method that shares `createActiveVersion` keeps the reuse without conflating the public contracts.

### Decision 2 — `branch` prompt: auto-fill when empty

`§6.5` says the new version's prompt may be "空或自动生成 'rollback to V_k'". `validatePrompt` rejects empty, so when the request omits `prompt` (or sends `""`) the service substitutes `"rollback to version <target_version_no>"` before validation. A caller MAY supply a real prompt to steer the re-run ("以 v_k 为起点继续走").

### Decision 3 — `switch` repoints `current_version` ONLY; never writes `tasks.status`

`switch` does, in one owner-scoped transaction:
1. `LockTaskForControl(owner, taskID)` → 404 on miss.
2. Non-active guard (see Decision 4) → 409 if active.
3. `GetVersionByTaskAndID(target_version_id, taskID)` → 404 if the target isn't in the task.
4. **Assert the target is terminal**: `GetVersionByTaskAndID` does `SELECT *`, so the target row carries the DB-computed generated column `is_active`. If `target.is_active` is true, return `ErrInvalidState` → `409 invalid_state` ("cannot switch to a non-terminal version"). This is an explicit, self-contained requirement — not a fact derived from the precondition — so `switch`'s safety does not depend on the `tasks.status`→version-status projection (see Decision 4).
5. `SwitchTaskCurrentVersion(taskID, target)` — a **new** query: `UPDATE tasks SET current_version = $2, updated_at = now() WHERE id = $1`. It does **not** touch `tasks.status`.

Response: `200 {current_version_id, version_no, status}` where `status` is the task's existing status taken from the **already-locked row** (step 1) — not written and not re-SELECTed (`switch` does not change it).

Why no status write: `task-control-api` spec §"…MUST NOT directly update tasks.status… The sole writer of those columns is task-event-ingest". The create/iterate paths legitimately *seed* `tasks.status='pending'` when spawning a run, but `switch` spawns no run, so it has no business writing status. Leaving `tasks.status` at the last terminal outcome is coherent: `tasks.status` describes "the last execution outcome", `current_version` describes "the working base"; they are orthogonal, and the next `iterate`/`branch` re-seeds status normally. This keeps the sole-writer invariant intact and avoids inventing an API status-write path.

**Reader-visible consequence (must be documented, not hidden):** after a `switch` from a `failed` v3 to a `succeeded` v2, `task-read-api` returns `data.task.status = "failed"` AND `data.current_version.status = "succeeded"` — they legitimately diverge. The detail page reads both; the **list/dashboard badge (`GET /tasks`) shows only `tasks.status`**, so it reflects "last execution outcome", not "working base". This is an intended product behavior, and the spec states it explicitly so the web (and any reader) treats the divergence as correct, not a bug. A future refinement could surface the current version's status in the list, but that is a web concern, out of scope here.

- **Alternative considered — `switch` sets `tasks.status = target.status`:** rejected. It makes the list badge match the working base, but introduces an API writer of `tasks.status` outside run-seeding, in tension with the control-api sole-writer invariant. Deferred: if the list-badge divergence proves confusing in practice, revisit then rather than weaken the invariant now.

### Decision 4 — Both modes require a NON-active task (409 otherwise)

`branch` already requires it (it creates an active version; the mutex rejects a second). `switch` adds the **same** precondition deliberately:

`task-event-ingest` transitions `tasks.status` only "when the event's `version_id` equals the owning task's `current_version`" (its CAS is gated on `current_version`). If `switch` moved `current_version` away from a still-running version, that run's subsequent status events would no longer match the gate, silently freezing `tasks.status` while the run keeps going. Requiring the task to be non-active (the user cancels or lets the run finish first) removes the hazard entirely.

In practice this also makes the `switch` target terminal — the single active version (the mutex allows only one) is always `current_version` (create/iterate/branch all set `current_version` to the new active version), and its status keeps `tasks.status` in the active set, so `IsActive(tasks.status) = false` ⟹ no active version exists. But note this is an *over-approximation* argument over a lossy projection (`tasks.status` has 6 values; version status has 8 — a `cancelling` version maps to no `tasks.status` write, leaving the task showing `running`). Rather than rely on that subtlety, `switch` does **not** derive target-terminality from the precondition — it asserts `!target.is_active` explicitly (Decision 3, step 4) using the DB-computed column. So the guard is robust even if the status projection changes later.

The architecture's "switch 不受任务级互斥约束" still holds in its intended sense (switch never *creates* an active version and is never blocked by the unique index); we add an orthogonal "no concurrent active run" guard for status-sync safety. The web already disables rollback while `task.status` is active, so this 409 is the backstop, not the common path.

### Decision 5 — Owner-scoping and error surface

The handler reads the principal (`principalOrAbort`) and passes `Owner{TenantID, UserID}` to the service; the owner predicate lives inside `LockTaskForControl` so unknown and unowned both yield `404 task_not_found` (never 403), consistent with read/control. Reused error codes: `400 invalid_input` (bad `task_id`/`target_version_id` UUID, missing `target_version_id`, unknown `mode`, malformed `params`), `404 task_not_found`, `404 version_not_found`, `409 active_version_exists` (with the existing `{active_version_id, active_version_status}` `data` block), and `409 invalid_state` (the defensive non-terminal-`switch`-target case, Decision 3). No new codes.

**Status-code convention** (pinned to foreclose re-litigation): `branch` → `201` mirroring `iterate`/`create` (a new version *resource* is created; the async execute downstream does NOT push it to `202`, exactly as iterate is `201` for the same shape). `switch` → `200` (a pointer mutation that creates no resource; not `201` since nothing is created, not `202` since the `current_version` write is synchronous and there is no async follow-up). Control's `202` is for a fire-and-forget signal, which neither rollback mode is.

### Decision 6 — Layering, query, and tests

- **Domain**: add `RollbackTask(ctx, owner, RollbackInput) (RollbackOutput, error)` to the write `Service` (it already owns `createActiveVersion`). `RollbackInput{TaskID, TargetVersionID, Mode, Prompt, Params, Lane}`; `Mode ∈ {branch, switch}` validated in-domain (re-asserting the HTTP guard).
- **Query**: `SwitchTaskCurrentVersion :exec` in `queries/tasks.sql`; `make sqlc` regen.
- **Application**: `RollbackTaskCommand` + `Service.RollbackTask` thin wrapper (mirrors `IterateTask`).
- **HTTP**: rollback handler in `interfaces/http/` registering `POST /tasks/:task_id/rollback`; a `tasks_rolled_back_total{mode, outcome}` counter that **extends** (not mirrors) the single-label `tasks_iterated_total{outcome}` pattern with an added `mode` label. Every increment site supplies both labels — including pre-`mode`-parse `400`s, which use `mode="unknown"` — replicating the iterate handler's discipline of labelling on the parse-error paths too.
- **Tests**: domain unit tests (branch happy → version+run+outbox; branch on active → 409; switch happy → only `current_version` moves, `status`/version rows untouched, no outbox; unknown target → 404; unowned task → 404; bad mode → 400). HTTP contract tests for each status/shape. An integration test (testcontainers) asserting `branch` writes an `execute` outbox row + `switch` writes none and leaves `tasks.status` unchanged.

## Risks / Trade-offs

- **[`switch` leaves `tasks.status` showing a different version's outcome]** → accepted and documented (Decision 3): status = "last execution outcome", `current_version` = "working base"; orthogonal, and re-seeded on the next run. Avoids an out-of-band status writer.
- **[Requiring non-active for `switch` is stricter than a literal reading of §6.5]** → deliberate (Decision 4) for status-sync safety; reconcile the doc wording in tasks so §6.5 reflects the implemented guard rather than diverging silently (AGENTS §1).
- **[`branch` 23505 vs app pre-check race]** → already handled: the savepoint path in `createActiveVersion` is the real arbiter; the app pre-check is only for a friendlier message. Reused unchanged.
- **[Iterate's missing owner check is not fixed here]** → out of scope; rollback is owner-scoped correctly from the start, and the gap is logged as pre-existing rather than drive-by-patched.

## Migration Plan

Pure additive API change: new endpoint + one new query, no schema migration, no MQ/worker contract change. Rollback safety: revert the api commits; nothing persisted by the feature outlives a revert (no new tables/columns).

## Open Questions

- None blocking. The deferred `add-worker-rollback-handling` may turn out to be a no-op for `branch` (the worker already handles the execute message); that change can confirm/close it.
