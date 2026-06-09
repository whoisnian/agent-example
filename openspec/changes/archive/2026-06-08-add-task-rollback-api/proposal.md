## Why

The MVP core loop is "创建 / 执行 / 实时观测 / 控制 / 迭代 / **回滚** / 成本统计" (`docs/ARCHITECTURE.md §1`), and rollback is the one remaining unbuilt verb: create, iterate, control, cost, and the version tree all ship, but `POST /tasks/{id}/rollback` (`§5` API table, `§6.5`) has no implementation. Users can iterate forward but cannot return to a historical version — either to re-run from it (`branch`) or to make it the working base without re-executing (`switch`). The version tree, mutex, outbox, and execute path needed to support it are all already in place.

## What Changes

- Add `POST /api/v1/tasks/{task_id}/rollback` accepting `{target_version_id, mode, prompt?, params?, lane?}` with two modes (`§6.5`):
  - **`branch`** — create a new version whose `parent_id` is the target historical version, then run it (INSERT `task_versions` + `task_runs` + execute `outbox`, point `tasks.current_version` at it, seed `tasks.status=pending`). Behaviourally identical to `iterate` with the base fixed to the chosen historical version; subject to the task-level mutex (409 if a version is active). When `prompt` is empty the server auto-fills `"rollback to version <n>"`.
  - **`switch`** — repoint `tasks.current_version` at the target historical version **only**: no new version, no run, no execute message. Returns the restored version.
- **Owner-scope** the endpoint via the request principal (owner-scoped lock + `404 task_not_found` for unknown/unowned, never 403), matching `task-control-api`.
- **Both modes require a non-active task**: if any version is active, return `409 active_version_exists`. For `branch` this is the existing mutex; for `switch` it is a deliberate guard — moving `current_version` while a run's status-sync CAS is gated on it (per `task-event-ingest`) would silently desync `tasks.status` (see design).
- `switch` writes **only** `tasks.current_version` (+ `updated_at`), never `tasks.status` — preserving `task-event-ingest` as the sole run-driven writer of `tasks.status`. `switch` explicitly asserts the target version is terminal (via its DB `is_active` flag; `409 invalid_state` otherwise) rather than inferring it from the precondition. A documented consequence: `tasks.status` (last execution outcome) and the now-current version's status may legitimately diverge, and the task-list badge — which shows only `tasks.status` — reflects the last outcome, not the working base.
- **No worker change**: `branch` emits the same `execute.*` message as `iterate` (carrying the target's `artifact_root` as `parent_artifact_root`), which the worker already handles; `switch` involves no worker at all.
- **Out of scope (not now):** merge/DAG semantics beyond the existing strict tree; a `--no-execute` variant of `branch`; auditing rollback into a dedicated audit table (`§9` audit log is a separate concern); any change to the iterate endpoint's pre-existing lack of owner-scoping (not fixed here to avoid drive-by scope).

## Capabilities

### New Capabilities
- `task-rollback-api`: the `POST /tasks/{id}/rollback` endpoint — its two modes (`branch` re-executes from a historical version under the task mutex; `switch` repoints `current_version` only), owner-scoping, the non-active precondition shared by both modes, and the `switch`-writes-only-`current_version` rule.

## Impact

- **Code (api):** `interfaces/http/` (new rollback handler + request/response types, route registration); `application/task/` (new `RollbackTask` command + service method); `domain/task/` (new `RollbackTask` on the write `Service`, reusing `createActiveVersion` for `branch`); `queries/tasks.sql` (new `SwitchTaskCurrentVersion` — `current_version` + `updated_at` only, no status); regenerated sqlc; metrics (a `tasks_rolled_back_total{mode,outcome}` counter mirroring `tasks_iterated_total`).
- **No new error codes**: reuses `version_not_found` (404), `active_version_exists` (409), `task_not_found` (404), `invalid_input` (400) — all already mapped.
- **No schema migration**: uses the existing `task_versions.parent_id` tree and `tasks.current_version`; no new tables/columns.
- **No worker / MQ contract change**: `branch` reuses the `execute` outbox message shape; `switch` publishes nothing.
- **Web (later):** a follow-up wires the rollback buttons (the control-bar already disables iterate/rollback while active); not in this change.
