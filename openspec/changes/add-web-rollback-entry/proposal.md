## Why

The rollback backend shipped end-to-end (`POST /api/v1/tasks/{id}/rollback`, both `branch` and `switch` modes — `task-rollback-api`), but the web app has **no entry point**: `features/tasks/api.ts` exposes create / iterate / control only, and `VersionTree` carries no actions (presentational aside from its local expand state). Rollback is a `[MVP]` capability in `docs/ARCHITECTURE.md` (§功能清单, §6.5) and ARCHITECTURE §4.3 / lines 151 & 788 explicitly call for the web to surface it with a task-level mutex. Without a UI, a shipped MVP path is unreachable to users.

## What Changes

- Add a `rollbackTask` HTTP wrapper (`features/tasks/api.ts`) and a `useRollbackTaskMutation` (`features/tasks/mutations.ts`), opting out of the global error toast like iterate/control so the page handles `409` / `invalid_input` inline.
- Add the rollback request/response DTO mirrors to `features/tasks/types.ts`: `RollbackTaskRequest`, `RollbackBranchResponse` (201, `{version_id, version_no, status}`), `RollbackSwitchResponse` (200, `{current_version_id, version_no, status}` — note the field name differs from branch). The two response bodies share **no discriminator field**, so the page reads the requested `mode` from the mutation variables (not the response) for success messaging.
- Surface a **per-version Rollback action** on each non-current `VersionTree` row: a small picker choosing `branch` (re-execute from that version, optional prompt) or `switch` (repoint `current_version` only, no run). Wire it through TaskDetail.
- **UI task-level mutex**: while `task.status` is active (`pending`/`running`/`paused`/`cancelling`) **both** rollback modes are disabled with a reason — the backend requires a non-active task for *both* modes (verified in `domain/task/rollback_service.go:93`), not just branch. The backend stays authoritative: a racing `409 active_version_exists` surfaces a message naming the active version and refetches.
- **Switch terminal guard (advisory)**: a `switch` to a non-terminal target returns `409 invalid_state`; the UI advisory-disables `switch` on rows whose `version.is_active` is true and surfaces the `409` if it races.
- On success, invalidate the task + versions queries (branch also seeds a new pending version; switch moves the pointer). No optimistic status mutation — status arrives via the existing live pipeline.
- Reconcile two stale ARCHITECTURE lines against the shipped behavior (§6.5 itself is already correct): **line 522** ("switch 模式仅切指针不受约束" — wrong, switch is 409-gated on active too) and **line 788** ("禁用迭代/回滚-branch 按钮" — should read 回滚, both modes are gated).

## Capabilities

### New Capabilities
<!-- none — this extends the existing tasks pages capability -->

### Modified Capabilities
- `web-tasks-pages`: ADD a "Rollback Action With Mode Selection And UI Task-Level Mutex" requirement (mirroring the existing Iterate Action requirement); the rollback entry lives on the Task Detail page's version tree.

## Impact

- **Code**: `web/src/features/tasks/{api,mutations,types}.ts`; `web/src/components/tasks/VersionTree.tsx` (+ a small mode-picker, possibly a `RollbackControl` component); `web/src/routes/TaskDetail.tsx` (wiring + toasts); matching `*.test.tsx`/`*.test.ts`.
- **APIs**: consumes existing `POST /api/v1/tasks/{id}/rollback` — **no backend change**.
- **Docs**: `docs/ARCHITECTURE.md` lines 522 & 788 reworded to match shipped behavior.
- **No** new global (Zustand) state — the mode picker is ephemeral local component state, like the iterate prompt box.
