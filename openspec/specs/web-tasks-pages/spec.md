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
