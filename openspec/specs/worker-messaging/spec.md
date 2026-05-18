# worker-messaging Specification

## Purpose
TBD - created by archiving change init-worker-scaffold. Update Purpose after archive.
## Requirements
### Requirement: Task Execute Consumer

The Worker SHALL consume from `q.task.execute.<lane>` (default lane: `default`) on `task.exchange` with routing key `execute.<task_type>.<lane>`. The consumer MUST set `prefetch_count=1` (one in-flight message per worker channel) and MUST use manual ack mode.

The consumer SHALL parse each delivery into a typed `TaskExecuteMessage` matching the envelope defined in `docs/ARCHITECTURE.md §5.3`: `{msg_id, idempotency_key, task_id, version_id, run_id, attempt_no, task_type, prompt, params, parent_version_id, parent_artifact_root, deadline_ts}`. Failure to parse MUST result in `nack(requeue=false)` (poison message → DLX) plus a `worker_invalid_message_total` increment.

#### Scenario: Prefetch limits concurrency to one
- **WHEN** the worker is processing a task message
- **THEN** the broker MUST NOT deliver a second message to the same channel until the first is ack'd or nack'd

#### Scenario: Poison message routed to DLX
- **WHEN** a delivery body cannot be parsed as `TaskExecuteMessage`
- **THEN** the consumer MUST `nack(requeue=false)` so the broker routes it to `task.dlx`, AND `worker_invalid_message_total` MUST be incremented

### Requirement: Idempotent Consumption

Before executing a parsed message, the consumer SHALL check whether `task_runs` already has a row for the given `idempotency_key`:
- If absent → insert a row with `status='running'`, `started_at=now()`, `worker_run_id=<this worker_id>` and proceed.
- If present and `status='running'` with a recent heartbeat (≤ 2× heartbeat interval) AND `worker_run_id` differs from this worker → `nack(requeue=true)` immediately (another worker owns it). Log at info.
- If present and `status` is terminal (`succeeded`/`failed`/`cancelled`) → `ack` and skip execution.
- If present and `status='running'` but heartbeat is stale (> 2× heartbeat interval) → take over: update `worker_run_id` to this worker and proceed from latest checkpoint.

#### Scenario: Already-completed run is skipped
- **WHEN** a delivery's `idempotency_key` matches a `task_runs` row with `status='succeeded'`
- **THEN** the consumer MUST `ack` the delivery without re-executing, and MUST log at info that the duplicate was skipped

#### Scenario: Stale-heartbeat run is taken over
- **WHEN** a delivery's `idempotency_key` matches a `task_runs` row with `status='running'` and `last_heartbeat < now() - 2 * heartbeat_interval`
- **THEN** the consumer MUST atomically update `worker_run_id` to its own ID with a guard on the stale heartbeat (CAS), then proceed from the latest `task_checkpoints` row

### Requirement: Control Signal Listener

The Worker SHALL bind a dedicated queue `q.task.control.<worker_id>` to the `task.control` direct exchange with routing key `control.<worker_id>`, AND simultaneously subscribe to a Redis Pub/Sub channel `control:<worker_id>` as a fast-path. Both channels carry the same payload shape (`{task_id, run_id, action: pause|resume|cancel, ts}`). Whichever arrives first wins; duplicates from the slower channel MUST be deduplicated by `(run_id, action, ts)`.

The listener MUST translate received signals into an in-memory `CancelToken` / `PauseToken` on the matching `RunContext`. The execution side (in `worker-execution-runtime`) is responsible for honoring the token at safe checkpoint boundaries.

#### Scenario: Cancel signal sets the token
- **WHEN** a `cancel` message arrives for the currently-executing `run_id` via either RMQ or Redis
- **THEN** the matching `RunContext.cancel_token` MUST be set, AND `worker_control_signals_total{action="cancel"}` MUST be incremented

#### Scenario: Duplicate signal across channels deduped
- **WHEN** the same `(run_id, action, ts)` is delivered via both RMQ and Redis within 5 seconds
- **THEN** the in-memory token MUST be set exactly once, AND `worker_control_signals_total` MUST be incremented exactly once

#### Scenario: Signal for an unknown run is ignored
- **WHEN** a control message arrives for a `run_id` not currently being executed by this worker
- **THEN** the worker MUST ack/discard the signal (without raising) and log at debug

### Requirement: Event Publisher

The Worker SHALL expose a typed `EventPublisher.publish_event(kind, payload, seq)` API that emits to `task.events` exchange with routing key `event.<task_type>.<kind>`. The message body MUST conform to `{task_id, version_id, run_id, seq, kind, payload, ts}` (matching `docs/ARCHITECTURE.md §5.3`), and the `idempotency_key` header MUST be `<run_id>:<seq>`. Publisher confirms MUST be enabled; the publish MUST block until ack/nack with a 5s timeout.

`seq` is per-run monotonic; the runtime layer (`worker-execution-runtime`) is responsible for maintaining the counter. The publisher itself MUST refuse decreasing or duplicate `seq` values for the same `run_id` within a process lifetime, raising a programming-error exception.

#### Scenario: Event publish is confirmed
- **WHEN** `publish_event` is called and the broker returns `ack`
- **THEN** the call MUST return successfully only after `ack` is observed, AND `worker_event_publish_duration_seconds` MUST be observed

#### Scenario: Decreasing seq is rejected
- **WHEN** `publish_event` is called with `seq=5` for a `run_id` after `seq=7` has already been published in the same process
- **THEN** the call MUST raise a programming-error exception without publishing

### Requirement: Cost Event Publisher

The Worker SHALL expose a separate `CostEventPublisher.publish_cost(kind, resource_name, **fields, seq)` API that emits to `cost.exchange` with routing key `cost.<kind>` (`llm`/`tool`/`compute`). The message body MUST match `docs/ARCHITECTURE.md §5.3` (`{task_id, version_id, run_id, seq, kind, resource_name, input_tokens, output_tokens, cached_tokens, duration_ms, occurred_at}` plus optional `calls`). Publisher confirms MUST be enabled with the same 5s timeout semantics as `EventPublisher`.

Cost events use a separate `seq` namespace from task events (per-run-per-kind monotonic), to allow independent consumers.

#### Scenario: LLM cost event published with token counts
- **WHEN** `publish_cost(kind="llm", resource_name="claude-opus-4-7", input_tokens=1200, output_tokens=480, duration_ms=4300)` is called
- **THEN** a message routed to `cost.llm` MUST be published, AND `worker_cost_events_published_total{kind="llm"}` MUST be incremented

### Requirement: Topology Assertion on Startup

Before opening any consumer, the Worker SHALL passively verify that required exchanges exist (`task.exchange`, `task.control`, `task.events`, `task.dlx`, `cost.exchange`). Required queues are declared by the Worker itself: `q.task.execute.<lane>` (quorum, with DLX argument set to `task.dlx`) AND `q.task.control.<worker_id>` (quorum, auto-delete on worker disconnect).

If any required exchange is missing or has incompatible type, startup MUST exit non-zero with a fatal log naming the conflicting entity.

#### Scenario: Missing required exchange aborts startup
- **WHEN** `task.events` does not exist at worker startup
- **THEN** the worker MUST exit non-zero before binding any consumer

