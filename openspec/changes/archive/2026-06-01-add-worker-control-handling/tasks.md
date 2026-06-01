## 1. Topology alignment

- [x] 1.1 Update `worker/worker/core/mq_connection.py::EXPECTED_EXCHANGES`: change `task.control` from `aio_pika.ExchangeType.DIRECT` to `aio_pika.ExchangeType.TOPIC` so the passive declare matches the API's `add-task-control-api` retype. Worker startup MUST fail clean against a stale-type broker (existing TopologyError path is sufficient).
- [x] 1.2 Update `mq_connection.py::declare_worker_queues`: keep the queue declaration (`q.task.control.<worker_id>`, `auto-delete=true`) but REMOVE the static `control_queue.bind(control_exchange, routing_key="control."+worker_id)` call. Return the unbound queue and the `task.control` exchange handle so the consumer can bind dynamically per claim.
- [x] 1.3 Update `assert_topology` (if needed) so its TopologyError message names the conflicting entity (worker_id passes through to the log already).

## 2. Listener consumer + payload

- [x] 2.1 Update `worker/worker/core/control.py::ControlListener._run_rmq`: stop re-declaring the queue (`declare_worker_queues` is now the single declaration site); accept the queue handle (and the exchange handle) via the constructor and consume from it directly. The listener becomes a pure consumer + dispatcher.
- [x] 2.2 Add `ControlListener.bind_for(task_id)` / `unbind_for(task_id)` methods: each calls the queue's `bind` / `unbind` against the `task.control` exchange with routing key `task.<task_id>`. The consumer (`worker/worker/core/consumer.py`) will call these around the run lifecycle.
- [x] 2.3 Update `_dispatch_payload` to parse the new payload shape `{task_id, version_id?, run_id?, action, reason, issued_at}`. The dedup key becomes `(run_id, action, issued_at)`. Unknown / extra fields are ignored.
- [x] 2.4 Update the listener's `worker_control_signals_total` metric usage to the new two-label form `{action, outcome}` where `outcome ∈ {handled, unknown_run, parse_error, dedup_drop}`. Every received delivery increments exactly one cell.

## 3. Control reactions (token flips + status events + race fix)

- [x] 3.0 **Register metrics** in `worker/worker/core/metrics.py` BEFORE the listener bumps them (reviewer S4): (a) widen `control_signals_total` to `labelnames=("action", "outcome")` so `outcome ∈ {handled, unknown_run, parse_error, dedup_drop}` is queryable; (b) add `control_emit_failed_total = Counter("worker_control_emit_failed_total", ..., labelnames=("action",))`. Update the `Metrics` dataclass field list to mirror these. The single-label `control_signals_total{action}` form is gone (flag-day swap; MVP-internal metric).
- [x] 3.1 Add a small `_emit_status_event(ctx, status)` helper on `ControlListener` that calls `ctx.event_publisher.publish_event(kind="status", payload={"status": status}, seq=ctx.next_event_seq(), traceparent=ctx.traceparent)` and swallows + logs WARN on failure. Bump `worker_control_emit_failed_total{action}` on failure (the counter must already exist per 3.0).
- [x] 3.2 In `_dispatch_payload`, after the dedup + run-match passes, emit + flip in this order:
  - `pause`: emit `status=paused`, then `ctx.pause_token.set_paused()`.
  - `resume`: `ctx.pause_token.resume()` first, then emit `status=running` (so unblock happens before the front-end sees the running flip).
  - `cancel`: emit `status=cancelling`, then `ctx.cancel_token.set()`, then `ctx.pause_token.resume()` (cancel-during-pause race fix per design D6 — set cancel first so a racing waker observes the cancel-set state, then unblock).
- [x] 3.3 Verify `worker/worker/agents/loop.py::_check_boundary` already reads cancel first then pause (it does, lines 309-311); no agent-loop changes needed for the race fix beyond the listener's resume-after-cancel order.

## 4. Consumer wiring (bind/unbind around claim/release)

- [x] 4.1 Update `worker/worker/core/consumer.py::_execute` to call `await self._control.bind_for(task_id)` **BEFORE** `claim_or_skip_run` (reviewer S10 — the original "bind after claim" order would hot-loop on bind failure because the claimed `task_runs` row stays `running` and the next worker hits `RUNNING_BY_OTHER_RECENT` until the stale-heartbeat timeout fires). On bind failure: log ERROR, nack(requeue=True), increment a metric (reuse `mq_publish_failures_total` or add a narrow one), return. No DB rollback is needed because the claim never happened. After a successful claim, `self._control.current_run = ctx` still runs at the existing site (~line 210).
- [x] 4.2 Update `consumer.py` around the existing `self._control.current_run = None` assignment (line ~284, in a `finally` block): wrap the unbind in `contextlib.suppress(BaseException)` (or equivalent broad-suppress) so an in-flight cancellation (e.g., `consumer_task.cancel()` from `lifecycle.py:156` during drain timeout) doesn't propagate as an unhandled exception out of the finally (reviewer S11). Call `await self._control.unbind_for(ctx.task_id)` AFTER clearing `current_run` and BEFORE the ack of the execute message. Unbind failure / cancellation logs WARN; the queue auto-deletes on disconnect anyway.

## 5. Lifecycle wiring

- [x] 5.1 Update `worker/worker/core/lifecycle.py`: `declare_worker_queues` now returns the queue + a separate handle to the `task.control` exchange. Pass both into the `ControlListener` constructor (or expose `listener.attach(queue, exchange)` if that fits the existing shape better). The listener no longer declares the queue.

## 6. Tests

- [x] 6.1 Update existing tests in `worker/tests/unit/test_control.py` (or equivalent) to use the new payload shape (`issued_at`); assert dedup key changed.
- [x] 6.2 Add a test for the cancel-during-pause race: arrange a `PauseToken` in the set state, start a task awaiting `wait_if_paused()`, dispatch a cancel payload through the listener, assert the awaiting task returns and `cancel_token.is_set()` is true.
- [x] 6.3 Add a test for status-event emission: stub `EventPublisher.publish_event`, dispatch each of pause/resume/cancel, assert the published `(kind, payload)` triples + that `seq` came from `ctx.next_event_seq()`.
- [x] 6.4 Add a test for the metric label shape: dispatch a control for a non-current run, assert `worker_control_signals_total{action, outcome="unknown_run"}` bumps.
- [x] 6.5 Add an integration-flavor test (with a fake AMQP channel that records bind/unbind/consume calls in order): run-claim sequence performs bind BEFORE `claim_or_skip_run` (S10) AND BEFORE `current_run` assignment; run-release sequence performs unbind AFTER `current_run` clears.
- [x] 6.6 Add a test for the emit-failure-still-flips invariant (reviewer S12): stub `EventPublisher.publish_event` to raise `RuntimeError("mq down")`, dispatch a `cancel`, assert `ctx.cancel_token.is_set()` is `True` AND `worker_control_emit_failed_total{action="cancel"}` increments AND the listener logged at WARN. Repeat for `pause` and `resume` so all three actions' "still flips the token" invariants are pinned.

## 7. Documentation

- [x] 7.1 Update `worker/README.md` (if it exists; otherwise the package docstrings in `core/control.py` and `core/consumer.py`) with a one-paragraph note on the dynamic-binding contract — bind on claim, unbind on release, dispatcher filters by `current_run.run_id`.
- [x] 7.2 Add a one-line pointer in `docs/ARCHITECTURE.md` (near the existing `q.task.control.<worker_id>` mention) noting that the binding is dynamic per claim, with the routing-key convention `task.<task_id>`.
- [x] 7.3 Update `docs/ARCHITECTURE.md` §5.3 control-payload JSON example (line ~650) from `{task_id, run_id, action: pause|resume|cancel, ts}` to `{task_id, version_id, run_id, action: pause|resume|cancel, reason, issued_at}` — the new shape from `add-task-control-api`'s outbox. The dual-channel (RMQ+Redis) note already implies both carry the same shape; updating the example keeps the architecture doc honest before the Redis path is ever activated (reviewer S5).
- [x] 7.4 Add a one-line code comment to `worker/worker/core/run_context.py::next_event_seq` saying "MUST remain sync (no `await`) — concurrent callers from the listener and the agent loop rely on cooperative-multitasking atomicity" so a future refactor doesn't silently break monotonicity (reviewer S7).
