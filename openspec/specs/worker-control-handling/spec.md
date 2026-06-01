# worker-control-handling Specification

## Purpose
Defines how the Worker dynamically binds, dispatches, and acknowledges per-task control signals (pause / resume / cancel) issued by `task-control-api`, including the binding lifecycle, run-id matching, token-flip races, status acknowledgement events, and observability.

## Requirements

### Requirement: Per-Task Dynamic Binding

The Worker SHALL bind its control queue `q.task.control.<worker_id>` to exchange `task.control` (now `topic`) with routing key `task.<task_id>` **only while** that task's run is currently being executed by this worker. The bind MUST happen before the run handler sets `ControlListener.current_run` so the listener never has a window where a delivered control message has no `current_run` to dispatch to. The unbind MUST happen after the run handler clears `current_run`, in a defer / finally block so it runs on any termination path (success, failure, cancellation, panic).

Pre-claim cancel messages (sent by the API while no worker has bound for the task yet) MAY be dropped by the broker; this matches the `add-task-control-api` "best_effort" disposition and is documented in this capability's design.

#### Scenario: Bind precedes current_run assignment
- **WHEN** the worker claims a `task.execute` message for task `T` and run `R`
- **THEN** `await control_queue.bind(task_control_exchange, routing_key="task."+T)` MUST complete BEFORE `listener.current_run = ctx` is assigned

#### Scenario: Unbind runs on every termination path
- **WHEN** the run handler exits for any reason (success, raised CancelledError, agent error, deadline)
- **THEN** `await control_queue.unbind(task_control_exchange, routing_key="task."+T)` MUST execute, AND `listener.current_run = None` MUST be assigned in the same defer / finally block

#### Scenario: Unbind failure or interruption is best-effort
- **WHEN** the broker rejects `unbind` (e.g., transient channel error) OR the unbind `await` is cancelled mid-flight (e.g., drain-timeout cancellation from `lifecycle.consumer_task.cancel()`)
- **THEN** the failure / cancellation MUST log at WARN and the run handler MUST NOT raise — leftover bindings get garbage-collected when the queue auto-deletes on disconnect, and the dispatcher's "wrong run_id → drop" branch makes any stale delivery harmless

#### Scenario: Topic fan-out across HA workers reaches only the run owner
- **GIVEN** two workers W1 and W2 are both running, and both have transient `task.T` bindings (e.g., W2 took over a stale-heartbeat run from W1 while W1's `finally` hasn't unbound yet)
- **WHEN** a control message for task T arrives at both W1's and W2's control queues (the topic exchange fans it out)
- **THEN** both dispatchers receive the message; W1's `current_run.run_id` no longer matches → drops + `outcome="unknown_run"`; W2's matches → handles + `outcome="handled"`. The user observes the expected reaction on exactly one worker (reviewer S14).

### Requirement: Control Message Dispatch

The Worker SHALL parse incoming control messages as `{task_id, version_id?, run_id?, action ∈ {pause,resume,cancel}, reason, issued_at}` (matching the API's payload shape from `add-task-control-api`). The dedup key is `(run_id, action, issued_at)` for the `_LruSet` defense against MQ + Redis dual-channel duplicates. Unknown / extra fields MUST be ignored.

The dispatcher MUST compare the message's `run_id` to `ControlListener.current_run.run_id` and silently drop (log at debug) when:
- `current_run` is `None` (no run currently active), OR
- `current_run.run_id != message.run_id` (control arrived for a different run, e.g. the prior run's tail message after rebinding).

The dispatcher MUST NOT consult `task_id` when deciding to dispatch; routing-key match has already filtered by task_id. Stale messages survive because of the binding lifecycle window described in §"Per-Task Dynamic Binding".

#### Scenario: Payload with issued_at parses correctly
- **WHEN** a control message arrives with body `{"task_id":"...","run_id":"R","action":"pause","reason":"manual","issued_at":"2026-06-01T..."}`
- **THEN** the dispatcher MUST extract `run_id="R"`, `action="pause"`, `issued_at="2026-06-01T..."` (the dedup key carries the issued_at value verbatim)

#### Scenario: Stale run dropped silently
- **GIVEN** `listener.current_run` was just cleared (run terminated) but a queued control message for that run is still being drained
- **WHEN** the dispatcher reads the message
- **THEN** the message MUST be dropped at debug-level log; no token MUST be flipped; no status event MUST be emitted

#### Scenario: Cross-run message dropped after rebind
- **GIVEN** the worker just finished run R1 of task T (unbound), then claimed run R2 of task T (rebound), so `current_run.run_id = R2`
- **WHEN** a queued control message with `run_id = R1` is drained (the rebind / dispatch race window the run-id check exists for; reviewer S1)
- **THEN** no token MUST flip; no status event MUST be emitted; `worker_control_signals_total{action="<x>", outcome="unknown_run"}` MUST increment

### Requirement: Token Flip + Cancel-During-Pause Race

On `cancel`, the listener MUST:

1. Emit a `kind=status, payload={"status": "cancelling"}` event via the run context's `event_publisher` (see §"Status Acknowledgement Events").
2. Set `ctx.cancel_token`.
3. Call `ctx.pause_token.resume()` so any agent task awaiting `wait_if_paused()` wakes and re-enters `_check_boundary` (which will then see the cancel token set and raise `CancelledError`).

Order: set cancel BEFORE calling resume so a racing waker can never observe an unblocked-but-still-not-cancelled state. The `resume()` call is idempotent; cancel-when-not-paused is a no-op on the pause side.

On `pause`, the listener MUST emit `kind=status, payload={"status": "paused"}` then set `ctx.pause_token`. The agent loop blocks on the NEXT `_check_boundary` call.

On `resume`, the listener MUST clear `ctx.pause_token` then emit `kind=status, payload={"status": "running"}`. Order: unblock the agent first so subsequent step events follow the status-running event in `seq` order.

#### Scenario: Cancel unblocks a paused agent, sets cancel BEFORE resuming the pause
- **GIVEN** an agent task is awaiting `ctx.pause_token.wait_if_paused()` (the pause was previously set)
- **WHEN** the listener receives a `cancel` control message for the current run
- **THEN** the listener MUST set `cancel_token` BEFORE calling `pause_token.resume()` (verifiable by spying both methods and recording call order — reviewer S3); the agent's `wait_if_paused()` MUST return; the agent's next `_check_boundary` MUST raise `asyncio.CancelledError`

#### Scenario: Pause acknowledged before agent blocks
- **WHEN** the listener receives a `pause` for an actively-running agent
- **THEN** the `status:paused` event MUST be published BEFORE `pause_token.set_paused()` returns, so `task-event-ingest` writes the user-visible state ahead of the agent actually blocking on its next `_check_boundary`

#### Scenario: Resume unblocks the agent BEFORE emitting status=running
- **GIVEN** the agent is awaiting `pause_token.wait_if_paused()` (paused) and `ctx.next_event_seq()` is currently N
- **WHEN** the listener receives `resume`
- **THEN** `pause_token.is_paused()` MUST be `False` BEFORE `event_publisher.publish_event` is called for the running-status acknowledgement (verifiable by spying both methods); the published event MUST carry `seq = N+1`; any agent step event emitted afterward MUST carry `seq >= N+2` (reviewer S2)

### Requirement: Status Acknowledgement Events

When the listener handles a control action, it SHALL emit a corresponding `kind=status` event via the current run's `event_publisher` using `ctx.next_event_seq()` for the seq counter:

| action  | emitted `payload.status` |
|---------|---------------------------|
| pause   | `paused`                  |
| resume  | `running`                 |
| cancel  | `cancelling`              |

The terminal `cancelled` (or `failed`) status is emitted by the run handler's existing cleanup path after `CancelledError` propagates and checkpoints / artifact finalization complete — this capability does not duplicate that emission.

Emission failures MUST be logged at WARN and MUST NOT raise. The bumped metric `worker_control_emit_failed_total{action}` (added by this change) tracks the count. The agent loop continues regardless — at worst the front-end sees the state convergence delayed until the next agent event arrives.

#### Scenario: pause emits status=paused
- **WHEN** the listener handles a `pause` for the current run
- **THEN** exactly one event MUST be published with `kind="status"`, `payload={"status": "paused"}`, and `seq = ctx.next_event_seq()` (interleaving correctly with any concurrent agent-emitted events)

#### Scenario: cancel emits status=cancelling (not cancelled)
- **WHEN** the listener handles a `cancel` for the current run
- **THEN** exactly one event MUST be published with `payload.status = "cancelling"`; the terminal `cancelled` event MUST NOT be emitted by the listener — that is the run handler's cleanup-path responsibility

#### Scenario: Emit failure logs WARN without raising
- **WHEN** the `EventPublisher.publish_event` call raises (e.g., MQ unavailable)
- **THEN** the listener MUST log at WARN with the action + run_id + error, increment `worker_control_emit_failed_total{action}`, AND continue to flip the in-memory token (the agent loop must still react even when the user-visible status event was lost)

### Requirement: Observability

The Worker SHALL emit the following Prometheus counters:

- `worker_control_signals_total{action, outcome}` where `action ∈ {pause, resume, cancel}` and `outcome ∈ {handled, unknown_run, parse_error, dedup_drop}` — every received message increments exactly one cell.
- `worker_control_emit_failed_total{action}` — bumped when the acknowledgement status-event emit fails.

The existing scaffold counter `worker_control_signals_total{action}` (single-label) is replaced by the two-label form; this is an MVP-internal metric so a flag-day swap is acceptable.

#### Scenario: Cross-run cancel bumps `unknown_run`
- **GIVEN** the listener's `current_run` is `None` or has a different `run_id`
- **WHEN** a control message arrives
- **THEN** `worker_control_signals_total{action="<x>", outcome="unknown_run"}` MUST increment, AND no token MUST flip
