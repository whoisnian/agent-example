## MODIFIED Requirements

### Requirement: Task Detail Page

The web app SHALL render a Task Detail page at `/tasks/:id` that fetches `GET /api/v1/tasks/{id}` and presents it as a **conversation column**: a compact header (task title, status badge, task_type, cost summary, and the control bar), a **scrollable conversation body** rendering one **turn per version** (see "Version Turns Rendering" semantics below), and a **persistent iterate composer pinned at the bottom** (see "Iterate Action With UI Task-Level Mutex"). The body MUST scroll independently while the header and the composer remain visible (the page MUST NOT rely on whole-page scrolling to reach the composer).

Versions from `GET /api/v1/tasks/{id}/versions` MUST render as turns in ascending `version_no` order (linear, no parent-indented tree). Each turn MUST show: the version's `prompt` as the user-message position (lazily fetched via the existing `GET /api/v1/versions/{id}` read, cached, with a quiet loading state and a silent fallback to the version number when the read fails); a result line with `version_no`, status badge, and cost summary; the turn's inline artifact list and rollback actions (see the dedicated requirements below). The turn for the task's `current_version` MUST be visually marked as current. A turn whose `parent_id` is not the immediately preceding version (a rollback-branch fork) MUST carry a "from v{n}" origin label naming its parent version. The event log from `GET /api/v1/versions/{version_id}/events` MUST render inline within the current version's turn only (the live/polling pipeline is unchanged).

A loading state MUST show while the detail query is pending, and an unowned/unknown task (`404`) MUST show a not-found state rather than a generic error toast loop. The rendering MUST NOT require any graph/visualization dependency.

#### Scenario: Detail renders header, turns, and the current turn's events

- **WHEN** the page loads for a task the caller owns with three versions
- **THEN** the compact header renders, three turns render in ascending `version_no` order each with its prompt and result line, and the current version's turn additionally renders the event log

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

### Requirement: Version Artifacts Expandable List With Direct Download

The TaskDetail page SHALL surface each version's produced artifacts as an **inline artifact list within that version's conversation turn**, replacing the previous version-tree row-selection contract. Each turn MUST lazily fetch its version's artifacts (consuming the existing `features/artifacts/` slice keyed by that `version_id`; React Query caching deduplicates re-renders, and terminal versions are never re-polled). A turn whose artifact read returns `artifacts: []` MUST omit the artifact section entirely (no empty-state noise inside the conversation); a pending read shows a quiet loading affordance and a failed read shows a quiet inline error without any toast and without blocking the turn.

Each inline artifact entry MUST display its `kind`, `mime` (or a neutral placeholder when `null`), and a human-readable size derived from `bytes` (neutral placeholder when `null`). **Activating an artifact entry MUST drive the right-column Artifact Preview panel**: it sets the global UI store's `selectedVersionId` to the turn's version and the store's selected-artifact id to that artifact, expanding the preview column when collapsed, so the panel previews exactly the activated artifact (see `web-artifact-preview`). Each entry MUST also offer a **Download** action: Download MUST mint a fresh presigned URL via the `features/artifacts/` presign action and hand the browser directly to that URL (bytes never proxied through the app); the app MUST NOT cache or reuse a previously minted URL — each Download re-mints. A presign failure MUST surface exactly one user-visible error and MUST NOT leave the turn in a silent broken state.

The default preview anchor on first render MUST remain the task's `current_version` (or no selection when there is none), so the preview panel lists the current version's artifacts before any explicit activation.

#### Scenario: Turns list their artifacts inline and lazily

- **GIVEN** a task with two versions where only the second produced artifacts
- **WHEN** the conversation renders
- **THEN** the second turn MUST list its artifacts (each with `kind`, mime label, size) via a lazy per-version read, and the first turn MUST omit the artifact section entirely

#### Scenario: Activating an inline artifact drives the preview panel

- **GIVEN** a turn listing an artifact
- **WHEN** the user activates that artifact entry
- **THEN** the global store's `selectedVersionId` MUST become that turn's version id and the selected-artifact id MUST become that artifact's id, the preview column MUST expand if collapsed, and the Artifact Preview panel MUST preview that exact artifact

#### Scenario: Download mints a fresh URL and navigates to OSS directly

- **GIVEN** a turn listing an artifact
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
- **THEN** the entry MUST still render with neutral placeholders for the missing `mime` and size and MUST still offer activation and Download

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

## REMOVED Requirements

### Requirement: Version Tree Rendering

**Reason**: 版本不再以父子缩进树呈现。TaskDetail 改为对话流，每个版本作为一个按 `version_no` 线性排布的回合（见 MODIFIED "Task Detail Page"），分支来源以回合内 "from v{n}" 标注表达，current 标记移至回合。

**Migration**: `components/tasks/VersionTree.tsx` 退役，由对话回合组件取代；其 status/cost 徽章与 current 标记语义并入回合的结果行，行选中（`version-select`）契约由回合内产物卡片激活取代（见 MODIFIED "Version Artifacts Expandable List With Direct Download"）。
