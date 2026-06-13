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

Versions from `GET /api/v1/tasks/{id}/versions` MUST render as turns in ascending `version_no` order (linear, no parent-indented tree). Each turn MUST present its content in **conversation order**: the version's `prompt` as the user-message position (lazily fetched via the existing `GET /api/v1/versions/{id}` read, cached, with a quiet loading state and a silent fallback to the version number when the read fails); a result line with `version_no`, status badge, and cost summary; then the turn's **execution section** (the assistant-position reply, see below); then the turn's artifact card **below the execution section** (see "Version Artifact Cards With Direct Download"); then rollback actions on non-current turns. The turn for the task's `current_version` MUST be visually marked as current. A turn whose `parent_id` is not the immediately preceding version (a rollback-branch fork) MUST carry a "from v{n}" origin label naming its parent version.

**Every turn owns an execution section** — the conversation history MUST stay continuous across iterations (iterating to v2 MUST NOT hide v1's execution process):

- The **current version's** turn renders its event log (from `GET /api/v1/versions/{version_id}/events`) expanded, appended to live via the existing live/polling pipeline (unchanged).
- A **historical (non-current) version's** turn renders its execution section **inline and expanded** — a prior version's conversation stays visible after iterating, like a chat history. There MUST be no collapse toggle and no truncated single-line summary affordance (that line overflowed the conversation column horizontally). The turn reads its own version's events (`GET /api/v1/versions/{version_id}/events`, React Query cached; a terminal version's events are static, so no polling) and renders the same assistant-position event log as the current turn. The version's `summary` surfaces inside that log as the assistant reply (the `summary` event), so no separate summary line is needed. Each turn's read loads the events' first page (`after_id=0`, default `limit`); a run with more events than one page MUST render the first page and indicate the log is truncated (a "load more" affordance is out of scope — see design), never silently dropping the tail without a marker.

The event log renders inside an **assistant message** block (left-aligned, visually consistent with a chat reply, distinct from the right-aligned user prompt), with each event rendered per its kind as defined in "Conversation-Style Event Rendering" — never as raw JSON-only monospace rows.

A loading state MUST show while the detail query is pending, and an unowned/unknown task (`404`) MUST show a not-found state rather than a generic error toast loop. The rendering MUST NOT require any graph/visualization dependency.

#### Scenario: Detail renders header and turns with conversation-ordered content

- **WHEN** the page loads for a task the caller owns with three versions
- **THEN** the compact header renders, three turns render in ascending `version_no` order, and within each turn the order is prompt → result line → execution section → artifact card, with the current version's event log expanded

#### Scenario: Iterating preserves the previous turn's execution history

- **GIVEN** a task whose v1 turn has a finished event log
- **WHEN** the user iterates and v2 becomes current
- **THEN** v1's turn MUST still render its execution section (its event log shown inline) and v2's turn renders the live log — no version's execution process disappears, and v1's history is NOT hidden behind a collapse control

#### Scenario: Historical turn shows its log inline without a collapse toggle

- **GIVEN** a historical (non-current) turn
- **WHEN** the conversation renders
- **THEN** that turn MUST render its version's event log inline (reading that version's events) with no collapse toggle and no truncated summary line

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

The composer SHALL use **chat-style keyboard shortcuts** that invert the textarea default: a plain **Enter** submits (same guards as the submit control — a no-op when the task is active, a submission is in flight, or the trimmed input is empty), and **Ctrl/Cmd+Enter** inserts a newline at the caret. **Shift+Enter** also inserts a newline (the native default). An Enter that is confirming an IME composition (e.g. a Chinese/Japanese input method) MUST NOT submit — it completes the composition only. A short hint near the composer SHALL communicate the Enter / Ctrl+Enter behavior.

#### Scenario: Composer disabled while task active

- **WHEN** the loaded task's status is `running` (or any active status)
- **THEN** the composer textarea and submit control are disabled and a reason is shown
- **AND** no iterate request can be sent from the UI

#### Scenario: Composer enabled in a terminal state

- **WHEN** the task's status is terminal (`succeeded` / `failed` / `cancelled`)
- **THEN** the composer is enabled
- **AND** submitting it issues `POST /api/v1/tasks/{id}/iterate` and, on success, clears the composer input and invalidates the task + versions queries

#### Scenario: Plain Enter submits

- **WHEN** the composer is enabled, holds a non-empty prompt, and the user presses Enter without a modifier (and not while composing via an IME)
- **THEN** the iterate request MUST be issued exactly as the submit control would, and on success the composer clears

#### Scenario: Ctrl+Enter inserts a newline and does not submit

- **WHEN** the user presses Ctrl (or Cmd) + Enter in the composer
- **THEN** a newline MUST be inserted at the caret and NO iterate request MUST be issued

#### Scenario: Empty Enter is a no-op

- **WHEN** the composer is empty (or whitespace only) and the user presses Enter
- **THEN** no iterate request MUST be issued

#### Scenario: 409 conflict is surfaced, not retried

- **WHEN** an iterate submission races the backend and receives `409 active_version_exists`
- **THEN** a message naming the active version is shown, the task query is refetched, the request is not retried, and the typed prompt remains in the composer

### Requirement: Live Observation With Polling Fallback

The Task Detail page SHALL subscribe to the realtime topic `task:<id>` (and, when the task has a non-null current version, `version:<current_version_id>`) via the existing `realtimeClient`, and on each received frame invalidate the corresponding React Query caches: a `task:` frame invalidates the task + versions queries; a `version:` frame invalidates that version's events query, AND — when the frame's `kind` is `"status"` — additionally invalidates that version's artifact-list query. The artifact list is refreshed on `status` frames only (NOT on `artifact` frames): the aggregate products card is withheld until the version is terminal (see "Version Artifact Cards With Direct Download"), so its data only needs to be current at completion — the terminal `status` frame both flips the card's gate (via the versions refetch) and refreshes its data, with no per-file churn mid-run and no manual refresh. Because the Realtime Gateway server may be unavailable, the page MUST additionally poll via React Query `refetchInterval` (function form, re-evaluated each tick) while the task is in an active status **and** no WS connection is open, and MUST stop polling once the task reaches a terminal status or the WS connection opens. Gap-fill of missed events MUST go through the client's `onGap` callback hitting `GET /versions/{id}/events?after_id=<global event id cursor>` — using the highest event `id` already seen, NOT the per-run `seq`.

#### Scenario: Frame invalidates caches

- **WHEN** a realtime frame arrives on `task:<id>`
- **THEN** the task (and its versions/events) React Query entries are invalidated and refetched

#### Scenario: Artifact frame does NOT refresh the artifact list

- **GIVEN** the detail page is open while the current version's run executes
- **WHEN** a `version:` frame with `kind = "artifact"` arrives mid-run
- **THEN** that version's events query MAY refresh but its artifact-list query MUST NOT be invalidated (the products card is withheld until completion — no mid-run product display)

#### Scenario: Status frame refreshes the artifact list at completion

- **WHEN** a `version:` frame with `kind = "status"` arrives
- **THEN** the version's artifact-list query MUST be invalidated so the completed artifact set is current at the moment the turn flips to its terminal badge and the products card becomes visible

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

The TaskDetail page SHALL surface each version's produced artifacts as **one aggregate artifact card per conversation turn**, positioned below the turn's execution section. The card MUST be shown **only once the version is in a terminal status** (`succeeded` / `failed` / `cancelled`): while the version is active (`pending` / `running` / `paused` / `cancelling`) the turn MUST NOT render the products card at all, because a mid-run product set is still changing and showing it is ambiguous. (Liveness is preserved without mid-run display: the terminal `status` frame flips this gate and refreshes the list — see "Live Observation" — so the card appears at completion with no manual refresh.) Once terminal, each turn MUST lazily fetch its version's artifacts (consuming the existing `features/artifacts/` slice keyed by that `version_id`; React Query caching deduplicates re-renders). A turn whose artifact read returns `artifacts: []` MUST omit the artifact section entirely (no empty-state noise inside the conversation); a pending read shows a quiet loading affordance and a failed read shows a quiet inline error without any toast and without blocking the turn.

The aggregate card MUST render: a file-type icon, a title naming the file count ("N file(s)"), a secondary line with the total human-readable size (summing non-null `bytes`) and a truncated preview of the first few artifact `path` values (falling back to `kind` for null paths), and a single **Download zip** action. **Activating the card (anywhere except the Download control) MUST drive the right-column Artifact Preview panel**: it sets the global UI store's `selectedVersionId` to the turn's version and the selected-artifact id to the version's first artifact, expanding the preview column when collapsed, so the panel lists that version's files and previews the first (see `web-artifact-preview` — per-file browsing/preview/download lives in the panel, not the card).

**Download zip** MUST mint a fresh archive URL via the `features/artifacts/` archive presign action and hand the browser directly to the returned relative `url` (the API streams the ZIP); the app MUST NOT cache or reuse a previously minted URL — each click re-mints. A presign failure MUST surface exactly one user-visible error and MUST NOT leave the turn in a silent broken state.

The default preview anchor on first render MUST remain the task's `current_version` (or no selection when there is none), so the preview panel lists the current version's artifacts before any explicit activation.

#### Scenario: A turn aggregates its artifacts into one card below the execution section

- **GIVEN** a task with two terminal versions where only the second produced three artifacts
- **WHEN** the conversation renders
- **THEN** the second turn MUST render exactly one aggregate card (icon, "3 files" title, total size + path preview secondary line, Download zip action) positioned below that turn's execution section, and the first turn MUST omit the artifact section entirely

#### Scenario: An active version withholds the products card

- **GIVEN** a turn whose version is in an active status (e.g. `running`), even if its artifact read would return files
- **WHEN** the turn renders
- **THEN** it MUST NOT render the aggregate products card; the card appears only after the version reaches a terminal status

#### Scenario: Activating the card opens the version's file list in the preview panel

- **GIVEN** a turn with an aggregate artifact card
- **WHEN** the user activates the card (anywhere except its Download zip control)
- **THEN** the global store's `selectedVersionId` MUST become that turn's version id and the selected-artifact id its first artifact, the preview column MUST expand if collapsed, and the Artifact Preview panel MUST list the version's files

#### Scenario: Download zip mints a fresh archive URL and navigates

- **GIVEN** a turn with an aggregate artifact card
- **WHEN** the user clicks Download zip
- **THEN** the app MUST request a fresh archive presign for that version and navigate the browser to the returned relative `url`, and a second click MUST re-mint rather than reuse the prior URL

#### Scenario: An archive presign failure surfaces a single error, the turn stays intact

- **WHEN** the Download zip presign request fails (`version_not_found` or `internal_error`)
- **THEN** the page MUST surface exactly one visible error (never a duplicate toast), MUST NOT navigate, and the turn and conversation MUST remain rendered

#### Scenario: Artifact read failure stays quiet inside the turn

- **WHEN** a turn's artifact-list read fails
- **THEN** that turn MUST show a quiet inline error affordance (no toast) and the rest of the conversation MUST render normally

#### Scenario: Nullable artifact metadata renders with placeholders

- **GIVEN** a turn whose artifacts all have `path` and `bytes` `null`
- **THEN** the aggregate card MUST still render (file count from the array length, `kind` fallback labels, neutral size placeholder) and MUST still offer activation and Download zip

### Requirement: Conversation-Style Event Rendering

The event log SHALL render the turn's events as **distinct blocks rather than one mixed bubble**, so plan / step / summary content is not crammed together. It MUST render at most three blocks, in this order, each its own card and each omitted when it has no content:

- **Plan card** — the single `plan` event whose `payload.steps` is a non-empty array, rendered as an ordered step list.
- **Process card** — every remaining recognized/unknown event (step / status / log / error / a malformed plan / unknown kinds), in sequence order, as de-emphasized rows: `step` as a progress row (verdict glyph for pass/finish vs retry + `title` + de-emphasized `summary`); `status` as a human-readable transition line; `log` as small monospace; `error` with destructive styling naming the code/message; anything else as a bounded compact payload preview (the only place a JSON-ish rendering may appear).
- **Summary card** — the last non-empty `summary` event's text, rendered as paragraph prose and visually distinct from the muted plan/process cards (it is the turn's "answer"). An empty summary renders nothing.

`artifact` events MUST NOT be rendered in the log at all — produced files surface only via the per-turn aggregate products card (and only once the version is terminal). `title` and any other non-conversational kind MUST also render nothing (the task title lives in the header; cost flows on a separate exchange and never reaches this event stream). **Never raw JSON for a recognized kind.** A recognized kind whose payload is missing expected fields MUST degrade to the compact fallback row, never throw.

#### Scenario: Plan, process, and summary render as separate blocks

- **WHEN** a turn's events include a `plan` (with steps), `step` events, and a `summary`
- **THEN** the plan MUST render in its own ordered-list card, the steps in a separate process card, and the summary in its own answer card — the summary MUST NOT be inside the process card

#### Scenario: Summary renders as the assistant answer card

- **WHEN** the events include a `summary` event with text
- **THEN** its text MUST render as paragraph prose in a dedicated, visually distinct card (no kind label, no JSON)

#### Scenario: Steps render structured inside the process card

- **WHEN** the events include `step` events with verdicts
- **THEN** each MUST render as a verdict-glyph progress row (title + de-emphasized summary) inside the process card — no raw JSON

#### Scenario: Artifact events are not rendered in the log

- **WHEN** the events include an `artifact` event with `path = "index.html"`
- **THEN** the log MUST NOT render any row or file line for it (products appear only in the aggregate card)

#### Scenario: Non-conversational kinds are hidden

- **WHEN** the events include a `title` event
- **THEN** the log MUST NOT render a row for it

#### Scenario: Unknown or malformed payloads degrade safely

- **WHEN** an event has an unrecognized `kind`, or a `plan` event lacks `payload.steps`
- **THEN** the offending event MUST render as the bounded compact payload preview (a process-card fallback row, no dedicated plan card) without throwing

