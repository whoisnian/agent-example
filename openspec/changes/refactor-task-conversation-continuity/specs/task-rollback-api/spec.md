# task-rollback-api Specification (Delta)

## MODIFIED Requirements

### Requirement: Branch Mode Re-executes from a Historical Version

In `branch` mode the API SHALL create a new version whose `parent_id` is `target_version_id`, insert its `task_runs` row, enqueue an `execute` message via the outbox (carrying the target version's `artifact_root` as the parent artifact root, identical to iterate, plus a `history` array assembled from the target version's parent chain per the `task-conversation-history` capability), point `tasks.current_version` at the new version, and seed `tasks.status = "pending"` — all in one transaction. The new `version_no` MUST be the task's current max plus one. When the request `prompt` is empty, the API SHALL substitute an auto-generated prompt naming the target version (e.g. `"rollback to version <n>"`) so the stored prompt is non-empty. On success the response MUST be HTTP `201` with `data = {version_id, version_no, status: "pending"}`.

`branch` is subject to the task-level mutex exactly as iterate: the create is guarded by the `one_active_version_per_task` index, and an attempt while a version is active MUST fail with `409 active_version_exists` (see "Non-Active Precondition").

#### Scenario: Branch from a historical version creates and enqueues a new version
- **GIVEN** a task whose versions are all terminal and a `target_version_id` belonging to that task
- **WHEN** the caller POSTs `mode = "branch"`
- **THEN** the response MUST be HTTP `201` with a new `version_id` whose `version_no` is max+1 and `status = "pending"`, a `task_runs` row MUST exist for it, exactly one `execute` outbox row MUST be enqueued carrying the target's artifact root, and `tasks.current_version` MUST point at the new version

#### Scenario: Branch with an empty prompt auto-fills
- **WHEN** the caller POSTs `mode = "branch"` with an empty or absent `prompt`
- **THEN** the created version's stored `prompt` MUST be a non-empty auto-generated value referencing the target version

#### Scenario: Branch history is the target's chain, not the abandoned branch
- **GIVEN** a task with chain v1←v2←v3 (all terminal) and a rollback-`branch` targeting v2
- **WHEN** the branch succeeds
- **THEN** `outbox.payload->'history'` MUST contain the turns `[v1, v2]` in that order and MUST NOT contain a turn for v3
