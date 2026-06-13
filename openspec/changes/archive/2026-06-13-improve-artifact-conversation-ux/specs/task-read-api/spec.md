## MODIFIED Requirements

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
