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

