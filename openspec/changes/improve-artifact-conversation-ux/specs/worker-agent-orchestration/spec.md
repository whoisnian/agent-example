## MODIFIED Requirements

### Requirement: Planner / Executor / Critic Step Loop

The agent SHALL drive an explicit outer loop the worker controls: a planner decomposes the prompt into an ordered step list; for each step an executor performs the work (invoking tools as needed) and a critic decides to advance, retry (bounded by a maximum retry count), or finish. The loop — not the underlying framework — MUST own step sequencing, checkpoint writes, event emission, artifact persistence, and pause/cancel checks at step boundaries. The retry count MUST be bounded; exceeding it MUST fail the step (and the run) rather than loop unbounded.

At each step boundary the loop MUST observe this order: (1) **persist the step's produced artifact rows** and **reserve the seqs** of the step event and every artifact event (via `ctx.next_event_seq()`); (2) **write the step checkpoint**, whose recorded event-sequence high-water mark therefore covers the step event and all artifact events; (3) **emit** the step event and the artifact events. Reserving seqs and persisting rows before the checkpoint is what makes a crash anywhere in the boundary resume-safe (see "Artifact Upload on Success" and "Resume-Safe Event Sequencing").

Each role's instruction (planner / executor / critic) SHALL be sourced from the corresponding registered `subagent` plugin's `SubagentDefinition.instruction` when the agent declares it via `subagent_names`; a built-in default instruction set MAY serve as the fallback for direct callers that supply none. Replacing a role's prompt MUST be possible by editing that subagent plugin's `prompt.md`, without changing loop code.

#### Scenario: Plan is produced and emitted once
- **WHEN** a run begins with no prior checkpoint
- **THEN** the planner MUST produce an ordered step list, the agent MUST emit one `task.events` event with `kind="plan"` carrying the steps, and MUST write a checkpoint at `step_seq=0` recording the plan

#### Scenario: Each completed step persists artifacts, checkpoints, then emits
- **WHEN** the executor and critic complete a step with verdict `advance` or `finish`
- **THEN** the agent MUST, in this order: persist that step's `artifacts` rows and reserve the step + artifact event seqs; increment `ctx.step` and write a checkpoint at the new `step_seq` via `CheckpointStore.write` (its `event_seq` snapshot covering those reserved seqs); then emit the `kind="step"` event and the per-artifact `kind="artifact"` events — before moving to the next step

#### Scenario: Role instructions come from the subagent plugins
- **WHEN** the production agent registry is built and a code-gen or research agent runs the loop
- **THEN** the planner / executor / critic instructions used MUST be those resolved from the registered `planner` / `executor` / `critic` subagent plugins, not an unrelated hardcoded value

#### Scenario: Run deadline exceeded fails at a step boundary
- **WHEN** the message's `deadline_ts` is in the past at a step boundary
- **THEN** the agent MUST stop without starting the next step and fail the run (error event + `nack(requeue=false)`) rather than continue

#### Scenario: Critic finish ends the loop
- **WHEN** the critic returns verdict `finish` for a step
- **THEN** the loop MUST stop after that step's artifact persistence, checkpoint, and events, and proceed to the run-summary emission

#### Scenario: Retry budget is bounded
- **WHEN** the critic returns `retry` for the same step more than the configured maximum
- **THEN** the agent MUST stop retrying and fail the step (propagating an error that the consumer turns into an `error` event + `nack(requeue=false)`)

### Requirement: Artifact Upload on Success

The agent SHALL upload produced files under the run's OSS prefix (`ctx.oss_prefix`) via `ctx.oss_client` and SHALL persist each produced file's `artifacts` row **at the end of the step that produced it** — not deferred to end-of-run — through the persistence layer (never via raw SQL), writing columns `kind`, `oss_key`, `path` (the version-relative path the file was written to), `mime`, `bytes`, `sha256`, `version_id`. Row persistence is the existing `(version_id, oss_key)` upsert, so a step that overwrites an earlier file collapses to one row.

**Checkpoint ordering (resume safety).** Within a step boundary the artifact row persistence and the reservation of each artifact event's `seq` MUST happen **before** that step's checkpoint is written, so that:
- a crash after the OSS object is written but **before** the step checkpoint leaves the row either already upserted or absent, and resume re-executes the whole step (the checkpoint never advanced) and re-upserts — there is NEVER a produced OSS object with no `artifacts` row, since there is no separate end-of-run persistence pass to depend on; and
- the checkpoint's recorded event-sequence high-water mark covers every artifact event's `seq`, so a resumed run's `restore_event_seq` cannot hand out a `seq` that a persisted artifact event already used (which the ingest `(run_id, seq)` idempotency would silently drop — the failure mode "Resume-Safe Event Sequencing" warns against).

Immediately after a step's checkpoint is written the agent SHALL emit, for each persisted row, a `kind="artifact"` task event through `ctx.event_publisher` with payload `{artifact_id, path, mime, bytes, sha256}` and the pre-reserved `seq`, alongside the step's `kind="step"` event. Row-vs-event ordering is **insert-then-publish**: a consumer reacting to the artifact event always observes the row already committed. On resume, steps already completed are not re-run and their artifacts are already persisted; a re-executed step's re-persistence collapses via the upsert, and any artifact events it re-emits use seqs above the restored high-water mark (never colliding with persisted ones).

If artifact upload, row persistence, or the artifact event publish fails, the run MUST fail (error event + `nack(requeue=false)`) rather than report success. The agent MUST NOT write `tasks` or `task_versions`. There is no separate end-of-run artifact persistence pass.

#### Scenario: Step completion persists rows before checkpoint and emits artifact events after

- **WHEN** a step finishes having written two files
- **THEN** the agent MUST upsert one `artifacts` row per file (each carrying its version-relative `path`) and reserve the two artifact event seqs BEFORE writing the step checkpoint, then emit one `kind="artifact"` event per file (payload `{artifact_id, path, mime, bytes, sha256}`) with each row insert preceding its event publish

#### Scenario: Artifacts are visible mid-run

- **GIVEN** a run whose step 1 produced `index.html` and whose step 2 is still executing
- **WHEN** a client lists the version's artifacts
- **THEN** the `index.html` row MUST already be returned (persistence is per-step, not end-of-run)

#### Scenario: Crash between OSS write and step checkpoint leaves no orphan and no seq reuse

- **GIVEN** a step that wrote its OSS object and upserted its row but crashed before the step checkpoint was written
- **WHEN** the run is redelivered and resumes
- **THEN** the loop MUST re-execute that step (the checkpoint never advanced), the `(version_id, oss_key)` upsert MUST leave exactly one row, and no post-resume event MUST reuse a `seq` that a persisted artifact event already consumed

#### Scenario: Artifact persistence failure fails the run

- **WHEN** uploading an artifact or writing its row fails
- **THEN** the agent MUST raise so the run is reported failed, and MUST NOT leave the run marked `succeeded`

### Requirement: Run Summary Event

On successful completion — after the final step's artifact rows are persisted and its events emitted, before returning — the agent SHALL emit exactly one `task.events` event with `kind="summary"` whose `payload.summary` is a deterministic concatenation of the run's per-step executor summaries (one line per step, prefixed by the step sequence), truncated on a rune boundary to at most 2048 bytes. The summary MUST be produced without an additional LLM call.

To make the concatenation resume-safe, each completed step's checkpoint state SHALL record that step's executor summary (capped at the existing 500-character step-summary cap) in its plan-state entry, so a resumed run reassembles the full set of step summaries — including steps executed by earlier attempts — from the restored checkpoint rather than only from in-memory state.

A failed or cancelled run MUST NOT emit a summary event. When every step summary is empty, the event MUST still be emitted with an empty `payload.summary` (the ingest side skips the column update but persists the event row).

#### Scenario: Successful run emits one summary event
- **WHEN** a 3-step run completes successfully with non-empty step summaries
- **THEN** exactly one `kind="summary"` event MUST be emitted whose `payload.summary` contains the three step summaries in step order, AND it MUST be emitted after the final step's artifact rows are persisted

#### Scenario: Resumed run summarizes all steps, not just its own
- **GIVEN** a run whose attempt 1 completed steps 1–2 (checkpointing each step's summary) and crashed, and whose attempt 2 resumed and completed steps 3–4
- **WHEN** attempt 2 finishes successfully
- **THEN** the emitted `payload.summary` MUST contain the summaries of steps 1–4 in step order

#### Scenario: Failed run emits no summary
- **WHEN** a run fails (error event path) or is cancelled
- **THEN** no `kind="summary"` event MUST be emitted for that run

#### Scenario: Oversized summary is truncated
- **WHEN** the concatenated step summaries exceed 2048 bytes
- **THEN** the emitted `payload.summary` MUST be truncated on a rune boundary to at most 2048 bytes with a trailing `…`
