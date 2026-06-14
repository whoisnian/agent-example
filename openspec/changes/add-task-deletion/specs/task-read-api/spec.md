## MODIFIED Requirements

### Requirement: List Tasks Endpoint

The API SHALL expose `GET /api/v1/tasks` that returns the caller's **live** (non-soft-deleted) tasks, newest first, with offset pagination and an optional `status` filter. The endpoint MUST accept query params `page` (1-based, default `1`) and `page_size` (default `20`, clamped to the inclusive range `[1, 100]`), and an optional `status` that, when present, filters to tasks in that status. The response MUST be HTTP `200` with the unified envelope and `data = {items, page, page_size, total}`, where `items` is an array of task summaries, `total` is the total count of matching **live** tasks for the caller (ignoring pagination), and `page`/`page_size` echo the effective (post-clamp) values. Each item MUST carry `{id, title, task_type, status, current_version, created_at, updated_at, cost}` where `cost` is the embedded cost summary.

`page` values less than `1` MUST be clamped to `1`; `page_size` MUST be clamped into `[1, 100]`. A `page` or `page_size` query value that is not a valid integer MUST be rejected with HTTP `400 invalid_input` naming the offending field. When `status` is present it MUST be one of the six task statuses (`pending`, `running`, `paused`, `cancelled`, `succeeded`, `failed`); any other value (including the version-only statuses `queued` / `cancelling`, or an unknown string) MUST be rejected with HTTP `400 invalid_input` rather than returning a silent empty result.

Listing MUST be scoped to the caller's `tenant_id`/`user_id`; tasks owned by anyone else MUST NOT appear, regardless of pagination. Soft-deleted tasks (`deleted_at IS NOT NULL`) MUST be excluded from both `items` and `total`, including when a `status` filter is applied.

#### Scenario: Paginated listing returns owner's tasks newest-first

- **WHEN** the caller owns 3 tasks and `GET`s `/api/v1/tasks?page=1&page_size=2`
- **THEN** the response MUST be HTTP `200` with `data.total = 3`, `data.page = 1`, `data.page_size = 2`, AND `data.items` MUST contain exactly 2 task summaries ordered by `created_at` descending

#### Scenario: Status filter narrows results

- **WHEN** the caller has tasks in mixed statuses and `GET`s `/api/v1/tasks?status=succeeded`
- **THEN** every item in `data.items` MUST have `status = "succeeded"` AND `data.total` MUST equal the count of the caller's **live** `succeeded` tasks

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

#### Scenario: Soft-deleted tasks are excluded from list and total

- **WHEN** the caller has live and soft-deleted tasks (including a soft-deleted `succeeded` task) and `GET`s `/api/v1/tasks` (with or without `status=succeeded`)
- **THEN** no soft-deleted task MUST appear in `data.items` AND `data.total` MUST count only the caller's live tasks

### Requirement: Task Detail Endpoint

The API SHALL expose `GET /api/v1/tasks/{task_id}` that returns one **live** task the caller owns. The response MUST be HTTP `200` with `data = {task, current_version, cost}`, where `task` is the full task row (`id, tenant_id, user_id, title, task_type, status, current_version, created_at, updated_at`), `current_version` is the summary of the version referenced by `tasks.current_version` (or JSON `null` when `tasks.current_version` is `NULL`), and `cost` is the task-level cost summary. A soft-deleted task (`deleted_at IS NOT NULL`) MUST respond HTTP `404 task_not_found` — indistinguishable from an unowned or non-existent task, so soft deletion does not leak existence.

#### Scenario: Detail returns task and current-version summary

- **WHEN** the caller `GET`s `/api/v1/tasks/{task_id}` for a live task they own whose `current_version` points at version `v`
- **THEN** the response MUST be HTTP `200` AND `data.task.id` MUST equal `task_id` AND `data.current_version.id` MUST equal `v`

#### Scenario: Null current_version renders as null

- **WHEN** the caller reads a live task whose `current_version` is `NULL`
- **THEN** the response MUST be HTTP `200` AND `data.current_version` MUST be JSON `null`

#### Scenario: Detail of a soft-deleted task is not found

- **WHEN** the owner `GET`s `/api/v1/tasks/{task_id}` for a task they soft-deleted
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"`, indistinguishable from an unowned or non-existent task

### Requirement: Version List (Tree) Endpoint

The API SHALL expose `GET /api/v1/tasks/{task_id}/versions` that returns every version of a **live** task the caller owns as a flat array ordered by `version_no` ascending. The response MUST be HTTP `200` with `data = {items}`, where each node carries exactly `{id, parent_id, version_no, status, is_active, artifact_root, created_at, cost}`. The tree node is intentionally lightweight and MUST omit `prompt` and `params` (those appear only in version detail). `parent_id` MUST render as JSON `null` for root versions, and `is_active` MUST always be a concrete boolean (never `null`). The server MUST NOT nest the tree; clients reconstruct edges from `parent_id`. When the task is soft-deleted, `GET /api/v1/tasks/{task_id}/versions` MUST respond HTTP `404 task_not_found`; a version read reachable through a soft-deleted task (`GET /api/v1/versions/{version_id}`) MUST respond HTTP `404 version_not_found` (matching the existing version-ownership contract, which maps a missing/unowned owning task to `version_not_found` — equally non-leaking).

#### Scenario: Versions returned flat and ordered

- **WHEN** a live task the caller owns has versions `v1 (parent NULL)`, `v2 (parent v1)`, `v3 (parent v1)` and the caller `GET`s `/api/v1/tasks/{task_id}/versions`
- **THEN** the response MUST be HTTP `200` AND `data.items` MUST list all three ordered by `version_no` ascending AND each node MUST expose its `parent_id` (`null` for `v1`)

#### Scenario: Versions of a soft-deleted task are not found

- **WHEN** the owner `GET`s `/api/v1/tasks/{task_id}/versions` for a task they soft-deleted
- **THEN** the response MUST be HTTP `404` with `code = "task_not_found"`

#### Scenario: Version-by-id under a soft-deleted task is not found

- **WHEN** the owner `GET`s `/api/v1/versions/{version_id}` for a version whose owning task they soft-deleted
- **THEN** the response MUST be HTTP `404` with `code = "version_not_found"` (the version-ownership probe maps a soft-deleted owning task to the same not-found as missing/unowned)
