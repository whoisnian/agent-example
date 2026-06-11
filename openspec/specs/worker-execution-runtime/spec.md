# worker-execution-runtime Specification

## Purpose
TBD - created by archiving change init-worker-scaffold. Update Purpose after archive.
## Requirements
### Requirement: Run Context

For each consumed `task.execute` message, the Worker SHALL construct a `RunContext` carrying at minimum: `task_id`, `version_id`, `run_id`, `attempt_no`, `task_type`, `worker_id`, `oss_prefix` (resolved from `tenant/task/version` per `docs/ARCHITECTURE.md §3.5`), `cancel_token`, `pause_token`, `cost_meter`, `event_publisher`, `checkpoint_store`, `oss_client`, `logger`, and `trace_span`.

`RunContext` MUST be the only object passed to agent / tool code. Direct access to MQ / DB / OSS clients from inside agents or plugins is forbidden — they MUST go through `RunContext` methods.

#### Scenario: Agent receives only RunContext
- **WHEN** the execution dispatcher invokes the (placeholder) agent for a task
- **THEN** the agent function signature MUST be `async def run(ctx: RunContext) -> RunResult`, and no other infrastructure handle MUST be passed

#### Scenario: OSS prefix is task-scoped
- **WHEN** `RunContext.oss_prefix` is read for a given run
- **THEN** it MUST equal `{tenant_id}/{task_id}/{version_id}/`, AND any `oss_client` write attempt outside this prefix MUST raise

### Requirement: Heartbeat

While a task is in-flight, the Worker SHALL update `task_runs.last_heartbeat = now()` for the current `run_id` every `HEARTBEAT_INTERVAL` seconds (default `5`, configurable). The heartbeat task MUST run in a separate asyncio task so it is decoupled from the execution coroutine; it MUST stop within 1 second of run completion or shutdown.

If 3 consecutive heartbeat writes fail (e.g. DB unreachable), the runtime MUST cancel the in-flight task via `RunContext.cancel_token`, `nack(requeue=true)` the message, and emit `worker_heartbeat_failures_total`.

#### Scenario: Heartbeat updates last_heartbeat
- **WHEN** a task is running and 5 seconds have elapsed since the previous heartbeat
- **THEN** `task_runs.last_heartbeat` MUST be updated to approximately `now()`

#### Scenario: Sustained heartbeat failure triggers nack
- **WHEN** 3 consecutive heartbeat UPDATEs fail with transient errors
- **THEN** the runtime MUST cancel the in-flight task, MUST `nack(requeue=true)` the source message, AND MUST increment `worker_heartbeat_failures_total`

### Requirement: Cost Meter

The Worker SHALL provide a `CostMeter` accessible via `RunContext.cost_meter` that intercepts all LLM and tool calls. The MVP scaffold MUST integrate with LangChain via a `BaseCallbackHandler` that captures `on_llm_end` to extract `prompt_tokens`, `completion_tokens`, optional `cached_tokens`, `model_name`, and wall-clock duration, then calls `CostEventPublisher.publish_cost(kind="llm", ...)` via the runtime's monotonic `cost_seq` counter.

Tool calls registered through the Plugin Loader MUST be wrapped by a decorator that times execution and emits `kind="tool"` with `duration_ms` and `calls=1`.

Failure to emit a cost event (e.g., MQ down) MUST be logged at WARN level and MUST NOT fail the host LLM/tool call. The unpublished event MUST be retried via an in-memory bounded buffer (capacity default 1000) drained on reconnect.

#### Scenario: LLM call emits cost event
- **WHEN** an LLM call wrapped by the `CostMeter` callback completes successfully
- **THEN** exactly one `cost.llm` event MUST be published with token fields populated from the LangChain response, AND `worker_cost_events_published_total{kind="llm"}` MUST be incremented

#### Scenario: Cost publish failure does not break task
- **WHEN** the LLM call completes but the cost event publish fails because RabbitMQ is unreachable
- **THEN** the host LLM call MUST still return its result to the caller, AND the failed event MUST be buffered in memory, AND `worker_cost_events_buffered` (gauge) MUST reflect the buffer depth

#### Scenario: Buffered cost events are drained on reconnect
- **WHEN** RabbitMQ reconnects after a buffer accumulated 5 cost events
- **THEN** all 5 events MUST be published in original order before any new cost event, AND the buffer gauge MUST return to 0

### Requirement: Checkpoint Store

The Worker SHALL provide a `CheckpointStore` accessible via `RunContext.checkpoint_store` with methods `write(step_seq, step_name, state, large_payload=None) -> None` and `latest() -> Optional[Checkpoint]`.

`write` MUST be idempotent: if `(run_id, step_seq)` already exists, the call MUST raise `CheckpointConflictError` (the caller is responsible for handling — typically by skipping a replayed step). `latest` returns the highest-`step_seq` checkpoint for the current `run_id`, or `None`.

Small `state` (JSON-serializable, ≤ `CHECKPOINT_INLINE_BYTES` default 8 KiB) MUST be written inline into the `task_checkpoints.state` JSONB column. Larger payloads MUST be uploaded to OSS under `checkpoints/{tenant}/{task}/{version}/{run_id}/{step_seq}.bin`, and only the `oss_key` MUST be stored in the row.

#### Scenario: Small state is stored inline
- **WHEN** `write(step_seq=3, step_name="plan", state={"steps":[...]})` is called with a JSON body of 2 KB
- **THEN** the resulting `task_checkpoints` row MUST have non-null `state` and null `oss_key`

#### Scenario: Large state is offloaded to OSS
- **WHEN** `write(step_seq=4, step_name="generate", state={...}, large_payload=<20 MB blob>)` is called
- **THEN** the blob MUST be uploaded to OSS at `checkpoints/.../4.bin`, the row MUST have `oss_key` set to that path, and `state` MUST contain the small JSON (≤ 8 KiB) without the blob

#### Scenario: Duplicate step_seq raises
- **WHEN** `write(step_seq=3, ...)` is called twice for the same `run_id`
- **THEN** the second call MUST raise `CheckpointConflictError` and the first row MUST remain unchanged

### Requirement: Plugin Loader

The Worker SHALL include a Plugin Loader that scans `worker/plugins/{tool,subagent}/<name>/plugin.yaml` at startup and populates an in-memory `PluginRegistry`. Each `plugin.yaml` MUST conform to the schema defined in `docs/ARCHITECTURE.md §8.2` (`kind`, `name`, `version`, `entrypoint`, optional `schema`, `permissions`, `applies_to`, `resources`).

Loader behavior:
- Discovers files matching the glob deterministically (sorted by path) so registration order is reproducible.
- Imports `entrypoint` (`module:callable`) lazily on first use, not at scan time.
- Rejects duplicate `(kind, name, version)` tuples with `PluginRegistrationError` at startup.
- Exposes registry queries: `get_tool(name, version=None)`, `get_subagent(name, version=None)`, `list_by_task_type(task_type)`.

The MVP scaffold MUST include exactly one stub plugin used by tests: `worker/plugins/tool/noop_tool/` with a `plugin.yaml` declaring `kind: tool`, `name: noop`, `version: 0.1.0`, `entrypoint: handler:noop`, and a corresponding `handler.py` returning `{"ok": True}`.

#### Scenario: Plugin registered from yaml
- **WHEN** the worker starts with `worker/plugins/tool/noop_tool/plugin.yaml` present and valid
- **THEN** `PluginRegistry.get_tool("noop")` MUST return a record whose `entrypoint` resolves lazily to `noop_tool.handler.noop`

#### Scenario: Duplicate plugin aborts startup
- **WHEN** two `plugin.yaml` files declare the same `(kind, name, version)` tuple
- **THEN** startup MUST exit non-zero with a fatal log naming the duplicate identifier and both file paths

### Requirement: Execution Dispatcher

The runtime SHALL include an `ExecutionDispatcher` that, given a parsed `TaskExecuteMessage` and prepared `RunContext`, resolves the appropriate agent by `task_type` from the `AgentRegistry` and invokes it via `await agent.run(ctx, message)`. The dispatcher MUST raise `AgentNotImplementedError` only when no agent is registered for the `task_type`; the consumer translates that into a final `task.events` `error` event with code `unimplemented` and `nack(requeue=false)` so the message goes to `task.dlx`. When an agent is registered, the dispatcher MUST run it and return normally on success (the consumer then marks the run `succeeded`), and MUST let agent exceptions (including `asyncio.CancelledError`) propagate unchanged so the consumer applies its requeue / error / DLX policy.

The dispatcher's constructor signature MUST accept the `AgentRegistry`, and the `dispatch(ctx, message)` method signature MUST remain stable so the consumer call site is unchanged.

#### Scenario: Unknown task_type produces DLX
- **WHEN** a `task.execute` message with a `task_type` that has no registered agent is consumed
- **THEN** the dispatcher MUST raise `AgentNotImplementedError`, the runtime MUST publish a `task.events` event with `kind="error"` and `payload.code="unimplemented"`, AND the message MUST be `nack(requeue=false)` so the broker routes it to `task.dlx`

#### Scenario: Registered task_type runs the agent
- **WHEN** a `task.execute` message with `task_type="code-gen"` is consumed and a code-gen agent is registered
- **THEN** the dispatcher MUST invoke `agent.run(ctx, message)`; on normal return the consumer MUST mark the run `succeeded`, publish a `status=succeeded` event, and `ack` the message

#### Scenario: Agent error propagates to the consumer
- **WHEN** the registered agent raises a non-cancellation exception during `run`
- **THEN** the dispatcher MUST NOT swallow it; the consumer MUST publish an `error` event and `nack(requeue=false)`

### Requirement: Semantic Title Generation

The runtime SHALL provide a `TitleGenerator` component that generates a semantic task title from the consumed message's `prompt` via a single LLM call and emits it as a `task.events` event with `kind="title"` and payload `{"title": "<generated title>"}` through the run's `EventPublisher` (normal per-run `seq`, idempotency key `<run_id>:<seq>`).

The generator MUST be invoked only when ALL of the following hold; otherwise no LLM call occurs and no `title` event is published:
- the `TaskExecuteMessage` carries `gen_title=true`;
- the run is fresh — `ctx.checkpoint_store.latest()` returns `None` (a redelivered or taken-over run that already wrote a checkpoint MUST NOT regenerate the title; correctness does not depend on this guard alone — a fresh redelivery that crashed before its first checkpoint regenerates once, and the ingest side applies last-write-wins, see `task-event-ingest`);
- `ctx.cancel_token` is not set (a cancelled run skips generation; this counts as a skip, not a failure);
- the `AgentRegistry` has an agent registered for the message's `task_type` (a DLX-bound `unimplemented` message MUST NOT burn an LLM call).

When invoked, the generator runs after the run is claimed and the `status=running` event is published, and before the agent is dispatched. The whole generation (LLM call + event publish) MUST be bounded by a 10s timeout. Title generation is best-effort: any failure (LLM error, timeout, publish failure, empty sanitized output) MUST be logged at WARN, MUST increment `worker_title_generation_failures_total`, and MUST NOT fail or delay the run beyond the timeout — the agent dispatch proceeds and the placeholder title persists.

The chat model MUST be obtained via `ModelFactory.get(<title model key>)` — resolved from the `WORKER_TITLE_MODEL_KEY` environment variable, falling back to the model key of the agent registered for the message's `task_type` when unset — and the call MUST attach `ctx.cost_meter` as a callback so the cost is emitted as a regular `cost.llm` event; the generator MUST NOT call `CostEventPublisher` directly and MUST NOT import a provider SDK directly.

The model input MUST be bounded to the first 2000 characters of `prompt`. The raw model output MUST pass through a pure sanitization function: take the first non-empty line, strip wrapping quotes, collapse internal whitespace, then truncate on a rune boundary so that the final string — including the `…` appended when truncation occurs — is at most 64 runes AND at most 200 bytes; when the sanitized result is empty, no `title` event is published.

#### Scenario: Flagged fresh run emits a title event
- **WHEN** a fresh run (no prior checkpoint) with `gen_title=true` is claimed and the LLM call returns a non-empty title
- **THEN** the worker MUST publish a `task.events` event with `kind="title"` and `payload.title` equal to the sanitized output before the agent is dispatched, AND a `cost.llm` event for the call MUST be emitted via the `CostMeter` callback

#### Scenario: Unflagged run skips generation entirely
- **WHEN** a run with `gen_title=false` (or absent) is claimed
- **THEN** the `TitleGenerator` MUST NOT be invoked, no extra LLM call occurs, and no `kind="title"` event is published

#### Scenario: Redelivered run with a checkpoint does not regenerate the title
- **GIVEN** a run with `gen_title=true` that already wrote at least one checkpoint (crash redelivery or stale-heartbeat takeover)
- **WHEN** the message is consumed again and `ctx.checkpoint_store.latest()` is not `None`
- **THEN** the `TitleGenerator` MUST NOT be invoked — no LLM call and no `kind="title"` event — and the run resumes from the checkpoint normally

#### Scenario: Cancelled run skips generation
- **WHEN** a run with `gen_title=true` is claimed but `ctx.cancel_token` is already set before generation starts
- **THEN** no LLM call occurs and no `kind="title"` event is published, and the skip is not counted as a generation failure

#### Scenario: Unregistered task_type does not burn an LLM call
- **WHEN** a message with `gen_title=true` carries a `task_type` with no registered agent
- **THEN** the `TitleGenerator` MUST NOT be invoked and the existing `unimplemented` → DLX path proceeds unchanged

#### Scenario: Generation failure does not affect the run
- **WHEN** a fresh run with `gen_title=true` is claimed and the LLM call raises or exceeds the 10s timeout
- **THEN** the worker MUST log at WARN and increment `worker_title_generation_failures_total`, AND the agent MUST still be dispatched so the run proceeds normally with no `kind="title"` event

#### Scenario: Oversized model output is sanitized and truncated
- **WHEN** the LLM returns a multi-line, quote-wrapped title exceeding 64 runes
- **THEN** the published `payload.title` MUST be the first non-empty line, unquoted, truncated on a rune boundary with a trailing `…` such that the final string including the `…` is within 64 runes and 200 bytes

#### Scenario: Empty sanitized output suppresses the event
- **WHEN** the LLM returns output that sanitizes to an empty string
- **THEN** no `kind="title"` event is published, the failure counter is incremented, and the run proceeds

