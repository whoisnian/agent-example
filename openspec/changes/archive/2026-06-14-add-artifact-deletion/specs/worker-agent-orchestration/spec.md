## ADDED Requirements

### Requirement: Executor-Declared File Deletion

The executor's structured output MAY carry an optional `deletions` field: a list of version-relative file paths the step removes (e.g. `{"summary": "...", "files": [...], "deletions": ["styles.css"]}`). The `files` (write) path MUST remain byte-identical to its pre-change handling — `deletions` is a separate, additive channel, and a file removal MUST be expressed through `deletions`, never by writing null/empty content.

For each path in `deletions`, within the boundary of the step that declared it, the agent SHALL:
1. remove the object at that path under the run's OSS prefix (`ctx.oss_client`), treating an already-absent object as success. The removal MUST go through the same OSS-tool surface as writes, cost-metered identically (the `oss_fs` `delete` op wrapped by `cost_metered_tool("oss_fs")`, so it emits a `cost.tool` event), and MUST apply the same prefix-safety normalization as `write`/`read`, rejecting a `path` that would escape `ctx.oss_prefix` (e.g. via `..`) exactly as a write would;
2. remove the current version's `artifacts` row for `(version_id, path)` through the persistence layer (never via raw SQL), treating an already-absent row as success; and
3. **only when a row was actually removed**, emit exactly one `kind="artifact_deleted"` task event through `ctx.event_publisher` with payload `{path, version_id}` (the `oss_key` MUST NOT appear in the payload, preserving the never-serialize-oss_key discipline) and a `seq` pre-reserved before the step checkpoint, alongside that step's `kind="step"` event.

**Within-step ordering.** When a single step's output contains both `files` and `deletions`, the agent SHALL apply the `files` writes (and their `(version_id, oss_key)` row upserts) **first**, then the `deletions`. Consequently, if the same `path` appears in both `files` and `deletions` of one step, the net result MUST be the file **absent** (delete wins within a step, matching the user's removal intent). Across steps, normal step ordering governs: a later step writing a path an earlier step deleted re-creates it.

Deletion MUST be idempotent and resume-safe: re-running a step (after a crash before its checkpoint) or re-applying a deletion MUST converge to the same end state — object absent, row absent — without raising, including a crash *after* the OSS object is removed but *before* the row delete (resume re-runs the whole step; both deletes no-op on the second pass). A deletion targeting a path not present in the version is a no-op (no OSS object, no row, no event) and MUST NOT fail the step. The agent MUST NOT delete artifacts belonging to any other version, and MUST NOT write `tasks` or `task_versions`.

#### Scenario: Executor deletes an inherited file

- **GIVEN** a run that inherited `styles.css` and `index.html` from its parent version (each with an `artifacts` row and an `artifact` event already emitted)
- **WHEN** the executor returns a step whose `deletions` contains `styles.css`
- **THEN** the object for `styles.css` MUST be removed under the run's OSS prefix, the `(version_id, "styles.css")` `artifacts` row MUST be deleted, and a single `kind="artifact_deleted"` event with payload `{path: "styles.css", version_id}` MUST be emitted; `index.html` MUST remain (object, row, and prior event untouched)

#### Scenario: Deleting an absent path is a silent no-op

- **WHEN** a step's `deletions` names a path that has no `artifacts` row in the current version
- **THEN** the step MUST complete successfully, no `artifact_deleted` event MUST be emitted, and the run MUST NOT fail

#### Scenario: Deletion is idempotent across a pre-checkpoint resume

- **GIVEN** a step that deleted `styles.css` but crashed before writing its checkpoint, so the run is redelivered and re-inherits then re-executes the step
- **WHEN** the step re-applies the deletion of `styles.css`
- **THEN** the OSS object delete and the `artifacts` row delete MUST each succeed as no-ops on the second pass and the end state MUST be `styles.css` absent (object and row), with no `(run_id, seq)` collision on the re-emitted event

#### Scenario: Parent version retains the deleted file

- **WHEN** a child version deletes `styles.css` that it inherited from parent version `P`
- **THEN** version `P`'s `styles.css` object and `artifacts` row MUST be unchanged, so a rollback-branch from `P` still has `styles.css`

#### Scenario: Same-step write and delete of one path nets to absent

- **WHEN** a single step's output has `files` containing `{path: "x", content: "..."}` AND `deletions` containing `x`
- **THEN** the write MUST be applied first and the delete second, so the step ends with `x`'s object and `(version_id, "x")` row absent (delete wins within a step)

#### Scenario: Deletion path escaping the run prefix is rejected

- **WHEN** a `deletions` entry is a path that resolves outside `ctx.oss_prefix` (e.g. `../other/secret`)
- **THEN** the OSS delete MUST reject it with the same prefix-safety guard that `write`/`read` apply, so the deletion cannot touch another run's namespace

### Requirement: Executor Output Validation Is Not an Internal Fault

A malformed executor `files` entry — one whose `content` is missing or is not a string (e.g. the model emitting `{"path": "styles.css", "content": null}` instead of using `deletions`) — MUST NOT propagate as an unhandled exception that the consumer records as a generic `internal` `dispatch_error`. The agent SHALL detect the malformed entry at the loop boundary and raise a **distinct typed error** carrying the offending path (`executor_output_invalid` is the worker error code for this class). The dispatch error path SHALL map that typed error to a `kind="error"` event with `code="executor_output_invalid"` (message naming the path) **and** pass the same code to the run's terminal marking — mirroring the existing typed-exception branch that maps `AgentNotImplementedError` to `code="unimplemented"`, rather than the catch-all that hardcodes `code="internal"` for both the event and the terminal mark. `executor_output_invalid` MUST be a registered, documented worker error code alongside `internal` / `unimplemented` / `deadline_exceeded`, not a bare literal. The `oss_fs(op="write")` `content`-required guard remains as defense-in-depth but MUST NOT be the surface through which a normal model mistake fails the run.

#### Scenario: Null content is mapped to a typed error code, not internal

- **WHEN** the executor returns a `files` entry `{"path": "styles.css", "content": null}` and the resulting typed error reaches the dispatch error path
- **THEN** the published `kind="error"` event AND the run's terminal-failure record MUST both carry `code="executor_output_invalid"` (message referencing `styles.css`), and neither MUST carry `code="internal"`

#### Scenario: Well-formed writes are unaffected

- **WHEN** the executor returns `files` entries each with a string `content`
- **THEN** validation MUST pass and each file MUST be written and persisted exactly as before this change (byte-identical write path)
