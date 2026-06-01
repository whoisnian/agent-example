## MODIFIED Requirements

### Requirement: Control Signal Listener

The Worker SHALL bind a dedicated queue `q.task.control.<worker_id>` (declared once at worker startup, `auto-delete=true` on disconnect) to the `task.control` **topic** exchange. Bindings are NOT static at startup — instead, when a run for task `T` is claimed, the worker SHALL `await control_queue.bind(task_control_exchange, routing_key="task."+T)`; when the run terminates the worker SHALL `await control_queue.unbind(...)`. The full lifecycle invariants are bound by the `worker-control-handling` capability.

The listener simultaneously subscribes to a Redis Pub/Sub channel `control:<worker_id>` as a fast-path (MVP gated by an empty default `redis_url` config; the channel stays inert until activated). Both channels carry the same payload shape `{task_id, version_id?, run_id?, action ∈ {pause|resume|cancel}, reason, issued_at}` matching `add-task-control-api`'s API contract. Whichever arrives first wins; duplicates from the slower channel MUST be deduplicated by `(run_id, action, issued_at)`.

The listener MUST translate received signals into an in-memory `CancelToken` / `PauseToken` on the matching `RunContext` AND emit a `kind="status"` acknowledgement event per the `worker-control-handling` capability (`paused` / `running` / `cancelling` respectively). The execution side (in `worker-execution-runtime`) is responsible for honoring the token at safe checkpoint boundaries.

#### Scenario: Cancel signal sets the token
- **WHEN** a `cancel` message arrives for the currently-executing `run_id` via either RMQ or Redis
- **THEN** the matching `RunContext.cancel_token` MUST be set, AND `RunContext.pause_token.resume()` MUST be called so any in-flight `wait_if_paused()` unblocks (worker-control-handling §"Cancel-During-Pause Race"), AND `worker_control_signals_total{action="cancel", outcome="handled"}` MUST be incremented

#### Scenario: Duplicate signal across channels deduped
- **WHEN** the same `(run_id, action, issued_at)` is delivered via both RMQ and Redis within the dedup window
- **THEN** the in-memory token MUST be set exactly once, AND `worker_control_signals_total{outcome="handled"}` MUST be incremented exactly once; the duplicate MUST bump `worker_control_signals_total{outcome="dedup_drop"}`

#### Scenario: Signal for an unknown run is ignored
- **WHEN** a control message arrives for a `run_id` not currently being executed by this worker (e.g., the rebind window after a run terminates, or a routing-key-matched but stale message)
- **THEN** the worker MUST ack/discard the signal (without raising) and log at debug, AND `worker_control_signals_total{outcome="unknown_run"}` MUST be incremented

### Requirement: Topology Assertion on Startup

Before opening any consumer, the Worker SHALL passively verify that required exchanges exist with the expected types: `task.exchange` (topic), `task.control` (**topic** — retyped by `add-task-control-api`), `task.events` (topic), `task.dlx` (direct), `cost.exchange` (topic). Required queues are declared by the Worker itself: `q.task.execute.<lane>` (quorum, with DLX argument set to `task.dlx`) AND `q.task.control.<worker_id>` (auto-delete on worker disconnect; bindings to `task.control` happen dynamically per claim, not at queue declaration time).

If any required exchange is missing or has incompatible type, startup MUST exit non-zero with a fatal log naming the conflicting entity. A worker that boots against a broker whose `task.control` is still the legacy `direct` type (because the API hasn't redeployed yet) MUST fail at this step with a clear error.

#### Scenario: Missing required exchange aborts startup
- **WHEN** the worker starts up and a required exchange is missing
- **THEN** the process MUST exit non-zero before the first message is consumed, with a fatal log naming the missing exchange

#### Scenario: `task.control` type mismatch aborts startup
- **GIVEN** the broker's `task.control` exchange is currently `direct` (pre-`add-task-control-api` state)
- **WHEN** the worker's `assert_topology` runs the passive declare with `aio_pika.ExchangeType.TOPIC`
- **THEN** the call MUST fail with `PRECONDITION_FAILED`, the worker MUST exit non-zero, and the fatal log MUST name `task.control` and the expected type
