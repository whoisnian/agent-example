# Independent Review — `add-cost-service`

Reviewer notes from cross-checking proposal/design/specs/tasks against the existing code surface (worker publisher + cost meter, API messaging, migrations) and the sibling `task-event-ingest` consumer it intends to mirror.

## Lead's verdict (applied after review)

| # | Verdict | Notes |
|---|---|---|
| S1 | accepted (must-fix) | Added migration `0004_cost_events_kind_unique`; renumbered seed to `0005`; MODIFIED `task-cost-data-model` cost-events uniqueness requirement; updated D6 + all spec refs to `(run_id, kind, seq)`. |
| S2 | accepted (must-fix) | Added "Aggregate Increment Mapping per Kind" requirement; settler binds NULL→0 per the table. |
| S3 | accepted (must-fix) | Down migration now uses `DELETE ... WHERE id IN (...) AND NOT EXISTS (... cost_events ...)`; Migration Plan + Task 1.3 updated. |
| S4 | accepted (should-fix) | Added startup pricing-coverage INFO log; README task captures the alert recommendation. |
| S5 | accepted (should-fix) | Metric names dropped `api_` prefix to match `events_ingested_total` style. |
| S6 | accepted (should-fix) | Introduced per-queue `CostConsumerConnected` gauge + task to plumb it through a Consumer constructor variant. |
| S7 | accepted (should-fix) | "FK violation routes to DLQ" scenario now names the `task_costs` UPSERT as the source. |
| S8 | accepted (should-fix) | Added unit-test + integration-test scenarios for sub-second compute durations; design note added. |
| **S9** | **rejected** | The codebase's worker targets Python `>=3.14,<3.15` (`worker/pyproject.toml`). On 3.14.5 the construct `except TypeError, ValueError:` is parsed as `except (TypeError, ValueError):` (verified via `ast.parse` → `ExceptHandler(type=Tuple(elts=[...]))`) and `compile()` accepts it without warning. So it is **not** a SyntaxError in this codebase. Style aside (parenthesised form is clearer), no functional defect — out of scope for this change. |
| S10 | accepted (should-fix) | Added "Pricing windows abut exactly at occurred_at" scenario. |
| S11 | accepted (should-fix) | Took option 2 — kept Haiku in seed but reworded requirement to acknowledge it is anticipatory beyond current worker defaults. |
| S12 | accepted (should-fix) | Added requirement that `task_costs.task_id` is immutable per `version_id`; UPSERT MUST NOT assign `task_id`; settler verifies via `task_versions` lookup, mismatch → DLQ. |
| S13 | accepted (nice-to-have) | Added Risks note that today's queues lack a broker-side delivery-attempt cap; tracked as a separate Post-MVP hardening change. |
| S14 | accepted (nice-to-have) | Moved former Open Question #2 (FK-violation → DLQ) into Decisions as a sub-bullet of D7. |
| S15 | accepted (should-fix) | Restructured Task 7: 7.x handler unit tests with `fakeAck` + fake settler; 7.y integration with `postgres:18.4-alpine` only (no broker container). |

---

## S1. (must-fix) Worker `seq` is per-run-per-kind but the DB unique key is `(run_id, seq)` — duplicate-collision hazard

**Issue.** The worker's `CostEventPublisher` allocates `seq` from a per-`(run_id, kind)` counter, so `cost.llm seq=1`, `cost.tool seq=1`, and `cost.compute seq=1` can all be emitted for the same `run_id`. The DB-side uniqueness is `cost_events_run_seq_key` on `(run_id, seq)` *without* `kind`. The first event wins; every other kind's `seq=1` lands as `ON CONFLICT DO NOTHING` → silently dropped, no cost recorded, no warning.

**Evidence.**
- `worker/worker/core/run_context.py:116-119` — `next_cost_seq` keys on `(run_id, kind)`:
  ```python
  def next_cost_seq(self, kind: str) -> int:
      cur = self.cost_seq_by_kind.get(kind, 0) + 1
      self.cost_seq_by_kind[kind] = cur
      return cur
  ```
- `worker/worker/core/publisher.py:174` — message-level idempotency key is `f"{run_id}:{kind}:{seq}"` (kind-aware), but the body's `seq` field is the per-kind value.
- `openspec/specs/worker-messaging/spec.md` §"Cost Event Publisher": *"Cost events use a separate `seq` namespace from task events (per-run-per-kind monotonic)"*.
- `api/migrations/0003_init_cost_domain.up.sql:56` — `CREATE UNIQUE INDEX cost_events_run_seq_key ON cost_events (run_id, seq);` (no `kind` column).
- D6 in `design.md`: `INSERT INTO cost_events ... ON CONFLICT (run_id, seq) DO NOTHING` — uses the existing index, so duplicate-on-kind events become silent no-ops.

**Suggested fix.** Pick one and bind it in the spec:
1. **Recommended:** Add `kind` to the uniqueness boundary. New requirement in `task-cost-data-model` (or a small migration `0004_cost_events_run_kind_seq.up.sql` that drops the index and re-creates `UNIQUE (run_id, kind, seq)`). Then D6's `ON CONFLICT` clause becomes `(run_id, kind, seq)`. This is the smallest change that matches the publisher contract.
2. Alternative: change the worker to a single per-run cost seq (no kind partition). Cheaper for the DB but rewrites worker spec and meter.

Either way, every "Idempotent Settlement Per (run_id, seq)" requirement in `task-cost-ingest/spec.md` (currently four references) needs to be re-keyed, the `InsertCostEvent :execrows` SQL needs to update its ON CONFLICT clause, and Tasks 1.x / 2.2 / 3.3 / 7.2 should reflect the new triple.

This is the single most consequential issue in the proposal.

---

## S2. (must-fix) `task_costs` UPSERT will incorrectly aggregate cross-kind columns

**Issue.** D6 lists a single UPSERT template that adds every column unconditionally (`tool_calls = task_costs.tool_calls + EXCLUDED.tool_calls`, `compute_seconds = ... + EXCLUDED.compute_seconds`, `wall_time_ms = ... + EXCLUDED.wall_time_ms`, etc.). Open Question #1 then "decides" that `compute_seconds` only accumulates from compute events and `wall_time_ms` accumulates from llm/tool. But the SQL template can't enforce that: it adds whatever `EXCLUDED.col` the caller binds, and the spec does not say the caller MUST zero out `compute_seconds` for non-compute kinds (or zero `wall_time_ms` for `compute`). Worse, `tool_calls` adds `EXCLUDED.tool_calls` for every event — for an `llm` event the worker emits `calls=NULL`, which is fine if mapped to 0, but no requirement says so.

**Evidence.**
- `design.md` lines 100-114 — single UPSERT, all columns add `EXCLUDED.col`.
- `design.md` line 118 — "compute_seconds aggregate increment is `(duration_ms / 1000)` truncated to integer" with no kind gate.
- `design.md` line 197 (Open Questions) — Q1 *declares* a decision but the spec doesn't bind it.
- `worker/worker/core/cost_meter.py:130-144` — `_emit` (llm) passes no `calls`; `emit_tool` passes `calls=1`. So `EXCLUDED.calls=NULL` for llm events; the spec needs to say "treat NULL as 0".

**Suggested fix.** In `task-cost-ingest/spec.md`, add a new requirement "Aggregate Increment Mapping" that pins, per-kind, what each EXCLUDED column resolves to:

| kind | tool_calls | wall_time_ms | compute_seconds |
|------|-----------|--------------|-----------------|
| llm  | 0         | duration_ms  | 0               |
| tool | calls (NULL→1 if a per_call price matched, else NULL→0) | duration_ms | 0 |
| compute | 0      | 0            | duration_ms / 1000 (floor) |

…plus token columns null-safe to 0. Then D6's SQL becomes a single statement parameterised on those resolved integers, and the test from Task 3.4 / 7.2 must cover one `compute` event to lock the column gating in.

(The `tool` row also clarifies an ambiguity: if the worker sends `calls=1` but only the `per_second` price matched, do we still count the call? Pick one — I'd vote yes, since `task_costs.tool_calls` is a *count of tool invocations*, independent of pricing existence.)

---

## S3. (must-fix) Seed `down.sql` will fail FK 23503 once any cost_events reference it

**Issue.** The seed migration's `.down.sql` deletes pricing rows by hard-coded UUID. `cost_events.pricing_id` is an FK to `pricing(id)`, and `task-cost-data-model` already pins that "we deliberately do not cascade — historical cost records must stay attributable". So after even one settled event, `0004_seed_pricing.down.sql` raises SQLSTATE 23503 and the migration leaves `schema_migrations.dirty=true`. The integration test in Task 1.3 (round-trip up→down→up) will pass on a fresh DB but break in any environment that has actually consumed an event.

**Evidence.**
- `api/migrations/0003_init_cost_domain.up.sql:51` — `pricing_id UUID REFERENCES pricing(id)` (no `ON DELETE`, so default RESTRICT).
- `openspec/specs/task-cost-data-model/spec.md` lines 36-38 — "Pricing rows are protected from delete while referenced" is a binding scenario.
- `design.md` Migration Plan step 4 — claims rollback is safe; this is wrong in any environment with traffic.

**Suggested fix.** Two acceptable resolutions:
1. **Recommended:** Make the down migration tolerant — `DELETE FROM pricing WHERE id IN (...) AND NOT EXISTS (SELECT 1 FROM cost_events WHERE cost_events.pricing_id = pricing.id)`. Documents the asymmetry honestly: seed disappears in dev, persists in any env where it was used. Update Task 1.2 wording and Migration-Plan step 4 to say "seed rows that have been referenced by cost_events remain — that's by design, immutable history".
2. Alternatively: bind a spec requirement that seed pricing rows are *not* removable once referenced, and have Task 1.3 assert that path explicitly.

Either way, the Migration Plan step 4 + Task 1.3 scenario must be revised. Right now they over-promise round-trippability.

---

## S4. (must-fix) The "missing pricing → ack" rule lets a typo silently produce $0 forever

**Issue.** D5 + the "Missing Pricing Is Non-Fatal" requirement instruct the consumer to ack and bump a counter when pricing is absent. That's defensible for genuinely unknown models, but it makes a `resource_name` *typo* (e.g. worker passes `"claude-opus-4-7 "` with a trailing space, or `"claude-opus47"`) indistinguishable from "expected unknown model". The only signal is `api_cost_pricing_missing_total{resource="..."}`, which nobody alerts on by default. The risk is called out in design.md but the mitigation ("`last_priced_at` UI in `add-task-cost-api`") is forward-looking and not part of this change.

**Evidence.** `design.md` Risks/Trade-offs §1 acknowledges it; the spec encodes the silent ack as a MUST.

**Suggested fix.** Add a low-cost guardrail now:
- Add a startup-time pricing-coverage check: on consumer start, log INFO listing the distinct `(resource_kind, resource_name)` for every active row in `pricing`. (No external dependency; just one query.)
- Add an alerting hint in the spec / design: `api_cost_pricing_missing_total` is the operational signal — recommend an alert rule shipped in `docs/` or the README in Task 8.1, e.g. "alert when `increase(api_cost_pricing_missing_total[10m]) > 5`".

Neither change requires moving any decision. Both make the "silent zero" failure mode *operationally* visible without changing the consumer's correctness contract.

---

## S5. (should-fix) Metric naming diverges from existing convention (no `api_` prefix in this codebase)

**Issue.** Every cost metric in proposal/design/spec is named `api_cost_*`. The existing metrics in `observability/metrics.go` have no `api_` prefix: `events_ingested_total`, `event_status_transitions_total`, `event_ingest_malformed_total`, `mq_publish_duration_seconds`, etc. Mixing prefixes makes Grafana dashboards harder and contradicts AGENTS.md §7 ("可观测性... 每新增一条状态翻转或外部调用，至少同步加一个 metric/log 字段" — implicitly, follow the existing style).

**Evidence.**
- `api/internal/infrastructure/observability/metrics.go:111-128` — sibling consumer metrics use `events_ingested_total` style.
- `proposal.md:12`, `design.md:165-172`, `task-cost-ingest/spec.md:117-122` — all prefix with `api_`.

**Suggested fix.** Rename throughout to match siblings: `cost_events_consumed_total`, `cost_events_settled_total`, `cost_pricing_missing_total`, `cost_amount_settled_usd_total`, `cost_event_settle_duration_seconds`. Update Task 5.1 field names accordingly. One-line edit per file.

---

## S6. (should-fix) `EventConsumerConnected` gauge is shared by the cost consumer — gauges will flap

**Issue.** `messaging.Consumer.setConnected` writes to `metrics.EventConsumerConnected` unconditionally. If you wire a second `Consumer` for the cost queue (Task 6.1), both consumers update the same gauge, and the value becomes "1 if at least one of them is subscribed", masking outages. The proposal doesn't note this.

**Evidence.** `api/internal/infrastructure/messaging/consumer.go:156-166`.

**Suggested fix.** Plumb a per-consumer gauge through `NewConsumer`. Two ways:
1. Pass a `prometheus.Gauge` directly (or a setter) into `NewConsumer` so each consumer is associated with its own gauge; add `cost_consumer_connected` to `Metrics`.
2. Refactor `Consumer` to take a `string queueLabel` and use a `GaugeVec` labelled by queue.

Cleaner is option 1 for now (only two consumers). Add an explicit task under §5 Observability for "make the consumer-connected gauge per-queue" or "introduce `CostConsumerConnected` gauge and wire it through a new Consumer constructor variant". Without this, the `event_consumer_connected` gauge will lie.

---

## S7. (should-fix) Spec/code mismatch: `cost_events.version_id` has no FK, so "FK violation → DLQ" applies only to `task_costs`

**Issue.** Design D7 lists "Permanent DB error (CHECK / FK violation / NOT NULL) → DLQ" and Open Question #2 / a spec scenario specifically wire SQLSTATE 23503 to DLQ for "event references a `version_id` that does not exist". But `cost_events` has no FK on `version_id` — only `task_costs.version_id → task_versions(id)` does. So the FK violation actually fires inside the UPSERT, not the cost_events insert; the cost_events row commits (well, would, except the tx aborts). It's worth being precise so the integration test in Task 7.2 doesn't assert on the wrong statement.

**Evidence.** `api/migrations/0003_init_cost_domain.up.sql:37-58` — `cost_events` has no FK declarations beyond `pricing_id`. `task_costs.version_id` (line 65) is the FK.

**Suggested fix.** In the spec scenario "FK violation routes to DLQ" (line 99-102) clarify the *source* of the violation: "Postgres MUST raise SQLSTATE 23503 *on the `task_costs` UPSERT* (the `cost_events` insert succeeds in the same tx but the tx is rolled back)". This wording also clarifies the developer expectation for the integration test.

Optional but cleaner: add `cost_events.version_id REFERENCES task_versions(id) ON DELETE RESTRICT` in a small migration in this change set so the constraint lives at the natural place. But that's scope-adjacent; the simpler doc fix is enough for MVP.

---

## S8. (should-fix) `compute_seconds = duration_ms / 1000` truncates silently and the test plan doesn't cover it

**Issue.** D6 explicitly truncates to int. So a `compute` event with `duration_ms = 800` contributes `compute_seconds += 0` while still counting toward `amount_usd` (via NUMERIC math at `per_second × 0.800`). Over many sub-second compute events this means `compute_seconds` stays 0 forever while `amount_usd` rises — surprising to a reader of the read API. Task 3.4 ("unit-test pricer.computeAmount exhaustively") does not enumerate this edge case; nor does Task 7.2.

**Evidence.** `design.md:118`.

**Suggested fix.** Either:
1. **Cleanest:** Change the column semantics — store `compute_ms` instead, derive `compute_seconds` in the read API. Requires changing `task_costs.compute_seconds` to `compute_ms` (migration) — scope-creep, probably reject.
2. **Pragmatic:** Round half-up (`(duration_ms + 500) / 1000`), document it, and add a test case `duration_ms=400 → compute_seconds += 0; duration_ms=600 → += 1`. Note the divergence between the `amount_usd` math (exact) and the `compute_seconds` aggregate (rounded).
3. **Minimal:** Keep truncation but add a Task 3.4 sub-bullet and a Task 7.2 scenario that locks the behaviour in tests so it doesn't regress.

Recommend option 3 for MVP scope, with a one-line note in the design that "sub-second compute durations are not reflected in compute_seconds; they are reflected exactly in amount_usd".

---

## S9. (should-fix) Worker `_int_or_none` is currently a SyntaxError — cost events for OpenAI/Anthropic responses without numeric tokens will explode

**Issue.** Unrelated-but-blocking: `worker/worker/core/cost_meter.py:227` reads `except TypeError, ValueError:` which is Python 2 syntax and is a `SyntaxError` in Python 3.10+. The function is in the LLM-response path. The consumer side of this proposal therefore can't be integration-tested end-to-end against a real worker run that produces non-numeric tokens (every callsite raises). If you don't fix this in worker, the proposal's "happy LLM with input+output" integration test (Task 7.2) won't be reproducible against a worker that just got a malformed token blob.

**Evidence.** `worker/worker/core/cost_meter.py:227`.

**Suggested fix.** This is genuinely out of scope for `add-cost-service` (which says "no worker changes"). But the proposal should *call it out* in the design's "Risks / Trade-offs" so the lead engineer knows the integration tests in Task 7.2 must stub the worker side. Two-line note + a follow-up issue/change `fix-cost-meter-syntax` (one-liner).

---

## S10. (should-fix) Spec wording for "Effective Pricing Lookup" leaves a corner case ambiguous

**Issue.** "Two pricing windows for the same unit" scenario covers the case where two open-ended (`expires_at IS NULL`) rows both have `effective_at <= occurred_at`. The "latest `effective_at` wins" rule is fine. But the scenario doesn't lock the behaviour when **two pricing windows abut exactly at `occurred_at`**: row A has `effective_at = 2026-01-01, expires_at = 2026-03-01`, row B has `effective_at = 2026-03-01, expires_at = NULL`, and an event has `occurred_at = 2026-03-01T00:00:00Z`. The current `GetEffectivePricing` predicate (`effective_at <= $4 AND (expires_at IS NULL OR expires_at > $4)`) excludes row A (its `expires_at = $4` is not `> $4`) and includes row B (its `effective_at = $4` is `<= $4`). Good — but the spec doesn't say so, so a future implementer might assert the wrong row.

**Evidence.**
- `task-cost-ingest/spec.md` §"Effective Pricing Lookup" Scenario only covers non-abutting windows.
- `api/queries/pricing.sql` line 11 uses `expires_at > $4`, matching the spec's tabular semantics.

**Suggested fix.** Add a scenario:

```
#### Scenario: Pricing windows abut exactly at occurred_at
- GIVEN row A (effective_at=2026-01-01, expires_at=2026-03-01) and row B (effective_at=2026-03-01, expires_at=NULL) for the same (resource_kind, resource_name, unit)
- WHEN an event with occurred_at = 2026-03-01T00:00:00Z is settled
- THEN row B's unit_price_usd MUST be used (the closing window is right-exclusive: expires_at > occurred_at)
```

---

## S11. (should-fix) `claude-haiku-4-5` is in the seed migration but not in worker config

**Issue.** Proposal §"Add a seed migration", design D8, and `task-cost-data-model/spec.md` "Default Pricing Seed" requirement all list `claude-haiku-4-5`. Worker `core/config.py:80-86` only defaults two models (`claude-opus-4-7`, `claude-sonnet-4-6`). Seeding pricing for a model the worker will never emit is harmless, but mis-stated as "every model in `model_by_key`". The fix is to be honest about what's being seeded.

**Evidence.**
- `worker/worker/core/config.py:80-86` — only `code_agent_model` (opus) and `research_agent_model` (sonnet) defaults exist.
- `proposal.md` line 11, `design.md` line 142, `specs/task-cost-data-model/spec.md` line 26 — all reference Haiku.

**Suggested fix.** Two ways:
1. Drop `claude-haiku-4-5` from this change's seed; let it be added when worker actually loads it.
2. Keep it (defensive seeding) but rewrite the requirement: "Seed rows MUST exist for every model in worker `model_by_key` defaults *and* for `claude-haiku-4-5` (anticipating its addition)." Honest about scope.

Recommend option 2 — pricing seed is cheap insurance.

---

## S12. (should-fix) Spec is silent on the `version_id` ↔ `task_id` mismatch the worker could send

**Issue.** Nothing prevents a worker from emitting a cost event with `task_id=T1, version_id=V2` where `V2` actually belongs to `task_id=T0`. The settler will happily insert `cost_events(task_id=T1, version_id=V2)` and then UPSERT `task_costs(version_id=V2, task_id=T1)` — but `task_costs(V2)` already exists with `task_id=T0` (from a prior event), and the ON CONFLICT UPDATE overwrites `task_id` to T1. The `task_costs.task_id` column should be considered immutable per version_id; the spec doesn't say so.

**Evidence.** `api/migrations/0003_init_cost_domain.up.sql:64-76` — no constraint pinning `task_costs.task_id` to `task_versions.task_id`.

**Suggested fix.** In `task-cost-ingest/spec.md` §"Idempotent Settlement", add a constraint to the UPSERT SQL: "the `task_id` column MUST NOT change on conflict (DO UPDATE SET ... does not assign task_id)." Or better, add a domain check in the settler: if the worker's `task_id` doesn't match `SELECT task_id FROM task_versions WHERE id = $version_id`, treat as a permanent DB error → DLQ. This is a tiny addition (one extra SELECT, or a CHECK constraint on the UPSERT).

---

## S13. (nice-to-have) Per-message retry cap is "open question" but design says it's mitigated by broker DLX

**Issue.** Risks §3 in design.md says "Mitigation: bound retries with the broker's DLQ-after-N policy" — but `q.cost.events` is declared with no DLX policy (see `api/internal/infrastructure/messaging/topology.go`). So an infinite serialisation-failure loop would, today, retry forever. Existing `q.task.events` has the same issue; the proposal inherits it.

**Evidence.** `api/internal/infrastructure/messaging/topology.go:49-53` — queue declarations have no `x-dead-letter-exchange` or `x-max-delivery-attempts` argument.

**Suggested fix.** Two-line note in design.md's Risks: "Today, q.cost.events has no broker-level retry cap. A consumer that keeps emitting `40001` will requeue indefinitely. Adding per-message attempt-count enforcement is tracked separately as a Post-MVP hardening item." Or, more aggressively, declare `x-dead-letter-exchange = task.dlx` on `q.cost.events` in this change set (small topology delta in `topology.go`). Either is fine — pick one and stop pretending DLX-after-N is in place.

---

## S14. (nice-to-have) Open Question #3 ("publish a derived `cost.settled` event") is the right open question, but Open Question #2 is over-specified

**Issue.** OQ #2 says "FK violation routes to DLQ. Acceptable for MVP since version deletion isn't a supported operation." This isn't actually an open question — it's a decision masquerading as one. It should move into the Decisions section (as a sub-bullet of D7) and the open-questions list should shrink to two genuine open issues. OQ #1 (compute_seconds gating) and OQ #3 (`cost.settled` publish) are real.

**Suggested fix.** Reword OQ #2 as "Decision: FK violations on version_id route to DLQ; backfill/replay is Post-MVP." Move it to Decisions.

---

## S15. (nice-to-have) Tasks 7.x integration tests are listed but should specify the RabbitMQ test-double choice

**Issue.** Task 7.1 says "use testcontainers `postgres:18.4-alpine` + an in-process RabbitMQ container (or shared with event-ingest fixture if available)". The codebase's existing pattern (per `event_ingest_test.go` and `testhelpers_test.go`) tests the handler against a `fakeAck` mock — there's no testcontainer'd RabbitMQ for messaging tests in `internal/infrastructure/messaging`. Spinning one up just for cost is asymmetric.

**Evidence.** `api/internal/infrastructure/messaging/event_ingest_test.go` exclusively uses `fakeAck` / `fakeIngester`. No RMQ container fixture exists.

**Suggested fix.** Split Task 7 into two halves:
- 7.1: Handler-level unit test using `fakeAck` + an in-memory fake settler (mirrors `event_ingest_test.go` exactly).
- 7.2: A `_integration_test.go` (build-tag `integration`) that uses *only* the testcontainer'd Postgres and feeds JSON bodies through the settler directly — no broker. The broker round-trip is already covered by topology tests; the cost-specific paths (decode, settle, idempotency) don't need a live broker to assert.

This both lowers test surface and matches the codebase's idiom.

---

## Summary

Overall assessment: the proposal is structurally solid — D1–D11 carve cleanly along existing boundaries, the spec mirrors `task-event-ingest` well, and the scope is appropriately narrow. The dominant flaw is a **contract mismatch between the worker's per-kind `seq` namespace and the DB's `(run_id, seq)` unique index (S1)**, which will cause silent data loss the moment a run emits one event of each kind. Two more must-fix items (S2 column-aggregation gating, S3 down-migration FK failure) reflect partial design decisions that haven't been pinned in spec text and would generate wrong data in production. S4 (silent pricing typos → $0) is a softer must-fix because the design *names* the risk but ships nothing to mitigate it within this change.

**Top 3 must-fix:**
1. **S1** — Fix the `(run_id, kind, seq)` uniqueness boundary. Without this, every non-first-kind cost event for a run silently disappears.
2. **S2** — Pin per-kind aggregate column mapping in the spec, not just in an Open Question.
3. **S3** — Make the seed `down.sql` tolerant of FK references; the current plan promises round-trippability it can't deliver in any live env.

After those three are addressed, the proposal is implementable as written — the rest are clarity/operability improvements that the lead can fold in opportunistically.
