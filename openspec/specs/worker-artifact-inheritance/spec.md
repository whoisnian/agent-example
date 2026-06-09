# worker-artifact-inheritance Specification

## Purpose

The worker's parent-artifact inheritance on the `execute` path: when a consumed `execute` message carries a `parent_version_id`, the worker copies the parent version's artifact objects into the new version's OSS prefix and records `artifacts` rows before the agent runs, so iterate and rollback-`branch` build incrementally on the parent rather than from scratch. Inheritance is keyed on the deterministic parent prefix, excludes run-internal objects, and records idempotently on `(version_id, oss_key)`. Established by archiving change `add-worker-rollback-handling`.

## Requirements

### Requirement: Parent Artifact Inheritance on Execute

When the worker consumes an `execute` message carrying a non-null `parent_version_id`, it SHALL, before running the agent, inherit the parent version's artifacts into the new version: list the artifact objects under the parent version's OSS prefix, server-side-copy each object into the new run's `RunContext.oss_prefix`, and record one `artifacts` row per copied object (keyed on the absolute object key the copy yields). The new version therefore begins as a copy of the parent's artifacts, onto which the agent's produced files overlay.

The inheritance MUST use server-side copy (no object bytes flow through the worker), MUST stay within the run's OSS prefix discipline (copies target only `RunContext.oss_prefix`), and MUST record each inherited object as an `artifacts` row so it appears in the new version's artifact listing.

Inheritance MUST copy only produced artifacts, NOT run-internal objects. In particular it MUST exclude the reserved `checkpoints/` sub-prefix (where the CheckpointStore offloads large checkpoint blobs under the same version prefix); those MUST NOT be copied into the new version or recorded as artifacts.

#### Scenario: A branch/iterate run inherits the parent's artifacts
- **GIVEN** an `execute` message whose `parent_version_id` refers to a version with artifact objects under its OSS prefix
- **WHEN** the worker processes it as a fresh run
- **THEN** before the agent loop runs, each parent artifact object MUST be server-side-copied into the new version's prefix and recorded as an `artifacts` row for the new `version_id`, so the new version's artifact set includes the parent's files plus whatever the agent then produces

#### Scenario: Checkpoint blobs are not inherited
- **GIVEN** a parent version prefix that contains both artifact objects and a `checkpoints/<n>.bin` object
- **WHEN** inheritance runs
- **THEN** the `checkpoints/` object MUST NOT be copied into the new version or recorded as an `artifacts` row; only the produced artifacts are inherited

#### Scenario: Inheritance precedes agent output
- **WHEN** inheritance runs
- **THEN** it MUST complete before the planner/executor/critic loop begins, so the agent's produced files overlay (and may overwrite) the inherited ones

### Requirement: Inheritance Keyed on parent_version_id

The worker SHALL derive the parent version's OSS prefix deterministically as `compute_oss_prefix(tenant_id, task_id, parent_version_id)` and key inheritance on `parent_version_id`. It MUST NOT depend on the message's `parent_artifact_root` for this (that field is not populated by the API). A message without a `parent_version_id` (a first/create run) MUST NOT trigger inheritance.

#### Scenario: Create/first run does not inherit
- **WHEN** an `execute` message has no `parent_version_id`
- **THEN** the worker MUST NOT perform any inheritance copy and the new version MUST start with no inherited artifacts

#### Scenario: Inheritance ignores the (null) parent_artifact_root
- **WHEN** a message carries a `parent_version_id` but a null/absent `parent_artifact_root`
- **THEN** inheritance MUST still occur, keyed on `parent_version_id` and the deterministic parent prefix

### Requirement: Inheritance Is Idempotent

Re-processing an `execute` message MUST NOT produce duplicate inherited `artifacts` rows. Artifact recording SHALL be idempotent on `(version_id, oss_key)` (the `artifacts` table carries a UNIQUE constraint on that pair and the worker upserts), so a re-inheritance — or the agent overwriting an inherited file — collapses to a single row. Independently, the worker SHALL skip inheritance entirely when the run is not fresh (a prior checkpoint exists) as a performance optimization; correctness MUST NOT depend on that skip, because the first checkpoint is written only after the planner runs, leaving a window where a fresh redelivery re-inherits.

#### Scenario: Resume skips inheritance
- **GIVEN** a run that already wrote at least one checkpoint (a prior attempt)
- **WHEN** the message is redelivered and processed again
- **THEN** the worker SHOULD skip re-running inheritance (no redundant copies)

#### Scenario: Re-inheritance does not duplicate rows
- **GIVEN** a fresh run that inherited the parent's artifacts but crashed before its first checkpoint
- **WHEN** the message is redelivered and inheritance runs again
- **THEN** the inherited `artifacts` rows MUST remain one per `(version_id, oss_key)` (the upsert collapses the repeat), not doubled

### Requirement: Empty or Missing Parent Is a No-Op

If the parent version's prefix contains no objects, inheritance MUST be a no-op (zero copies, zero rows) and the run MUST proceed normally with the new version starting empty. A missing parent prefix MUST NOT be treated as an error; only a genuine OSS/storage failure during listing or copying MUST propagate so the run is failed/retried by the existing policy.

#### Scenario: Parent with no artifacts yields an empty start
- **GIVEN** a `parent_version_id` whose OSS prefix has no objects
- **WHEN** the worker processes the fresh run
- **THEN** inheritance MUST copy nothing and record no rows, and the agent loop MUST proceed as for a from-scratch run
