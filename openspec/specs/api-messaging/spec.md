# api-messaging Specification

## Purpose
TBD - created by archiving change init-api-scaffold. Update Purpose after archive.
## Requirements
### Requirement: RabbitMQ Topology Declaration

At startup, the service SHALL idempotently declare the messaging topology defined in `docs/ARCHITECTURE.md §3.6`:
- exchange `task.exchange` (type `topic`, durable),
- exchange `task.control` (type `direct`, durable),
- exchange `task.events` (type `topic`, durable),
- exchange `task.dlx` (type `direct`, durable),
- exchange `cost.exchange` (type `topic`, durable),
- queue `q.task.events` bound to `task.events` with routing key `event.#`,
- queue `q.cost.events` bound to `cost.exchange` with routing key `cost.#`,
- queue `q.task.dlq` bound to `task.dlx` for dead-letter routing.

All declared queues SHALL be `quorum` type. Per-worker / per-lane execute queues are declared lazily by the worker side and are out of scope for this capability.

#### Scenario: Topology is idempotent across restarts
- **WHEN** the service restarts against a RabbitMQ that already has all topology declared
- **THEN** declaration MUST succeed without error and MUST NOT modify existing entities

#### Scenario: Topology fails fast on incompatible existing entity
- **WHEN** an existing exchange has a different type than declared
- **THEN** startup MUST fail with a fatal log naming the conflicting entity, and the process MUST exit non-zero

### Requirement: Startup Connectivity Check

After topology declaration, the service SHALL verify connectivity by opening a channel and closing it cleanly. Failure MUST abort the process with a non-zero exit code and a fatal log entry naming RabbitMQ as the failed dependency.

#### Scenario: RabbitMQ unreachable at startup
- **WHEN** RabbitMQ is unreachable during startup probe
- **THEN** the process MUST exit non-zero before opening the HTTP listener

### Requirement: Publisher Abstraction

The service SHALL expose a `Publisher` interface providing `Publish(ctx, topic, routingKey, msg)` where `msg` is a typed envelope `{msg_id, idempotency_key, payload, occurred_at}`. The implementation MUST:
- enable publisher confirms on the underlying channel,
- block until the broker confirms `ack`, returning an error on `nack` or timeout (default 5s),
- set messages as persistent (`delivery_mode=2`),
- inject the current trace context as a `traceparent` header.

Direct use of `amqp091.Channel.Publish` outside this abstraction is forbidden in application code.

#### Scenario: Publish blocks until confirmed
- **WHEN** `Publisher.Publish` is called and the broker returns `ack`
- **THEN** the call MUST return `nil` after `ack` is observed (not before)

#### Scenario: Publish surfaces nack
- **WHEN** the broker returns `nack` or the confirm channel closes
- **THEN** `Publisher.Publish` MUST return a non-nil error and increment `mq_publish_failures_total`

### Requirement: Outbox Relayer

The service SHALL run a background Outbox Relayer that:
- scans `outbox` rows with `status='pending' AND (next_retry_at IS NULL OR next_retry_at <= now())` in batches of configurable size (default 100),
- publishes each row via the Publisher abstraction,
- on success, updates `status='sent'` in the same transaction as marking `attempts+1`,
- on failure, updates `attempts+1`, sets `next_retry_at = now() + backoff(attempts)` using exponential backoff with jitter (base 2s, cap 5m), and after `attempts >= max_attempts` (default 10) sets `status='failed'` and emits a metric.

Only one Relayer instance MAY be actively publishing per database; coordination uses an advisory lock `pg_try_advisory_lock(<relayer_lock_id>)`.

#### Scenario: Pending row is published and marked sent
- **WHEN** a row with `status='pending'` is scanned and the Publisher returns success
- **THEN** the row MUST be updated to `status='sent'`, `attempts` MUST be incremented by 1, and a `mq_outbox_published_total` metric MUST be incremented

#### Scenario: Failed publish backs off
- **WHEN** publishing fails for a row with current `attempts=2`
- **THEN** the row MUST have `attempts=3` and `next_retry_at` set approximately `now() + 8s ± jitter`, and the row MUST remain `status='pending'`

#### Scenario: Max attempts moves to failed
- **WHEN** publishing fails for a row whose `attempts` would reach `max_attempts`
- **THEN** the row MUST be updated to `status='failed'`, `outbox_failed_total` MUST be incremented, and no further publish attempts MUST occur for that row

#### Scenario: Single-active relayer via advisory lock
- **WHEN** a second API instance attempts to acquire the relayer advisory lock while the first holds it
- **THEN** the second MUST skip its scan tick and log at debug level

### Requirement: Readiness Reflects Messaging Health

The service's `/readyz` endpoint (defined in `api-bootstrap`) MUST return 503 when the underlying RabbitMQ connection is closed and no recovery is in progress. Transient reconnect attempts SHALL not flip readiness for the first 10 seconds of disconnection.

#### Scenario: Sustained disconnect fails readiness
- **WHEN** the RabbitMQ connection has been closed for more than 10 seconds with no successful reconnect
- **THEN** `GET /readyz` MUST return 503 and include `rabbitmq` in the failed-dependencies list

