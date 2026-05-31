## Context

Architecture §5.1 line 519 documents `POST /tasks/{task_id}/control` for `pause` / `resume` / `cancel`; §6.3 lines 741-744 sketch the flow: API writes outbox inside a tx, returns **202 Accepted**, does NOT immediately flip task/version status — the worker acts on the control signal and emits status events that `task-event-ingest` consumes to drive the actual state machine.

Existing pieces already in place:

- `task.control` exchange declared by `messaging/topology.go` (today: `direct`).
- `task_runs` table has `status` ∈ `{queued, running, paused, cancelling, cancelled, succeeded, failed}`; `task_versions` and `tasks` share the same active-vs-terminal split.
- `task-event-ingest` is the authoritative writer of `task_versions.status` + `tasks.status`. Task `paused` / `running` flips already happen as a side effect of worker-reported events.
- Outbox + Relayer is built (`add-api-messaging`, `add-api-persistence`), but the Relayer hard-codes a single exchange.

What's missing (and what this change ships):

1. The HTTP endpoint, request/response shape, and state guards.
2. The outbox row pattern for control messages — including teaching the Relayer to route by per-row exchange.
3. The exchange-type change on `task.control` (direct → topic) so workers can wildcard-subscribe by `task_id` without an API-side `worker_id` lookup.

What's explicitly NOT in this change: the worker-side consumer / subscription / cooperative-pause mechanism. That lands in `add-worker-control-handling` and consumes the contract this change establishes.

## Goals / Non-Goals

**Goals**

1. One endpoint `POST /api/v1/tasks/{task_id}/control` for all three actions; one outbox row per request; one 202 response shape.
2. Owner-scoped 404 (same posture as `task-read-api`, `task-cost-api`).
3. State-machine preconditions on the API side (409 for pause-when-not-active, resume-when-not-paused, cancel-when-terminal) so the broker doesn't get flooded with pointless messages.
4. Per-row exchange on outbox so the Relayer routes `task.control` rows to the control exchange and `task.exchange` rows to the existing one — both via the same Relayer instance.
5. `task.control` retyped to `topic` to enable worker wildcard subscription on `task.<task_id>`.

**Non-Goals**

- Worker control-handling — `add-worker-control-handling` consumes this contract.
- Redis Pub/Sub fast-path for control (architecture §5.3 line 652) — Post-MVP; MQ alone gives us order-of-seconds latency, which is fine.
- Pre-claim cancel cleanup (when no worker has bound yet, the message dies in a topic exchange with no matching binding). MVP-acceptable; a "control_pending sweep" job can ship Post-MVP if real users hit this.
- WebSocket push of "control accepted" — the front-end polls `task-read-api` for status changes.
- Per-tenant rate-limit on control requests — DoS-shaped only if a user spams; not in MVP scope.
- Multi-target control (`cancel all my running tasks`) — single-task endpoint only.

## Decisions

### D1: Single endpoint, three actions, 202 Accepted

`POST /api/v1/tasks/{task_id}/control` body `{action: "pause"|"resume"|"cancel", reason?: string}`. The handler dispatches on `action` after a shared owner check + state-guard step. Successful response is HTTP **202** with payload `{accepted: true, action, task_id, effective}` — 202 because the state change is asynchronous (worker reads outbox-relayed message and acts at next safe checkpoint).

`effective` is the post-resolution disposition of the request:

- `"queued"` — the API resolved an active `task_runs.id` for the task's current version; the control message is targeted and the worker will receive it within seconds (subject to MQ + relayer latency).
- `"best_effort"` — no active run was resolved (task is `pending`, no claim yet, or `current_version` is NULL). The outbox row is still written and the relayer still publishes, but with no worker bound to the routing key the broker will drop it. Front-end MAY surface this to the user as "cancel queued; will apply when a worker claims this task" (S9 reviewer recommendation).

The `outbox_id` is deliberately NOT in the response body — front-ends never use it, and the access-log JSON already carries it for log correlation. Surfacing internal ids in the public envelope makes the contract harder to evolve.

**Alternative considered**: three endpoints (`/pause`, `/resume`, `/cancel`). **Rejected** — the request shape is identical except the verb; routing by JSON-body field keeps the handler set small and matches the architecture's exact path.

### D2: API writes the outbox only — no direct state-machine flip

Per architecture §6.3 (line 742: "*tasks.status 不立刻改，等 Worker 确认*"), the API does NOT update `tasks.status` / `task_versions.status` / `task_runs.status` on a control request. The worker reads the control, acts, and emits status events that `task-event-ingest` writes — so `task-event-ingest` remains the sole writer of those columns and its CAS guards stay intact.

For pre-claim control (the task is `pending` and no worker has claimed a run yet): the message still goes out. If no worker is bound to that task's routing key, the message dies. Documented in Non-Goals; not a regression because the same task would also be stalled in the execute queue. A worker that later claims the run will see the no-op cancel only if the user re-sends the control (manual today; "control_pending sweep" Post-MVP).

**Alternative considered**: API writes `cancelling` directly on cancel to mark intent durably. **Rejected** — it would carve a hole in the event-ingest "sole-writer" invariant for a corner case the front-end can paper over by retrying control after a timeout.

### D3: Per-row outbox exchange + topology change

Migration `0006_outbox_exchange` adds:

```sql
ALTER TABLE outbox ADD COLUMN exchange TEXT NOT NULL DEFAULT 'task.exchange';
```

Existing rows backfill to `'task.exchange'` (the only exchange the Relayer used until now). New control rows write `'task.control'`. The Relayer's `publishRow` reads `row.Exchange` and passes it through to `publisher.Publish(ctx, row.Exchange, row.Topic, env)`. No per-relayer-instance exchange constant is needed any more (we drop `r.exchange` in favor of a per-row read).

The `task.control` exchange's **type** changes from `direct` to `topic` so workers can bind with wildcards like `task.*` (all tasks) or `task.<specific>`. The change is breaking for any existing binding — in MVP there are no bound consumers yet, so `DeclareTopology` can re-declare safely. The implementation deletes the existing exchange and re-declares as `topic` (idempotent in dev; documented in Migration Plan for prod-style envs).

**Alternative considered**: route by topic prefix in the existing Relayer (no schema change). **Rejected** — implicit routing is fragile; a typo in `outbox.topic` would silently send to the wrong exchange. An explicit `exchange` column fails loud.

### D4: Routing-key convention `task.<task_id>`

Each control message uses routing key `task.<task_id>` on the (now-topic) `task.control` exchange. Workers — in the future `add-worker-control-handling` change — declare per-process queues bound with patterns matching the tasks they're currently running.

This change does NOT establish worker-side binding behavior. The spec records the routing-key contract so the worker change has a fixed target.

**Alternative considered**: `control.<worker_id>` per architecture line 648 ("Routing key: `control.<worker_id>` 或 fanout 到所有 Worker（订阅 task_id 维度）"). **Rejected** for MVP because `task_runs` has no `worker_id` column and adding one requires a worker-side change to set it; punting that complexity to the worker proposal lets this API land standalone.

### D5: Control payload shape

The outbox `payload` for a control row is:

```json
{
  "task_id": "<uuid>",
  "version_id": "<uuid|null>",     // current active version, null when none
  "run_id": "<uuid|null>",          // latest task_runs row for that version, null when none
  "action": "pause" | "resume" | "cancel",
  "reason": "<string|empty>",       // capped at 200 chars (matches task.title validation)
  "issued_at": "<RFC3339>"          // API's clock at request time
}
```

Worker reads this body and acts. The `run_id` / `version_id` MAY be null (pre-claim state); the worker is responsible for filtering messages it can't act on.

`run_id` resolves to the **latest attempt** for the task's current version (`ORDER BY attempt_no DESC LIMIT 1`), not necessarily an "active" one — a `succeeded` first attempt followed by no further attempts still surfaces here, and a control message would arrive carrying that terminal run's id. The worker's local "is this run currently active in my process" check is the authoritative filter; the API does NOT pre-filter on `task_runs.status` (deliberate simplicity — adding the filter would require an extra index path and the worker has the cheaper check). Documented per reviewer S10.

`reason` is free-form, server-trims trailing whitespace, capped at **200 chars** to match the existing `task.title` validation cap in `domain/task/validation.go`. 400 `invalid_input` on overflow; the field is logged in the access-log JSON for audit. No structured-reason enum in MVP.

### D6: State-machine preconditions

Inside the control transaction, after the owner check, the handler reads `tasks.status` (via the new `LockTaskForControl :one`) and applies:

| action  | preconditions on `tasks.status`         | on fail                  |
|---------|----------------------------------------|--------------------------|
| pause   | ∈ {`pending`, `running`}               | 409 `invalid_state`      |
| resume  | ∈ {`paused`}                            | 409 `invalid_state`      |
| cancel  | ∉ {`cancelled`, `succeeded`, `failed`} | 409 `invalid_state`      |

These guards are advisory not safety-critical — even if a race lets a pause through after the worker terminated, the worker side rejects it harmlessly. They exist to prevent obvious-mistake messages from flooding MQ and to give the front-end an explicit failure reason.

`tasks.status` may briefly disagree with `task_versions.status` mid-transition (worker has emitted version=cancelling but tasks=running because cancelling is not in the task-status domain — see `taskStatusFromVersion`). The state guard reads `tasks.status` only — versions' `cancelling` doesn't make a `cancel` request 409 (the task hasn't reached terminal yet, so re-sending cancel is harmless).

`SELECT … FOR UPDATE` locks the task row for the duration of the tx so concurrent control requests serialise. Outbox INSERT happens in the same tx — the row goes out atomically with the precondition observation.

### D7: Owner check mirrors task-read-api

Same `Owner{TenantID, UserID}` value type and `404 task_not_found` on mismatch. The control transaction's first SQL hit (`LockTaskForControl`) carries the ownership predicate so an unowned/unknown task returns no rows → handler renders 404 (never 403, never differentiates).

### D8: sqlc / domain surface

- `LockTaskForControl :one` — `SELECT id, status, current_version FROM tasks WHERE id=$1 AND tenant_id=$2 AND user_id=$3 FOR UPDATE`. Owner predicate inline so unknown / unowned task → no rows → `ErrTaskNotFound`.
- `GetActiveRunIDForTask :one` — narrow lookup returning `task_runs.id` for the task's current version, ordered by `attempt_no DESC LIMIT 1`; nullable (no rows when current_version is NULL or no runs claimed yet). Domain handles NULL.
- Domain `TaskControlService.Apply(ctx, owner, taskID, action, reason) (outboxID int64, err error)` runs the tx end-to-end. Returns `ErrTaskNotFound` / `ErrInvalidState` (new sentinel) / wrapped DB errors. Application layer wraps it with `(tenantID, userID)` → `Owner`.

### D9: Outbox row mechanics

- `aggregate = "task"`, `aggregate_id = task_id`, `topic = "task." + task_id`, `exchange = "task.control"`, `payload = <JSON D5>`.
- `InsertOutbox` signature gains the `exchange` parameter (was 4 args, becomes 5). `tasks.sql` callers of `InsertOutbox` pass `"task.exchange"` for backwards-compat with the existing iterate/create flows.
- Relayer's `publishRow` (today: `r.publisher.Publish(ctx, r.exchange, row.Topic, env)`) becomes `r.publisher.Publish(ctx, row.Exchange, row.Topic, env)`. The `r.exchange` field is removed; `NewRelayer` drops the implicit default. Tests update accordingly.

### D10: HTTP error mapping

`ErrInvalidState` is a new sentinel; `MapError` adds: `errors.Is(err, ErrInvalidState)` → `(http.StatusConflict, "invalid_state", err.Error())`. The error message includes the current `tasks.status` so the front-end can show actionable text.

### D11: Metrics

- `task_control_requests_total{action, outcome}` — counter, labels `action ∈ {pause, resume, cancel, unknown}` × `outcome ∈ {accepted, conflict, not_found, invalid}`. The `unknown` label value is emitted only when the request body's `action` field couldn't be parsed (so the only legal combination is `{action="unknown", outcome="invalid"}`); the four legitimate action values pair with any of the four outcomes. `invalid` covers 400s (missing field, malformed body); `conflict` covers 409 `invalid_state`; `not_found` covers 404; `accepted` is the 202 path.

No per-handler latency histogram — control is the cheapest possible HTTP write (one lock + two SQL writes); add a histogram only if pressure shows up.

### D12: Topology drift on existing dev DBs

Changing `task.control` from `direct` to `topic` requires either (a) deleting the existing exchange before re-declaring, or (b) declaring a new exchange under a different name. We pick (a): `DeclareTopology` runs `ExchangeDelete("task.control", false, false)` *before* the declare loop, only for exchanges whose type we're changing — gated by a small `retypableExchanges` list. The amqp091-go signature is `ExchangeDelete(name string, ifUnused, noWait bool)`; we pass `ifUnused = false` (delete even if bindings exist) and `noWait = false` (block until broker confirms). There is no `if-empty` argument — that's queue-deletion semantics, not exchange. Reviewer S2.

This is a one-time evolution per exchange, but the `retypableExchanges` list MUST be **append-only across releases**: once an exchange enters it (here, `"task.control"`), future versions MUST keep that entry indefinitely so an operator rolling forward against a database whose `task.control` is still `direct` (because they skipped this version) can still recover. Reviewer S12 — bound in the api-messaging spec.

### D13: Pause-when-paused (and resume-when-running) return 409, not 202-noop

The state-guard table in D6 says pause-when-`paused` is 409 `invalid_state`. The alternative — return 202 with `effective: "noop"` and write no outbox row — was considered (and matches `task-cost-api`'s leniency on empty `?group_by=`).

**Rejected.** The asymmetry is intentional:

- Empty `?group_by=` is *form-shape* leniency: the front-end may construct URLs with the param always present but sometimes blank, and that's a presentation concern, not an intent mismatch.
- `pause` against a `paused` task is *intent* mismatch: the user thinks the task is running but it's already paused, or they're race-clicking. Returning 409 with the current status in the `message` surfaces the disagreement so the front-end can refetch and show truth ("task is already paused"). Returning 202 would silently confirm a request the user didn't actually mean.

The same logic applies to `resume`-when-not-`paused`. `cancel`-when-terminal is harder to rationalise as "intent mismatch" — the user really does want it cancelled, just too late — but consistency (and the cheap MQ-flood guard) wins.

**Reviewer S8**: made this decision visible rather than implicit.

## Risks / Trade-offs

- **[Risk]** Changing `task.control` from `direct` to `topic` is a runtime topology change. → **Mitigation**: in MVP no worker has bound to it yet (consumer ships in the next change), so the `ExchangeDelete` + re-declare is a no-op for any live binding. In prod-style envs the operator runs a brief drain first; documented in Migration Plan.
- **[Risk]** Pre-claim cancel can disappear (the message routes to a topic exchange with no matching binding → dropped). → **Mitigation**: documented in Non-Goals; the front-end can retry. A "control_pending sweep" job (workers periodically scan for cancelled-but-not-yet-handled tasks) is Post-MVP.
- **[Risk]** The Relayer change is across-cutting — affects every outbox write. → **Mitigation**: existing callers default to `'task.exchange'` via the migration's `DEFAULT`. Tests for the write path stay green because `InsertOutboxParams.Exchange` is the new required field, which existing callers explicitly pass.
- **[Risk]** State-machine guards read `tasks.status` only, but the real driver is event-ingest's tx — a stale read is possible. → **Mitigation**: `SELECT … FOR UPDATE` serialises control requests; the worker's event stream remains the authoritative writer of state, and any disagreement self-heals at the next event-ingest cycle. The guards are best-effort; the worker is the safety net.
- **[Risk]** A user spams pause/resume → many outbox rows, many MQ messages. → **Mitigation**: worker dedupes via its in-memory flag (the spec for `add-worker-control-handling` will bind this). API-level rate-limit is Post-MVP.
- **[Trade-off]** No Redis fast-path for control. → MQ alone gives us seconds-level latency, which is fine for "request to stop" UX. Sub-second cancel is a real future requirement but not MVP.

## Migration Plan

1. Apply migration `0006_outbox_exchange.up.sql` adding the `exchange` column (default `'task.exchange'`). The up uses `ADD COLUMN IF NOT EXISTS` so a re-run after rollback is a no-op (see step 4).
2. Deploy the new API binary with the relayer update + topology change. On startup, `DeclareTopology` deletes the existing `task.control` exchange and re-declares it as `topic`.
3. The worker side is unchanged — the next deployment will pick up `add-worker-control-handling`.
4. **Rollback**: revert the binary. Migration `0006_outbox_exchange.down.sql` is intentionally a **no-op** (forward-only schema evolution per reviewer S6): the `exchange` column stays in place because (a) its `NOT NULL DEFAULT 'task.exchange'` makes it harmless to leave, and (b) dropping it after some `'task.control'` rows have been written would silently re-route those rows to the wrong exchange on a subsequent re-up. The `task.control` exchange likewise stays as `topic`; the old (pre-change) Relayer code reads `outbox.exchange` and would publish there too once re-rolled, but if the operator truly wants the legacy behavior they re-declare `task.control` as `direct` out-of-band. Documented as a known trade-off; the alternative ("destructive drop") is gated behind a separate operations runbook, not the migration itself.

## Open Questions

(None — D11 / D12 cover what was previously deferred.)
