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

The agent SHALL drive an explicit outer loop the worker controls: a planner decomposes the prompt into an ordered step list; for each step an executor performs the work (invoking tools as needed) and a critic decides to advance, retry (bounded by a maximum retry count), or finish. The loop — not the underlying framework — MUST own step sequencing, checkpoint writes, event emission, and pause/cancel checks at step boundaries. The retry count MUST be bounded; exceeding it MUST fail the step (and the run) rather than loop unbounded.

Each role's instruction (planner / executor / critic) SHALL be sourced from the corresponding registered `subagent` plugin's `SubagentDefinition.instruction` when the agent declares it via `subagent_names`; a built-in default instruction set MAY serve as the fallback for direct callers that supply none. Replacing a role's prompt MUST be possible by editing that subagent plugin's `prompt.md`, without changing loop code.

#### Scenario: Plan is produced and emitted once
- **WHEN** a run begins with no prior checkpoint
- **THEN** the planner MUST produce an ordered step list, the agent MUST emit one `task.events` event with `kind="plan"` carrying the steps, and MUST write a checkpoint at `step_seq=0` recording the plan

#### Scenario: Each completed step checkpoints and emits
- **WHEN** the executor and critic complete a step with verdict `advance` or `finish`
- **THEN** the agent MUST increment `ctx.step`, write a checkpoint at the new `step_seq` via `CheckpointStore.write`, and emit a `task.events` event with `kind="step"` whose payload includes the `step_seq` and critic verdict — in that order, before moving to the next step

#### Scenario: Role instructions come from the subagent plugins
- **WHEN** the production agent registry is built and a code-gen or research agent runs the loop
- **THEN** the planner / executor / critic instructions used MUST be those resolved from the registered `planner` / `executor` / `critic` subagent plugins, not an unrelated hardcoded value

#### Scenario: Run deadline exceeded fails at a step boundary
- **WHEN** the message's `deadline_ts` is in the past at a step boundary
- **THEN** the agent MUST stop without starting the next step and fail the run (error event + `nack(requeue=false)`) rather than continue

#### Scenario: Critic finish ends the loop
- **WHEN** the critic returns verdict `finish` for a step
- **THEN** the loop MUST stop after that step's checkpoint and event, and proceed to artifact upload

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

On successful completion the agent SHALL upload produced files under the run's OSS prefix (`ctx.oss_prefix`) via `ctx.oss_client` and write the corresponding `artifacts` rows (the only business table the worker may write) through the persistence layer — never via raw SQL. If artifact upload or row persistence fails, the run MUST fail (error event + `nack(requeue=false)`) rather than report success. The agent MUST NOT write `tasks` or `task_versions`.

#### Scenario: Successful run uploads artifacts and returns
- **WHEN** the loop finishes successfully having produced output files
- **THEN** the agent MUST upload them under `ctx.oss_prefix`, write `artifacts` rows via `Persistence.insert_artifact` (columns `kind`, `oss_key`, `mime`, `bytes`, `sha256`, `version_id`), and return normally so the consumer marks the run `succeeded`

#### Scenario: Artifact upload failure fails the run
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

On successful completion — after artifact upload, before returning — the agent SHALL emit exactly one `task.events` event with `kind="summary"` whose `payload.summary` is a deterministic concatenation of the run's per-step executor summaries (one line per step, prefixed by the step sequence), truncated on a rune boundary to at most 2048 bytes. The summary MUST be produced without an additional LLM call.

To make the concatenation resume-safe, each completed step's checkpoint state SHALL record that step's executor summary (capped at the existing 500-character step-summary cap) in its plan-state entry, so a resumed run reassembles the full set of step summaries — including steps executed by earlier attempts — from the restored checkpoint rather than only from in-memory state.

A failed or cancelled run MUST NOT emit a summary event. When every step summary is empty, the event MUST still be emitted with an empty `payload.summary` (the ingest side skips the column update but persists the event row).

#### Scenario: Successful run emits one summary event
- **WHEN** a 3-step run completes successfully with non-empty step summaries
- **THEN** exactly one `kind="summary"` event MUST be emitted whose `payload.summary` contains the three step summaries in step order, AND it MUST be emitted after artifact rows are written

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
