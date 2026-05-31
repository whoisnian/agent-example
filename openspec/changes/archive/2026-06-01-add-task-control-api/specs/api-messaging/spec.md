## MODIFIED Requirements

### Requirement: RabbitMQ Topology Declaration

At startup, the service SHALL idempotently declare the messaging topology defined in `docs/ARCHITECTURE.md §3.6`:
- exchange `task.exchange` (type `topic`, durable),
- exchange `task.control` (type `topic`, durable) — retyped from `direct` to `topic` by `add-task-control-api` so workers can wildcard-subscribe by `task_id` (routing-key convention `task.<task_id>`),
- exchange `task.events` (type `topic`, durable),
- exchange `task.dlx` (type `direct`, durable),
- exchange `cost.exchange` (type `topic`, durable),
- queue `q.task.events` bound to `task.events` with routing key `event.#`,
- queue `q.cost.events` bound to `cost.exchange` with routing key `cost.#`,
- queue `q.task.dlq` bound to `task.dlx` for dead-letter routing.

All declared queues SHALL be `quorum` type. Per-worker / per-lane execute queues are declared lazily by the worker side and are out of scope for this capability. The same applies to per-worker control queues bound to `task.control`.

The declaration step SHALL include a small set of "retypable exchanges" — exchanges whose `type` was changed by a previous OpenSpec change — and pre-delete them before re-declaring, so the topology can evolve. For `add-task-control-api`, that set contains exactly `task.control`. The pre-delete uses `ExchangeDelete(name, ifUnused=false, noWait=false)` from `amqp091-go` — `ifUnused=false` lets the delete proceed even when bindings exist, and `noWait=false` blocks until the broker confirms before the subsequent `ExchangeDeclare`. (There is no `if-empty` argument on exchange deletion; that's a queue-deletion semantic.)

The retypable-exchanges list MUST be **append-only across releases**: once an exchange name enters it, future versions of the API MUST keep that entry indefinitely so an operator rolling forward against a database whose corresponding exchange is still the *old* type (because they skipped this version) can still recover the right declaration. Removing an entry would silently regress the FAIL-FAST behavior on stale environments.

#### Scenario: Topology is idempotent across restarts
- **WHEN** the service restarts against a RabbitMQ that already has all topology declared with the current types
- **THEN** declaration MUST succeed without error and MUST NOT modify existing entities

#### Scenario: Topology fails fast on incompatible existing entity
- **WHEN** an existing exchange has a different type than declared AND it is NOT in the retypable set
- **THEN** startup MUST fail with a fatal log naming the conflicting entity, and the process MUST exit non-zero

#### Scenario: Retypable exchange is re-declared
- **GIVEN** `task.control` was previously declared as `direct` (pre-add-task-control-api)
- **WHEN** the service starts up with the updated topology code
- **THEN** the existing `task.control` MUST be deleted then re-declared as `topic`; startup MUST succeed; the pre-delete step MUST run before any `ExchangeDeclare` call for that exchange (so the FAIL-FAST scenario above never trips for entries that are in the retypable set)

### Requirement: Outbox Relayer

The service SHALL run a background Outbox Relayer that:
- scans `outbox` rows with `status='pending' AND (next_retry_at IS NULL OR next_retry_at <= now())` in batches of configurable size (default 100),
- publishes each row via the Publisher abstraction to the exchange named on the row (`outbox.exchange`, added by `add-task-control-api`); the row's `topic` column supplies the routing key,
- on success, updates `status='sent'` in the same transaction as marking `attempts+1`,
- on failure, updates `attempts+1`, sets `next_retry_at = now() + backoff(attempts)` using exponential backoff with jitter (base 2s, cap 5m), and after `attempts >= max_attempts` (default 10) sets `status='failed'` and emits a metric.

The Relayer MUST NOT carry an implicit "default exchange" constant; the per-row `exchange` is authoritative. Migration `0006_outbox_exchange` backfills existing rows to `'task.exchange'` so the change is transparent for the task-execute path.

Only one Relayer instance MAY be actively publishing per database; coordination uses an advisory lock `pg_try_advisory_lock(<relayer_lock_id>)`.

#### Scenario: Pending row is published and marked sent
- **WHEN** a row with `status='pending'` and `exchange='task.exchange'` is scanned and the Publisher returns success
- **THEN** the row MUST be updated to `status='sent'`, `attempts` MUST be incremented by 1, and a `mq_outbox_published_total` metric MUST be incremented

#### Scenario: Control row is published to the control exchange
- **WHEN** a row with `status='pending'`, `exchange='task.control'`, and `topic='task.{uuid}'` is scanned
- **THEN** the Publisher MUST be invoked with exchange `task.control` and routing key `task.{uuid}` (NOT `task.exchange`), AND the row MUST end at `status='sent'` on success

#### Scenario: Failed publish backs off
- **WHEN** publishing fails for a row with current `attempts=2`
- **THEN** the row MUST have `attempts=3` and `next_retry_at` set approximately `now() + 8s ± jitter`, and the row MUST remain `status='pending'`

#### Scenario: Max attempts moves to failed
- **WHEN** publishing fails for a row whose `attempts` would reach `max_attempts`
- **THEN** the row MUST be updated to `status='failed'`, `outbox_failed_total` MUST be incremented, and no further publish attempts MUST occur for that row

#### Scenario: Single-active relayer via advisory lock
- **WHEN** a second API instance attempts to acquire the relayer advisory lock while the first holds it
- **THEN** the second MUST skip its scan tick and log at debug level
