## MODIFIED Requirements

### Requirement: Parent Artifact Inheritance on Execute

When the worker consumes an `execute` message carrying a non-null `parent_version_id`, it SHALL, before running the agent, inherit the parent version's artifacts into the new version: list the artifact objects under the parent version's OSS prefix, server-side-copy each object into the new run's `RunContext.oss_prefix`, and record one `artifacts` row per copied object (keyed on the absolute object key the copy yields). Each inherited row MUST carry the object's version-relative `path` (the key suffix below the version prefix — inheritance preserves relative paths, so the parent's `css/style.css` is the new version's `css/style.css`). The new version therefore begins as a copy of the parent's artifacts, onto which the agent's produced files overlay.

After recording the inherited rows the worker SHALL emit one `kind="artifact"` task event per inherited artifact (payload `{artifact_id, path, mime, bytes, sha256}`, `seq` from the run's event sequencing, insert-then-publish), so an observing client sees the new version's starting artifact set immediately rather than after the run completes.

The inheritance MUST use server-side copy (no object bytes flow through the worker), MUST stay within the run's OSS prefix discipline (copies target only `RunContext.oss_prefix`), and MUST record each inherited object as an `artifacts` row so it appears in the new version's artifact listing.

Inheritance MUST copy only produced artifacts, NOT run-internal objects. In particular it MUST exclude the reserved `checkpoints/` sub-prefix (where the CheckpointStore offloads large checkpoint blobs under the same version prefix); those MUST NOT be copied into the new version or recorded as artifacts.

#### Scenario: A branch/iterate run inherits the parent's artifacts
- **GIVEN** an `execute` message whose `parent_version_id` refers to a version with artifact objects under its OSS prefix
- **WHEN** the worker processes it as a fresh run
- **THEN** before the agent loop runs, each parent artifact object MUST be server-side-copied into the new version's prefix and recorded as an `artifacts` row for the new `version_id` (carrying the preserved version-relative `path`), so the new version's artifact set includes the parent's files plus whatever the agent then produces

#### Scenario: Inherited artifacts are announced as events
- **WHEN** inheritance records the copied rows
- **THEN** the worker MUST emit one `kind="artifact"` event per inherited artifact after its row is persisted, before the agent loop begins

#### Scenario: Checkpoint blobs are not inherited
- **GIVEN** a parent version prefix that contains both artifact objects and a `checkpoints/<n>.bin` object
- **WHEN** inheritance runs
- **THEN** the `checkpoints/` object MUST NOT be copied into the new version or recorded as an `artifacts` row; only the produced artifacts are inherited

#### Scenario: Inheritance precedes agent output
- **WHEN** inheritance runs
- **THEN** it MUST complete before the planner/executor/critic loop begins, so the agent's produced files overlay (and may overwrite) the inherited ones
