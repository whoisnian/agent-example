## MODIFIED Requirements

### Requirement: Agent Registry and Contract

The worker SHALL define an `Agent` contract exposing a `task_type` and an `async run(ctx: RunContext, message: TaskExecuteMessage) -> None` method, and an `AgentRegistry` mapping `task_type` to a registered agent (or agent spec). The registry MUST be populated at process startup and validated once (system-prompt path resolvable, declared tools known, **declared subagents known**); a malformed agent spec MUST fail startup non-zero rather than fail at first message. The agent spec SHALL carry the names of the subagents it uses (`subagent_names`), each of which MUST resolve to a registered `subagent` plugin. Agents MUST reach all infrastructure (MQ, DB, OSS, cost, checkpoint, logger, cancel/pause tokens) exclusively through the injected `RunContext`; importing MQ/DB/OSS clients directly inside agent code is forbidden.

#### Scenario: Registry resolves a registered task type
- **WHEN** the registry is queried for `task_type="code-gen"` after startup
- **THEN** it MUST return the registered code-gen agent whose `task_type` equals `"code-gen"`

#### Scenario: Unknown task type is not registered
- **WHEN** the registry is queried for a `task_type` with no registered agent
- **THEN** it MUST return no agent (so the dispatcher raises `AgentNotImplementedError`)

#### Scenario: Malformed agent spec aborts startup
- **WHEN** an agent spec references a system-prompt file that does not exist, a tool name not present in the `PluginRegistry`, OR a `subagent_names` entry that is not a registered `subagent` plugin
- **THEN** worker startup MUST exit non-zero with a fatal log naming the offending agent and reason

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
