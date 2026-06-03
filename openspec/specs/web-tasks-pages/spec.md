# web-tasks-pages Specification

## Purpose
TBD - created by archiving change add-web-tasks-pages. Update Purpose after archive.
## Requirements
### Requirement: Task List Page

The web app SHALL render a Task List page at `/tasks` that fetches `GET /api/v1/tasks` via React Query and displays the caller's tasks newest-first. The page MUST support offset pagination (page / page_size controls) and a **single-select** status filter restricted to the six task statuses (`pending`, `running`, `paused`, `cancelled`, `succeeded`, `failed`) plus an "all" option that omits the param; it MUST NOT send `queued` / `cancelling` (the API rejects those with `400 invalid_input`). Each row MUST show title, task_type, a status badge, and the embedded cost summary's `amount_usd` (rendered as a string, never parsed to a number), and MUST link to that task's detail page. The `current_version` column MUST tolerate a `null` value. The page MUST expose a "New task" action that navigates to the create page.

#### Scenario: Tasks render newest-first with pagination

- **WHEN** the page loads and `GET /api/v1/tasks` returns `{items, page, page_size, total}`
- **THEN** each item is rendered as a row with title, type, status badge, and cost
- **AND** changing the page control refetches with the new `page` query param

#### Scenario: Status filter narrows the list

- **WHEN** the user selects a status filter value
- **THEN** the query refetches with that `status` param and only matching tasks are shown

#### Scenario: Empty list shows an empty state

- **WHEN** `GET /api/v1/tasks` returns zero items
- **THEN** an empty-state message is shown instead of a table body, with the "New task" action still available

### Requirement: Task Create Page

The web app SHALL render a Task Create page at `/tasks/new` with a form for `title`, `task_type`, `prompt`, an optional `params` (JSON), and an optional `lane`, submitting to `POST /api/v1/tasks` via a React Query mutation. On success the app MUST navigate to the created task's detail page (`/tasks/{task_id}`). On `400 invalid_input` the offending field's error MUST be shown inline without losing the user's input.

#### Scenario: Successful create navigates to detail

- **WHEN** the form is submitted with valid fields and `POST /api/v1/tasks` returns `{task_id, version_id, version_no, status}`
- **THEN** the app navigates to `/tasks/{task_id}`
- **AND** the task list query is invalidated so the new task appears on return

#### Scenario: Invalid params JSON is rejected before submit

- **WHEN** the `params` field contains text that is not valid JSON
- **THEN** the form blocks submission and shows a params validation error
- **AND** no request is sent

#### Scenario: Server validation error shows inline

- **WHEN** the server responds `400` with `code:"invalid_input"` naming a field
- **THEN** that field shows the error message and the entered values are preserved

### Requirement: Task Detail Page

The web app SHALL render a Task Detail page at `/tasks/:id` that fetches `GET /api/v1/tasks/{id}` and shows the task title, status badge, task_type, and cost summary. It MUST render a version tree from `GET /api/v1/tasks/{id}/versions` and an event log from `GET /api/v1/versions/{version_id}/events` for the task's current version. A loading state MUST show while the detail query is pending, and an unowned/unknown task (`404`) MUST show a not-found state rather than a generic error toast loop.

#### Scenario: Detail renders task, versions, and events

- **WHEN** the page loads for a task the caller owns
- **THEN** the task header, the version tree, and the current version's event log are rendered

#### Scenario: Unknown task shows not-found

- **WHEN** `GET /api/v1/tasks/{id}` returns `404`
- **THEN** a not-found state is shown (no infinite ret/refetch loop; 404 is not retried)

### Requirement: Version Tree Rendering

The Task Detail page SHALL render the versions returned by `GET /api/v1/tasks/{id}/versions` as a parent-indented list ordered by `version_no`, where each node shows its `version_no`, status badge, and cost. The node corresponding to the task's `current_version` MUST be visually marked as current. The rendering MUST NOT require any graph/visualization dependency.

#### Scenario: Versions render as an indented tree

- **WHEN** the versions endpoint returns nodes with `parent_id` links
- **THEN** child versions are indented under their parent and ordered by `version_no`
- **AND** the current version node is marked as current

### Requirement: Iterate Action With UI Task-Level Mutex

The Task Detail page SHALL expose an Iterate action that submits `POST /api/v1/tasks/{id}/iterate` via a mutation. While `task.status` is in an active state (`pending`, `running`, `paused`, `cancelling`) the action MUST be disabled with a reason indicating the task is busy. The backend remains the source of truth: a `409 active_version_exists` response MUST surface a message naming the active version (from `data.active_version_id` / `data.active_version_status`) and trigger a refetch of the task; it MUST NOT be retried.

#### Scenario: Iterate disabled while task active

- **WHEN** the loaded task's status is `running` (or any active status)
- **THEN** the Iterate action is disabled and a reason is shown
- **AND** no iterate request can be sent from the UI

#### Scenario: Iterate enabled in a terminal state

- **WHEN** the task's status is terminal (`succeeded` / `failed` / `cancelled`)
- **THEN** the Iterate action is enabled
- **AND** submitting it issues `POST /api/v1/tasks/{id}/iterate` and, on success, invalidates the task + versions queries

#### Scenario: 409 conflict is surfaced, not retried

- **WHEN** an iterate submission races the backend and receives `409 active_version_exists`
- **THEN** a message naming the active version is shown, the task query is refetched, and the request is not retried

### Requirement: Live Observation With Polling Fallback

The Task Detail page SHALL subscribe to the realtime topic `task:<id>` (and, when the task has a non-null current version, `version:<current_version_id>`) via the existing `realtimeClient`, and on each received frame invalidate the corresponding React Query caches: a `task:` frame invalidates the task + versions queries; a `version:` frame invalidates that version's events query. Because the Realtime Gateway server may be unavailable, the page MUST additionally poll via React Query `refetchInterval` (function form, re-evaluated each tick) while the task is in an active status **and** no WS connection is open, and MUST stop polling once the task reaches a terminal status or the WS connection opens. Gap-fill of missed events MUST go through the client's `onGap` callback hitting `GET /versions/{id}/events?after_id=<global event id cursor>` — using the highest event `id` already seen, NOT the per-run `seq`.

#### Scenario: Frame invalidates caches

- **WHEN** a realtime frame arrives on `task:<id>`
- **THEN** the task (and its versions/events) React Query entries are invalidated and refetched

#### Scenario: Polling runs only while active and WS not open

- **WHEN** the task is in an active status and `realtimeClient.getConnectionState()` is not `"open"`
- **THEN** the detail/events queries refetch on an interval (function-form `refetchInterval`, re-evaluated each tick)
- **AND** once the task reaches a terminal status, or the WS connection opens, the interval refetch stops

#### Scenario: Subscriptions cleaned up on unmount

- **WHEN** the user navigates away from the detail page
- **THEN** the topic subscriptions are released (unsubscribe) so no stale handlers remain

### Requirement: Routing And Placeholder Removal

The router SHALL point `/tasks` at the Task List page, `/tasks/:id` at the Task Detail page, and add `/tasks/new` for the Task Create page, all inside the authenticated `RootLayout`. The consumed `TaskListPlaceholder` and `TaskDetailPlaceholder` modules MUST be removed along with their references.

#### Scenario: Routes resolve to real pages

- **WHEN** an authenticated user visits `/tasks`, `/tasks/new`, or `/tasks/:id`
- **THEN** the Task List, Task Create, and Task Detail pages render respectively

#### Scenario: Unauthenticated access still redirects to login

- **WHEN** an unauthenticated user visits any `/tasks*` route
- **THEN** the existing `RequireAuth` guard redirects to `/login`

### Requirement: Task Control Bar With State-Machine-Aware Actions

The Task Detail page SHALL render a control bar with `Pause`, `Resume`, and `Cancel` actions that issue `POST /api/v1/tasks/{task_id}/control` with body `{action}` (`action ∈ {pause, resume, cancel}`). Each action's enabled state MUST mirror the `task-control-api` state-machine preconditions against the current `task.status`:

- `Pause` enabled only when `status ∈ {pending, running}`.
- `Resume` enabled only when `status = paused`.
- `Cancel` enabled only when `status ∉ {cancelled, succeeded, failed}` (i.e. not terminal).

A disabled action button MUST carry a human-readable reason (e.g. via `title`) explaining why it is unavailable. This client-side gating is advisory UX; the API remains authoritative, so a `409 invalid_state` returned for an action that the stale client view believed was allowed MUST still be handled (see below) rather than treated as impossible.

The control mutation MUST opt out of the global error toast and have the page handle outcomes inline (mirroring the Iterate Action). The success response is HTTP `202` and MUST NOT be treated as an immediate status change: the page MUST NOT optimistically mutate `task.status`. Instead, the new status arrives through the existing live pipeline — the mutation MUST invalidate the task and versions queries on settle, and the Live Observation pipeline reflects the worker-driven status change when its `status` event arrives.

While a control request is in flight, the page MUST disable all three actions to prevent accidental double submission (the API tolerates duplicates, so this is a UX guard, not a correctness requirement).

#### Scenario: Action enablement follows task status
- **WHEN** the Task Detail page renders a task in `running` status
- **THEN** `Pause` and `Cancel` MUST be enabled and `Resume` MUST be disabled; AND for a task in `paused` status, `Resume` and `Cancel` MUST be enabled and `Pause` disabled; AND for a task in a terminal status (`succeeded` / `failed` / `cancelled`) all three MUST be disabled

#### Scenario: Disabled action exposes a reason
- **WHEN** an action is disabled because the current status does not permit it (e.g. `Resume` on a `running` task)
- **THEN** the disabled button MUST expose a human-readable reason (e.g. a `title` attribute) explaining why it is unavailable

#### Scenario: Pause sends a control request and confirms
- **GIVEN** a Task Detail page for a task in `running` status
- **WHEN** the user clicks `Pause`
- **THEN** the page MUST `POST /api/v1/tasks/{id}/control` with `{action:"pause"}`, MUST show a confirmation toast, and MUST NOT change the displayed `status` until a subsequent status update (live frame or refetch) reports it

#### Scenario: Status updates through the live pipeline, not optimistically
- **GIVEN** a control request that returned `202`
- **WHEN** no status event has yet arrived
- **THEN** the displayed `task.status` MUST remain the pre-request value; AND when the task/versions queries are next invalidated (by the live frame or the mutation's settle) and refetched, the buttons MUST re-derive their enabled state from the new status

#### Scenario: 409 invalid_state is surfaced with the server message
- **GIVEN** a task whose status changed after the page rendered (stale view)
- **WHEN** the user clicks an action and the API returns `409` with `code = "invalid_state"` and a message naming the current status
- **THEN** the page MUST surface a warning carrying that server message, MUST NOT retry it, and MUST NOT leave the action stuck in a pending state

#### Scenario: Unexpected control error surfaces a generic toast, not a retry
- **WHEN** a control request fails with an `ApiError` other than `409 invalid_state` (e.g. an unexpected `400` / `500` / network error)
- **THEN** the page MUST surface a generic error toast, MUST NOT retry the request, and the action buttons MUST return to their status-derived enabled state once the request settles

#### Scenario: Best-effort cancel is flagged as possibly not-yet-effective
- **GIVEN** a task with no active run (pre-claim)
- **WHEN** the user clicks `Cancel` and the API returns `202` with `effective = "best_effort"`
- **THEN** the page MUST inform the user that the cancel may not take effect until the task is claimed

#### Scenario: In-flight control disables the actions
- **WHEN** a control request is in flight
- **THEN** all three action buttons MUST be disabled until the request settles

### Requirement: Task Detail Cost Panel With Token Breakdown

The Task Detail page SHALL render a cost panel that shows the task's aggregate `CostSummary` breakdown — the amount plus the `input` / `output` / `cached` token counts, `tool_calls`, and `wall_time_ms` — sourced from `GET /api/v1/tasks/{task_id}/cost` (`data.total`). This is in addition to the existing inline `CostBadge`; the badge (driven by the already-loaded read DTO `cost`) MUST remain.

The amount displayed in the panel MUST follow the same decimal-string display rule as `CostBadge` (treat `amount_usd` as a string, truncate for display, keep the full value available), and MUST NOT parse the amount to a float. Token / tool / wall fields are JSON numbers and render as integers.

The panel MUST render loading and zero states gracefully: a task with no settled cost yet (the endpoint returns a zero-filled `CostSummary`, never 404 for an owned task) MUST show an all-zero breakdown, not an error. The cost query MUST skip retry on 404 and suppress its React Query cache toast (`meta.silent`), mirroring the existing task query. Because the page is already gated by the task query (which resolves the task before the panel mounts), a panel-only `404 task_not_found` implies a mid-flight ownership/race change and MUST be treated as a defensive no-op — the panel renders nothing/zero and MUST NOT raise a second not-found screen; the task-level not-found handling stays authoritative.

#### Scenario: Cost panel shows the token breakdown
- **GIVEN** a Task Detail page for an owned task whose `/tasks/{id}/cost` total has `amount_usd = "1.72000000"`, input/output/cached tokens, tool calls, and wall time
- **THEN** the page MUST render a cost panel showing the amount (e.g. `"$1.7200"`) alongside the input / output / cached token counts, tool calls, and wall time

#### Scenario: Zero-cost task renders an all-zero panel
- **GIVEN** an owned task whose `/tasks/{id}/cost` total is the zero-filled `CostSummary` (`amount_usd = "0.00000000"`)
- **THEN** the cost panel MUST render with all zeros and MUST NOT show an error state

#### Scenario: Panel amount is not parsed to float
- **GIVEN** a total with `amount_usd = "0.06750000"`
- **THEN** the displayed amount MUST be derived from the string by truncation (`"$0.0675"`), never via `parseFloat`

#### Scenario: Inline badge and panel coexist
- **WHEN** the Task Detail page renders
- **THEN** both the inline `CostBadge` (from the read DTO `cost`) and the cost panel (from `/tasks/{id}/cost`) MUST be present; they MAY momentarily differ during settle and MUST converge on the next refetch

### Requirement: Version Artifacts Expandable List With Direct Download

The TaskDetail page SHALL surface a version's produced artifacts inline in the version tree. Each `VersionTree` row MUST present a per-version disclosure (expand/collapse) control. The artifact list for a version MUST be **lazily** fetched only when its row is first expanded (consuming the `features/artifacts/` slice keyed by that `version_id`), so collapsed versions issue no artifact request. Because the backend exposes artifacts strictly per version (no task-level aggregate), the surface MUST allow any version row — not only the current version — to be expanded.

When expanded, the row MUST render distinct loading, empty, and error states for that version's artifacts: a pending fetch shows a loading affordance; an empty result (`artifacts: []`) shows an explicit "no artifacts" message (NOT a not-found or error screen); and a transport/server failure shows a per-version error affordance without crashing the page or collapsing sibling rows. Each artifact entry MUST display its `kind`, `mime` (or a neutral placeholder when `null`), a human-readable size derived from `bytes` (with a neutral placeholder when `bytes` is `null`), and a **Download** action.

The Download action MUST mint a fresh presigned URL via the `features/artifacts/` presign action and then hand the browser directly to that URL (e.g. an anchor navigation), so artifact bytes are downloaded straight from OSS and never proxied through the web app. The app MUST NOT cache or reuse a previously minted URL for a subsequent click — each Download re-mints. A presign failure MUST surface a user-visible error (toast and/or inline) and MUST NOT leave the row in a silent broken state.

The expandable artifact surface MUST NOT alter the existing version-tree behavior (parent-linked flattening, status/cost badges, the `current` marker) and MUST NOT block on the artifact fetch — the tree renders immediately and artifacts load per expansion.

#### Scenario: Expanding a version lazily loads and lists its artifacts

- **GIVEN** the TaskDetail version tree is rendered with a version that produced two artifacts
- **WHEN** the user expands that version's row for the first time
- **THEN** the page MUST issue exactly one artifact-list request for that `version_id`, render the two entries in server order each with `kind`, `mime`, a human-readable size, and a Download action, and MUST NOT have issued any artifact request for still-collapsed rows

#### Scenario: Expanding a version with no artifacts shows an empty state

- **GIVEN** a version whose artifact list is `[]`
- **WHEN** the user expands its row
- **THEN** the row MUST show an explicit "no artifacts" empty message, NOT a not-found or error screen

#### Scenario: Download mints a fresh URL and navigates to OSS directly

- **GIVEN** an expanded version listing an artifact
- **WHEN** the user clicks Download
- **THEN** the app MUST request a fresh presigned URL for that artifact and navigate the browser to the returned `url` (direct OSS download, bytes not proxied through the app), and a second click MUST re-mint rather than reuse the prior URL

#### Scenario: A presign failure surfaces a single error, tree stays intact

- **WHEN** the Download presign request fails (`artifact_not_found` or `internal_error`)
- **THEN** the page MUST surface exactly one visible error (a single toast and/or inline hint, never a duplicate toast), MUST NOT navigate, and MUST keep the version tree and other expanded rows rendered

#### Scenario: Nullable artifact metadata renders with placeholders

- **GIVEN** an expanded version with an artifact whose `mime` and `bytes` are `null`
- **THEN** the entry MUST still render with a neutral placeholder for the missing `mime` and size and MUST still offer the Download action (which echoes the same nullable fields from presign)
