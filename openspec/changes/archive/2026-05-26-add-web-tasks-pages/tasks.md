# Implementation Tasks

## 1. Data layer — `features/tasks/`

- [x] 1.1 `features/tasks/types.ts` — TS mirrors of the API DTOs: `CostSummary` (`amount_usd: string`), `TaskSummary`, `TaskListPage`, `TaskInfo`, `VersionNode`, `TaskDetail`, `VersionFull`, `RunSummary`, `VersionDetail`, `EventItem`, `EventPage`; request types `CreateTaskRequest` / `IterateTaskRequest`; response types `CreateTaskResponse` / `IterateTaskResponse` / `ActiveVersionConflict`. Define `ACTIVE_STATUSES` + `isActiveStatus(s)` and `TASK_STATUSES` (the six) mirroring the API sets.
- [x] 1.2 `features/tasks/api.ts` — `apiFetch` wrappers: `listTasks({page,pageSize,status})`, `getTask(id)`, `listVersions(taskId)`, `getVersion(id)`, `listVersionEvents(versionId, afterId, limit)`, `createTask(body)`, `iterateTask(id, body)`. Build query strings; never parse `amount_usd` to number.
- [x] 1.3 `features/tasks/queries.ts` — query-key factory (`taskKeys.list/detail/versions/events`) + hooks `useTasksQuery`, `useTaskQuery`, `useVersionsQuery`, `useVersionEventsQuery`. `useTaskQuery` (detail) sets a per-query `retry: (n, err) => !(err instanceof ApiError && err.status === 404) && n < 2` and `meta:{silent:true}` so a `404 task_not_found` is neither retried nor toasted (D7). `useVersionEventsQuery` is `enabled: !!versionId` (nullable current version, S12). Accept an optional `refetchInterval` arg in **function form** (driven by D2).
- [x] 1.4 `features/tasks/mutations.ts` — `useCreateTaskMutation` (invalidate `taskKeys.list`) and `useIterateTaskMutation` (invalidate `taskKeys.detail(id)` + `taskKeys.versions(id)`); both use `meta:{silent:true}` so create `400 invalid_input` and iterate `409` are handled in-page, not via the global toast.
- [x] 1.5 `services/http.ts` — additive: `ApiError` gains an optional `data?: unknown` field populated from the error envelope's `data`, so the create form can read `invalid_input`'s `{field, reason}`. Backward-compatible; update `http.test.ts` if it asserts the constructor shape.

## 2. Live-update hook + bootstrap gap-fill

- [x] 2.1 `features/tasks/use-task-live.ts` — `useTaskLive(taskId, currentVersionId, isActive)`: a `useCallback` handler with deps exactly `[taskId, currentVersionId]` (closes over the singleton `queryClient` only — no changing objects, per the `useRealtime` stable-handler contract, S17). Per topic: a `task:` frame invalidates `taskKeys.detail(taskId)` + `taskKeys.versions(taskId)`; a `version:` frame invalidates `taskKeys.events(currentVersionId)`. `useRealtime("task:"+taskId, handler)` always; `useRealtime(currentVersionId ? "version:"+currentVersionId : null, handler)` (the hook no-ops on a null topic).
- [x] 2.2 Register `setRealtimeOnGap` **once at app bootstrap** (next to `setRealtimeNavigator` in `main.tsx`), NOT per page — avoids the singleton-clobber on remount (S8). The handler matches only `version:<id>` topics (task topics carry no `task_events` ids), reads the **max event `id`** already in the `taskKeys.events(versionId)` cache, and backfills `listVersionEvents(versionId, afterId=<that id>, limit)` — **id-based, never `seq`** (S2). Ignore the `fromSeq/toSeq` args for the cursor.
- [x] 2.3 Thread `refetchInterval` into the read hooks in **function form**: `() => isActive && getRealtimeClient().getConnectionState() !== "open" ? 3000 : false`, re-evaluated each tick so it self-corrects when the WS opens (S10). Polling runs only while active + WS not open; stops on terminal status or open WS.

## 3. Presentational components — `components/tasks/`

- [x] 3.1 `StatusBadge.tsx` — colored badge per status (active vs terminal vs failed palettes), using existing Tailwind tokens.
- [x] 3.2 `CostBadge.tsx` — render `amount_usd` string for display (format without float arithmetic); show token counts on hover/title.
- [x] 3.3 `VersionTree.tsx` — parent-indented list from `VersionNode[]` (order by `version_no`, indent by `parent_id` depth); mark the `current_version` node; each node shows version_no, `StatusBadge`, `CostBadge`.
- [x] 3.4 `EventLog.tsx` — render `EventItem[]` newest-last with `kind`, `seq`, and a compact payload preview; stable keys on `id`.

## 4. Pages

- [x] 4.1 `routes/TaskList.tsx` — `useTasksQuery`; table of rows (title / type / `StatusBadge` / `current_version` / `CostBadge`), row → `/tasks/{id}`; page + page_size controls (clamp mirrors server); **single-select** status `<select>` over the six `TASK_STATUSES` + "all" (never emits `queued`/`cancelling`); empty-state; "New task" → `/tasks/new`. Expose a stable `data-testid` on the page root for the router test. Render `current_version` null-tolerantly.
- [x] 4.2 `routes/TaskCreate.tsx` — form (`title`, `task_type` select of `code-gen`/`research` per D8, `prompt`, `params` textarea, optional `lane`); client-side params JSON validation (block + inline error); `useCreateTaskMutation`; on success navigate to `/tasks/{task_id}`; on `400 invalid_input` read `err.data.{field, reason}` and show inline on that field, mapping `field:"body"` to a form-level error (preserve entered values).
- [x] 4.3 `routes/TaskDetail.tsx` — `useTaskQuery` (header + `StatusBadge` + `CostBadge`), `useVersionsQuery` → `VersionTree`, `useVersionEventsQuery(current_version_id)` (gated on non-null) → `EventLog`; wire `useTaskLive`; loading state; `404`→not-found render from the query `error` (not a toast, not retried). Expose a stable `data-testid` on the page root.
- [x] 4.4 Iterate action in TaskDetail: disabled while `isActiveStatus(task.status)` (reachable active set for a task is `pending`/`running`/`paused`) with reason tooltip; on click open a minimal prompt input → `useIterateTaskMutation`; on `409 active_version_exists` show a toast naming `err.data.active_version_id`/`active_version_status` and refetch the task.

## 5. Routing + cleanup

- [x] 5.1 `router.tsx` — `tasks` → `<TaskList/>`, `tasks/:id` → `<TaskDetail/>`, add `tasks/new` → `<TaskCreate/>`; drop the placeholder imports.
- [x] 5.2 Delete `routes/placeholders/TaskListPlaceholder.tsx` and `TaskDetailPlaceholder.tsx`.
- [x] 5.3 Update `routes/router.test.tsx`: it imports `TaskListPlaceholder`/`TaskDetailPlaceholder` and asserts `placeholder-tasks` / `placeholder-task-detail` / `task-id` testids (lines ~8–9, 37, and assertions) — replace with the real pages. The harness must add a `QueryClientProvider` (fresh `createQueryClient()`) and rely on the MSW handlers (§6.1) so the data-driven pages render; assert the new pages' root `data-testid`s instead of the placeholder ids (S7).

## 6. Test fixtures + tests

- [x] 6.1 Extend `test/mocks/handlers.ts` with **absolute** `http://localhost/api/v1/...` handlers (matching the existing `__scaffold/*` pattern — relative paths won't match, and `onUnhandledRequest:"error"` hard-fails any un-mocked call): `GET /tasks`, `GET /tasks/:id`, `GET /tasks/:id/versions`, `GET /versions/:id/events`, `POST /tasks`, `POST /tasks/:id/iterate` (incl. a 409 `active_version_exists` variant with `data:{active_version_id,active_version_status}`, and a `400 invalid_input` with `data:{field,reason}`). Envelope every response `{code:0,message,data,trace_id}`; fixtures shaped exactly like the DTOs (`amount_usd` a string). Every endpoint a page hits on mount (detail = task + versions + events) must be mocked.
- [x] 6.2 `TaskList` test — renders rows from the mocked list; status filter changes the query; empty-state path.
- [x] 6.3 `TaskCreate` test — invalid params JSON blocks submit; successful submit navigates to detail and invalidates the list; server `400` shows inline field error.
- [x] 6.4 `TaskDetail` test — renders header + version tree + event log; Iterate disabled when status active and enabled when terminal; 409 path surfaces the active-version message and refetches; 404 shows not-found.
- [x] 6.5 `use-task-live` test — a realtime frame invalidates the detail/events queries; polling interval is active only while `isActive` and WS not `open`; unsubscribe on unmount.

## 7. Verification + docs

- [x] 7.1 `npm run lint` (eslint) and `npm run typecheck` (`tsc --noEmit`) clean. (Repo uses **npm**, not pnpm — `package.json packageManager: npm@11`.)
- [x] 7.2 `npm test` (`vitest run`) green, including the new page/hook tests.
- [x] 7.3 `npm run build` succeeds (production bundle).
- [x] 7.4 Update `web/README.md` — document the three pages, the `features/tasks/` layer, the UI task-level mutex, and the WS-first-with-polling-fallback live-update strategy (noting it upgrades automatically when `add-realtime-gateway` lands).
