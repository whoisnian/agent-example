## Why

The backend control loop is complete end-to-end — `task-control-api` accepts `POST /api/v1/tasks/{id}/control` (pause/resume/cancel), the worker acts on the signal (`worker-control-handling`), and now `realtime-gateway` streams the resulting `status:paused|running|cancelling` events live. But the web `TaskDetail` page has no way to *issue* a control: a user can watch a task run but cannot pause, resume, or cancel it. This is the last missing piece of the documented "创建 / 执行 / 实时观测 / **控制** / 迭代" MVP loop (ARCHITECTURE §1, the TaskDetail "控制按钮" box in §3).

## What Changes

- Add a **task control bar** to `TaskDetail`: `Pause` / `Resume` / `Cancel` buttons wired to `POST /api/v1/tasks/{id}/control`.
- Each button's **enablement mirrors the API state-machine preconditions** (advisory, client-side): `pause` only when `status ∈ {pending, running}`, `resume` only when `paused`, `cancel` only when non-terminal. A disabled button carries a `title` explaining why (e.g. "Only a paused task can be resumed"). The API stays authoritative — a slipped-through `409 invalid_state` is surfaced, not assumed-impossible.
- **Asynchronous status reflection**: the `202 Accepted` does NOT flip status — the worker does, asynchronously, and the new status arrives through the existing `useTaskLive` cache invalidation (live via the realtime gateway, polling as fallback). The bar shows a transient confirmation ("Pause requested") and lets the live status update redraw the buttons. No optimistic local status mutation (the worker may not honor it).
- **Error handling** mirrors the iterate action: the control mutation opts out of the global error toast and the page handles outcomes inline — `409 invalid_state` → a warning toast carrying the server message (which names the current status); `404 task_not_found` → the existing not-found render; a `best_effort` cancel (pre-claim, no active run) → an info note that it may not take effect until the task is claimed.
- **Double-submit guard**: while a control request is in flight, the three buttons are disabled (the API tolerates duplicate controls, but the UX shouldn't invite them).
- Data layer: a `controlTask(taskId, {action, reason?})` wrapper in `features/tasks/api.ts` (`toastOnError:false`), `ControlAction` / `ControlRequest` / `ControlResponse` types mirroring the `task-control-api` DTO, a pure `controlAvailability(status)` helper, and a `useControlTaskMutation` (`meta:{silent:true}`; invalidates the task + versions on settle). Both `toastOnError:false` AND `meta:{silent:true}` are set — they suppress the apiFetch-layer and mutation-cache-layer toasts respectively, exactly as iterate does, so the page owns all error UX.

Non-goals (this round): a free-form `reason` input (the API accepts one; the bar sends `{action}` only — reason is Post-MVP), a confirm dialog for cancel, and rollback/branch controls (deferred per the rollback-postponed direction).

## Capabilities

### New Capabilities

(none.)

### Modified Capabilities

- `web-tasks-pages`: ADD a "Task Control Bar With State-Machine-Aware Actions" requirement to the Task Detail page — the pause/resume/cancel controls, their precondition-driven enablement, the async-status-reflection behavior, and the 409/404/best_effort outcome handling. No existing requirement's behavior changes (the Iterate Action and Live Observation requirements are unaffected and complementary).

## Impact

- **Web only.** No API, worker, schema, or MQ change — the `task-control-api` contract and the realtime stream already exist and are unchanged.
- New code: `web/src/components/tasks/ControlBar.tsx`; additions to `features/tasks/{api,mutations,types}.ts` (incl. the pure `controlAvailability` helper + its unit test); `TaskDetail.tsx` renders the bar.
- Tests: a new MSW handler for `POST /api/v1/tasks/:id/control` (202 queued / 202 best_effort / 409 / 404) plus `ControlBar` / `TaskDetail` unit tests for enablement, double-submit guard, error surfacing, and live status reflection.
- Reuses: the `Button` primitive (incl. `danger` variant for Cancel), the `apiFetch` envelope client + `ApiError`, the `useUiStore` toast, the `isActiveStatus` helper, and the `useTaskLive` invalidation already on the page.
- Unblocks the remaining MVP web work (`add-web-cost-views`, `add-web-artifacts-views`) without coupling to them.
