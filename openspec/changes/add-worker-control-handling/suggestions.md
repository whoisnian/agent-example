# Independent Review — `add-worker-control-handling`

Reviewer notes for the lead engineer. The proposal is in good shape; this file lists what I'd tighten up before implementation. Suggestions are ordered roughly by severity (must-fix first, then should-fix, then nice-to-have).

## Lead's verdict (applied after review)

| # | Verdict | Notes |
|---|---|---|
| S1 | accepted (should-fix) | Added "Cross-run message dropped after rebind" scenario under "Control Message Dispatch". |
| S2 | accepted (must-fix) | Added "Resume unblocks before emitting running" scenario asserting `pause_token.is_paused() == False` before `publish_event` is called. |
| S3 | accepted (should-fix) | Strengthened "Cancel unblocks a paused agent" scenario to assert `cancel_token.set()` happens BEFORE `pause_token.resume()` (call-order spy assertion). |
| S4 | accepted (must-fix) | Added explicit task 3.0 to register `control_emit_failed_total` AND widen `control_signals_total` labels in `worker/worker/core/metrics.py` before the listener tries to bump them. |
| S5 | accepted (should-fix) | Added task 7.3 to update `docs/ARCHITECTURE.md` §5.3 control-payload example so it matches the new shape (avoid silently changing the Redis contract). |
| S6 | accepted (nice-to-have) | Added a one-line note to design D3 stating the empty `issued_at` case is unreachable in normal operation; the empty-key collapse is defense-only. |
| S7 | accepted (nice-to-have) | Added a code-comment task on `next_event_seq()` to lock in the "must remain sync" invariant. |
| S8 | accepted (should-fix) | Appended a sentence to design D4 covering the `NOT_FOUND` window during the API's atomic retype. |
| S9 | accepted (should-fix) | Updated proposal "Why" + Migration Plan to call out that **today's worker doesn't boot** against the API-current broker. |
| S10 | accepted (must-fix) | Reordered task 4.1: bind happens BEFORE `claim_or_skip_run`. Bind failure costs nothing (no DB rollback needed). Updated spec scenario accordingly. |
| S11 | accepted (should-fix) | Spec scenario "Unbind failure is best-effort" widened to include in-flight cancellation; task 4.2 says to wrap unbind in `contextlib.suppress`. |
| S12 | accepted (should-fix) | Added task 6.6: test that emit failure logs WARN, bumps the counter, and the token still flips. |
| S13 | rejected | Pre-existing quorum+auto-delete oddity is not new to this change. Leaving it; a separate cleanup proposal can re-examine. |
| S14 | accepted (nice-to-have) | Added "Topic fan-out across HA workers reaches only the run owner" scenario under "Per-Task Dynamic Binding". |
| S15 | accepted (nice-to-have) | Added a one-line forward-looking note to design D10 about adding `source` label when Redis fast-path activates. |

---

## S1 — Spec doesn't cover the "rebound to same task, new run" stale-drop case

**Severity**: should-fix

**Issue**: The dispatcher's run-id filter (D2 + spec "Control Message Dispatch") is essential because dynamic binding only narrows on `task_id`, not `run_id`. The spec's only "stale" scenario covers `current_run is None`; it does not exercise the case where the worker has *already rebound* for a fresh run R2 of the same task T while a queued control for the previous run R1 is still being drained. That's the exact case the run-id check exists for, and it's where the binding-lifecycle bug would silently regress.

**Evidence**:
- `openspec/changes/add-worker-control-handling/specs/worker-control-handling/spec.md` lines 35-38 only cover the `current_run is None` shape:
  > **GIVEN** `listener.current_run` was just cleared (run terminated) but a queued control message for that run is still being drained
- The dispatch rule on line 26-27 enumerates both branches:
  > - `current_run` is `None` (no run currently active), OR
  > - `current_run.run_id != message.run_id`
  but no scenario exercises the second branch.

**Suggested fix**: Add a second scenario under "Control Message Dispatch":

```
#### Scenario: Cross-run message dropped after rebind
- **GIVEN** the worker just finished run R1 of task T (unbound), then claimed run R2 of task T (rebound), so `current_run.run_id = R2`
- **WHEN** a queued control message with `run_id = R1` is drained
- **THEN** no token MUST flip; no status event MUST be emitted; `worker_control_signals_total{action="<x>", outcome="unknown_run"}` MUST increment
```

This pairs neatly with the existing `unknown_run` outcome scenario in "Observability" (line 98-101) so the metric test from task 6.4 doubles as the regression test for it.

---

## S2 — Resume action: spec text and tasks.md disagree on emit-vs-flip ordering

**Severity**: must-fix (contract drift)

**Issue**: Design D5 and the spec say `resume` should "unblock first, then emit `status:running`" (so subsequent step events follow the status flip in `seq` order). The tasks.md item 3.2 follows that order. BUT the spec text in the "Token Flip + Cancel-During-Pause Race" requirement (line 52) phrases it as "the listener MUST clear `ctx.pause_token` then emit `kind=status, payload={"status": "running"}`" — fine for `resume`. The contradiction is more subtle, in the **Status Acknowledgement Events** requirement scenario "pause emits status=paused" (line 77-79) which only constrains pause. There's no scenario for the resume ordering. Walk it carefully: if the listener emits *before* unblocking, an out-of-order race becomes possible where a paused agent's wakeup runs after task-event-ingest already wrote `running`. That ordering matters because the user sees state flip *before* the agent actually resumes producing step events; that's actually fine for the user — what would NOT be fine is the opposite. So the spec phrasing is right; tasks.md item 3.2 is right; but no scenario or test pins it.

**Evidence**:
- Spec line 52: "On `resume`, the listener MUST clear `ctx.pause_token` then emit ... Order: unblock the agent first so subsequent step events follow the status-running event in `seq` order."
- `tasks.md` line 19: "`resume`: `ctx.pause_token.resume()` first, then emit `status=running`"
- No corresponding `#### Scenario:` exercises the order assertion.

**Suggested fix**: Add a resume-ordering scenario to "Token Flip + Cancel-During-Pause Race" (or to "Status Acknowledgement Events") so test 6.3 can assert it:

```
#### Scenario: Resume unblocks before emitting running
- **GIVEN** the agent is awaiting `pause_token.wait_if_paused()` (paused) and `ctx.next_event_seq()` is currently N
- **WHEN** the listener receives `resume`
- **THEN** `pause_token.is_paused()` MUST be false BEFORE `event_publisher.publish_event` is called; the published event MUST carry `seq = N+1`; any agent step event emitted afterward MUST carry `seq >= N+2`
```

The seq monotonicity falls out of the existing publisher contract, but pinning it in the spec scenario protects against a future refactor reversing the order "for symmetry" with `pause`.

---

## S3 — Cancel-during-pause race: walk the asyncio mechanics in spec, not just design

**Severity**: should-fix

**Issue**: Design D6 walks the race carefully ("set cancel BEFORE calling resume so a racing waker can never observe an unblocked-but-still-not-cancelled state") but the spec's scenario "Cancel unblocks a paused agent" (line 54-57) only asserts the post-condition (`wait_if_paused() returns` AND `cancel_token.is_set()`). It does NOT assert the *order* in which the listener performed the two operations. A future refactor that reverses the order would still pass that scenario, because both calls have completed before the assertion runs.

The order matters specifically because: after `resume()` clears `_paused` and sets `_resumed`, the awaiting coroutine is *scheduled* to wake on the next event-loop tick. If `cancel_token.set()` happens AFTER `resume()`, in principle the same loop iteration could schedule both — but `asyncio.Event.set()` is synchronous, so by the time the listener's await yields, both flags have flipped. In practice this race only opens up if a third coroutine slices in between the two `set()` calls. That's unlikely in this code, but the spec should fence the order rather than rely on "in practice".

**Evidence**:
- `worker/worker/core/run_context.py` lines 57-70 — the `PauseToken.resume()` clears `_paused` and sets `_resumed`; the waker await is `await self._resumed.wait()` and returns to `_check_boundary` (loop.py 309-311), which reads cancel_token first.
- Spec line 48 already names the order: "Order: set cancel BEFORE calling resume so a racing waker can never observe an unblocked-but-still-not-cancelled state."

**Suggested fix**: Strengthen the scenario to assert order:

```
#### Scenario: Cancel sets the cancel token BEFORE unblocking the pause
- **WHEN** the listener handles `cancel` for a paused run
- **THEN** `cancel_token.is_set()` MUST return true BEFORE `pause_token.resume()` is called (verifiable by spying `cancel_token.set` and `pause_token.resume` in order)
```

Task 6.2 already partially covers this; just extend the assertion to record the call order, not only the final state.

---

## S4 — `worker_control_emit_failed_total` is never registered in `metrics.py`

**Severity**: must-fix

**Issue**: Spec line 94 and design D10 add a new counter `worker_control_emit_failed_total{action}`. Task 3.1 says "Bump the new `worker_control_emit_failed_total{action}` counter on failure" — "bump", not "register". `worker/worker/core/metrics.py` doesn't have a field for it on the `Metrics` dataclass, nor a `Counter(...)` declaration in `build_metrics()`. As written, the listener can't bump a counter that doesn't exist.

**Evidence**:
- `worker/worker/core/metrics.py` lines 22-49: `Metrics` dataclass currently has `control_signals_total: Counter` only, no `control_emit_failed_total`.
- `worker/worker/core/metrics.py` line 113-118: only `control_signals_total` is registered.
- `tasks.md` line 16: bump only; no register step.

**Suggested fix**: Add an explicit subtask:

```
- [ ] 3.0 Register two new/changed metrics in `worker/worker/core/metrics.py`:
  - Replace `control_signals_total` to add the `outcome` label dimension (labelnames=("action","outcome")).
  - Add `control_emit_failed_total = Counter("worker_control_emit_failed_total", ..., labelnames=("action",))`.
  Update the `Metrics` dataclass field list accordingly.
```

While there: task 2.4 says the metric is widened but also doesn't reference `metrics.py`. Roll the widening into 3.0 so the metric surface is updated in one place.

---

## S5 — `_run_redis` is left in place but its payload contract isn't reconciled

**Severity**: should-fix

**Issue**: Design (Non-Goals + "Worker code that's correct and stays as-is") says `_run_redis` doesn't need MVP changes because `redis_url` defaults empty. But the listener's `_dispatch_payload` is shared by both `_run_rmq` and `_run_redis`; the change rewrites the payload contract from `ts` → `issued_at`. If Redis is ever turned on (locally, in a dev environment, or by a future change), the listener will now expect `issued_at` from the Redis stream too. The architecture doc (`docs/ARCHITECTURE.md` line 650) documents the dual-channel payload as `{task_id, run_id, action, ts}` — that's the OLD shape. If we're saying "both channels carry the same payload" (spec-modified `worker-messaging` line 7), then we've quietly changed the Redis contract too without anyone publishing to it.

Two consistent options: (a) Document that the Redis fast-path now expects `issued_at` and update architecture-doc line 650 in the same change. (b) Have `_dispatch_payload` accept both keys for backward compat (`payload.get("issued_at") or payload.get("ts", "")`).

**Evidence**:
- `worker/worker/core/control.py` lines 97-123: `_run_redis` calls `_dispatch_payload(body, source="redis")`.
- `docs/ARCHITECTURE.md` line 650: `{ "task_id": "...", "run_id": "...", "action": "pause|resume|cancel", "ts": "..." }` — needs update.
- Spec (modified worker-messaging) line 7: "Both channels carry the same payload shape `{task_id, version_id?, run_id?, action, reason, issued_at}`" — implicitly mandates the Redis shape change.

**Suggested fix**: Pick one. I'd recommend option (a) plus an explicit task entry to keep ARCHITECTURE.md aligned (task 7.2 touches the same file but only for binding lifecycle):

```
- [ ] 7.3 Update `docs/ARCHITECTURE.md` §5.3 line 650's control-payload example from `{..., ts}` to `{task_id, version_id?, run_id?, action, reason, issued_at}`, mirroring the API's `add-task-control-api` outbox shape.
```

---

## S6 — Empty `issued_at` collapses the dedup key across distinct messages

**Severity**: should-fix

**Issue**: Design D3 acknowledges that `issued_at` empty → dedup key `(run_id, action, "")`. The defense-in-depth this provides "within a dual-channel duplicate case" is fine. But the API spec (`task-control-api` line 106) guarantees `issued_at` is always present in the outbox payload — it's "RFC3339 timestamp, API process clock". So in normal operation `issued_at` will never be empty for messages originating from the API. Empty `issued_at` is reachable only from: (a) the Redis fast-path with an old payload shape, (b) malformed inputs.

That means the LRU's "collapse repeated same-action messages within an empty-key window" is a behavior protecting against a path that the API contract says will never happen. That's fine for defense-in-depth — but as a side effect, if a user pauses, resumes, then pauses again for the *same* run (rare but legal — see `task-control-api`'s "Duplicate accepted controls both produce outbox rows" scenario which only constrains pause-pause; the equivalent pause-resume-pause is implicit), and the dual-channel ever delivers them both with empty `issued_at` (because the legacy Redis path got reactivated), the second pause would be deduped as a duplicate. Worth a one-line note in design D3 that this is "by-design and acceptable because the API's contract forbids the empty case in normal operation".

**Evidence**:
- `openspec/specs/task-control-api/spec.md` line 106: `issued_at` is required, RFC3339.
- design D3 paragraph 1.

**Suggested fix**: Add to design D3: "Empty `issued_at` is unreachable in normal operation because the API contract requires it; the empty-key collapse is a defense for malformed legacy inputs only and won't drop legitimate user-initiated pause/resume/pause sequences."

---

## S7 — `next_event_seq()` concurrent-mutation note is technically correct but worth fencing in spec

**Severity**: nice-to-have

**Issue**: Design D5 says "Acknowledgement events interleave with step events in the natural seq order." `RunContext.next_event_seq` does `self.event_seq += 1; return self.event_seq` — not atomic at the bytecode level, but Python+asyncio is single-threaded so as long as no `await` is interleaved between the read and the write, increment is uninterruptible. Looking at the code, that's true: `next_event_seq` has no awaits, so it's safe even if the listener and the agent loop both call it from different coroutines. Worth noting explicitly because a future refactor that adds an `await` (e.g., to persist seq) would silently break monotonicity.

**Evidence**:
- `worker/worker/core/run_context.py` lines 112-114: `next_event_seq` is sync, no awaits, safe under cooperative multitasking.

**Suggested fix**: Optionally add a one-line comment to `run_context.py::next_event_seq` saying "must remain sync (no awaits) — concurrent callers from the listener and the agent loop rely on cooperative atomicity". This is a code change, not a spec change; the design note is fine as-is.

---

## S8 — Migration plan should call out the topology-evolution window during API redeploy

**Severity**: should-fix

**Issue**: Design D4 documents "deploy API first, then worker". The API's `api-messaging` spec line 33-35 says the retypable-exchanges mechanism `ExchangeDelete` then `ExchangeDeclare` — a sub-second window where `task.control` does not exist at all. A worker that boots in *that* window also hits TopologyError (missing exchange, not wrong-type). The current design only mentions the wrong-type failure mode. Both are clean fail-fast TopologyErrors with sensible logs, so it's not a correctness issue, but it's worth one sentence so operators know "restart the worker after the API finishes coming up, not concurrent with the redeploy".

**Evidence**:
- `openspec/specs/api-messaging/spec.md` lines 20, 32-35: pre-delete-then-redeclare semantics.
- `openspec/changes/add-worker-control-handling/design.md` D4 lines 66-70: only addresses the "still direct" case, not the "briefly missing" case.

**Suggested fix**: Append to design D4:

> If the worker boots during the API's atomic retype window (between the `ExchangeDelete` and the subsequent `ExchangeDeclare`), the passive declare fails with `NOT_FOUND` instead of `PRECONDITION_FAILED`. Same fail-fast disposition; operators should sequence redeploys (API → wait for API ready → worker), not parallelize them.

---

## S9 — Today's production worker is broken until this change ships — call it out

**Severity**: should-fix (operational visibility)

**Issue**: `add-task-control-api` shipped (committed `ae98001`) and retyped `task.control` to topic. The pre-change worker still expects `direct` (verified in `worker/worker/core/mq_connection.py` line 34). That means any worker currently booting against a fresh broker that includes the API's `DeclareTopology` will fail at startup. This change fixes it, but the proposal's "Why" section says "would fail to receive any control message after the API redeploys" — that understates it; the worker won't even start. The migration plan paragraph (design D4 / Migration Plan §1) similarly downplays it.

**Evidence**:
- `worker/worker/core/mq_connection.py` line 34: `ExpectedExchange("task.control", aio_pika.ExchangeType.DIRECT),` — current main-branch worker.
- `git log --oneline` shows `add-task-control-api` was archived already; the API code is in.

**Suggested fix**: Update proposal "Why" first paragraph:

> ... and would fail **at startup** (not merely "fail to receive control messages") — the passive `task.control` declare expects DIRECT, the actual exchange is now TOPIC, so the worker hits `TopologyError` before it ever consumes an execute message.

And add to Migration Plan §1: "As of this change, **no worker can boot** against the API-current broker; this change is therefore on the critical path for unblocking worker deployment, not just for unblocking pause/resume/cancel UX."

---

## S10 — Bind-failure path needs to release the claimed `task_runs` row

**Severity**: must-fix (correctness)

**Issue**: Tasks 4.1 says: "Wrap in a try/except that, on bind failure, logs ERROR and nacks the execute message so it requeues for another worker." But by the time `_execute` reaches the `self._control.current_run = ctx` line (consumer.py line 210), it has *already* claimed the run via `claim_or_skip_run` (line 139). That claim inserted/updated a `task_runs` row to `status='running'` with `worker_run_id=<self>`. If we nack-and-requeue without rolling that back, the next worker to pick the message sees `status='running'` with a recent heartbeat (this very same worker's stale claim) and hits the `RUNNING_BY_OTHER_RECENT` branch, also nack-requeues, and we have a hot loop until the heartbeat times out (~ 2× heartbeat interval).

The bind has to either: (a) happen *before* the claim, so a bind failure means we never claim; or (b) on bind failure we rollback the claim (mark the run back to `queued` / `failed`, or update `worker_run_id` so the takeover path picks it up).

**Evidence**:
- `worker/worker/core/consumer.py` lines 139, 210: claim happens at 139, listener attachment at 210, no rollback path between them on bind failure.
- `openspec/specs/worker-messaging/spec.md` lines 22-26 (Idempotent Consumption): the takeover behavior depends on stale heartbeat, which takes ≥ 2× heartbeat interval (default 10s) to kick in.

**Suggested fix**: Reorder the consumer wiring (preferred) — move `bind_for(task_id)` before `claim_or_skip_run`. The bind is purely an MQ operation; the claim is a DB operation; doing the cheaper, idempotent MQ-side first means a bind failure costs nothing. Update tasks 4.1 and the spec scenario "Bind precedes current_run assignment" to either reflect the new order or explicitly cover the rollback. Recommended task wording:

```
- [ ] 4.1 In `consumer.py::_process`, call `await self._control.bind_for(ctx.task_id)` BEFORE `claim_or_skip_run`. Bind failure: log ERROR, nack(requeue=True), increment a metric, return. No DB rollback needed because the claim never happened.
```

If the reviewer disagrees and wants to keep bind-after-claim, the must-fix becomes "add a rollback path that marks `task_runs.status='queued'` (or sets `worker_run_id=null`) on bind failure before the nack". Either way, the current tasks.md flow as written produces a hot requeue loop.

---

## S11 — Unbind is documented to run AFTER `current_run` clears, but consumer.py defer pattern doesn't yet exist

**Severity**: should-fix (implementation hazard)

**Issue**: Spec line 15 + task 4.2 say unbind goes in the same defer/finally that clears `current_run`. Looking at consumer.py, `current_run = None` is at line 284 inside a `finally` block of `_execute`. Putting `await unbind_for` in that `finally` is fine — but `unbind_for` is async and the `finally` block currently has no `await` calls. Inserting one introduces a new failure mode: if cancellation propagates *through* the `finally` (e.g., the consumer task itself is being shut down via `consumer_task.cancel()` from lifecycle.py line 156), the `await self._control.unbind_for(ctx.task_id)` can be interrupted before the broker confirms. That's the design's "best-effort" disposition — but spec scenario "Unbind failure is best-effort" only mentions broker rejection, not in-flight cancellation.

**Evidence**:
- `worker/worker/core/lifecycle.py` line 156: `consumer_task.cancel()` on drain timeout.
- `worker/worker/core/consumer.py` line 283-284: bare `finally` block.

**Suggested fix**: Either (a) widen the "best-effort" scenario to include the cancellation-during-unbind case, or (b) wrap the unbind in `with contextlib.suppress(asyncio.CancelledError, Exception):` so the cancellation propagates after the unbind attempt is abandoned. (a) is cheaper:

```
#### Scenario: Unbind failure or interruption is best-effort
- **WHEN** the broker rejects `unbind` OR the unbind await is cancelled (drain timeout)
- **THEN** the failure/cancellation MUST log at WARN and the run handler MUST NOT re-raise — the queue auto-deletes on disconnect anyway
```

Task 4.2 should also explicitly say "wrap the unbind in `contextlib.suppress(BaseException)` or equivalent to honor the best-effort disposition under cancellation".

---

## S12 — Tests don't cover the "emit failed but token still flips" path

**Severity**: should-fix

**Issue**: Spec line 86-88 says emission failure logs WARN, bumps `worker_control_emit_failed_total`, AND continues to flip the in-memory token. Tasks 6.1-6.5 don't list a test that exercises the emission-failure → token-still-flips path. That's the exact invariant the operator cares about during an MQ blip — the user-visible status event might be lost but the agent must still react to the cancel. Without a test, a future refactor that wraps emit+flip in a shared `try` and aborts the whole sequence on emit failure would silently break this invariant.

**Evidence**:
- `tasks.md` lines 32-38: tests 6.1-6.5 enumerated; none stub `publish_event` to raise.
- Spec line 87-88: the invariant is asserted but not tested.

**Suggested fix**: Add task 6.6:

```
- [ ] 6.6 Add a test for the emit-failure-still-flips invariant: stub `EventPublisher.publish_event` to raise `RuntimeError("mq down")`, dispatch a `cancel`, assert `ctx.cancel_token.is_set()` is true AND `worker_control_emit_failed_total{action="cancel"}` incremented AND the listener logged at WARN.
```

---

## S13 — Existing `_run_rmq` declares the queue with `quorum`; new flow uses lifecycle's queue — confirm arg parity

**Severity**: nice-to-have

**Issue**: The listener's current `_run_rmq` declares `q.task.control.<worker_id>` with `arguments={"x-queue-type": "quorum"}` (control.py line 89). After the change, the queue comes from `declare_worker_queues`, which also passes `"x-queue-type": "quorum"` (mq_connection.py line 193). Good. But the modified `worker-messaging` spec (line 25-26) says the control queue is `auto-delete on worker disconnect` — `auto_delete=True` is set in both places; quorum queues with auto-delete are slightly unusual (quorum is typically durable-cluster-replicated; auto-delete on disconnect contradicts that durability intent). Worth a quick spec-vs-implementation reconciliation: do we really want `quorum` *and* `auto-delete`, or should the worker control queue be the simpler classic-with-auto-delete (matching its actually-ephemeral semantic)?

**Evidence**:
- `worker/worker/core/mq_connection.py` line 189-195: declared with both `auto_delete=True` AND `"x-queue-type": "quorum"`.
- `openspec/specs/worker-messaging/spec.md` line 80 (pre-change): `q.task.control.<worker_id>` declared as `quorum` but `auto-delete on worker disconnect`.

**Suggested fix**: This is an inherited oddity, not new to this change — but since the change is touching the queue declaration, it's a good moment to ask. Either change to classic auto-delete (probably what was meant; quorum's value is HA durability which is meaningless for an ephemeral per-worker queue) or document explicitly why both. If we want to keep it as-is for this change and address separately, leave a one-line note in `design.md` under a new "Open Questions" or "Deferred" item.

---

## S14 — Open Questions claims "None" but operational sequencing under HA workers is non-obvious

**Severity**: nice-to-have

**Issue**: Design line 145 says open questions are "None — D1-D10 cover what was previously deferred." Walking the design once more: what happens when multiple workers run in parallel (HA, scale-out)? Each worker has its own `q.task.control.<W>` queue. The API publishes to `task.control` with routing key `task.<T>`. Only the worker that has *bound* `task.<T>` receives the message. If two workers attempted to claim the same `task_runs` row (idempotency_key collision) and one took over via the stale-heartbeat path, both have technically bound `task.<T>` for distinct runs (since `worker_run_id` differs). The topic exchange will fan the control message out to BOTH queues. The losing worker's dispatcher checks `current_run.run_id` vs message.run_id and drops — by design. But this should be in the spec under "Goals" or as a scenario: "Multi-worker HA: control fans out to all bound workers; only the active run-owner reacts."

**Evidence**:
- Design "Non-Goals" line 37: "Multi-task-per-worker concurrent execution. The worker still runs **one task at a time**." This is a goal-level constraint; the multi-worker fan-out case is separate and not addressed.
- spec "Per-Task Dynamic Binding" doesn't enumerate the HA case.

**Suggested fix**: Add a scenario under "Per-Task Dynamic Binding":

```
#### Scenario: Topic fan-out across HA workers reaches only the run owner
- **GIVEN** two workers W1 and W2 are both running, and both have transient `task.T` bindings (e.g., W2 took over a stale-heartbeat run from W1 → both still bound until W1's `finally` runs)
- **WHEN** a control message for task T arrives at both W1's and W2's control queues
- **THEN** both dispatchers receive it; W1's `current_run.run_id` no longer matches → drops + `outcome="unknown_run"`; W2's matches → handles + `outcome="handled"`
```

This is mostly a documentation improvement, not a behavior change — the dispatcher already does the right thing.

---

## S15 — `_dispatch_payload` source label gets lost in the new metric

**Severity**: nice-to-have

**Issue**: Current `_dispatch_payload` accepts a `source: "rmq" | "redis"` keyword (control.py line 95/120/125) and logs it on parse failures. The new metric `worker_control_signals_total{action, outcome}` drops that dimension. If the Redis fast-path is ever turned on, ops loses the ability to distinguish "we got it from Redis first" vs "RMQ first" — useful for tuning the dual-channel race window. Cardinality cost is +1 dimension × 2 values, so 16 → 32 cells total. Reasonable.

**Evidence**:
- `worker/worker/core/control.py` lines 95, 120: source is already tracked.
- Spec line 93: `{action, outcome}` — no source.

**Suggested fix**: Defer to Post-MVP since Redis is inert. But worth one line in design D10:

> If the Redis fast-path is activated in a future change, consider widening to `{action, outcome, source}` (cardinality stays small: 3×4×2=24 cells).

Treat as a forward-looking note, not a blocking change.

---

## Summary

**Overall assessment**: The proposal is implementable as written. The wire contract, design decisions (D1-D10), and task list are coherent and trace cleanly back to the API-side `add-task-control-api` archive. The Goals/Non-Goals scoping is appropriately MVP-focused. Test coverage (6.1-6.5) is roughly right but has a couple of holes I'd close before declaring done.

**Top 3 must-fix items**:
1. **S10** — Bind-after-claim ordering creates a hot requeue loop on bind failure. Move bind before claim (or add a claim-rollback path).
2. **S4** — `worker_control_emit_failed_total` is referenced in spec + design but never registered in `metrics.py`. Add a task subitem for the metric registration so the listener can actually bump it.
3. **S2** — Resume action's "unblock first, then emit" ordering is correct in design/tasks but not pinned by any spec scenario. A test-discoverable scenario protects against future refactors.

**Other notable items**: S9 (today's worker is broken on main, worth explicit migration-plan language), S5 (Redis path payload contract is implicitly changed; reconcile architecture doc), S1 (rebind-cross-run dispatch case lacks a spec scenario).

No correctness bugs hide in the design itself — D6 (cancel-during-pause race) and D5 (seq monotonicity) hold up under careful walkthrough; the must-fixes are about (a) one ordering choice in the consumer wiring that produces an operational hot-loop and (b) two follow-through items where the spec/design name a thing the implementation tasks then fail to fully wire up.
