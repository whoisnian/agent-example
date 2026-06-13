# task-read-api Specification

## Purpose
TBD - created by archiving change add-task-read-api. Update Purpose after archive.
## Requirements
### Requirement: List Tasks Endpoint

The API SHALL expose `GET /api/v1/tasks` that returns the caller's tasks, newest first, with offset pagination and an optional `status` filter. The endpoint MUST accept query params `page` (1-based, default `1`) and `page_size` (default `20`, clamped to the inclusive range `[1, 100]`), and an optional `status` that, when present, filters to tasks in that status. The response MUST be HTTP `200` with the unified envelope and `data = {items, page, page_size, total}`, where `items` is an array of task summaries, `total` is the total count of matching tasks for the caller (ignoring pagination), and `page`/`page_size` echo the effective (post-clamp) values. Each item MUST carry `{id, title, task_type, status, current_version, created_at, updated_at, cost}` where `cost` is the embedded cost summary.

`page` values less than `1` MUST be clamped to `1`; `page_size` MUST be clamped into `[1, 100]`. A `page` or `page_size` query value that is not a valid integer MUST be rejected with HTTP `400 invalid_input` naming the offending field. When `status` is present it MUST be one of the six task statuses (`pending`, `running`, `paused`, `cancelled`, `succeeded`, `failed`); any other value (including the version-only statuses `queued` / `cancelling`, or an unknown string) MUST be rejected with HTTP `400 invalid_input` rather than returning a silent empty result.

Listing MUST be scoped to the caller's `tenant_id`/`user_id`; tasks owned by anyone else MUST NOT appear, regardless of pagination.

#### Scenario: Paginated listing returns owner's tasks newest-first

- **WHEN** the caller owns 3 tasks and `GET`s `/api/v1/tasks?page=1&page_size=2`
- **THEN** the response MUST be HTTP `200` with `data.total = 3`, `data.page = 1`, `data.page_size = 2`, AND `data.items` MUST contain exactly 2 task summaries ordered by `created_at` descending

#### Scenario: Status filter narrows results

- **WHEN** the caller has tasks in mixed statuses and `GET`s `/api/v1/tasks?status=succeeded`
- **THEN** every item in `data.items` MUST have `status = "succeeded"` AND `data.total` MUST equal the count of the caller's `succeeded` tasks

#### Scenario: page_size is clamped

- **WHEN** the caller `GET`s `/api/v1/tasks?page_size=9999`
- **THEN** the effective page size MUST be `100` and `data.page_size` MUST be `100`

#### Scenario: page below 1 is clamped to 1

- **WHEN** the caller `GET`s `/api/v1/tasks?page=0`
- **THEN** the response MUST be HTTP `200`, `data.page` MUST be `1`, AND the query MUST NOT error (offset is never negative)

#### Scenario: Non-integer pagination is rejected

- **WHEN** the caller `GET`s `/api/v1/tasks?page_size=abc`
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` AND `data` MUST name `page_size`

#### Scenario: Invalid status is rejected

- **WHEN** the caller `GET`s `/api/v1/tasks?status=queued` (a version-only status) or `status=bogus`
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` AND `data` MUST name `status`

#### Scenario: Other owners' tasks are invisible

- **WHEN** another tenant/user owns tasks and the caller `GET`s `/api/v1/tasks`
- **THEN** none of those tasks MUST appear in `data.items` AND they MUST NOT be counted in `data.total`

### Requirement: Task Detail Endpoint

The API SHALL expose `GET /api/v1/tasks/{task_id}` that returns one task the caller owns. The response MUST be HTTP `200` with `data = {task, current_version, cost}`, where `task` is the full task row (`id, tenant_id, user_id, title, task_type, status, current_version, created_at, updated_at`), `current_version` is the summary of the version referenced by `tasks.current_version` (or JSON `null` when `tasks.current_version` is `NULL`), and `cost` is the task-level cost summary.

#### Scenario: Detail returns task and current-version summary

- **WHEN** the caller `GET`s `/api/v1/tasks/{task_id}` for a task they own whose `current_version` points at version `v`
- **THEN** the response MUST be HTTP `200` AND `data.task.id` MUST equal `task_id` AND `data.current_version.id` MUST equal `v`

#### Scenario: Null current_version renders as null

- **WHEN** the caller reads a task whose `current_version` is `NULL`
- **THEN** the response MUST be HTTP `200` AND `data.current_version` MUST be JSON `null`

### Requirement: Version List (Tree) Endpoint

The API SHALL expose `GET /api/v1/tasks/{task_id}/versions` that returns every version of a task the caller owns as a flat array ordered by `version_no` ascending. The response MUST be HTTP `200` with `data = {items}`, where each node carries exactly `{id, parent_id, version_no, status, is_active, artifact_root, created_at, cost}`. The tree node is intentionally lightweight and MUST omit `prompt` and `params` (those appear only in version detail). `parent_id` MUST render as JSON `null` for root versions, and `is_active` MUST always be a concrete boolean (never `null`). The server MUST NOT nest the tree; clients reconstruct edges from `parent_id`.

#### Scenario: Versions returned flat and ordered

- **WHEN** a task the caller owns has versions `v1 (parent NULL)`, `v2 (parent v1)`, `v3 (parent v1)` and the caller `GET`s `/api/v1/tasks/{task_id}/versions`
- **THEN** the response MUST be HTTP `200` AND `data.items` MUST list all three ordered by `version_no` ascending AND each node MUST expose its `parent_id` (`null` for `v1`)

#### Scenario: Version list for unowned task is not found

- **WHEN** the caller `GET`s `/api/v1/tasks/{task_id}/versions` for a `task_id` owned by another user
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"`

### Requirement: Version Detail Endpoint

The API SHALL expose `GET /api/v1/versions/{version_id}` that returns one version reachable through a task the caller owns. The response MUST be HTTP `200` with `data = {version, runs, cost}`, where `version` is the full version row — including `prompt` and `params` (rendered as raw JSON), a concrete boolean `is_active`, and `summary` (the `task_versions.summary` the worker's run-summary event populates; a **decimal-free** plain string, serialized as JSON `null` when the column is `NULL`) — `runs` is the array of that version's `task_runs` ordered by `attempt_no` ascending (each exposing `{id, attempt_no, status, started_at, ended_at, last_heartbeat, error}`, where `error` is raw JSON and MUST be JSON `null` when the column is `NULL`), and `cost` is the version-level cost summary. `runs` MUST always be an array, never `null`.

`summary` is added so the web conversation can label a turn's collapsed execution section without eagerly fetching that version's events (the detail read is already issued per turn for `prompt`); it MUST be present-and-null (never omitted) so the client distinguishes "no summary yet" from a missing field.

#### Scenario: Version detail includes its runs

- **WHEN** a version the caller can reach has two runs (attempts 1 and 2) and the caller `GET`s `/api/v1/versions/{version_id}`
- **THEN** the response MUST be HTTP `200` AND `data.runs` MUST contain both runs ordered by `attempt_no` ascending AND `data.version.id` MUST equal `version_id`

#### Scenario: Version with no runs returns empty array

- **WHEN** the caller reads a version that has no `task_runs` rows
- **THEN** the response MUST be HTTP `200` AND `data.runs` MUST be an empty array (not `null`)

#### Scenario: Version detail exposes summary present-and-null

- **WHEN** the caller reads a version whose run has emitted a run-summary (`task_versions.summary` populated) and, separately, a version whose `summary` is still `NULL`
- **THEN** the first response MUST carry `data.version.summary` as the stored string, and the second MUST carry `data.version.summary = null` (present, never omitted)

### Requirement: Version Event Backfill Endpoint

The API SHALL expose `GET /api/v1/versions/{version_id}/events?after_id=&limit=` that returns the version's `task_events` with `id` strictly greater than `after_id`, ordered by `id` ascending, for a version reachable through a task the caller owns. The cursor `after_id` is the global `task_events.id` (`BIGSERIAL`), **not** the per-frame `seq` (which is only unique per `run_id`). `after_id` MUST default to `0`; `limit` MUST default to `200` and be clamped to the inclusive range `[1, 1000]`. A non-integer `after_id` or `limit` MUST be rejected with HTTP `400 invalid_input` naming the field. The response MUST be HTTP `200` with `data = {items, next_after_id}`, where `next_after_id` is the `id` of the last returned event, or the input `after_id` when no events are returned, so a client can resume polling.

Each item in `items` MUST carry `{id, version_id, run_id, seq, kind, payload, created_at}`, where `run_id` is nullable and MUST render as JSON `null` when absent, `payload` MUST be passed through as raw JSON (never a base64 string), and both `id` and `seq` are exposed so a realtime client can reconcile its cursor.

#### Scenario: Backfill returns events after the cursor

- **WHEN** a version reachable by the caller has events with ids `[10, 11, 12]` and the caller `GET`s `/api/v1/versions/{version_id}/events?after_id=10`
- **THEN** the response MUST be HTTP `200` AND `data.items` MUST contain the events with ids `11` and `12` in ascending order AND `data.next_after_id` MUST equal `12`

#### Scenario: No new events echoes the cursor

- **WHEN** the caller requests events with `after_id` equal to the highest existing event id
- **THEN** the response MUST be HTTP `200`, `data.items` MUST be empty, AND `data.next_after_id` MUST equal the supplied `after_id`

#### Scenario: Events scoped to the version

- **WHEN** the task has events belonging to other versions
- **THEN** `data.items` MUST contain only events whose `version_id` equals the path `version_id`

#### Scenario: Event with null run_id renders run_id as null

- **WHEN** a returned event has `run_id = NULL` in the database
- **THEN** that item's `run_id` MUST be JSON `null` AND its `payload` MUST be returned as raw JSON

### Requirement: Owner-Scoped Reads Hide Unowned Resources

Every read endpoint MUST resolve and enforce ownership against the caller's `tenant_id`/`user_id` before returning data. When a `task_id` references a task the caller does not own (or that does not exist), the API MUST return HTTP `404` with `code = "task_not_found"`. When a `version_id` references a version that does not exist, or whose owning task the caller does not own, the API MUST return HTTP `404` with `code = "version_not_found"`. The API MUST NOT return `403` for unowned resources, so their existence is not disclosed.

#### Scenario: Unknown task

- **WHEN** the caller `GET`s `/api/v1/tasks/{task_id}` for a `task_id` with no row
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"`

#### Scenario: Unowned resource is indistinguishable from missing

- **WHEN** the caller `GET`s `/api/v1/versions/{version_id}` for a version that exists but whose owning task belongs to another user
- **THEN** the response MUST be HTTP `404` with `code = "version_not_found"` AND the response MUST NOT be `403`

#### Scenario: Same tenant, different user is still hidden

- **WHEN** the caller `GET`s `/api/v1/tasks/{task_id}` for a task with the caller's `tenant_id` but a different `user_id`
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"` (ownership requires both `tenant_id` and `user_id` to match)

#### Scenario: Malformed id

- **WHEN** the caller `GET`s `/api/v1/tasks/{task_id}` where `task_id` is not a valid UUID
- **THEN** the response MUST be HTTP `400` with `code = "invalid_input"` AND `data` MUST name the offending field

### Requirement: Embedded Cost Summary Is Best-Effort

List rows, task detail, version nodes, and version detail MUST embed a `cost` object with the shape `{amount_usd, input_tokens, output_tokens, cached_tokens, tool_calls, wall_time_ms}`, sourced from the `task_costs` table (task-level totals for task scope, the per-version row for version scope). `amount_usd` MUST be a **decimal string** preserving the full `NUMERIC(18,8)` scale (e.g. `"0.62000000"`), not a JSON number — to avoid `float64` rounding of an 8-dp money value; the remaining fields are JSON integers. When no `task_costs` row exists for the scope, every token/`tool_calls`/`wall_time_ms` field MUST be `0` and `amount_usd` MUST be `"0.00000000"`; a read MUST NOT fail because cost data is absent. Cost values are eventually-consistent: they reflect whatever the Cost Service has populated so far.

#### Scenario: Missing cost rows render as zero

- **WHEN** the caller reads a task or version that has no `task_costs` row
- **THEN** the response MUST be HTTP `200`, its `cost` integer fields MUST all equal `0`, AND `cost.amount_usd` MUST equal `"0.00000000"`

#### Scenario: Cost summary reflects populated rows

- **WHEN** `task_costs` holds a per-version row with `amount_usd = 0.62` for a version the caller reads
- **THEN** the version's `cost.amount_usd` MUST equal the decimal string `"0.62000000"`

