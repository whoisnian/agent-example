## 1. Data layer (types + api + mutation)

- [ ] 1.1 In `features/tasks/types.ts` add `ControlAction = "pause" | "resume" | "cancel"`, `ControlRequest { action: ControlAction; reason?: string }`, and `ControlResponse { accepted: boolean; action: ControlAction; task_id: string; effective: "queued" | "best_effort" }` — mirroring the `task-control-api` 202 DTO.
- [ ] 1.2 In `features/tasks/api.ts` add `controlTask(taskId, body: ControlRequest): Promise<ControlResponse>` calling `POST /api/v1/tasks/{taskId}/control` with `toastOnError:false` (page surfaces 409/best_effort inline), matching the `iterateTask` shape.
- [ ] 1.3 In `features/tasks/mutations.ts` add `useControlTaskMutation` (`meta:{silent:true}`); `onSettled` invalidates `taskKeys.detail(taskId)` + `taskKeys.versions(taskId)` so the bar re-reads the worker-driven status even if the live frame is delayed (design D3).
- [ ] 1.4 Add a pure helper `controlAvailability(status: string): {canPause, canResume, canCancel}` in `features/tasks/types.ts`, encoding the preconditions **positively over the reachable task statuses**: pause={pending,running}; resume={paused}; cancel={pending,running,paused}. Any unknown status (incl. the version-only `queued`/`cancelling` that never reach `task.status`) returns all-false so a typo can't enable an action (design D2).

## 2. ControlBar component

- [ ] 2.1 Add `components/tasks/ControlBar.tsx` — presentational: props `{ status: string; pending: boolean; onAction: (action: ControlAction) => void }`. Render Pause / Resume / Cancel via the `Button` primitive (Cancel uses `variant="danger"`). Derive enablement from `controlAvailability(status)` AND `!pending`; each disabled button gets a `title` reason (design D6).
- [ ] 2.2 Give the bar + buttons stable `data-testid`s (`control-bar`, `control-pause`, `control-resume`, `control-cancel`) for tests.

## 3. Wire into TaskDetail

- [ ] 3.1 In `TaskDetail.tsx` instantiate `useControlTaskMutation` and render `<ControlBar>` in the header area, passing `loadedTask.status`, `pending = control.isPending`, and an `onAction` that calls `control.mutate({ taskId: id, body: { action } })`.
- [ ] 3.2 On success: show a transient confirmation toast (e.g. "Pause requested"); if `data.effective === "best_effort"` (cancel pre-claim) show an `info` toast that it may not take effect until the task is claimed. Do NOT optimistically mutate status (design D3).
- [ ] 3.3 On error: `409 invalid_state` → `warning` toast using `err.message` verbatim (it names the current status); other `ApiError` → `error` toast. Never retry; never leave a button stuck pending (handled by `isPending` settling).

## 4. Tests

- [ ] 4.1 Add an MSW handler for `POST /api/v1/tasks/:id/control` returning `202 {accepted:true, action, task_id, effective:"queued"}`; export a helper so tests can `server.use()` the `best_effort`, `409 invalid_state`, and `404` variants. NOTE: the default `GET tasks/:id` fixture is `succeeded` (terminal → all controls disabled), so the TaskDetail control tests (4.3–4.6) MUST `server.use()` a `running`/`paused` detail fixture (the override pattern already exists in `TaskDetail.test.tsx`).
- [ ] 4.2 `controlAvailability` unit test: assert the enablement triple across all six `TASK_STATUSES` AND that an unknown status returns all-false (design D2 / item 16).
- [ ] 4.3 `ControlBar` unit test: enablement matrix across `running` / `paused` / each terminal status; a disabled button exposes its reason via `title`; `pending` disables all three.
- [ ] 4.4 `TaskDetail` test: clicking Pause on a `running` task POSTs `{action:"pause"}` and shows the confirmation toast; the displayed status does NOT change optimistically.
- [ ] 4.5 `TaskDetail` test: a `409 invalid_state` surfaces a warning carrying the server message and is not retried; a `best_effort` cancel shows the info note; an unexpected non-409 `ApiError` surfaces a generic error toast (spec "Unexpected control error" scenario).
- [ ] 4.6 `TaskDetail` test: after a control action settles, the `onSettled` invalidation refetches a now-terminal detail fixture (`server.use()`) and the bar re-derives to all-disabled — the deterministic, unit-observable form of live status reflection (design D3).

## 5. Gates

- [ ] 5.1 `pnpm typecheck` (or `npm run typecheck`) clean — strict TS, no `any` leak in the new DTOs/helper.
- [ ] 5.2 `pnpm lint` clean.
- [ ] 5.3 `pnpm test` green (new + existing).
- [ ] 5.4 `pnpm format:check` clean (or run `format`).
- [ ] 5.5 `openspec validate add-web-control-bar --strict` valid.
