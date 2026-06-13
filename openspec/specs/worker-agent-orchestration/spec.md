# worker-agent-orchestration Specification

## Purpose
TBD - created by archiving change add-worker-code-agent. Update Purpose after archive.
## Requirements
### Requirement: Agent Registry and Contract

The worker SHALL define an `Agent` contract exposing a `task_type` and an `async run(ctx: RunContext, message: TaskExecuteMessage) -> None` method, and an `AgentRegistry` mapping `task_type` to a registered agent (or agent spec). The registry MUST be populated at process startup and validated once (system-prompt path resolvable, declared tools known, declared subagents known); a malformed agent spec MUST fail startup non-zero rather than fail at first message. The agent spec SHALL carry the names of the subagents it uses (`subagent_names`), each of which MUST resolve to a registered `subagent` plugin. Agents MUST reach all infrastructure (MQ, DB, OSS, cost, checkpoint, logger, cancel/pause tokens) exclusively through the injected `RunContext`; importing MQ/DB/OSS clients directly inside agent code is forbidden.

#### Scenario: Registry resolves a registered task type
- **WHEN** the registry is queried for `task_type="code-gen"` after startup
- **THEN** it MUST return the registered code-gen agent whose `task_type` equals `"code-gen"`

#### Scenario: Unknown task type is not registered
- **WHEN** the registry is queried for a `task_type` with no registered agent
- **THEN** it MUST return no agent (so the dispatcher raises `AgentNotImplementedError`)

#### Scenario: Malformed agent spec aborts startup
- **WHEN** an agent spec references a system-prompt file that does not exist, a tool name not present in the `PluginRegistry`, OR a `subagent_names` entry that is not a registered `subagent` plugin
- **THEN** worker startup MUST exit non-zero with a fatal log naming the offending agent and reason

### Requirement: Deep Agent Assembly via Model Factory

The worker SHALL assemble each agent's planner / executor / critic roles plus task-scoped tools around an injected chat model. Agents MUST obtain their chat model through an injected `ModelFactory.get(model_key)` and MUST NOT import a provider SDK directly. Per-run state — the OSS-prefixed filesystem, the run's `CostMeter` callback, and the cancel/pause tokens — MUST be bound to that run's `RunContext` (constructed per consumed message), never to a process-global singleton. The provider API key MUST be read from environment/secret and MUST NOT be logged or committed.

Agents MAY build on `deepagents.create_deep_agent` for the richer-reasoning assembly path. The MVP loop instead invokes the chat model directly once per role to keep the per-step checkpoint/event cadence and fake-model tests deterministic (see design D2b); this satisfies the assembly requirement provided the `ModelFactory` seam and per-run binding above hold.

#### Scenario: Model resolved through the factory
- **WHEN** an agent runs with a `ModelFactory` configured for its `model_key`
- **THEN** the agent MUST acquire its chat model from `ModelFactory.get(model_key)` and attach `ctx.cost_meter` as a callback on model invocations

#### Scenario: Fake model drives the loop in tests
- **WHEN** a test injects a `FakeModelFactory` returning a scripted chat model
- **THEN** the full plan → execute → critic → checkpoint → event → artifact loop MUST run to completion with no network call and no provider API key

#### Scenario: Per-run binding across sequential runs
- **WHEN** a worker process (consumer `prefetch=1`, runs sequential) handles two messages in succession
- **THEN** each run MUST use a freshly built agent bound to its own `RunContext`, so a prior run's `CostMeter` / cancel / pause state never leaks into the next and cost events carry the correct `run_id` and sequence per run

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

### Requirement: Checkpoint-Based Resume

On `attempt_no > 1` (or any redelivery), the agent SHALL read the latest checkpoint via `ctx.checkpoint_store.latest()` and resume from the next step rather than from the beginning. The plan MUST be restored from the `step_seq=0` checkpoint without re-invoking the planner. Already-completed steps MUST NOT be re-executed. The `CheckpointStore` is NOT an upsert: a duplicate `(run_id, step_seq)` write raises `CheckpointConflictError`. The loop MUST catch this and treat it as "this step is already durably persisted" — advancing past the step rather than failing the run.

#### Scenario: Resume replays only the next step
- **WHEN** a run is redelivered with a latest checkpoint at `step_seq=2` of a 4-step plan
- **THEN** the agent MUST restore the plan from `step_seq=0`, skip steps 1–2, and resume execution at step 3 without re-invoking the planner

#### Scenario: Fresh run has no checkpoint
- **WHEN** a run begins and `checkpoint_store.latest()` returns nothing
- **THEN** the agent MUST start from planning (step 0)

#### Scenario: Duplicate checkpoint on replay is not an error
- **WHEN** the loop writes a checkpoint for a `step_seq` that already exists (crash-after-checkpoint, before-ack redelivery) and `CheckpointStore.write` raises `CheckpointConflictError`
- **THEN** the agent MUST catch it, treat the step as already persisted, and continue — it MUST NOT re-execute the step or fail the run

### Requirement: Cost Emission Through Existing Wrappers

The agent SHALL emit cost events only through the existing primitives: LLM cost via the `CostMeter` callback attached to model invocations, and tool cost via the `cost_metered_tool` wrapper. Agent code MUST NOT call the `CostEventPublisher` directly. This change reuses the existing cost-event `kind` values (`llm`, `tool`) and MUST NOT introduce new cost kinds; only `task.events` gains the new `kind` values `plan` and `step`.

#### Scenario: LLM call produces a cost event
- **WHEN** an agent invokes its chat model with `ctx.cost_meter` attached as a callback
- **THEN** a `cost.events` message of `kind="llm"` MUST be emitted for that call without the agent touching the publisher

#### Scenario: Tool call produces a cost event
- **WHEN** an agent invokes a tool wrapped by `cost_metered_tool`
- **THEN** a `cost.events` message of `kind="tool"` MUST be emitted with the tool name and duration

### Requirement: Cooperative Pause and Cancel

The agent SHALL check `ctx.cancel_token` and `ctx.pause_token` at every step boundary. On cancel it MUST stop promptly by raising `asyncio.CancelledError` (which the consumer translates to a requeue). On pause it MUST first ensure the current step's checkpoint is durable, then block in `pause_token.wait_if_paused()` until resumed.

#### Scenario: Cancel at a step boundary stops the run
- **WHEN** `ctx.cancel_token` is set between steps
- **THEN** the agent MUST raise `asyncio.CancelledError` rather than start the next step

#### Scenario: Pause releases only after a durable checkpoint
- **WHEN** `ctx.pause_token` is set after a step completes
- **THEN** the agent MUST have written that step's checkpoint before blocking on `wait_if_paused()`, and MUST resume the next step when the token is released

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

### Requirement: Agent Observability

The worker SHALL record agent metrics in its existing registry: `agent_runs_total{task_type, outcome}`, `agent_steps_total{task_type}`, and an `agent_step_duration_seconds` histogram. The `outcome` label values MUST reuse the consumer's vocabulary — `success | error | cancelled` (matching `messages_consumed_total`) — for consistent dashboard aggregation. Each step boundary MUST log structured fields `task_id`, `run_id`, `version_id`, `step`, and critic `verdict`.

#### Scenario: Successful run increments run and step metrics
- **WHEN** a code-gen run completes 3 steps successfully
- **THEN** `agent_runs_total{task_type="code-gen",outcome="success"}` MUST increase by 1 and `agent_steps_total{task_type="code-gen"}` MUST increase by 3

### Requirement: Conversation Context Injection

When the consumed message carries a non-empty `history` and/or the run inherited parent artifacts, the loop SHALL assemble a conversation-context block and prepend it to the planner input and to the executor's per-step input. The block MUST contain, in order:

1. **Conversation history**: every history turn rendered oldest→newest as the user's prompt for that version followed by its result summary (an explicit "no summary" marker when the turn's `summary` is null); a turn whose `status` is not `succeeded` MUST be rendered with an explicit failure/cancellation marker so the model does not treat it as a successful prior result;
2. **Inherited artifact inventory** (only when inheritance copied at least one object): the path and byte size of every inherited artifact, and for small text artifacts (single object ≤ 8 KiB, identified by MIME/extension) the file content, subject to a total content budget of 24 KiB. Excerpt selection MUST be deterministic: eligible files are included in ascending byte-size order, ties broken by lexicographic path order, until the budget is exhausted; artifacts beyond the budget appear in the inventory without content. Inventory data MUST be obtained from the keys `inherit_parent_artifacts` actually copied (and their content via `ctx.oss_client`); the agent MUST NOT query business tables for it and MUST NOT derive the inventory from a fresh listing of its own run prefix (which would misattribute this run's own outputs as inherited).

The context block MUST be assembled exactly once per run, on the fresh path after inheritance completes and before planning, and persisted as part of the `step_seq=0` (plan) checkpoint state. A resumed run (`attempt_no > 1` or any redelivery with a prior checkpoint) MUST restore the block from the checkpoint instead of re-assembling it, so every attempt of a run injects byte-identical context.

The critic input MUST remain unchanged (it reviews step results only). When `history` is empty AND no artifacts were inherited, the context block MUST be omitted entirely and the role inputs MUST be byte-identical to the pre-change behavior, so create runs and legacy messages are unaffected. The context block MUST be assembled deterministically from the message and the inherited objects (no LLM call), preserving fake-model test determinism.

#### Scenario: Planner sees the conversation history
- **GIVEN** an execute message whose `history` contains turns for v1 and v2
- **WHEN** the loop invokes the planner
- **THEN** the planner's input MUST contain both turns' prompts and summaries (oldest first) followed by the current request's prompt

#### Scenario: Failed prior turns are marked
- **GIVEN** an execute message whose `history` contains a turn with `status = "failed"`
- **WHEN** the context block is rendered
- **THEN** that turn MUST carry an explicit failure marker distinguishing it from succeeded turns

#### Scenario: Executor sees inherited artifacts
- **GIVEN** a run that inherited `index.html` (2 KiB, text) and `logo.png` (300 KiB) from its parent
- **WHEN** the loop invokes the executor for a step
- **THEN** the executor's input MUST list both paths with sizes AND include the content of `index.html`, while `logo.png` appears without content

#### Scenario: Content budget caps excerpt volume deterministically
- **GIVEN** inherited text artifacts whose combined size exceeds the 24 KiB content budget
- **WHEN** the context block is assembled
- **THEN** file contents MUST be included in ascending size order (ties by path) only up to the budget and every remaining artifact MUST still appear in the inventory by path and size

#### Scenario: Resume restores the context block from the checkpoint
- **GIVEN** a run whose first attempt assembled a context block and wrote the plan checkpoint, then crashed
- **WHEN** the message is redelivered and the run resumes from the checkpoint
- **THEN** the executor inputs MUST use the checkpointed context block verbatim, AND the worker MUST NOT re-list OSS or re-read artifact contents to rebuild it

#### Scenario: Empty history and no inheritance leaves inputs unchanged
- **GIVEN** an execute message with no `history` and no `parent_version_id`
- **WHEN** the loop invokes the planner and executor
- **THEN** their inputs MUST be byte-identical to the pre-change composition (no context block, no extra markers)

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

### Requirement: Resume-Safe Event Sequencing

Each checkpoint's state SHALL record the run's event-sequence high-water mark (the last `task.events` seq emitted) at the moment the checkpoint is written. On resume, the loop MUST initialize the run's event-sequence counter from the restored checkpoint before emitting any event, so events emitted by a later attempt continue the sequence instead of restarting at 1 — without this, the ingest side's `(run_id, seq)` idempotency silently drops every post-resume event (including the summary event), and a same-process publisher rejects the non-increasing seq as a programming error.

#### Scenario: Post-resume events continue the sequence
- **GIVEN** a run whose attempt 1 emitted events up to `seq=5` and checkpointed, then crashed
- **WHEN** attempt 2 resumes from that checkpoint and emits its next event
- **THEN** that event's `seq` MUST be greater than 5, AND the ingest side MUST persist it as a new `task_events` row rather than dropping it as a duplicate

#### Scenario: Fresh runs are unaffected
- **WHEN** a run starts with no prior checkpoint
- **THEN** event sequencing MUST start from 1 exactly as before this change

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

