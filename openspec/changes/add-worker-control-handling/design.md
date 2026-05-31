## Context

The Worker has had a `ControlListener` scaffold since `add-worker-bootstrap`, plus token plumbing (`CancelToken`, `PauseToken`) in `RunContext` that the agent loop's `_check_boundary` already reads (`worker/worker/agents/loop.py` lines 303-313). The agent reacts correctly to the tokens — what's missing is the messaging-layer side that flips them based on the API's control messages, plus the acknowledgement events that close the user-visible loop.

The just-shipped `add-task-control-api` locked the wire contract. Concretely:

- Exchange `task.control` is now `topic` (retyped from `direct`).
- Routing key for each control message is `task.<task_id>`.
- Payload is `{task_id, version_id?, run_id?, action ∈ {pause,resume,cancel}, reason, issued_at}`.

The existing worker code targets the prior contract:

- `mq_connection.py::EXPECTED_EXCHANGES` passively asserts `task.control` is `DIRECT` → fails the worker's startup probe after the API redeploys (TopologyError on `passive=True` declare with mismatched type).
- `declare_worker_queues` binds `q.task.control.<worker_id>` to `task.control` with routing key `control.<worker_id>` → no message the API now writes will match.
- `ControlListener._dispatch_payload` reads `payload["ts"]`; the API writes `issued_at`.
- The listener never emits any acknowledgement event when it sets a token, so `task.event-ingest` has nothing to write and the user sees stale state until some other event fires.

Worker code that's correct and stays as-is:

- `RunContext.cancel_token` / `pause_token` mechanics.
- `agent_loop._check_boundary`'s cancel-then-pause read order.
- `ControlListener._run_redis` (Redis fast-path) — disabled-by-default; doesn't need MVP changes.
- `ControlListener` dedup mechanism (`_LruSet` over `(run_id, action, ts)`).

## Goals / Non-Goals

**Goals**

1. The worker side actually receives the control messages the API writes.
2. The listener emits status acknowledgement events so the user-visible state converges quickly.
3. Cancel always wins over a concurrently-pending pause (no deadlock where the agent is blocked on `wait_if_paused` and cancel never gets observed).
4. The binding lifecycle stays scoped to "tasks this worker is currently running" — no fan-out broadcast.
5. Restart semantics survive: a worker that crashes mid-run loses its auto-delete control queue; on restart the binding is re-established by the normal claim path. Control sent during the crash window dies in the topic exchange (consistent with the API's documented best-effort posture for pre-claim cancel).

**Non-Goals**

- Multi-task-per-worker concurrent execution. The worker still runs **one task at a time** (single `current_run` on the listener). Changing that is a separate proposal.
- Redis Pub/Sub fast-path activation in MVP — the code path stays but `redis_url` defaults empty.
- Backfill / replay of control messages that were dropped because no worker was bound (the "control_pending sweep" noted in `add-task-control-api` Non-Goals).
- Direct DB writes of `task_runs.status` from the listener — `task_runs.status` is worker-managed but its terminal write is part of the run-handler's existing cleanup, not the control listener.
- A new domain-event vocabulary. We emit existing `kind="status"` events with the standard payload shape.

## Decisions

### D1: Topic-exchange binding lifecycle — dynamic per-task

The control queue is `q.task.control.<worker_id>` (per-process, auto-delete). On `task.control` (topic) it binds **per-task dynamically**:

- When the worker claims a run for task `T`, `await control_queue.bind(task_control_exchange, routing_key=f"task.{T}")` runs **before** the listener's `current_run` is set.
- When the run terminates (any outcome), `await control_queue.unbind(...)` runs in the same defer that clears `current_run`.

Why dynamic rather than `task.*` catch-all: every worker would otherwise receive every control message and filter in code. At scale that's chatty; for MVP it would also obscure the contract ("the broker decides who hears about each task"). The dynamic binding gives us the same property the API's `effective: "best_effort"` discriminator already documents — pre-claim cancel may die, but a claimed run's control message is guaranteed to land in this worker's queue.

**Alternative considered**: `task.*` permanent binding at worker startup. **Rejected** for fan-out reasons above.

### D2: Listener bypass on stale / cross-run messages

The dispatcher compares the message's `run_id` against `self.current_run.run_id`. Mismatch (or `current_run` is None) → log debug + drop. This already exists in the scaffold. Re-asserted here because dynamic binding can briefly outlive the run on the unbind side — `defer unbind` happens after the run handler exits, but a control message published just before the unbind ack lands gets delivered to the now-empty queue. The dispatcher's "no current run / wrong run" branch eats it. Acceptable.

### D3: Payload field — `issued_at`, not `ts`

`_dispatch_payload` reads `payload["issued_at"]` as the third tuple member of the dedup key. Empty / missing `issued_at` → dedup key `(run_id, action, "")` which collapses repeated same-action messages; this only matters in the dual-channel (MQ+Redis) case where the same `issued_at` arrives twice, and an empty value would still correctly dedup *within* that case (both copies share the empty).

The empty-`issued_at` case is unreachable in normal operation — `add-task-control-api` §"Outbox Payload Shape" requires `issued_at` (RFC3339, API process clock); legitimate user-initiated pause/resume/pause sequences therefore carry distinct values and never collapse. The empty-key collapse is defense-only against malformed legacy / future-Redis-path inputs (reviewer S6).

Other new payload fields (`task_id`, `version_id`, `reason`) are simply ignored by the listener today. They're audit info the consumer logs and the agent doesn't need.

### D4: Topology declaration — `task.control` is `TOPIC`

`mq_connection.py::EXPECTED_EXCHANGES` updates the entry for `task.control` from `aio_pika.ExchangeType.DIRECT` to `aio_pika.ExchangeType.TOPIC`. The passive declare in `assert_topology` now matches the actual broker entity (re-declared by the API's `DeclareTopology`). This is a one-time evolution; once both sides are deployed there's no further drift to worry about.

If a worker boots against a broker that **still has the old direct exchange** (because the API hasn't redeployed yet), the passive declare fails with `PRECONDITION_FAILED` and the worker exits. Documented operational sequence: deploy the API first, then the worker.

If the worker boots during the API's atomic retype window (between the `ExchangeDelete` and the subsequent `ExchangeDeclare` from `add-task-control-api`'s `retypableExchanges` mechanism), the passive declare fails with `NOT_FOUND` instead of `PRECONDITION_FAILED` — same fail-fast disposition, different error code. Operators should sequence redeploys (API → wait for API readyz → worker), not parallelize them. Reviewer S8.

### D5: Status acknowledgement events

The listener gains a small `_emit_status_event(ctx, status)` helper that calls the existing `event_publisher.publish_event` with `kind="status"` and `payload={"status": status}`. Emitted:

| action  | status emitted | downstream effect via `task-event-ingest` |
|---------|----------------|--------------------------------------------|
| pause   | `paused`       | `task_versions.status='paused'` + `tasks.status='paused'` |
| resume  | `running`      | `task_versions.status='running'` + `tasks.status='running'` |
| cancel  | `cancelling`   | `task_versions.status='cancelling'`; tasks.status unchanged (`cancelling` is not in the task-status domain) |

The terminal `cancelled` (or `failed`) event is emitted by the agent run's **existing** cleanup path — the run handler catches `asyncio.CancelledError` from `_check_boundary`, runs its checkpoint/cleanup, and writes the final status event. This change adds no new code there.

`seq` for the listener's status events uses `ctx.next_event_seq()` — the same counter the agent loop uses. Acknowledgement events interleave with step events in the natural `seq` order. Emission failures are logged WARN + swallowed (matches the cost-meter's "never break the host"); the agent loop will eventually emit a state-revealing event anyway.

### D6: Cancel-during-pause race

`_check_boundary` reads:

```python
if ctx.cancel_token.is_set():
    raise asyncio.CancelledError
await ctx.pause_token.wait_if_paused()
```

If the agent is currently blocked inside `wait_if_paused()` (i.e., already paused) and a cancel arrives, just setting `cancel_token` doesn't unblock — the agent stays paused forever. Fix: when the listener handles `cancel`, it MUST also call `pause_token.resume()` so any pending waiters wake. Order: `cancel_token.set()` first, then `pause_token.resume()` — that way a racing waiter that wakes between the two calls still finds the cancel token set and the next `_check_boundary` raises immediately.

The resume-call is idempotent (`PauseToken.resume()` clears the paused flag whether set or not), so cancel-when-not-paused is a no-op for the pause-side and stays correct.

### D7: Bind/unbind error handling

`channel.bind` / `channel.unbind` can fail if the broker is in a recovering state. The bind call sits on the run-claim hot path and a failure there is fatal for the claim — log ERROR + reject the run (let it requeue). The unbind sits in a finally / defer and a failure there is **best-effort** (just log WARN); the worst case is the worker keeps receiving stale control messages for an old task, but the dispatcher's "wrong run_id → drop" branch makes that harmless.

### D8: Where the bind/unbind goes in `consumer.py`

`consumer.py` has these pieces in `_execute`:
```
ctx, claim_outcome = await self._claim_or_skip_run(...)   # line ~139 (DB claim)
...
self._control.current_run = ctx                            # line ~210 (listener attach)
...
self._control.current_run = None                           # line ~284 (release, in finally)
```

**Bind happens BEFORE `claim_or_skip_run`** (reviewer S10). If we put the bind AFTER the claim and the bind fails, we nack-and-requeue with a `task_runs` row still marked `running` by this worker; the next worker hits `RUNNING_BY_OTHER_RECENT` (per `worker-messaging`'s idempotent-consumption rule) and also nacks, creating a hot requeue loop until the ~10 s stale-heartbeat takeover kicks in. The fix is structural: bind is a pure MQ operation and idempotent; do it first. If it fails, the DB claim was never attempted, no rollback is needed, the message simply requeues.

The flow becomes:
```
1. await self._control.bind_for(ctx_task_id)               # cheap MQ op; nack-requeue on failure
2. ctx, claim_outcome = await self._claim_or_skip_run(...) # DB claim; preserves existing 4-branch semantics
3. self._control.current_run = ctx                          # listener attach (still after claim — see §"Per-Task Dynamic Binding")
... run executes ...
4. self._control.current_run = None                         # in finally
5. await self._control.unbind_for(ctx_task_id)              # in finally (best-effort; see D-Risks)
```

Subtle but correct: step 1's bind is per-task; if two concurrent messages for the same task_id race two workers, both bind, both run `claim_or_skip_run`, exactly one wins the DB claim. The losing worker has a benign extra binding that lasts until its `finally` runs; any control message during that window fans out to both queues but the loser's dispatcher drops on `current_run.run_id != message.run_id` (or on `current_run is None`) — matches the HA scenario in spec §"Per-Task Dynamic Binding".

The listener exposes a `bind_for(task_id)` / `unbind_for(task_id)` pair that takes the AMQP channel internally so the consumer doesn't need direct exchange access. The control channel is the same channel the listener uses for its consumer iterator — single-channel ownership keeps lifecycle simple.

### D9: Tests

- Unit test for `_dispatch_payload` against the new payload shape (`issued_at`, ignored extras).
- Unit test for the cancel-during-pause race: set the pause token, kick off `await wait_if_paused()` in a task, then dispatch a `cancel` payload → verify the awaiting task returns (resume cleared the wait) and `cancel_token.is_set()`.
- Unit test for status-event emission: stub `EventPublisher`, dispatch each of the three actions, assert the published `(kind, payload)` and the seq increment.
- Integration with a fake AMQP channel that records bind / unbind / publish calls in order; assert the run-claim sequence performs bind before setting `current_run`, and unbind after clearing it.
- The existing `_run_rmq` queue-declare logic moves out; ensure the listener's `run()` is a pure consumer (uses the queue passed in from `lifecycle.py`).

### D10: Metrics

The existing `worker_control_signals_total{action}` counter stays. Add one new label dimension by **outcome**, becoming `worker_control_signals_total{action, outcome}` where `outcome ∈ {handled, unknown_run, parse_error, dedup_drop}`. This lets ops distinguish "we handled a cancel" from "we received a cancel for a run we don't know about" (which is a useful early-warning signal of binding-lifecycle bugs).

`worker_control_emit_failed_total{action}` — counter, bumped when the acknowledgement event emit fails (logged WARN, agent loop unaffected).

When the Redis fast-path is activated in a future change (currently inert per Non-Goals), consider widening to `{action, outcome, source}` where `source ∈ {rmq, redis}` so ops can see which channel arrived first. Cardinality stays small (3×4×2=24 cells). Forward-looking note per reviewer S15.

## Risks / Trade-offs

- **[Risk]** Worker deployed against an old (still-direct) `task.control` exchange dies on startup. → **Mitigation**: documented operational sequence (API first, then worker); the failure is a clean `TopologyError` with the exchange name, not a silent data-loss case.
- **[Risk]** A control message for the unbound-but-not-yet-dispatched window (between the broker accepting the bind and the listener setting `current_run`) gets eaten by the "no current run" branch. → **Mitigation**: this is a sub-millisecond window. If a user actually races the worker that closely, they retry. Documented as MVP-acceptable.
- **[Risk]** The listener now emits status events directly. If a stale `pause` ack races a worker's own newer `status:running` (e.g., user un-paused while the queue was draining), event-ingest's `IS DISTINCT FROM` + terminal-guard CAS still serializes them correctly; the final converged state matches the last logical action. → **Mitigation**: documented; CAS guards make this safe.
- **[Trade-off]** Dynamic bind on every claim costs one AMQP roundtrip per run. At MVP rates (sub-Hz) this is invisible; at scale it might matter. If/when it does, switch to `task.*` catch-all with code-side filter — same semantics, different cost profile. The contract doesn't depend on which mechanism we pick, so this is a Post-MVP perf knob.

## Migration Plan

1. Deploy the API first (`add-task-control-api`; already shipped). After this, the broker's `task.control` is `topic`. **As of this point no worker can boot** against the API-current broker — the pre-change worker's passive `task.control` declare expects `DIRECT` and hits `TopologyError` at startup before consuming any execute message (reviewer S9). This change is therefore on the critical path for unblocking worker deployment, not just for unblocking pause/resume/cancel UX.
2. Deploy the worker side of this change. The worker's `assert_topology` now expects `topic`, the bind happens dynamically per claim, and status events flow.
3. **Rollback**: revert the worker binary only — the API's topic exchange continues to accept publishes that simply route to an empty set of bindings. Control becomes a no-op (the reverted worker can't bind to a topic exchange with the old `control.<worker_id>` routing key + `direct` expectation). The reverted worker also fails to boot, same `TopologyError` as today. Practical posture: this change is forward-only; if you reverted the worker you'd revert the API too.

## Open Questions

(None — D1-D10 cover what was previously deferred.)
