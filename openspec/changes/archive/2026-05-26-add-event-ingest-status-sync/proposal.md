## Why

The write endpoints (`add-task-create-api`) and read endpoints (`add-task-read-api`) are live, but nothing on the API side consumes what the worker emits. After a worker runs, `task_events` stays empty and `task_versions.status` / `tasks.status` never move off `pending` — so `GET /tasks/{id}` and `GET /versions/{id}/events` always report a task that never started. This change is the keystone of the "submit → execute → observe result" loop: it ingests the worker's `event.<task_type>.<kind>` stream, persists it, and drives the version/task state machine. It also gives `tasks.status` a real owner, paying off Open Question #4 logged in `add-task-create-api`.

## What Changes

- Add an **event-ingest consumer** on the API side that subscribes to the existing `q.task.events` queue (binding `event.#` on `task.events`) and processes each message in a single DB transaction:
  - **Persist** every event into `task_events`, idempotent on `(run_id, seq)` (reuses the existing `InsertTaskEvent` query).
  - **Drive the state machine** for `task_versions.status` and `tasks.status` via Domain Service methods (no raw `UPDATE` in the consumer, per AGENTS.md §4.1).
- Treat a `kind=status` event (payload `{status: ...}`) as a version/task transition; treat a `kind=error` event as a `failed` transition — the worker's error path emits `error` without a trailing `status:failed`, so the consumer must recognise both.
- Apply a **terminal guard**: a version/task already in a terminal state (`succeeded`/`failed`/`cancelled`) is never moved by a later or out-of-order event (monotonic CAS in the `WHERE` clause).
- `tasks.status` is mirrored from the version's new status **only when that version is the task's `current_version`** (the active version), matching ARCHITECTURE §4.3's "task.status is the derived state of the current active version".
- **Out of scope (by decision):** `task_runs.status` stays worker-owned (ARCHITECTURE §6.1); the consumer never writes it. The Realtime Gateway WS fan-out (consuming the same queue for push) remains a later change — this consumer only does the DB-persistence + state-sync half.
- Add a generic AMQP **Consumer** wiring in `infrastructure/messaging` (manual ack after commit, prefetch, reconnect), mirroring the existing Relayer/Publisher shape.
- Add observability: ingest counters, status-transition counters, and a consumer-connected gauge.

## Capabilities

### New Capabilities
- `task-event-ingest`: API-side consumption of the worker `task.events` stream — idempotent persistence into `task_events` plus the `task_versions` / `tasks` state-machine transitions derived from `status` and `error` events, including the terminal guard and ack/requeue/DLQ semantics.

### Modified Capabilities
<!-- None. The `q.task.events` queue + `event.#` binding already exist in api-messaging topology (declared by add-api-messaging); this change adds a consumer over the existing topology without changing the messaging contract. State-machine writes are new behaviour captured wholly by the new capability above. -->

## Impact

- **New code**
  - `api/internal/infrastructure/messaging/consumer.go` — generic consumer loop (subscribe, prefetch, manual ack, reconnect).
  - `api/internal/infrastructure/messaging/event_ingest.go` — the `q.task.events` handler that decodes the envelope and calls the domain service inside a tx.
  - `api/internal/domain/task/event_sync.go` — `Service` method(s) that, in one tx, insert the event and apply the version/task transition via the status mapping + terminal guard.
- **Modified code**
  - `api/queries/task_versions.sql`, `api/queries/tasks.sql` — append CAS status-update queries (`UpdateVersionStatus`, `UpdateTaskStatus`); regenerate sqlc.
  - `api/internal/infrastructure/observability/metrics.go` — new counters/gauge.
  - `api/cmd/api/main.go` — construct and start the consumer alongside the Relayer.
  - `api/README.md` — document the new env knobs (prefetch, etc.) and the consumer.
- **Reused, unchanged**: `InsertTaskEvent` query, `task.events` exchange/queue/binding (already declared), the connection/reconnect machinery.
- **Dependencies**: none new; uses existing `amqp091-go`, `pgx`, sqlc.
- **No DB migration**: all touched tables/columns already exist (migration 0002).
