## Why

The API now exposes a full vertical slice — create/iterate (`task-write-api`), read (`task-read-api`), and worker event ingest driving real `task.status` / version status (`add-event-ingest-status-sync`). But the web app still ships only placeholder routes (`TaskListPlaceholder`, `TaskDetailPlaceholder`). There is no way for a user to submit a task, watch it run, see its result, or iterate — the whole point of the platform. This change builds the first business pages on top of the live read+write APIs so the "submit → execute → observe → iterate" loop is usable end-to-end from the browser.

## What Changes

- Replace the `TaskList` / `TaskDetail` placeholders with real pages, and add a `TaskCreate` page, all under the existing authenticated `RootLayout`:
  - **TaskList** (`/tasks`) — paginated table from `GET /tasks` (page / page_size / status filter), each row showing title, type, status badge, current version, and the embedded cost summary; row → detail; a "New task" action.
  - **TaskCreate** (`/tasks/new`) — form (`title`, `task_type`, `prompt`, optional `params` JSON, optional `lane`) → `POST /tasks`; on success navigate to the new task's detail. Surfaces `400 invalid_input` field errors inline.
  - **TaskDetail** (`/tasks/:id`) — task header + status, `GET /tasks/{id}`; a **lightweight indented version tree** from `GET /tasks/{id}/versions` (each node shows version_no / status / cost); an **event log** from `GET /versions/{id}/events?after_id=` for the current version; and an **Iterate** action (`POST /tasks/{id}/iterate`).
- **Task-level mutex in the UI**: while `task.status` is active (`pending`/`running`/`paused`/`cancelling`), the Iterate button is disabled with a reason tooltip. The backend `409 active_version_exists` remains the source of truth — a 409 surfaces a toast naming the active version and refetches.
- **Live observation, WS-first with a polling fallback**: TaskDetail subscribes via the existing `realtimeClient` (`task:<id>` / `version:<id>`) and invalidates the relevant React Query caches on incoming frames; the client's `onGap` callback backfills missed events via the events REST endpoint. Because the Realtime Gateway server is not built yet (`add-realtime-gateway`), a React Query `refetchInterval` fallback (active-status-gated, stops on terminal) carries live updates until the WS server exists — matching ARCHITECTURE's "WebSocket，失败降级为 5s 轮询".
- Add a `features/tasks/` slice: typed API response/request models mirroring the API DTOs, plus React Query hooks (`useTasksQuery`, `useTaskQuery`, `useVersionsQuery`, `useVersionEventsQuery`, `useCreateTaskMutation`, `useIterateTaskMutation`).
- Add small presentational components (StatusBadge, CostBadge, version-tree node, event-log row) and wire `/tasks/new` into the router; remove the two consumed placeholders.

## Capabilities

### New Capabilities
- `web-tasks-pages`: the TaskCreate / TaskList / TaskDetail pages, the `features/tasks/` data layer (React Query hooks over the read+write endpoints), the UI task-level mutex (disable iterate while active), and the WS-first-with-polling-fallback live-update behaviour for TaskDetail.

### Modified Capabilities
<!-- None. web-data-access (apiFetch / React Query / Zustand conventions) and web-realtime-client (the WS client + subscribe/onGap) already exist and are consumed as-is; this change adds pages on top without changing those contracts. -->

## Impact

- **New code (`web/src/`)**
  - `features/tasks/` — `types.ts` (DTO mirrors), `api.ts` (apiFetch calls), `queries.ts` (React Query keys + hooks), `mutations.ts` (create / iterate).
  - `routes/TaskList.tsx`, `routes/TaskCreate.tsx`, `routes/TaskDetail.tsx`.
  - `components/tasks/` — `StatusBadge.tsx`, `CostBadge.tsx`, `VersionTree.tsx`, `EventLog.tsx`.
  - `features/tasks/use-task-live.ts` — the WS-subscribe + cache-invalidate + polling-fallback hook (wraps `useRealtime`).
- **Modified code**
  - `src/router.tsx` — point `tasks` / `tasks/:id` at the real pages, add `tasks/new`.
  - `src/services/http.ts` — **additive**: `ApiError` gains an optional `data?: unknown` from the error envelope so the create form can read `invalid_input`'s `{field, reason}` (backward-compatible; existing callers ignore it).
  - App bootstrap (`main.tsx` / wherever `setRealtimeNavigator` is wired) — register a module-level `setRealtimeOnGap` handler (id-based event backfill), so it isn't a per-page global that remounts can clobber.
  - Remove `routes/placeholders/TaskListPlaceholder.tsx` and `TaskDetailPlaceholder.tsx`; update `router.test.tsx` (which imports/asserts them) to wrap the real pages in a `QueryClientProvider` + MSW.
  - `web/README.md` — document the new pages and the live-update strategy.
- **Reused, unchanged**: the `QueryClient`, `useAuthStore` / `useUiStore`, `realtimeClient` + `useRealtime`, `Button` primitive, MSW test harness (`test/mocks`).
- **Dependencies**: none new (no react-flow — the version tree is a plain indented list; rich DAG viz deferred to a later VersionTree change).
- **Tooling**: web uses **npm** (`package.json` `packageManager: npm@11`); commands are `npm run lint` / `npm run typecheck` / `npm test` / `npm run build`.
- **No backend changes**: consumes existing endpoints only.
