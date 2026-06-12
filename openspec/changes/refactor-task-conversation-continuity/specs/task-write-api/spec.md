# task-write-api Specification (Delta)

## MODIFIED Requirements

### Requirement: Iterate Task Endpoint

The API SHALL expose `POST /api/v1/tasks/{task_id}/iterate` that derives a new version from a base version, in a single PostgreSQL transaction. The transaction MUST: (a) `SELECT … FOR UPDATE` the `tasks` row; (b) resolve the base version (request `base_version_id` if present, else `tasks.current_version`); (c) insert one row into `task_versions` (parent_id = base, version_no = MAX(version_no)+1 within the task, status `pending`); (d) insert one row into `task_runs`; (e) insert one row into `outbox` with the same payload contract as create plus `parent_version_id` and `parent_artifact_root` filled from the base row, plus a `history` array assembled from the base version's parent chain per the `task-conversation-history` capability (oldest→newest, bounded); (f) `UPDATE tasks SET status='pending', current_version=$new, updated_at=now()`. The endpoint MUST return HTTP `201` with envelope `data = {version_id, version_no, status}`.

#### Scenario: Happy iterate derives a child version
- **WHEN** a client `POST`s `/api/v1/tasks/{task_id}/iterate` with a valid `prompt` while the task has no active version and `current_version` points at a terminal version
- **THEN** the response MUST be HTTP `201` AND a new `task_versions` row MUST exist whose `parent_id` equals the resolved base, whose `version_no` is the previous max plus one, and whose `is_active` is true AND `tasks.current_version` MUST now equal the new version id

#### Scenario: Explicit base_version_id is honored
- **WHEN** the request body supplies `base_version_id` that belongs to the path `task_id` and is a terminal version
- **THEN** the new `task_versions.parent_id` MUST equal the supplied `base_version_id` (not `tasks.current_version`)

#### Scenario: Outbox payload carries parent context
- **WHEN** an iterate succeeds against base version `vB` that has `artifact_root='oss://bucket/tenant/task/vB/'`
- **THEN** `outbox.payload->>'parent_version_id'` MUST equal `vB.id::text` AND `outbox.payload->>'parent_artifact_root'` MUST equal `oss://bucket/tenant/task/vB/`

#### Scenario: Outbox payload carries conversation history
- **GIVEN** a base version `vB` whose parent chain is v1←v2 (vB = v2) with `summary` set on v1 and v2
- **WHEN** an iterate succeeds against `vB`
- **THEN** `outbox.payload->'history'` MUST be a JSON array of exactly two turns ordered `[v1, v2]`, each carrying `version_no`, `prompt`, `summary`, and `status` per the `task-conversation-history` bounds

#### Scenario: Iterate from a v1-only task carries a single-turn history
- **GIVEN** a task whose only version is v1 (terminal)
- **WHEN** an iterate succeeds with `tasks.current_version` as the implicit base
- **THEN** `outbox.payload->'history'` MUST be a one-element array whose turn carries v1's `version_no`, `prompt`, `status`, and `summary` (null when v1 has no summary)
