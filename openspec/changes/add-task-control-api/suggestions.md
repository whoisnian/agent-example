# add-task-control-api — Reviewer Suggestions

Independent review of `proposal.md`, `design.md`, `specs/`, `tasks.md`. Findings are ordered roughly by severity, then by where they live.

## Lead's verdict (applied after review)

| # | Verdict | Notes |
|---|---|---|
| S1 | accepted (must-fix) | `proposal.md` line 9 fixed: pause precondition is `{pending, running}` (consistent with design D6 + spec). |
| S2 | accepted (must-fix) | `ExchangeDelete(name, ifUnused, noWait)`; design D12 + api-messaging spec rewritten to drop `if-empty` and name the args correctly. |
| S3 | accepted (must-fix) | Task 3.2 rewritten: hand-edit `persistence/outbox.go`'s `OutboxRow` struct + `ScanPending` SELECT — sqlc regen does NOT touch this file. Added an explicit warning. |
| S4 | accepted (should-fix) | Task 2.1 caller list corrected — only `createActiveVersion` (in `domain/task/service.go`) is an existing `InsertOutbox` caller. |
| S5 | accepted (should-fix) | api-messaging spec's "retypable exchange is re-declared" scenario now binds ordering: pre-delete MUST happen before any `ExchangeDeclare` for that exchange. |
| S6 | accepted (should-fix) | Down migration is now a no-op (forward-only schema evolution). Up uses `ADD COLUMN IF NOT EXISTS`. Spec scenario updated; risk acknowledged in design. |
| S7 | accepted (nice-to-have) | Dropped `outbox_id` from the 202 body. Log access-line carries it; debug-only correlation via headers can ship later if needed. |
| S8 | accepted (should-fix) | Promoted the pause-when-paused → 409 stance to an explicit Decision D13 with rationale. |
| S9 | accepted (should-fix) | 202 body now carries `effective ∈ {queued, best_effort}` to disambiguate "active run found, control will reach worker" from "pre-claim, may be dropped". Spec scenario added. |
| S10 | accepted (nice-to-have) | Design D5 documents that `run_id` is the latest attempt's id, possibly terminal; worker filters semantics. |
| S11 | accepted (should-fix) | Added "Duplicate accepted controls both produce outbox rows" spec scenario. |
| S12 | accepted (should-fix) | api-messaging spec adds the append-only invariant: once an exchange enters the retypable list, future versions MUST keep it. |
| S13 | accepted (nice-to-have) | `reason` cap aligned to `200` to match the existing `title` cap (`validation.go` line 31). Updated spec + design + tasks. |
| S14 | accepted (should-fix) | Added "Concurrent cancel requests serialise on the task row" spec scenario; integration test task already exercised it. |
| S15 | accepted (should-fix) | Design D11 label set widened to `action ∈ {pause, resume, cancel, unknown}` (unknown only paired with `outcome="invalid"`) — reconciled with spec. |

---

## S1 — `proposal.md` lists `queued` in the pause precondition, but `tasks.status` cannot be `queued`

**Issue.** `proposal.md` says pause is allowed when `tasks.status ∈ {pending, queued, running}`, but the `tasks_status_check` CONSTRAINT and the in-code `taskStatuses` set both exclude `queued` (it is a version-only status). `design.md` D6 and `specs/task-control-api/spec.md` both have the correct set `{pending, running}`; only the proposal disagrees.

**Evidence.**
- `openspec/changes/add-task-control-api/proposal.md:9` — "pause is rejected with 409 invalid_state unless the task is in pending / queued / running."
- `openspec/changes/add-task-control-api/design.md:101-104` — table says `{pending, running}` only.
- `openspec/changes/add-task-control-api/specs/task-control-api/spec.md:34` — spec says `{pending, running}`.
- `api/migrations/0002_init_task_domain.up.sql:29-31` — `tasks_status_check CHECK (status IN ('pending','running','paused','cancelled','succeeded','failed'))`.
- `api/internal/domain/task/status.go:40-47` — `taskStatuses` map without `queued`.

**Suggested fix.** Update `proposal.md` line 9 to read `{pending, running}` so the three artifacts agree. (Reword: "pause is rejected with 409 unless `tasks.status ∈ {pending, running}`.")

**Severity.** `must-fix` — internal inconsistency that will trip a reader following the proposal first.

---

## S2 — `ExchangeDelete` does not take an `if-empty` argument; design D12 and the spec misname the third parameter

**Issue.** Both `design.md` D12 ("`ExchangeDelete("task.control", false, false)`") and the api-messaging spec ("`if-unused = false, if-empty = false`") describe `ExchangeDelete`'s third positional as `if-empty`. The amqp091-go library does not have such an argument — the signature is `ExchangeDelete(name string, ifUnused, noWait bool)`. (`if-empty` is a queue-deletion semantic, not exchange.) The intended meaning — "delete even if it has bindings" — is achieved by `ifUnused=false` alone.

**Evidence.**
- `openspec/changes/add-task-control-api/specs/api-messaging/spec.md:17` — "uses `if-unused = false, if-empty = false`".
- `openspec/changes/add-task-control-api/design.md:140` — "runs `ExchangeDelete("task.control", false, false)`" with an `if-empty` gloss.
- `/home/nian/Go/pkg/mod/github.com/rabbitmq/amqp091-go@v1.11.0/channel.go:1365` — `func (ch *Channel) ExchangeDelete(name string, ifUnused, noWait bool) error`.

**Suggested fix.** Rewrite both sentences as "`ifUnused = false` (delete even if bindings exist), `noWait = false` (block until broker confirms)." Drop any reference to `if-empty`.

**Severity.** `must-fix` — the spec asserts a contract on a non-existent argument; implementers reading the spec will write incorrect code if they trust the wording.

---

## S3 — Hand-written `persistence/outbox.go` needs an explicit task entry; "sqlc regen should produce this" doesn't apply

**Issue.** Task 3.2 says "ensure `OutboxRow` carries `Exchange string` (sqlc regen should produce this)" — but `api/internal/infrastructure/persistence/outbox.go` is hand-written raw pgx code, not sqlc-generated. The `OutboxRow` struct and the `ScanPending` SQL there must both be edited by hand. Specifically, the SELECT list in `outbox.go:69-75` needs to add `exchange`, and the `OutboxRow` struct in `outbox.go:17-27` needs an `Exchange string` field plus a matching `Scan(&r.Exchange, ...)` call in `outbox.go:85`. The api-persistence spec already calls out this file as the one allowed direct-pgx exception, so it cannot be regenerated.

**Evidence.**
- `openspec/changes/add-task-control-api/tasks.md:17` — task 3.2 implies sqlc regen.
- `api/internal/infrastructure/persistence/outbox.go:17-27` — hand-written `OutboxRow` (no `Exchange` field).
- `api/internal/infrastructure/persistence/outbox.go:69-91` — hand-written `ScanPending` SQL (no `exchange` column in SELECT).
- `openspec/specs/api-persistence/spec.md:44` — "The single exception is the Outbox Relayer's batched scan, which MAY use direct `pgx` calls".

**Suggested fix.** Rewrite task 3.2 as: "Update `OutboxRow` in `api/internal/infrastructure/persistence/outbox.go` to add `Exchange string`; update `ScanPending` SQL to include `exchange` in the SELECT list and the corresponding row scan. (This file is hand-written, not sqlc.) Note that the sqlc-generated `outbox.sql.go` is a parallel path used only by the writer side; the relayer reads through this hand-written file."

**Severity.** `must-fix` — without this, the relayer will compile but `row.Exchange` will always be empty, so `Publish(ctx, "", row.Topic, env)` will publish to the default (nameless) exchange and route nothing.

---

## S4 — Task 2.1 names "event-ingest" as an existing `InsertOutbox` caller, but it never writes to `outbox`

**Issue.** Task 2.1 enumerates "task-create-api iterate / create / event-ingest" as callers that must pass `'task.exchange'` explicitly. Event-ingest does not call `InsertOutbox` today (a `grep` for `InsertOutbox` in `api/internal/` finds only `service.go`'s `createActiveVersion` helper). Listing it as a caller will cause a reader to look for a non-existent call site and waste cycles.

**Evidence.**
- `openspec/changes/add-task-control-api/tasks.md:9` — "All existing callers (task-create-api iterate / create / event-ingest) pass `'task.exchange'` explicitly."
- `grep -rn "InsertOutbox" api/internal/` returns only `api/internal/domain/task/service.go:438` and the sqlc-generated files.
- `api/internal/domain/task/event_sync.go` — no outbox references.

**Suggested fix.** Replace task 2.1's parenthetical with "All existing callers — `createActiveVersion` in `domain/task/service.go` (used by both create and iterate) — pass `'task.exchange'` explicitly."

**Severity.** `should-fix` — not a correctness bug but a factual error in tasks; will confuse the implementer.

---

## S5 — Topology spec leaves the ordering of "fail-fast on incompatible entity" vs "retypable exchange" ambiguous

**Issue.** The modified api-messaging spec adds (a) a "Topology fails fast on incompatible existing entity" scenario and (b) a "Retypable exchange is re-declared" scenario, but does not bind the implementation order. If the topology code declares first and the fail-fast check trips on `task.control` (currently `direct` on existing dev DBs), the retypable pre-delete never runs and startup hard-fails. The intent (per design D12) is clearly "pre-delete retypable exchanges *before* the declare loop", but the spec scenario only states an outcome — it does not require the pre-delete to happen first.

**Evidence.**
- `openspec/changes/add-task-control-api/specs/api-messaging/spec.md:17` — "pre-delete them before re-declaring".
- `openspec/changes/add-task-control-api/specs/api-messaging/spec.md:23-26` — "fails fast on incompatible existing entity" scenario lacks an "NOT in retypable set" qualifier on the trigger.

**Suggested fix.** Tighten the fail-fast scenario's trigger:

> #### Scenario: Topology fails fast on incompatible existing entity
> - **WHEN** an existing exchange has a different type than declared AND it is NOT in the retypable set
> - **THEN** startup MUST fail with a fatal log naming the conflicting entity ...

(actually already present at line 24-25 — verified). However, also bind the ordering inside the "Retypable exchange is re-declared" scenario:

> - **AND** the retypable-pre-delete step MUST run before any `ExchangeDeclare` call for that exchange.

This makes the order explicit and testable.

**Severity.** `should-fix` — implementation as designed will be correct, but the spec is the contract that prevents future regressions; binding ordering in writing is cheap insurance.

---

## S6 — Migration `0006_outbox_exchange.down.sql` drops a column that may contain `'task.control'` rows; current spec scenario only round-trips against a fresh DB

**Issue.** The api-persistence spec's round-trip scenario applies up→down→up against a *fresh* database (line 20). In practice, the down migration is run against a populated DB (e.g., during a partial rollback after some control rows were written). Those rows will lose their `exchange` discriminator on `DROP COLUMN`, and on re-up they default back to `'task.exchange'` — so a row that was `'task.control'` becomes `'task.exchange'`, and the relayer will publish stale control rows to the wrong exchange. Design's Migration Plan acknowledges "existing data lose the field" but the spec scenarios don't surface this as a known limitation.

**Evidence.**
- `openspec/changes/add-task-control-api/specs/api-persistence/spec.md:19-21` — "round-trip" tested only on a fresh DB.
- `openspec/changes/add-task-control-api/design.md:156` — Migration Plan rollback section: "existing rows lose the field but the column was always task.exchange for non-control rows".
- `openspec/changes/add-task-control-api/tasks.md:4` — task 1.2 only says "existing data loses the column but reverts cleanly".

**Suggested fix.** Either (a) make `down.sql` a no-op (recommended for forward-only schema evolution; the column is harmless to leave behind), or (b) keep the drop but add an explicit warning to the `.down.sql` header AND add a spec scenario:

> #### Scenario: Down migration on a DB with control rows is destructive
> - **GIVEN** the outbox table contains rows where `exchange = 'task.control'`
> - **WHEN** `0006_outbox_exchange.down.sql` runs
> - **THEN** those rows MUST first be `UPDATE outbox SET status='failed' WHERE exchange='task.control' AND status='pending'` (or operator-equivalent) before the column is dropped; otherwise on re-up they will silently re-route to `task.exchange`

Option (a) is cleaner — a forward-only schema change is preferable when the field is purely informational on the writer side.

**Severity.** `should-fix` — a real footgun on rollback, but only operationally relevant once production exists.

---

## S7 — The 202 response body's `outbox_id` is an internal implementation detail; clients have no use for it

**Issue.** The spec and proposal both promise `data.outbox_id` in the 202 body. There is no client-side use case in the proposal: front-end polls `task-read-api` for status, not the outbox. The `bigserial` id is convenient for log correlation, but logs already get it via the access-log JSON (per the Observability requirement). Once exposed, the field becomes part of the API contract and removing it later is breaking.

**Evidence.**
- `openspec/changes/add-task-control-api/specs/task-control-api/spec.md:5` — "envelope `data = {accepted, action, task_id, outbox_id}`".
- `openspec/changes/add-task-control-api/design.md:43` — "payload `{accepted: true, action, task_id, outbox_id}`".
- No client-side requirement for `outbox_id` anywhere in the design or proposal.

**Suggested fix.** Drop `outbox_id` from the public response body. Keep it in the access log (as the design already implies). If a debug-only header is desired, add `X-Outbox-Id: 12345` on the 202 — headers are easier to remove later than body fields.

**Severity.** `nice-to-have` — keeping it is harmless; removing it is cheap API hygiene before the spec gets archived.

---

## S8 — Pause-when-paused and resume-when-running return 409, but pause-when-paused has a credible "idempotent → 202 no-op" argument

**Issue.** The spec hard-codes pause-when-paused → 409 and resume-when-running → 409. The 409 for resume-when-running is unambiguous (user thought it was paused; tell them it isn't). But pause-when-paused has the property that the user's *intent* is already satisfied; returning 409 makes the front-end re-fetch state just to discover the same answer it would have shown if it had pre-checked. Compare with `task-cost-api`'s decision to treat empty-as-absent for `group_by` — that change leaned toward "don't punish the caller for redundant input." The Open Questions section says "(None — D11/D12 cover what was previously deferred)", but this is a real open design choice that wasn't explicitly considered.

**Evidence.**
- `openspec/changes/add-task-control-api/specs/task-control-api/spec.md:34, 42-45` — pause-when-paused is 409.
- `openspec/changes/add-task-control-api/design.md:104, 106` — "These guards are advisory not safety-critical".
- `openspec/changes/add-task-control-api/design.md:160` — Open Questions = "None".

**Suggested fix.** Either (a) keep 409 (current spec) and explicitly call this out in design's "Decisions" with a one-line rationale ("the worker has acted; the front-end's stale view is the bug to surface"), or (b) relax to "pause-when-paused returns 202 with `effective: 'noop'` and no outbox row". I lean (a) — the 409 message can include the current status so the front-end shows "task is already paused" without an extra round-trip. But the decision should be visible, not implicit.

**Severity.** `should-fix` — surface as a documented decision, not a hidden default.

---

## S9 — 202 semantic mismatch: pre-claim cancel succeeds but the message effectively dies; clients can't distinguish "queued for worker" from "dropped by broker"

**Issue.** The "Owned task with no active run still accepts" scenario returns 202 even though the resulting message routes to a topic exchange with no binding and is dropped (acknowledged in Non-Goals). A typical 202 means "the request was accepted and will eventually take effect"; in this case it won't, until either a worker binds (post-claim) or the user retries. The 202 is technically defensible (the outbox row exists, the relayer published, the broker accepted — the message just had nowhere to land) but is operationally misleading.

**Evidence.**
- `openspec/changes/add-task-control-api/specs/task-control-api/spec.md:21-24` — "Owned task with no active run still accepts" → 202.
- `openspec/changes/add-task-control-api/design.md:34, 51` — Non-Goals + D2 acknowledge the message dies.
- `openspec/changes/add-task-control-api/design.md:145` — "front-end can retry. A control_pending sweep job is Post-MVP."

**Suggested fix.** Add an `effective` discriminator to the 202 body:

```json
{
  "accepted": true,
  "action": "cancel",
  "task_id": "...",
  "effective": "queued"  // "applied" when a run exists, "queued" when pre-claim (best-effort)
}
```

This sets honest user expectations and gives the front-end something to show ("cancel queued; will apply when worker claims this task"). Could be added later without breaking — but defaulting now is cheaper than a v2 contract change.

**Severity.** `should-fix` — UX clarity; not a correctness bug.

---

## S10 — `GetActiveRunIDForTask` query joins on `current_version` but does not pin to a single version's run; semantics differ from "active run" wording

**Issue.** The task's description: "resolves the latest `task_runs.id` for the task's current version" (proposal line 15; design D8 line 119). The SQL in task 2.3 is:
```sql
SELECT r.id FROM task_runs r
  JOIN tasks t ON t.current_version = r.version_id
  WHERE t.id = $1
  ORDER BY r.attempt_no DESC LIMIT 1
```
This returns the run with the highest `attempt_no` for the task's current version. But "latest attempt" ≠ "active attempt" — a `failed` first attempt followed by a `succeeded` second attempt returns the second's id, which is fine; but what about `failed` first attempt with `pending` second attempt → returns the `pending` (good). What about a terminal `succeeded` task with one run? Returns that succeeded run's id, which gets shipped to the worker as `run_id`. The worker spec is supposed to filter, but the payload semantics ("the run the user is targeting") become muddied. Verify whether the spec scenario binds the right invariant.

**Evidence.**
- `openspec/changes/add-task-control-api/tasks.md:11` — query body.
- `openspec/changes/add-task-control-api/specs/task-control-api/spec.md:93-103` — payload spec says "resolves to the latest `task_runs.id` for the task's `current_version`, ordered by `attempt_no DESC`" — accurately describes the SQL.
- `openspec/changes/add-task-control-api/design.md:85-89` — payload doc says "most-recent task_runs row for that version".

**Suggested fix.** The SQL is fine as-is for MVP — but consider whether to filter `WHERE r.status IN ('queued','running','paused','cancelling')` to make `run_id` carry "active run" rather than "latest run". Either is defensible; the current choice trades worker-side filtering complexity for API-side simplicity. Document the choice in design D5 explicitly: "run_id is the *latest attempt's id*, not necessarily an active one; the worker is responsible for filtering."

**Severity.** `nice-to-have` — current behavior is consistent, just lightly counter-intuitive given the "active" framing in the proposal.

---

## S11 — Spec lacks a duplicate-control-row scenario; the design says workers dedupe but doesn't bind it at the API contract level

**Issue.** Design's Risks section notes "a user spams pause/resume → many outbox rows, many MQ messages" with "worker dedupes via its in-memory flag". The spec has no scenario asserting that two rapid pauses both produce outbox rows (i.e., the API does *not* dedupe), and no scenario asserting that the second request's outbox payload differs from the first's only by `issued_at`. Future readers (especially worker-side implementers) will wonder whether they need to expect duplicates.

**Evidence.**
- `openspec/changes/add-task-control-api/design.md:148` — "worker dedupes via its in-memory flag".
- `openspec/changes/add-task-control-api/specs/task-control-api/spec.md` — no duplicate-request scenario.

**Suggested fix.** Add a scenario under "Control Endpoint":

> #### Scenario: Duplicate accepted controls both produce outbox rows
> - **GIVEN** an owned task in `running` status
> - **WHEN** the caller `POST`s `{action: "pause"}` twice within the same `tasks.status='running'` window
> - **THEN** both responses MUST be HTTP `202`, AND exactly two new outbox rows MUST exist (the API does NOT dedupe; the worker is responsible for in-flight deduplication)

This is a small but useful contract for the next change (`add-worker-control-handling`).

**Severity.** `should-fix` — closes a documented contract gap.

---

## S12 — `retypableExchanges` list will be append-only forever, but neither spec nor design says so

**Issue.** Design D12 calls this "a one-time evolution" but the implementation must keep `"task.control"` in the list *forever* — otherwise a future operator running an old binary (or rolling forward against a DB whose `task.control` is still `direct` because they skipped this version) will hit the FAIL-FAST scenario. Spec wording today is "exchanges whose type was changed by a previous OpenSpec change", which suggests append-only but doesn't say it outright.

**Evidence.**
- `openspec/changes/add-task-control-api/design.md:140` — "future changes just add new exchanges, not retype existing ones".
- `openspec/changes/add-task-control-api/specs/api-messaging/spec.md:17` — list description.

**Suggested fix.** Add one sentence to the api-messaging spec right after the "retypable exchanges" sentence:

> The list MUST be append-only across releases: once an exchange enters it, future versions MUST keep that entry indefinitely so the topology code can still recover a database that skipped intermediate versions.

Optionally also add a code-comment to that effect when implementing the list in `topology.go`.

**Severity.** `should-fix` — easy to forget when adding the next retype; codify the invariant.

---

## S13 — `reason` cap of 500 chars has no precedent rationale; check whether it should match the existing `title` / `prompt` cap

**Issue.** The proposal sets `reason` ≤ 500 chars without explaining why. `title` validation in the task-write-api lives in `api/internal/domain/task/validation.go`. If `title` is capped at, e.g., 200 or 256, then a 500-char `reason` is more generous than the title field — which may or may not be the intent. No big deal either way, but worth one minute of cross-check before locking in the spec.

**Evidence.**
- `openspec/changes/add-task-control-api/specs/task-control-api/spec.md:5, 62` — "capped at 500 characters".
- `api/internal/domain/task/validation.go` (not read; check for `title` / `prompt` length caps).

**Suggested fix.** Either align with the existing `title` cap (recommended for consistency) or leave at 500 and add a one-line justification in design D5: "500 chars matches \<some external precedent\>".

**Severity.** `nice-to-have` — cosmetic consistency.

---

## S14 — Concurrent control-request serialization via `SELECT … FOR UPDATE` is mentioned only in passing; spec should make it testable

**Issue.** Design D6 notes "`SELECT … FOR UPDATE` locks the task row for the duration of the tx so concurrent control requests serialise. Outbox INSERT happens in the same tx — the row goes out atomically with the precondition observation." This is the only place that pins down the serialization behavior. The spec ("State-Machine Preconditions") only says "The state read happens inside a `SELECT … FOR UPDATE` lock on the `tasks` row so concurrent control requests serialise." There's no scenario testing this. Task 7.2 lists "concurrent control requests serialise via the `FOR UPDATE` lock" as an integration-test target — good. Promote that to the spec as a scenario.

**Evidence.**
- `openspec/changes/add-task-control-api/specs/task-control-api/spec.md:38` — only a one-liner inside the requirement narrative.
- `openspec/changes/add-task-control-api/tasks.md:43` — integration test mentions it.

**Suggested fix.** Add a scenario under "State-Machine Preconditions":

> #### Scenario: Concurrent cancel requests serialise on the task row
> - **GIVEN** an owned task in `running` status
> - **WHEN** two `POST /api/v1/tasks/{id}/control` with `{action: "cancel"}` arrive in parallel
> - **THEN** both responses MUST be HTTP `202`, AND both outbox rows MUST exist; the second handler MUST have observed the task row only after the first's transaction committed

The current design is correct; the spec just needs to bind it.

**Severity.** `should-fix` — closes a contract / test gap.

---

## S15 — Tasks 6.1 / 6.2 don't pin the malformed-action label exactly the way the spec does

**Issue.** Spec Observability requirement: "malformed requests (those increment `outcome="invalid"` with `action="unknown"` when the action couldn't be parsed)". Task 6.2 says: "use `action="unknown"` when the value couldn't be parsed". Consistent so far. But the metric is `task_control_requests_total{action, outcome}` with `action ∈ {pause, resume, cancel}` per design D11 — `unknown` is not in that set. Either widen the design's stated label domain or the spec's exception is a contradiction.

**Evidence.**
- `openspec/changes/add-task-control-api/design.md:134` — "`action ∈ {pause, resume, cancel}` × `outcome ∈ {accepted, conflict, not_found, invalid}`".
- `openspec/changes/add-task-control-api/specs/task-control-api/spec.md:107` — "with `action="unknown"` when the action couldn't be parsed".

**Suggested fix.** Reconcile in design D11: "labels `action ∈ {pause, resume, cancel, unknown}` × `outcome ∈ {accepted, conflict, not_found, invalid}`" (with the note that `unknown` is emitted only for `outcome="invalid"`).

**Severity.** `should-fix` — small but real contradiction between two spec/design lines.

---

## Summary

**Overall.** The proposal is solid and implementable end-to-end. It correctly preserves the event-ingest "sole writer" invariant, mirrors `task-read-api`'s owner-scoped 404, and threads a clean outbox-per-row exchange refactor through three layers without breaking the existing iterate/create flow. The state-machine preconditions are well-walked. Tests and metrics are appropriately scoped. The architecture-vs-proposal mismatch on `worker_id` routing is consciously and defensibly punted to the worker-side change.

**Top must-fix items** (3):

1. **S1** — `proposal.md` line 9 lists `queued` in pause precondition; `tasks.status` cannot be `queued`. Fix to `{pending, running}`.
2. **S2** — `ExchangeDelete`'s third arg is `noWait`, not `if-empty`. Both design D12 and the api-messaging spec misname it. Fix the prose.
3. **S3** — Task 3.2 implies sqlc regen produces `OutboxRow.Exchange`, but `persistence/outbox.go` is hand-written; the SELECT list and struct both need a manual edit, or the relayer will publish to `""` and silently drop control messages.

**Recommendation.** Implementable as written *after* the three must-fix items land. The `should-fix` items (S5/S6/S8/S9/S11/S12/S14/S15) are quality improvements that the lead can address in the same PR or as follow-up nits. The nice-to-haves (S7/S10/S13) can defer until archive.
