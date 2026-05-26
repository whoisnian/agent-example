## Context

`add-task-create-api` writes `tasks` / `task_versions` / `task_runs` + an `execute` outbox row; the Outbox Relayer publishes it; the worker (`add-worker-code-agent`) consumes, runs, and emits an `event.<task_type>.<kind>` stream on the `task.events` topic exchange. But nothing on the API side consumes that stream, so `task_events` is empty and `task_versions.status` / `tasks.status` are frozen at `pending`. `add-task-read-api`'s endpoints therefore report a task that never starts.

Concrete current state (read before designing):

- **Queue already exists.** `messaging/topology.go` declares `q.task.events` (quorum) bound `event.#` on `task.events`. No topology change needed.
- **Wire format.** `worker/core/publisher.py:67` emits JSON `{task_id, version_id, run_id, seq, kind, payload, ts}`, headers `idempotency_key=<run_id>:<seq>`, `message_id=<run_id>:<seq>`, routing key `event.<task_type>.<kind>`.
- **Emitted kinds (MVP).** The worker emits `status` (`payload.status` ∈ {`running`, `succeeded`}), `error` (`payload {code, message}`), and the agent loop emits `plan` / `step`. There is **no** `status:failed` event — the error path (`consumer.py` `_publish_error` / `_publish_unimplemented`) emits `kind=error` then calls `mark_run_terminal(failed)` and nacks. `queued` / `paused` / `cancelling` are not emitted by any current worker code.
- **`task_runs.status` is worker-owned.** The worker claims the run (`running`) and calls `mark_run_terminal` itself. Per ARCHITECTURE §6.1 this is intentional ("Worker 先持久化 status=running 再 ack").
- **Insert query exists.** `InsertTaskEvent` is already `ON CONFLICT (run_id, seq) DO NOTHING`.
- **No consumer infra yet.** `messaging/` has `Connection` (with `Channel()` + reconnect), `Publisher`, and the `Relayer`. There is no inbound consumer loop.

Constraints: AGENTS.md §4.1 — "任何状态翻转走 Domain Service 的状态机方法，禁止裸 UPDATE"; outbox/idempotent-consumer is the consistency model; ARCHITECTURE is source of truth where AGENTS.md conflicts.

## Goals / Non-Goals

**Goals:**
- Persist the worker event stream into `task_events` idempotently.
- Own and drive `task_versions.status` + `tasks.status` from `status`/`error` events — giving `tasks.status` a real writer (pays off `add-task-create-api` Open Question #4).
- Be correct under at-least-once, possibly out-of-order, multi-replica delivery.

**Non-Goals:**
- **`task_runs.status`** — stays worker-owned (per user decision; matches ARCHITECTURE §6.1). The consumer never reads or writes it.
- **WebSocket fan-out** — the Realtime Gateway that pushes these events to browsers is a separate later change (`add-realtime-gateway`). This change is the DB-persistence + state-sync half only.
- **Cost events** — `cost.events` / `q.cost.events` is a different queue owned by the future Cost Service.
- **Control-driven statuses** (`paused`/`cancelling`/`cancelled`) — no worker emits them yet; arrive with `add-task-control-api` + `add-worker-control-handling`. The mapping is defined defensively but these paths are untested here.
- **No DB migration** — every touched column exists in migration 0002.

## Decisions

### D1 — A generic `Consumer` in `infrastructure/messaging`, mirroring the Relayer

Add `consumer.go`: opens a channel on the shared `Connection`, sets `Qos(prefetch)`, calls `Consume(queue, manual-ack)`, and dispatches each `amqp.Delivery` to a registered handler. On channel/connection close it re-subscribes (the `Connection` already reconnects underneath). This keeps the messaging boundary owning all AMQP details, symmetric with `Publisher`/`Relayer`.

- **Run/Stop shape** mirrors the Relayer exactly: `atomic.Bool stopped` + ctx (`outbox_relayer.go:70`). `Connection.Channel()` (`connection.go:121`) returns a *fresh* channel and errors while the conn is down; the re-subscribe loop must back off and retry `Channel()` until the underlying `watchLoop` reconnects (same lazy pattern as `publisher.go:62`) (S10).
- **The handler is a plain func tested without a broker** — it takes the decoded delivery and returns an ack/nack decision (or `(action, error)`), so unit tests can drive malformed/transient/permanent paths with a fake delivery; no AMQP integration harness is needed for those (S14).

- *Alternative rejected*: folding consume logic into `cmd/api/main.go` — leaks AMQP into wiring and is untestable.

### D2 — No advisory-lock leader election (unlike the Relayer)

The Relayer is a singleton-per-DB because it drains a table; a competing consumer on a **work queue** is the opposite — RabbitMQ load-balances deliveries across all subscribers, and processing is idempotent (D4/D6). So every API replica runs a consumer; they share the queue. More replicas = more throughput, no coordination.

- *Alternative rejected*: advisory lock around the consumer — needlessly serialises ingestion and wastes the quorum queue's fan-out.

### D3 — One transaction per message: persist + transition, then ack

The handler opens a pgx tx, runs `InsertTaskEvent`, then (for `status`/`error`) the CAS status updates, commits, then acks. This makes "event recorded" and "state advanced" atomic, so a crash between them can't leave the read API showing a persisted event with a stale status (or vice-versa). Ack strictly follows commit (at-least-once; redelivery is safe by D4/D6).

### D4 — Idempotency is structural, not a dedupe cache

`task_events` insert is `ON CONFLICT (run_id, seq) DO NOTHING` and every status write is a guarded CAS (D6). So a redelivered message re-runs the same tx to a no-op. No seen-set, no Redis. The `(run_id, seq)` unique index is the dedupe key.

### D5 — Event-kind → action mapping

| `kind` | action |
|---|---|
| `status` (payload.status known) | transition version + (if current) task to mapped status |
| `status` (payload.status unknown) | persist only, no transition |
| `error` | transition version + (if current) task to `failed` |
| `plan`, `step`, anything else | persist only |

Treating `error` as the failure trigger (per user decision) is required because the worker never emits `status:failed`. The error `payload {code, message}` is preserved in the `task_events` row for the read API / debugging.

### D6 — Status mapping + terminal guard in SQL (compare-and-set)

Two new sqlc queries, both guarding in the `WHERE`:

```sql
-- name: UpdateVersionStatus :execrows
UPDATE task_versions SET status = $2
WHERE id = $1
  AND status NOT IN ('succeeded','failed','cancelled')   -- terminal guard
  AND status IS DISTINCT FROM $2;                          -- real-transition guard (S13)

-- name: UpdateTaskStatus :execrows
UPDATE tasks SET status = $2, updated_at = now()
WHERE id = $1
  AND current_version = $3                                 -- only the active version drives task
  AND status NOT IN ('succeeded','failed','cancelled')
  AND status IS DISTINCT FROM $2;
```

- **Two separate mapping functions** (not one), because the version and task status domains differ — `task_versions_status_check` allows `cancelling`/`queued` but `tasks_status_check` does **not**:
  - `versionTargetStatus(kind, payloadStatus) (Status, bool)` — identity over the 8 known version statuses; `kind=error` → `failed`; unknown → `(_, false)`.
  - `taskStatusFromVersion(Status) (Status, bool)` — `queued`→`pending`; `cancelling`→`(_, false)` (no task equivalent → skip `UpdateTaskStatus`, never push `cancelling` into `tasks.status` or it trips 23514); `pending`/`running`/`paused`/`cancelled`/`succeeded`/`failed`→1:1.
  - Both live in `domain/task` next to `status.go` and gate through the existing `taskStatuses` / `activeStatuses` maps so an unknown string can never reach the DB. (MVP note: the worker only emits `running`/`succeeded`/`failed`+`error` today, so `queued`/`cancelling`/`paused`/`cancelled` paths are defensive, not yet exercised — they arrive with the control-API changes.)
- `status IS DISTINCT FROM $2` makes `:execrows` reflect a *real* transition: a redelivered `running` while already `running` matches zero rows, so `EventStatusTransitionsTotal` is not double-counted (S13). The terminal guard alone would not prevent a `running→running` re-apply.
- The guard in SQL (not just Go) is what makes out-of-order and concurrent deliveries safe: a late `running` after `succeeded` matches zero rows.
- `current_version = $3` ties the task update to the active version without a separate read: only the current version moves the task, per ARCHITECTURE §4.3.
- Setting `status='succeeded'` automatically flips the **generated** `is_active` column to false (migration 0002), freeing the `one_active_version_per_task` slot — the consumer never writes `is_active` itself (S16).

### D7 — Transition applied via a Domain Service method, not raw UPDATE in the consumer

`domain/task` gains `event_sync.go` with e.g. `Service.IngestEvent(ctx, evt)` that runs the tx (insert + the two CAS calls + mapping). The messaging handler only decodes the envelope and calls this method — keeping all state-machine knowledge in the domain layer per AGENTS.md §4.1, consistent with how `CreateTask`/`IterateTask` already live there.

Decode details (verified against `worker/core/publisher.py`):
- The inbound body is the worker's **bare** `TaskEvent` JSON — `{task_id, version_id, run_id, seq, kind, payload, ts}`. **Do NOT reuse `messaging.Envelope`**; that `{msg_id, idempotency_key, payload, occurred_at}` wrapper is API→worker (Relayer) only. An implementer copying the Relayer will get this wrong by default (S1).
- The dedupe key is the **body's** `(run_id, seq)` (the `task_events` unique index), not the AMQP `idempotency_key` header — the header is informational (S2).
- Decode `payload` as `json.RawMessage` to preserve the worker's exact bytes for the `Payload []byte` column (no re-marshal). Branch on `kind` **first**: `kind=error` targets `failed` and never reads `payload.status` (error payload is `{code,message}`, no `status` key); `kind=status` reads `payload.status`; other kinds persist-only (S15).
- ids arrive as strings → `uuid.Parse` then `toPgUUID(...)`. `toPgUUID`/`fromPgUUID` are **unexported** in package `task` (`service.go:451`); since `IngestEvent` lives in the same package it uses them directly — keep the conversion inside the domain method, not the messaging handler (S3). The CAS calls run on the tx-bound `s.Queries.WithTx(tx)`, and are issued via the generated params structs (`sqlc.UpdateTaskStatusParams{ID, Status, CurrentVersion}`), not positional args (S5, S6).

### D8 — Malformed vs transient vs permanent failure → DLQ / requeue, via an explicit classifier

A binary "decode-fail → DLQ, else requeue" is **too coarse** and would loop poison messages forever: a message that decodes fine but can never commit (e.g. a `payload.status` that slips past the Go mapping and trips the `23514` CHECK, or a `23502` not-null) would be requeued endlessly. The codebase has **no** transient-vs-permanent pgx classifier today, so this change adds one.

Three outcomes:
- **Malformed** — body undecodable / missing required envelope field → `Nack(requeue=false)` (DLQ), increment `EventIngestMalformedTotal`. Decoding happens in the handler before any DB call.
- **Permanent processing error** — `IngestEvent` returns an error that `isRetryable` rejects (any `pgconn.PgError` with class `23xxx`, and other non-retryable codes) → `Nack(requeue=false)` (DLQ). These can never succeed on redelivery.
- **Transient processing error** — `isRetryable(err)` is true: `pgconn.PgError` class `08*` (connection) / `53*` (insufficient resources) / `40001` (serialization_failure) / `40P01` (deadlock_detected), `context.DeadlineExceeded`, or pool-acquire failures → `Nack(requeue=true)`: redeliver and retry.

`isRetryable(err) bool` is a small helper (messaging package, or shared with domain). Default for an unclassifiable error is **non-retryable → DLQ** (safer than an infinite loop; DLQ has alerting). This is the single biggest correctness gap the review surfaced.

### D9 — Observability

`observability.Metrics` gains: `EventsIngestedTotal{kind}`, `EventStatusTransitionsTotal` (incremented when a CAS affected ≥1 row), `EventIngestMalformedTotal`, and an `EventConsumerConnected` gauge. Structured log per event with `task_id`/`version_id`/`run_id`/`seq`/`kind`. Mirrors the Relayer's existing metric style.

## Risks / Trade-offs

- **[Two writers touch `task_runs` vs `task_versions`/`tasks` separately]** The worker owns `task_runs.status`; this consumer owns the other two. They can momentarily disagree (e.g. worker marked run `succeeded`, consumer hasn't processed the `status:succeeded` event yet). → Acceptable: they converge within one event round-trip; the read API already treats `task.status` as the authoritative user-facing field and `task_runs` as execution detail.
- **[`error` always maps to `failed`]** A future worker that emits `error` as a non-fatal warning would be mis-handled. → Today no such usage exists (error path always also marks the run failed). Documented as an Open Question; revisit when control/retry semantics land.
- **[Event for a version whose task row is mid-iterate]** `current_version` could change between the worker emitting and the consumer applying. → The `current_version = $3` guard means a stale event simply won't move the task (the new active version owns the task status); the version's own row still updates under its terminal guard. Safe.
- **[Poison message to DLQ is fire-and-forget for MVP]** DLQ has no auto-reconciler (per ARCHITECTURE §12.1 "DLQ 仅做告警"). → Counter + alert only; manual drain. Consistent with MVP scope.
- **[Out-of-order within a run]** `seq` is monotonic per run but the consumer does not order by it; it relies on the terminal guard + 1:1 mapping. A `running` arriving after `succeeded` is correctly ignored; a `step` arriving after `status:running` is just persisted. No reordering buffer needed for the states MVP emits.
- **[No observable `queued` phase]** The version is created `pending` (`service.go:389`) and the worker's first event is `status:running` — it never emits `queued` on the version. So `task_versions.status` jumps `pending → running`, and `tasks.status` likewise. This matches "task.status is the derived state of the current active version"; integration tests must seed the version as `pending` (not `queued`) as the realistic precondition (S8).

## Migration Plan

1. Append the two CAS queries; `make sqlc`.
2. Land `consumer.go`, `event_ingest.go`, `event_sync.go`, metrics, and `main.go` wiring.
3. Deploy: the consumer attaches to the existing `q.task.events`; no schema change, no topology change, so rollout is additive and rollback = stop consuming (events buffer in the durable quorum queue until a consumer returns).
4. Backfill is not required — events already in the queue will be drained on first start.

## Open Questions

1. **Non-fatal `error` events** — if the worker later distinguishes recoverable warnings from fatal errors, the `error → failed` mapping (D5) must be revisited (e.g. gate on `payload.fatal` or a dedicated kind).
2. **`paused`/`cancelling`/`cancelled` transitions** — defined in the mapping but exercised only once `add-task-control-api` + `add-worker-control-handling` emit them; their scenarios are out of scope here and should be covered by those changes.
3. **`task_runs.status` reconciliation** — deliberately not done here. If worker-vs-consumer divergence proves visible in practice, a reconciler (or moving the run writer to the consumer) can be proposed later.
