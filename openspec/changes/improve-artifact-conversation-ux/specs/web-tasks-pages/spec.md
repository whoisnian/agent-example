## MODIFIED Requirements

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

The Task Detail page SHALL subscribe to the realtime topic `task:<id>` (and, when the task has a non-null current version, `version:<current_version_id>`) via the existing `realtimeClient`, and on each received frame invalidate the corresponding React Query caches: a `task:` frame invalidates the task + versions queries; a `version:` frame invalidates that version's events query, AND — when the frame's `kind` is `"artifact"` or `"status"` — additionally invalidates that version's artifact-list query, so produced files surface in the conversation while the run executes and the final set lands at completion without a manual refresh. Because the Realtime Gateway server may be unavailable, the page MUST additionally poll via React Query `refetchInterval` (function form, re-evaluated each tick) while the task is in an active status **and** no WS connection is open, and MUST stop polling once the task reaches a terminal status or the WS connection opens. Gap-fill of missed events MUST go through the client's `onGap` callback hitting `GET /versions/{id}/events?after_id=<global event id cursor>` — using the highest event `id` already seen, NOT the per-run `seq`.

#### Scenario: Frame invalidates caches

- **WHEN** a realtime frame arrives on `task:<id>`
- **THEN** the task (and its versions/events) React Query entries are invalidated and refetched

#### Scenario: Artifact frame surfaces new files without a refresh

- **GIVEN** the detail page is open while the current version's run executes
- **WHEN** a `version:` frame with `kind = "artifact"` arrives
- **THEN** that version's artifact-list query MUST be invalidated and refetched, so the turn's artifact card appears/updates live (no manual page refresh)

#### Scenario: Terminal status frame refreshes the artifact list

- **WHEN** a `version:` frame with `kind = "status"` carrying a terminal status arrives
- **THEN** the version's artifact-list query MUST be invalidated so the completed artifact set is current at the moment the turn flips to its terminal badge

#### Scenario: Polling runs only while active and WS not open

- **WHEN** the task is in an active status and `realtimeClient.getConnectionState()` is not `"open"`
- **THEN** the detail/events queries refetch on an interval (function-form `refetchInterval`, re-evaluated each tick)
- **AND** once the task reaches a terminal status, or the WS connection opens, the interval refetch stops

#### Scenario: Subscriptions cleaned up on unmount

- **WHEN** the user navigates away from the detail page
- **THEN** the topic subscriptions are released (unsubscribe) so no stale handlers remain

### Requirement: Version Artifact Cards With Direct Download

The TaskDetail page SHALL surface each version's produced artifacts as **one aggregate artifact card per conversation turn**, positioned below the turn's execution section. Each turn MUST lazily fetch its version's artifacts (consuming the existing `features/artifacts/` slice keyed by that `version_id`; React Query caching deduplicates re-renders). A turn whose artifact read returns `artifacts: []` MUST omit the artifact section entirely (no empty-state noise inside the conversation); a pending read shows a quiet loading affordance and a failed read shows a quiet inline error without any toast and without blocking the turn.

The aggregate card MUST render: a file-type icon, a title naming the file count ("N file(s)"), a secondary line with the total human-readable size (summing non-null `bytes`) and a truncated preview of the first few artifact `path` values (falling back to `kind` for null paths), and a single **Download zip** action. **Activating the card (anywhere except the Download control) MUST drive the right-column Artifact Preview panel**: it sets the global UI store's `selectedVersionId` to the turn's version and the selected-artifact id to the version's first artifact, expanding the preview column when collapsed, so the panel lists that version's files and previews the first (see `web-artifact-preview` — per-file browsing/preview/download lives in the panel, not the card).

**Download zip** MUST mint a fresh archive URL via the `features/artifacts/` archive presign action and hand the browser directly to the returned relative `url` (the API streams the ZIP); the app MUST NOT cache or reuse a previously minted URL — each click re-mints. A presign failure MUST surface exactly one user-visible error and MUST NOT leave the turn in a silent broken state.

The default preview anchor on first render MUST remain the task's `current_version` (or no selection when there is none), so the preview panel lists the current version's artifacts before any explicit activation.

#### Scenario: A turn aggregates its artifacts into one card below the execution section

- **GIVEN** a task with two versions where only the second produced three artifacts
- **WHEN** the conversation renders
- **THEN** the second turn MUST render exactly one aggregate card (icon, "3 files" title, total size + path preview secondary line, Download zip action) positioned below that turn's execution section, and the first turn MUST omit the artifact section entirely

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

## ADDED Requirements

### Requirement: Conversation-Style Event Rendering

The event log SHALL render each event by its `kind` as conversational content — dialogue substance as body text, process information as de-emphasized lines, and **never raw JSON for a recognized kind**:

- `summary` → the turn's assistant reply body: the payload's `summary` text rendered as normal paragraph prose (this is the primary "answer" content of a turn).
- `plan` → an ordered step list rendered from `payload.steps`.
- `step` → a progress line: a verdict glyph (distinct visual for pass/finish vs retry), the step `title`, and the step `summary` as de-emphasized text.
- `artifact` → a compact file line (file icon + the payload's `path`); activating it selects that artifact in the preview panel (same store write as the aggregate card). Because a resumed run MAY re-emit an `artifact` event for the same file under a fresh `seq`, the log MUST de-duplicate artifact lines by `payload.artifact_id` (render one line per distinct artifact, last occurrence wins), so a re-execution does not show the same file twice.
- `status` → a de-emphasized status-transition line (human-readable, e.g. naming the new status).
- `log` → de-emphasized small monospace text.
- `error` → destructive styling naming the error code/message (existing behavior preserved).
- `title` and any other non-conversational kind → not rendered in the log (the task title already lives in the header; cost flows on a separate exchange and never reaches this event stream).
- An unrecognized `kind` → the bounded compact payload preview as fallback (the only place a JSON-ish rendering may appear).

A recognized kind whose payload is missing expected fields MUST degrade to the fallback rendering for that event, never throw.

#### Scenario: Summary renders as the assistant reply body

- **WHEN** the current turn's events include a `summary` event
- **THEN** its `summary` text MUST render as paragraph prose inside the assistant block (no kind label, no JSON), as the turn's primary reply content

#### Scenario: Plan and steps render structured, not as JSON

- **WHEN** the events include a `plan` event with three steps and `step` events with verdicts
- **THEN** the plan MUST render as an ordered list of the three step titles and each step event as a verdict-glyph progress line with its title and summary — no raw JSON for either kind

#### Scenario: Artifact events render as selectable file lines

- **WHEN** the events include an `artifact` event with `path = "index.html"`
- **THEN** the log MUST render a file line showing `index.html`, and activating it MUST select that artifact in the preview panel

#### Scenario: Non-conversational kinds are hidden

- **WHEN** the events include a `title` event
- **THEN** the log MUST NOT render a row for it

#### Scenario: Repeated artifact events for one file render once

- **WHEN** the event stream contains two `artifact` events sharing the same `payload.artifact_id` (e.g. a resumed run re-emitted it)
- **THEN** the log MUST render exactly one file line for that artifact, not two

#### Scenario: Unknown or malformed payloads degrade safely

- **WHEN** an event has an unrecognized `kind`, or a `plan` event lacks `payload.steps`
- **THEN** the log MUST render the bounded compact payload preview for that event without throwing
