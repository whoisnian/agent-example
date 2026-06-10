# web-tasks-pages Specification

## Purpose
TBD - created by archiving change add-web-tasks-pages. Update Purpose after archive.
## Requirements
### Requirement: Task List Page

The web app SHALL render a Task List page at `/tasks` that fetches `GET /api/v1/tasks` via React Query and displays the caller's tasks newest-first. The page MUST support offset pagination (page / page_size controls) and a **single-select** status filter restricted to the six task statuses (`pending`, `running`, `paused`, `cancelled`, `succeeded`, `failed`) plus an "all" option that omits the param; it MUST NOT send `queued` / `cancelling` (the API rejects those with `400 invalid_input`). Each row MUST show title, task_type, a status badge, and the embedded cost summary's `amount_usd` (rendered as a string, never parsed to a number), and MUST link to that task's detail page. The `current_version` column MUST tolerate a `null` value. The page MUST NOT render its own "New task" action — task creation is reached exclusively via the shell's left-navigation "New task" button (see `web-bootstrap`).

#### Scenario: Tasks render newest-first with pagination

- **WHEN** the page loads and `GET /api/v1/tasks` returns `{items, page, page_size, total}`
- **THEN** each item is rendered as a row with title, type, status badge, and cost
- **AND** changing the page control refetches with the new `page` query param

#### Scenario: Status filter narrows the list

- **WHEN** the user selects a status filter value
- **THEN** the query refetches with that `status` param and only matching tasks are shown

#### Scenario: Empty list shows an empty state

- **WHEN** `GET /api/v1/tasks` returns zero items
- **THEN** an empty-state message is shown instead of a table body, and the page renders no "New task" button of its own (the shell's left-nav action remains the creation entry point)

### Requirement: Task Create Page

The web app SHALL render a Task Create page at `/tasks/new` styled as a **chat composer**, visually consistent with the Task Detail conversation: a centered greeting headline above a **composer card** containing a multiline `prompt` input and a submit affordance. `task_type` MUST be selected via **chips** beneath or inside the composer (one chip per registered type, single-select, with a default selection). The optional `params` (JSON) and `lane` fields MUST be available behind a collapsed "advanced" affordance rather than as always-visible form rows. The page MUST NOT render a `title` input — the request body omits `title` and the server derives it from the prompt (see `task-write-api`). Submission posts to `POST /api/v1/tasks` via the existing React Query mutation; `Ctrl/Cmd+Enter` inside the prompt input MUST also submit. On success the app MUST navigate to the created task's detail page (`/tasks/{task_id}`). On `400 invalid_input` the offending field's error MUST be shown inline without losing the user's input.

#### Scenario: Composer renders without a title field

- **WHEN** the user opens `/tasks/new`
- **THEN** the page renders the greeting headline, the composer card with the prompt input and submit affordance, and task-type chips — and no title input exists

#### Scenario: Successful create navigates to detail

- **WHEN** the composer is submitted with a valid prompt and selected task-type chip and `POST /api/v1/tasks` returns `{task_id, version_id, version_no, status}`
- **THEN** the request body contains `task_type` and `prompt` but no `title`, the app navigates to `/tasks/{task_id}`
- **AND** the task list query is invalidated so the new task appears on return

#### Scenario: Keyboard submit

- **WHEN** the user presses `Ctrl+Enter` (or `Cmd+Enter`) with a non-empty prompt
- **THEN** the composer submits identically to activating the submit affordance

#### Scenario: Invalid params JSON is rejected before submit

- **WHEN** the advanced `params` field contains text that is not valid JSON
- **THEN** the form blocks submission and shows a params validation error
- **AND** no request is sent

#### Scenario: Server validation error shows inline

- **WHEN** the server responds `400` with `code:"invalid_input"` naming a field
- **THEN** that field shows the error message and the entered values are preserved

### Requirement: Task Detail Page

The web app SHALL render a Task Detail page at `/tasks/:id` that fetches `GET /api/v1/tasks/{id}` and presents it as a **conversation column**: a compact header (task title, status badge, task_type, cost summary, and the control bar), a **scrollable conversation body** rendering one **turn per version** (see "Version Turns Rendering" semantics below), and a **persistent iterate composer pinned at the bottom** (see "Iterate Action With UI Task-Level Mutex"). The body MUST scroll independently while the header and the composer remain visible (the page MUST NOT rely on whole-page scrolling to reach the composer).

Versions from `GET /api/v1/tasks/{id}/versions` MUST render as turns in ascending `version_no` order (linear, no parent-indented tree). Each turn MUST show: the version's `prompt` as the user-message position (lazily fetched via the existing `GET /api/v1/versions/{id}` read, cached, with a quiet loading state and a silent fallback to the version number when the read fails); a result line with `version_no`, status badge, and cost summary; the turn's inline artifact cards and rollback actions (see the dedicated requirements below). The turn for the task's `current_version` MUST be visually marked as current. A turn whose `parent_id` is not the immediately preceding version (a rollback-branch fork) MUST carry a "from v{n}" origin label naming its parent version.

The event log from `GET /api/v1/versions/{version_id}/events` MUST render inline within the current version's turn only, presented as an **assistant message**: a left-aligned assistant-position block (visually consistent with a chat reply, distinct from the right-aligned user prompt), inside which each event renders as a readable line — `status` events as a human-readable status transition, `error` events with destructive styling naming the error code/message, and other kinds as the kind plus a bounded payload summary. The raw single-line monospace log presentation is superseded. The live/polling pipeline is unchanged.

A loading state MUST show while the detail query is pending, and an unowned/unknown task (`404`) MUST show a not-found state rather than a generic error toast loop. The rendering MUST NOT require any graph/visualization dependency.

#### Scenario: Detail renders header, turns, and the current turn's events

- **WHEN** the page loads for a task the caller owns with three versions
- **THEN** the compact header renders, three turns render in ascending `version_no` order each with its prompt and result line, and the current version's turn additionally renders the event log as an assistant-position message block

#### Scenario: Events render as readable assistant content

- **WHEN** the current version's events include a `status` event and an `error` event
- **THEN** the assistant block MUST render the status event as a human-readable transition line and the error event with destructive styling naming its code, not as raw JSON-only monospace rows

#### Scenario: Composer stays pinned while the body scrolls

- **WHEN** the conversation body exceeds the visible height of the center column
- **THEN** the body region scrolls independently and the header and the iterate composer remain visible without whole-page scrolling

#### Scenario: Branch fork carries an origin label

- **GIVEN** a version created by a branch rollback whose `parent_id` is not the immediately preceding version
- **WHEN** its turn renders
- **THEN** the turn MUST display a "from v{n}" label naming the parent version, while remaining in linear `version_no` position

#### Scenario: Prompt read failure degrades silently

- **WHEN** the per-turn version read (`GET /api/v1/versions/{id}`) fails for a turn
- **THEN** that turn MUST still render its result line (falling back to the version number, no prompt text) without any toast and without blocking other turns

#### Scenario: Unknown task shows not-found

- **WHEN** `GET /api/v1/tasks/{id}` returns `404`
- **THEN** a not-found state is shown (no infinite ret/refetch loop; 404 is not retried)

### Requirement: Iterate Action With UI Task-Level Mutex

The Task Detail page SHALL expose the Iterate action as a **persistent composer** (a prompt textarea plus a submit control) pinned at the bottom of the conversation column, replacing the previous toggle-button-revealed form. Submitting the composer issues `POST /api/v1/tasks/{id}/iterate` via a mutation; on success the composer's input MUST be cleared. While `task.status` is in an active state (`pending`, `running`, `paused`, `cancelling`) the composer (textarea and submit) MUST be disabled with a reason indicating the task is busy; while a submission is in flight the submit control MUST be disabled to prevent double submission. The backend remains the source of truth: a `409 active_version_exists` response MUST surface a message naming the active version (from `data.active_version_id` / `data.active_version_status`) and trigger a refetch of the task; it MUST NOT be retried, and the composer input MUST be preserved on failure.

#### Scenario: Composer disabled while task active

- **WHEN** the loaded task's status is `running` (or any active status)
- **THEN** the composer textarea and submit control are disabled and a reason is shown
- **AND** no iterate request can be sent from the UI

#### Scenario: Composer enabled in a terminal state

- **WHEN** the task's status is terminal (`succeeded` / `failed` / `cancelled`)
- **THEN** the composer is enabled
- **AND** submitting it issues `POST /api/v1/tasks/{id}/iterate` and, on success, clears the composer input and invalidates the task + versions queries

#### Scenario: 409 conflict is surfaced, not retried

- **WHEN** an iterate submission races the backend and receives `409 active_version_exists`
- **THEN** a message naming the active version is shown, the task query is refetched, the request is not retried, and the typed prompt remains in the composer

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

### Requirement: Rollback Action With Mode Selection And UI Task-Level Mutex

The Task Detail page SHALL expose a per-version Rollback action **in the footer of each non-current conversation turn** (replacing the previous version-tree row placement) that submits `POST /api/v1/tasks/{id}/rollback` via a mutation, supporting both modes:
- **`branch`** — re-execute from the target version (optional `prompt`; an empty prompt is valid and the backend auto-fills it). Success is `201` with `{version_id, version_no, status}`.
- **`switch`** — repoint `current_version` at the target only, with no run. Success is `200` with `{current_version_id, version_no, status}`.

While `task.status` is in an active state (`pending`, `running`, `paused`, `cancelling`) **both** rollback modes MUST be disabled with a reason indicating the task is busy — the backend requires a non-active task for both modes, not only branch. The `switch` option MUST additionally be disabled (advisory) on a turn whose version `is_active` is true, because the backend rejects a switch to a non-terminal version. The current version's turn MUST NOT offer a rollback action.

The backend remains the source of truth. The rollback mutation MUST opt out of the global error toast and have the page handle outcomes inline (mirroring the Iterate and Control actions); it MUST NOT optimistically mutate `task.status`. A `409 active_version_exists` response MUST surface a message naming the active version (from `data.active_version_id` / `data.active_version_status`); a `409 invalid_state` (switch to a non-terminal target) MUST surface a warning message. Neither 409 MUST be retried. On settle (success or conflict) the mutation MUST invalidate the task + versions queries.

#### Scenario: Rollback disabled on all turns while task active

- **WHEN** the loaded task's status is `running` (or any active status)
- **THEN** no turn footer offers an enabled rollback action (branch or switch) and a reason is shown
- **AND** no rollback request can be sent from the UI

#### Scenario: Branch rollback from a terminal task

- **WHEN** the task's status is terminal and the user picks `branch` in a non-current turn's footer
- **THEN** the page issues `POST /api/v1/tasks/{id}/rollback` with `{target_version_id, mode:"branch"}` (plus the optional prompt when provided)
- **AND** on the `201` success it invalidates the task + versions queries so the new version appears as a new turn

#### Scenario: Branch with empty prompt is accepted

- **WHEN** the user picks `branch` in a non-current turn's footer and leaves the prompt empty
- **THEN** the request is still sent with `mode:"branch"` and the empty prompt, and the `201` is handled normally (the backend auto-fills the prompt)

#### Scenario: Switch rollback repoints the current version

- **WHEN** the task's status is terminal and the user picks `switch` in a terminal non-current turn's footer
- **THEN** the page issues `POST /api/v1/tasks/{id}/rollback` with `{target_version_id, mode:"switch"}`
- **AND** on the `200` success it invalidates the task + versions queries so the "current" marker moves to the target turn on refetch

#### Scenario: Switch disabled on a non-terminal target

- **WHEN** a non-current turn's version has `is_active` true
- **THEN** that turn's `switch` option is disabled with a reason, while `branch` remains governed only by the task-level mutex

#### Scenario: 409 active_version_exists is surfaced, not retried

- **WHEN** a rollback submission races the backend and receives `409 active_version_exists`
- **THEN** a message naming the active version is shown, the task + versions queries are refetched, and the request is not retried

#### Scenario: 409 invalid_state on switch is surfaced as a warning

- **WHEN** a `switch` submission races and the target is no longer terminal, returning `409 invalid_state`
- **THEN** a warning message is shown, the queries are refetched, and the request is not retried

### Requirement: Version Artifact Cards With Direct Download

The TaskDetail page SHALL surface each version's produced artifacts as **inline artifact cards within that version's conversation turn**. Each turn MUST lazily fetch its version's artifacts (consuming the existing `features/artifacts/` slice keyed by that `version_id`; React Query caching deduplicates re-renders, and terminal versions are never re-polled). A turn whose artifact read returns `artifacts: []` MUST omit the artifact section entirely (no empty-state noise inside the conversation); a pending read shows a quiet loading affordance and a failed read shows a quiet inline error without any toast and without blocking the turn.

Each artifact card MUST render a file-type icon, the artifact's `kind` as the card title, and a secondary line with the mime label (neutral placeholder when `null`) and a human-readable size derived from `bytes` (neutral placeholder when `null`). **Activating the card MUST drive the right-column Artifact Preview panel**: it sets the global UI store's `selectedVersionId` to the turn's version and the store's selected-artifact id to that artifact, expanding the preview column when collapsed, so the panel previews exactly the activated artifact (see `web-artifact-preview`); the card's activation hit area MUST cover the card surface except the Download control. Each card MUST also offer a **Download** action: Download MUST mint a fresh presigned URL via the `features/artifacts/` presign action and hand the browser directly to that URL (bytes never proxied through the app); the app MUST NOT cache or reuse a previously minted URL — each Download re-mints. A presign failure MUST surface exactly one user-visible error and MUST NOT leave the turn in a silent broken state.

The default preview anchor on first render MUST remain the task's `current_version` (or no selection when there is none), so the preview panel lists the current version's artifacts before any explicit activation.

#### Scenario: Turns list their artifacts as cards, lazily

- **GIVEN** a task with two versions where only the second produced artifacts
- **WHEN** the conversation renders
- **THEN** the second turn MUST render one card per artifact (icon, `kind` title, mime/size secondary line) via a lazy per-version read, and the first turn MUST omit the artifact section entirely

#### Scenario: Activating a card drives the preview panel

- **GIVEN** a turn listing an artifact card
- **WHEN** the user activates the card (anywhere except its Download control)
- **THEN** the global store's `selectedVersionId` MUST become that turn's version id and the selected-artifact id MUST become that artifact's id, the preview column MUST expand if collapsed, and the Artifact Preview panel MUST preview that exact artifact

#### Scenario: Download mints a fresh URL and navigates to OSS directly

- **GIVEN** a turn listing an artifact card
- **WHEN** the user clicks its Download action
- **THEN** the app MUST request a fresh presigned URL for that artifact and navigate the browser to the returned `url` (direct OSS download), and a second click MUST re-mint rather than reuse the prior URL

#### Scenario: A presign failure surfaces a single error, the turn stays intact

- **WHEN** an inline Download presign request fails (`artifact_not_found` or `internal_error`)
- **THEN** the page MUST surface exactly one visible error (never a duplicate toast), MUST NOT navigate, and the turn and conversation MUST remain rendered

#### Scenario: Artifact read failure stays quiet inside the turn

- **WHEN** a turn's artifact-list read fails
- **THEN** that turn MUST show a quiet inline error affordance (no toast) and the rest of the conversation MUST render normally

#### Scenario: Nullable artifact metadata renders with placeholders

- **GIVEN** a turn with an artifact whose `mime` and `bytes` are `null`
- **THEN** the card MUST still render with neutral placeholders for the missing `mime` and size and MUST still offer activation and Download

