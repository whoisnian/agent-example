## MODIFIED Requirements

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

## REMOVED Requirements

### Requirement: Version Artifacts Expandable List With Direct Download

**Reason**: 更名并改形态——产物在回合内的呈现从"行式列表"升级为"卡片"，且旧名中的 "Expandable List" 自上一轮起已名不副实（归档备忘已记录）。语义由新 requirement "Version Artifact Cards With Direct Download" 全量接管。
**Migration**: 数据访问、选中态成对写入、Download presign 语义全部原样保留在新 requirement 中；仅呈现形态与 requirement 名称变化。

## ADDED Requirements

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
