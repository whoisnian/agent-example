## MODIFIED Requirements

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
