# Critical Review — `add-web-tasks-pages`

Reviewed against the actual web codebase (`web/src/...`) and the live API DTOs
(`api/internal/domain/task/read_dtos.go`, `api/internal/interfaces/http/tasks.go`,
`task_reads.go`, `status.go`, `errors.go`). Findings are ordered by severity.

---

## Critical

### S1 — Package manager is npm, not pnpm; every command/script reference is wrong
- **Where:** `tasks.md §7.1–7.3`, `design.md` Goals, `proposal.md` (implied).
- **Finding:** `web/package.json` declares `"packageManager": "npm@11.12.1"` and `engines.npm >= 11`. There is no `pnpm-lock.yaml`. The tasks say `pnpm lint` / `pnpm typecheck` / `pnpm test` / `pnpm build`. Those commands are wrong for this repo.
- **Also:** the script names differ. `package.json` has `lint` (`eslint .`), `typecheck` (`tsc --noEmit`), `test` (`vitest run`), `build` (`tsc --noEmit && vite build`). So the correct invocations are `npm run lint`, `npm run typecheck`, `npm test`, `npm run build`.
- **Recommend:** replace all `pnpm <x>` with `npm run <x>` (and `npm test`). Drop the "pnpm" mention in the proposal/design conventions list.

### S2 — `onGap` passes `seq`, but the events endpoint cursor is `id` (`after_id`), not `seq`
- **Where:** `design.md D2 step 2`, `tasks.md §2.2`, `spec.md` "Live Observation" requirement.
- **Finding:** `RealtimeClient.onGap(topic, fromSeq, toSeq)` is invoked with **seq** numbers (`ws.ts` line ~272: `this.onGap(frame.topic, entry.lastDeliveredSeq + 1, seq - 1)` where `lastDeliveredSeq` tracks `frame.seq`). But `GET /versions/{id}/events?after_id=` takes the **global event `id` cursor**, explicitly NOT `seq` — both the DTO comment (`read_dtos.go` line 137: "`id` (the global cursor), `seq` (the per-run frame number)") and `design.md` line 19 say so. The design's backfill `listVersionEvents(versionId, fromSeq-1)` therefore passes a seq where an id is required, producing a wrong window.
- **Recommend:** Either (a) backfill by fetching `after_id=<last known event id>` (track the max `id` seen in the events cache, ignore the seq args), or (b) fetch the full recent window and let seq-dedup in the client/cache reconcile. Do NOT feed `fromSeq` into `after_id`. Update D2, task 2.2, and the spec scenario to describe an id-based (not seq-based) backfill. Note also that `task:<id>` topic frames carry a *task* seq, not version-event ids at all — gap-fill only makes sense for the `version:<id>` topic that maps to `task_events`.

### S3 — 404 not-found will retry + toast; spec asserts "no retry loop" but nothing implements it
- **Where:** `spec.md` "Task Detail Page" / "Unknown task shows not-found" scenario; `tasks.md §4.3`.
- **Finding:** `query-client.ts` `retry` only returns `false` for `code === "unauthenticated"` and `status === 409`. A `404` (`code:"task_not_found"`, confirmed in `errors.go`) is retried up to `failureCount < 2` (3 attempts total) and then the global `onError` fires a toast — exactly the "generic error toast loop" the spec says must NOT happen. The design provides no mechanism to suppress this.
- **Recommend:** the `useTaskQuery` for detail must set a per-query `retry` that returns `false` on `status === 404` (or `code === "task_not_found"`), and `meta:{silent:true}` (or handle 404 as a normal not-found render state via `error` rather than a toast). Add a task for this and reference the exact `code`/`status`.

---

## Important

### S4 — `task.status` can never be `cancelling` or `queued`; mutex spec/design is internally inconsistent
- **Where:** `proposal.md` line 11, `spec.md` "Iterate Action" requirement + scenarios, `design.md D3`, `tasks.md §1.1/§4.4`.
- **Finding:** Three different active-status sets appear:
  - Proposal / spec mutex text: `pending, running, paused, cancelling` (4).
  - Design D3 `isActive`: `{pending, queued, running, paused, cancelling}` (5).
  - The API's task-level `activeStatuses` (`status.go`): `{pending, queued, running, paused, cancelling}` (5).
  But the API also documents that **tasks only ever carry the six `taskStatuses`** (`pending/running/paused/cancelled/succeeded/failed`) — `queued` and `cancelling` are *version-only* states (`status.go` lines 36–47, `mapVersionToTaskStatus` collapses `queued→pending` and drops `cancelling`). So for `task.status`, `queued`/`cancelling` are unreachable.
- **Recommend:** Pick ONE source of truth and state it precisely. For the UI mutex over `task.status`, the reachable active set is effectively `{pending, running, paused}`. Either (a) mirror the API constant `IsActive` verbatim (5 values) and note the extra two are dead-but-harmless for tasks, or (b) use the three reachable ones. Make `proposal`, `spec`, `design`, and `tasks.md §1.1 ACTIVE_STATUSES` agree. Note the spec scenario "status is `running` (or any active status)" and the disabling reason text should not promise `cancelling` will ever appear on a task.

### S5 — Status filter list and `TASK_STATUSES` must exactly match the API's six (and the rejection message)
- **Where:** `spec.md` "Task List Page" (lists six: pending/running/paused/cancelled/succeeded/failed — correct), `tasks.md §1.1` `TASK_STATUSES`, `§4.1` status `<select>`.
- **Finding:** The list is correct, but the API rejects any other `status` value with `400 invalid_input` and message "must be one of pending/running/paused/cancelled/succeeded/failed" (`task_reads.go` line 60). The `status` query param is a **single value**, not repeated/multi (`task_reads.go` line 58: `c.Query("status")`). The spec/tasks don't state this; a multi-select would silently break.
- **Recommend:** Specify the filter is a single-select (or "all"). Ensure `TASK_STATUSES` is exactly those six and never sends `queued`/`cancelling` (which would 400).

### S6 — `400 invalid_input` `data` shape is `{field, reason}`, not a per-field map
- **Where:** `design.md D5`, `spec.md` "Server validation error shows inline", `tasks.md §4.2`.
- **Finding:** Both `createTask` (`writeInvalidInput`, `tasks.go` line 209) and the read handlers emit `data: {"field": <name>, "reason": <text>}` with `message: "invalid_input: <field>: <reason>"`. The design says "mapped to the named field" but doesn't state the contract. The current `ApiError` (`http.ts`) only carries `code/message/status/traceId` — it does **not expose `data`**. So the page cannot read `data.field`; it can only parse the `message` string or the design must extend `ApiError` to surface `data`.
- **Recommend:** State the exact `data` shape (`{field, reason}`, single field). Decide the mechanism: either (a) extend `ApiError` to carry the parsed envelope `data` (a code change to `http.ts` — currently out of the "reused, unchanged" list, so call it out), or (b) parse `field` from `err.message`. Note the create handler emits `field:"body"` for malformed JSON and `field:"title"/"task_type"/...` for domain validation — the form must map `"body"` to a form-level error, not a single named input.

### S7 — Removing the placeholders breaks `router.test.tsx`, which is a separate test file the tasks under-scope
- **Where:** `tasks.md §5.3`, `proposal.md` line 33.
- **Finding:** `routes/router.test.tsx` imports `TaskListPlaceholder` and `TaskDetailPlaceholder` and asserts `placeholder-tasks`, `placeholder-task-detail`/`task-id` test ids (lines 8–9, 37, 67, 73, 79). Deleting the placeholder modules makes this test file fail to compile/run. Task 5.3 says "update to import/assert the real pages" but the real pages need a `QueryClientProvider` and MSW data to render (the placeholders were pure). The existing `TestApp` harness wraps only `MemoryRouter` — it has no `QueryClientProvider`, so swapping in `<TaskList/>`/`<TaskDetail/>` will throw "No QueryClient set".
- **Recommend:** Make 5.3 explicit: the router test must add a `QueryClientProvider` (fresh `createQueryClient()`) and rely on MSW handlers (S?-fixtures) for the real pages to render, or assert only that the route resolves to a stable testid the new pages expose (e.g. a `data-testid` on the page root) without needing data. Add the test-ids the new pages must expose so this test (and S9) can assert them. Also confirm whether `router.tsx` itself has a co-test; it does not, only `router.test.tsx`.

### S8 — `setRealtimeOnGap` is a global singleton mutation; concurrent/remounting TaskDetail clobbers it
- **Where:** `design.md D2 step 2`, `tasks.md §2.2`.
- **Finding:** `setRealtimeOnGap(cb)` mutates the one shared `RealtimeClient` (`ws.ts` lines 436–438, singleton via `getRealtimeClient()`). The design says TaskDetail "sets the client's onGap." If two detail views mount (unlikely in this router, but Strict Mode double-invoke and mount/unmount churn are real), the last writer wins and unmount that "restores/no-ops" (task 2.2) will null out a still-needed callback. There is no save/restore of the previous `onGap`, and `setOnGap` has no stacking.
- **Recommend:** The onGap handler should be keyed by topic, not per-mount global. Recommend setting onGap **once** at app bootstrap (a module-level handler that, given `version:<id>`, fetches that version's events by id and primes the cache) rather than per-page in TaskDetail. If it must be per-page, save the prior callback and restore it on unmount, and guard React Strict Mode double-mount. Update D2 and task 2.2.

### S9 — Testing libraries ARE installed — but the harness gaps are elsewhere (correct an assumption, fix the real gaps)
- **Where:** review premise; `tasks.md §6`.
- **Finding (good news):** `@testing-library/react@16`, `@testing-library/user-event@14`, `@testing-library/jest-dom@6`, `jsdom`, `msw@2`, `vitest@4` are all present in `web/package.json` devDeps; `vitest.config.ts` sets `environment:"jsdom"`, `globals:true`, `setupFiles:["src/test/setup.ts"]`. So component render tests are feasible as written — the "are testing libs installed?" risk is resolved.
- **Real gap:** MSW base-URL resolution is fine. `setup.ts` sets `process.env.VITE_API_BASE_URL = "http://localhost"`, the `@vitejs/plugin-react` transform maps `import.meta.env.VITE_*` from `process.env` under vitest, and the existing `http.test.ts` (7 tests) passes with handlers registered at `http://localhost/...`. New handlers must therefore also be absolute `http://localhost/api/v1/...` (matching the existing `__scaffold` pattern) — **relative paths will not match**. `server.listen({onUnhandledRequest:"error"})` means any un-mocked request hard-fails the test.
- **Recommend:** In task 6.1 state explicitly that handlers use absolute `http://localhost/api/v1/...` URLs (mirroring `__scaffold/*`), enveloped as `{code:0,message,data,trace_id}`. Remove/replace the "if testing libs absent, add dep" framing — they exist. Every endpoint a page touches on mount (tasks + versions + events for detail) must be mocked or `onUnhandledRequest:"error"` fails the test.

### S10 — Polling gate via `getConnectionState()` won't re-evaluate on connection-state change
- **Where:** `design.md D2 step 3`, `tasks.md §2.3`, `spec.md` "Polling runs only while active".
- **Finding:** React Query's `refetchInterval` accepts a function `(query) => number | false` and is re-evaluated **after each fetch / on observer updates**, but a *change in `realtimeClient.getConnectionState()`* (e.g. WS transitions `reconnecting → open`) does **not** itself trigger a re-render or re-evaluation. Computing the interval once at hook call time (`isActive && getConnectionState() !== "open" ? 3000 : false`) freezes whatever state was current at render. So when the gateway later connects mid-session, polling won't silence until something else re-renders the component.
- **Recommend:** Use the **function form** of `refetchInterval: () => isActiveRef.current && getRealtimeClient().getConnectionState() !== "open" ? 3000 : false` so it's re-read each tick (it self-corrects within one interval), AND ensure `isActive` flips re-render the component when `task.status` changes (it will, via the query data). Acknowledge the up-to-3s lag before polling stops after WS opens. For MVP (no gateway yet) this is moot, but the design claims "zero page changes" when the gateway lands — qualify that. Update D2 step 3.

### S11 — `RealtimeEvent.payload` is unknown and `task:<id>` carries no version-event id; cache invalidation is fine but "frame backfills events" is muddled
- **Where:** `design.md D2 step 1`, `spec.md` "Frame invalidates caches".
- **Finding:** `RealtimeEvent` (`envelope.ts`) is `{topic, kind, seq, ts, payload}` with `payload:unknown`. The handler that "invalidates the relevant caches" is fine and doesn't need payload. But the design conflates the `task:<id>` topic (task-level status) with version events. Only `version:<id>` corresponds to `task_events`/the events endpoint. Invalidating `["events", currentVersionId]` on a `task:<id>` frame is harmless but the prose should not imply task frames feed the event log.
- **Recommend:** Clarify which topic invalidates which cache: `task:<id>` → `["task",id]` + `["versions",taskId]`; `version:<currentVersionId>` → `["events",currentVersionId]` (+ versions). Tie gap-fill (S2) to the version topic only.

---

## Minor

### S12 — `current_version` is a nullable UUID *pointer*; mark-current and "current version events" must null-guard
- **Where:** `spec.md` "Version Tree Rendering", `design.md` line 16, `tasks.md §3.3/§4.3`.
- **Finding:** `TaskInfo.CurrentVersion` and `TaskSummary.CurrentVersion` are `*uuid.UUID` (JSON `null` when unset) — confirmed `read_dtos.go` lines 46/69; `TaskDetail.CurrentVersion` is `*VersionNode` (the node, not just an id). So a freshly-created task may have `current_version: null`, in which case the event log query (`useVersionEventsQuery(currentVersionId)`) must be disabled (`enabled: !!currentVersionId`) and the tree "mark current" must no-op. The spec/tasks don't mention the null case.
- **Recommend:** Note `current_version` nullability; gate the events query on a non-null current version; render the tree without a "current" marker when null. Also note TaskList row "current version" column will be null for new tasks.

### S13 — `next_after_id` returns the input `after_id` when empty — don't treat as "no more" sentinel
- **Where:** `design.md` line 19, event log windowing (`Risks`), `tasks.md §6.1`.
- **Finding:** `EventPage.NextAfterID` = last returned id, or the **input after_id** when the page is empty (`read_dtos.go` lines 148–150). So polling that resends `after_id=next_after_id` is correct, but you cannot infer "done" from `next_after_id` being unchanged vs a `0` sentinel — it is never `0` after the first page unless empty from the start.
- **Recommend:** Document the cursor resume semantics in the events hook; "no new events" is `items.length === 0`, not a special `next_after_id`.

### S14 — "newest-first" ordering claim is unverified for the list endpoint
- **Where:** `spec.md` "Task List Page" / "Tasks render newest-first", `proposal.md`.
- **Finding:** I could not locate the sqlc query file to confirm `ORDER BY created_at DESC` for `ListTasks` (the generated file wasn't found at the guessed path). The versions list IS documented as `version_no asc` (ascending, not newest-first). Asserting "newest-first" in a test will fail if the server orders otherwise.
- **Recommend:** Verify the actual `ListTasks` ORDER BY before asserting order in the spec/test; if unconfirmed, soften the spec to "in the order the API returns" and have the MSW fixture define the order the test asserts.

### S15 — `task_type` select values are invented from worker tests, with no API enforcement
- **Where:** `design.md` Open Question 2, `tasks.md §4.2`, `spec.md` create form.
- **Finding:** There is no canonical task-type list in `api/` or a shared registry; the only concrete values are `code-gen` and `research`, found in `worker/tests/*` and `worker/README.md` (the worker `AgentRegistry`). The API `createTask` does not validate `task_type` against an enum at the HTTP layer (it forwards to the domain). So a constrained `<select>` of `code-gen`/`research` is a reasonable MVP choice but is a frontend invention, not a contract.
- **Recommend:** Keep the constrained select but explicitly note in the design that the values mirror the worker `AgentRegistry` (`code-gen`, `research`) and are not API-enforced; flag that adding a worker agent type requires updating this list until a registry endpoint exists. This matches Open Question 2 — make it a decision, not an open question, for MVP.

### S16 — `web/README.md` existence not confirmed; doc task may be create-not-edit
- **Where:** `proposal.md` Impact, `tasks.md §7.4`.
- **Finding:** Tasks assume editing `web/README.md`. (A `web/README.md` may or may not exist; a worker README does.) Minor, but if absent, AGENTS.md §"NEVER proactively create documentation" tension — though here it's user-scoped work so fine.
- **Recommend:** Confirm `web/README.md` exists; if not, either create it deliberately or fold the notes into existing docs.

### S17 — `RootLayout`/`RequireAuth` re-render and `useRealtime` stable-handler requirement
- **Where:** `design.md D2 step 1`, `tasks.md §2.1`.
- **Finding:** `use-realtime.ts` effect deps are `[topic, handler]` — an unstable handler re-subscribes every render (churns refcount). The design correctly says "stable via useCallback," good. But the handler closes over `queryClient` + ids; ensure the `useCallback` deps are exactly `[taskId, currentVersionId]` (stable strings) and it calls `queryClient.invalidateQueries` (a stable singleton) — do not include changing objects. Also `topic` strings `"task:"+taskId` are recomputed each render but are value-stable, so fine.
- **Recommend:** In task 2.1 spell out the `useCallback` dependency list and that the topic strings must be memoized or are primitive-stable, to satisfy the `useRealtime` contract and avoid resubscribe churn.

---

## Summary of required proposal edits
- Fix S1 (npm/script names) and S2 (id-vs-seq backfill) before implementation — both are wrong as written.
- Resolve the not-found retry mechanism (S3) and the three-way active-status inconsistency (S4).
- Pin the `invalid_input` data contract and decide whether `ApiError` must be extended (S6) — that touches "reused, unchanged" code.
- Tighten the router-test rewrite (S7), onGap singleton handling (S8), and the refetchInterval function form (S10).
- The testing-library-absent risk does not apply (S9): the harness is complete; the real constraint is absolute MSW URLs + `onUnhandledRequest:"error"`.

---

## Evaluation & disposition (proposal author)

Verified against code: `web/package.json` (`packageManager: npm@11`, scripts lint/typecheck/test/build), `web/src/services/http.ts` (`ApiError` carries only code/message/status/traceId — no `data`), `api/queries/tasks.sql` (`ListTasks ORDER BY created_at DESC, id DESC`), `api/internal/interfaces/http/errors.go` (`task_not_found`→404, `invalid_input`→400). **All actionable findings accepted.** Two turned out to be non-issues on verification.

| # | Severity | Disposition |
|---|----------|-------------|
| S1 | Critical | ✅ tasks §7 → `npm run lint`/`typecheck`/`build`, `npm test`; proposal Impact "Tooling: npm". |
| S2 | Critical | ✅ D2 step 2 + task 2.2 + spec: gap-fill is **id-based** (`after_id`=max event `id` seen), version-topic only; never feed `seq`. |
| S3 | Critical | ✅ new design D7; `useTaskQuery` per-query `retry` skips 404 + `meta:{silent}`; TaskDetail renders not-found from `error` (tasks 1.3/4.3). |
| S4 | Important | ✅ D3 reconciled: mirror API `IsActive` (5) but note task-reachable set is `{pending,running,paused}`; spec/tasks reason text no longer promises `cancelling`. |
| S5 | Important | ✅ spec + task 4.1: **single-select** filter, exactly the six, never `queued`/`cancelling`. |
| S6 | Important | ✅ D5 + new task 1.5 + Impact: `ApiError` gains additive `data?: unknown`; `{field, reason}` shape stated; `field:"body"`→form-level error. |
| S7 | Important | ✅ task 5.3 spells out `QueryClientProvider` + MSW + root `data-testid`s; pages expose testids (4.1/4.3). |
| S8 | Important | ✅ D2 + task 2.2: `setRealtimeOnGap` registered **once at bootstrap**, topic-keyed, not per page. |
| S9 | Important (correction) | ✅ premise corrected; task 6.1: absolute `http://localhost/api/v1/...` handlers, `onUnhandledRequest:"error"`, mock every mount call. |
| S10 | Important | ✅ D2 step 3 + task 2.3: `refetchInterval` **function form**, re-read each tick; "self-corrects within one interval" (not "zero page changes"). |
| S11 | Important | ✅ D2 step 1 + spec: explicit topic→cache mapping; payload not read; gap-fill version-only. |
| S12 | Minor | ✅ events query `enabled:!!versionId`; null-tolerant tree/column; design risk added. |
| S13 | Minor | ✅ design risk: "no more" = `items.length===0`, `next_after_id` echoes input on empty. |
| S14 | Minor | ✅ **verified OK** — `ListTasks` is newest-first; kept, MSW fixture defines asserted order. |
| S15 | Minor | ✅ promoted to decision D8: `task_type` select = worker `AgentRegistry` (`code-gen`/`research`), not API-enforced; removed from Open Questions. |
| S16 | Minor | ✅ **verified** — `web/README.md` exists; task 7.4 edits it. |
| S17 | Minor | ✅ task 2.1: `useCallback` deps exactly `[taskId, currentVersionId]`, closes over singleton `queryClient` only. |
