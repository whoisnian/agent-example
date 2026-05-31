## Why

`add-task-control-api` shipped the API side of pause/resume/cancel and locked the wire contract: `task.control` is now a `topic` exchange, messages route on `task.<task_id>`, payload is `{task_id, version_id, run_id, action, reason, issued_at}`. The existing worker `ControlListener` (scaffolded by `add-worker-bootstrap`) is bound to the **old** contract — `direct` exchange, routing key `control.<worker_id>`, payload field `ts`. As of the API's deploy, **the worker no longer boots at all** against the API-current broker: the passive `task.control` declare expects `DIRECT`, the actual exchange is now `TOPIC`, so `assert_topology` raises `TopologyError` before the worker ever consumes an execute message (reviewer S9). The listener also never emits status-event acknowledgements, so even if it received a control message a user who clicks pause would see `running` until the worker happens to emit some other event. This change makes the worker boot again **and** actually consume the API's control messages, react via the existing `CancelToken` / `PauseToken` plumbing the agent loop already reads, and emit `status:paused` / `status:running` / `status:cancelling` so the front-end converges on the real state.

## What Changes

- Re-align `mq_connection.py::EXPECTED_EXCHANGES` so the passive `task.control` declaration expects `TOPIC` (not `DIRECT`). The API's `add-task-control-api` made this change a one-time topology evolution; the worker side has to catch up or its startup fails with `TopologyError`.
- Re-align `declare_worker_queues` so `q.task.control.<worker_id>` binds to `task.control` with a **dynamic per-task** routing key — bound when a run is claimed (`task.<task_id>`), unbound when it terminates. The previous static `control.<worker_id>` binding never matched anything the API now writes.
- Update `core/control.py::ControlListener._dispatch_payload` to read the new payload shape (`issued_at` instead of `ts`; tolerate `version_id` / `reason` fields by ignoring them); the dedup key becomes `(run_id, action, issued_at)`.
- Remove the duplicate queue declaration inside `ControlListener._run_rmq` — that's now `declare_worker_queues`'s sole responsibility, so the listener just consumes from the queue passed in.
- Add status-event emission on action acknowledgement (this is the actual "handling" behavior — currently the listener just flips tokens silently):
  - **cancel** → emit `kind=status, payload={"status": "cancelling"}`. The agent loop's `_check_boundary` raises `asyncio.CancelledError` on the next iteration; the run handler's existing failure path emits the final `status:cancelled` (or `kind=error → failed` if cleanup itself fails).
  - **pause** → emit `kind=status, payload={"status": "paused"}` so `task-event-ingest` writes the user-visible state before the agent loop actually blocks on `wait_if_paused()`.
  - **resume** → emit `kind=status, payload={"status": "running"}` so the front-end can observe the state flip before any subsequent step events arrive.
- Fix the **cancel-during-pause race**: when the listener handles `cancel`, it MUST call `pause_token.resume()` before/together with `cancel_token.set()` so an agent currently awaiting `wait_if_paused()` wakes up, re-enters `_check_boundary`, sees the cancel token, and raises.
- Wire `Consumer` (the task-execute consumer) to bind/unbind the control routing key around the run lifecycle: bind right before setting `listener.current_run`; unbind in the same defer that clears `current_run`.
- Spec changes:
  - **New** capability `worker-control-handling` for the end-to-end contract (binding lifecycle, dispatch invariants, ack-event emission, cancel-after-pause race).
  - **Modify** `worker-messaging` so the "Control Signal Listener" requirement reflects topic-exchange + dynamic per-task binding + the corrected payload shape + the updated dedup key + the corrected `EXPECTED_EXCHANGES` entry.

## Capabilities

### New Capabilities

- `worker-control-handling`: the worker-side consumer of control signals — how it binds/unbinds per-task on the topic exchange, dispatches to the active `RunContext`, emits acknowledgement status events, and handles the cancel-during-pause race.

### Modified Capabilities

- `worker-messaging`: updates the "Control Signal Listener" requirement (exchange type, routing key, payload field, dedup key) and the "Topology assertions" requirement (`task.control` is `TOPIC`, not `DIRECT`).

## Impact

- New code: status-event emission helper inside `ControlListener` (uses the existing `EventPublisher` injected via `RunContext`).
- Touches: `worker/worker/core/mq_connection.py` (`EXPECTED_EXCHANGES`, `declare_worker_queues`), `worker/worker/core/control.py` (`_run_rmq` → just consume; `_dispatch_payload` → new payload shape + dedup key + status-event emit + cancel-unblocks-pause), `worker/worker/core/consumer.py` (bind/unbind around the run claim/release block).
- No new dependencies. No DB schema changes. Worker continues to consume `task.execute` exactly as before; only the control plane changes.
- Unblocks the互动闭环 demo end-to-end: API accepts control → outbox publishes → worker consumes → tokens flip → agent loop honors → status events flow → event-ingest writes → read API returns the updated state → web UI sees pause/cancel buttons actually work.
- The Redis Pub/Sub fast-path code path in `ControlListener._run_redis` already exists and stays untouched — it's gated by `redis_url` config which is empty in MVP defaults, so no behavior change there. Documented as deferred.
