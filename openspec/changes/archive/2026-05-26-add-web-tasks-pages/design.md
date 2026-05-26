## Context

The web app (`init-web-scaffold`) ships a complete data/realtime substrate but no business pages: `src/router.tsx` wires `/tasks` and `/tasks/:id` to placeholder components, and `/cost`, `/settings`, `/login` to their own placeholders. The substrate this change builds on (verified):

- **`services/http.ts`** — `apiFetch<T>(path, init)` unwraps the `{code,message,data,trace_id}` envelope to `data`, throws typed `ApiError` (with `.code`, `.status`, `.traceId`), injects `Authorization` + `X-Request-Id`, and centrally handles `401` (clear token → `/login`).
- **`services/query-client.ts`** — one `QueryClient`; `retry` already skips `unauthenticated` and `status===409`; global `onError` toasts unless `meta:{silent:true}`.
- **`features/auth/store.ts` / `features/ui/store.ts`** — Zustand token store + toast store. Convention (web-data-access): **server entities live in React Query, not Zustand.**
- **`services/ws.ts`** — robust `RealtimeClient` singleton: lazy connect on first `subscribe`, exponential-backoff reconnect, `getConnectionState()`, seq dedupe + `onGap(topic,from,to)` for REST backfill, `subscribe(topic,handler)` returning an unsubscribe fn. `hooks/use-realtime.ts` binds a subscription to component lifecycle (handler must be stable).
- **`components/primitives/Button.tsx`**, `RootLayout` + `RequireAuth`, and the MSW test harness (`test/mocks/handlers.ts`, currently only `__scaffold/*` routes).

Live API contracts this consumes (field names verified against the Go DTOs):

- `POST /tasks` ← `{title, task_type, prompt, params?, lane?}` → `{task_id, version_id, version_no, status}`.
- `POST /tasks/{id}/iterate` ← `{base_version_id?, prompt, params?, lane?}` → `{version_id, version_no, status}` or `409 {code:"active_version_exists", data:{active_version_id, active_version_status}}`.
- `GET /tasks` → `{items:[{id,title,task_type,status,current_version,created_at,updated_at,cost}], page, page_size, total}`; `cost = {amount_usd:"0.00000000", input_tokens, output_tokens, cached_tokens, tool_calls, wall_time_ms}` (**`amount_usd` is a decimal string, never a number**).
- `GET /tasks/{id}` → `{task, current_version: VersionNode|null, cost}`.
- `GET /tasks/{id}/versions` → `{items:[VersionNode{id,parent_id,version_no,status,is_active,artifact_root,created_at,cost}]}` (flat, version_no asc).
- `GET /versions/{id}` → `{version, runs, cost}`.
- `GET /versions/{id}/events?after_id=&limit=` → `{items:[{id,version_id,run_id,seq,kind,payload,created_at}], next_after_id}` (`id` is the global cursor, not `seq`).

## Goals / Non-Goals

**Goals:**
- A usable submit → observe → iterate loop in the browser, on the live read+write APIs.
- Live-ish TaskDetail that works *today* (no WS server yet) and upgrades cleanly when the gateway lands.
- Strict adherence to the scaffold's conventions (React Query for server state, Zustand only for UI/auth, `apiFetch`, the `Button` primitive, MSW tests).

**Non-Goals:**
- **CostDashboard** — `task_costs` is all-zero until `add-cost-service`; rendering only zeros adds no value. Deferred.
- **Rich react-flow DAG** — version tree is a plain indented list; the graph viz is a later change. No new dependency.
- **Control actions** (pause/resume/cancel) and **rollback** — owned by `add-task-control-api` / `add-task-rollback-api`; only **iterate** is wired here.
- **Realtime Gateway server** — not built here; this change only *wires the client* and degrades to polling.
- **Auth/login** — stays the scaffold placeholder; the API uses dev tenant/user IDs.

## Decisions

### D1 — `features/tasks/` slice: types + api + queries + mutations

A self-contained feature folder: `types.ts` (TS mirrors of the API DTOs — `amount_usd` typed as `string`), `api.ts` (thin `apiFetch` wrappers), `queries.ts` (query-key factory + read hooks), `mutations.ts` (create / iterate). Pages stay presentational and call hooks. Matches the web-data-access convention and keeps DTO drift contained to one file.

- **Query keys**: `["tasks", {page,pageSize,status}]`, `["task", id]`, `["versions", taskId]`, `["events", versionId]`. Mutations invalidate by prefix (`["tasks"]`, `["task", id]`, `["versions", taskId]`).

### D2 — WS-first, polling fallback (honors "wire WS now"; satisfies ARCHITECTURE's 5s-poll fallback)

A `use-task-live(taskId, currentVersionId, isActive)` hook:
1. `useRealtime("task:"+taskId, handler)` and — only when `currentVersionId` is non-null — `useRealtime("version:"+currentVersionId, handler)`. `handler` is stable via `useCallback` with deps `[taskId, currentVersionId]` only; it closes over the singleton `queryClient` (stable) and invalidates per topic:
   - a `task:<id>` frame → invalidate `taskKeys.detail(id)` + `taskKeys.versions(taskId)`;
   - a `version:<currentVersionId>` frame → invalidate `taskKeys.events(currentVersionId)` (+ versions).
   `RealtimeEvent.payload` (`{topic,kind,seq,ts,payload}`) is **not read** — invalidation is enough; the refetch re-pulls authoritative state (S11, S17).
2. **Gap-fill is id-based and set once at app bootstrap, not per page.** `onGap(topic, fromSeq, toSeq)` is invoked with per-run **seq** numbers, but `GET /versions/{id}/events?after_id=` takes the **global event `id` cursor** (`read_dtos.go`: `id` is the cursor, `seq` the per-run frame number) — so we must **never** feed `fromSeq` into `after_id`. The bootstrap `onGap` handler matches a `version:<id>` topic (task topics carry no `task_events` ids — gap-fill is version-only), reads the max event `id` already in `taskKeys.events(versionId)` cache, fetches `after_id=<that id>`, and primes the cache. Registered once via `setRealtimeOnGap` at app init (next to `setRealtimeNavigator`), so concurrent/remounting detail views can't clobber a per-page callback (S2, S8).
3. Read hooks take `refetchInterval` in its **function form** `() => isActive && getRealtimeClient().getConnectionState() !== "open" ? 3000 : false`, re-evaluated each tick so a `reconnecting → open` transition silences polling within one interval (the value form would freeze the state captured at render). `isActive` re-renders the component when `task.status` changes (it comes from query data), so terminal status flips polling off. There is an up-to-3s lag before polling stops after the WS opens — acceptable; not "zero page changes" but "self-correcting within one interval" (S10).

*Why both*: the user chose "wire WS now," but the gateway server doesn't exist yet, so WS frames won't actually flow — the connection sits in `reconnecting`. ARCHITECTURE §3.1 mandates a 5s polling fallback for exactly this. Polling gated on `!== "open"` means that once the gateway ships, an open socket silences the poll automatically. *Alternative rejected*: WS-only now → a dead "observe" experience until a separate change; polling-only now → throws away the WS wiring the user asked for.

### D3 — UI task-level mutex mirrors the DB invariant, backend stays source of truth

`isActiveStatus(status)` mirrors the API's task-level `IsActive` verbatim — `{pending, queued, running, paused, cancelling}` (one source of truth, copied from `status.go`). **Caveat (S4):** `task.status` can only ever be one of the six task statuses (`pending/running/paused/cancelled/succeeded/failed`); `queued` and `cancelling` are *version-only* states the API never writes to `tasks.status` (`add-event-ingest-status-sync` maps `queued→pending`, skips `cancelling`). So in practice the reachable active set for a task is `{pending, running, paused}` — the extra two are harmless dead entries kept only to match the constant. The disabling reason text must not promise `cancelling` will appear on a task. The Iterate button is `disabled` while active with a tooltip. The **backend 409 is authoritative**: the iterate mutation does not pre-check beyond disabling; a `409 active_version_exists` (which React Query already does not retry) maps to a toast naming `data.active_version_id`/`active_version_status` and invalidates `taskKeys.detail(id)`. This matches ARCHITECTURE §6.4 ("前端配合 … 提交时仍以后端 409 为准").

### D4 — Money is a string end-to-end

`amount_usd` is rendered verbatim (a `NUMERIC(18,8)` decimal string like `"0.62000000"`); the UI never parses it to `number`. A `CostBadge` formats for display (e.g. trim to a sensible precision) without arithmetic, preserving the API's no-float-rounding guarantee.

### D5 — Create form: client-side params JSON guard + inline server errors

The `params` textarea is validated as JSON on the client before submit (block + inline error) so an obvious mistake never round-trips. Server `400 invalid_input` carries `data: {field, reason}` (a **single** field; `field:"body"` for malformed JSON, else `"title"`/`"task_type"`/`"prompt"`/… for domain validation — `tasks.go writeInvalidInput`). The form maps a named field to that input's inline error and `"body"` to a form-level error; the mutation uses `meta:{silent:true}` so the inline error shows instead of a generic toast. Other codes fall through to the global toast.

**ApiError must expose `data` (S6).** Today `apiFetch` throws `ApiError` with only `code/message/status/traceId` and discards the envelope `data`, so the field name is unreachable. This change makes a small **additive** extension to `services/http.ts`: `ApiError` gains an optional `data?: unknown` populated from the error envelope. This is backward-compatible (existing callers ignore it) but does touch shared code — see Impact. Parsing `field` out of the message string was rejected as brittle.

### D7 — 404 is a not-found render state, never a retry/toast loop (S3)

`query-client.ts`'s global `retry` only skips `unauthenticated` and `status===409`; a `404 task_not_found` would otherwise retry 3× then toast — exactly what the spec forbids. So `useTaskQuery` (detail) sets a **per-query** `retry: (n, err) => !(err instanceof ApiError && err.status === 404) && n < 2` and `meta:{silent:true}`, and TaskDetail renders a not-found state from the query `error` (`status===404`) instead of relying on a toast.

### D8 — `task_type` is a constrained select mirroring the worker AgentRegistry (S15)

The create form's `task_type` is a `<select>` of `code-gen` / `research` — the agent types the worker `AgentRegistry` actually handles (`worker/`). The API does **not** enforce a `task_type` enum at the HTTP layer (it forwards to the domain), so this is a frontend convention, not a contract: adding a worker agent type means updating this list until a registry endpoint exists. (Resolves the former open question — a decision for MVP.)

### D6 — Routing + placeholder removal + tests

`router.tsx`: `tasks` → `<TaskList/>`, `tasks/:id` → `<TaskDetail/>`, add `tasks/new` → `<TaskCreate/>`. Delete `TaskListPlaceholder` / `TaskDetailPlaceholder` and update `routes/router.test.tsx` (which currently imports and asserts them). MSW `handlers.ts` gains the `/api/v1/tasks*` + `/versions/*` routes used by the new page tests (kept alongside the existing `__scaffold/*` routes).

## Risks / Trade-offs

- **[WS wired but inert until the gateway ships]** TaskDetail will keep the socket in `reconnecting`. → The connection-state-gated polling fallback (D2) carries updates; the reconnect backoff is bounded (cap 30s) so it's cheap. Documented; `add-realtime-gateway` flips it live with no page change.
- **[Polling load]** A ~3s interval per open active task. → Only active tasks poll, only while no WS is open, and it stops on terminal status; acceptable for MVP single-user scale.
- **[DTO drift]** TS types are hand-mirrored from Go DTOs. → Confined to `features/tasks/types.ts`; the MSW fixtures double as a contract check, and any field rename surfaces as a test/type failure.
- **[Event log volume]** A long-running task accrues many events. → The events query is windowed via `limit` + `after_id`; the page shows the latest window and backfills gaps on demand rather than holding unbounded history. `EventPage.next_after_id` echoes the *input* `after_id` on an empty page, so "no new events" is `items.length === 0`, not a sentinel value (S13).
- **[Nullable `current_version`]** `task.current_version` is a nullable pointer (JSON `null`) — a task can momentarily have none. → The events query is `enabled: !!currentVersionId`, the version-topic WS subscription is skipped when null, and the tree renders without a "current" marker; the TaskList "current version" column tolerates null (S12).
- **[List ordering verified]** `ListTasks` is `ORDER BY created_at DESC, id DESC` (`api/queries/tasks.sql`) — "newest-first" in the spec is accurate; the MSW fixture still defines the order the test asserts so it doesn't depend on incidental insertion order (S14).

## Migration Plan

1. Land `features/tasks/` (types/api/queries/mutations) + components, then the three pages.
2. Flip `router.tsx`, delete the two placeholders, update `router.test.tsx`, extend MSW handlers.
3. Purely additive on the web side; no backend or API change. Rollback = revert the router to the placeholders.

## Open Questions

1. **WS→polling handoff jitter** — when the gateway lands, is connection-state gating enough, or do we also want a short grace window before silencing polls on first connect? Defer to `add-web-realtime`.
