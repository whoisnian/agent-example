# Implementation Tasks

## 1. sqlc queries (state-machine CAS)

- [ ] 1.1 Append `UpdateVersionStatus :execrows` to `api/queries/task_versions.sql` — `SET status=$2 WHERE id=$1 AND status NOT IN ('succeeded','failed','cancelled') AND status IS DISTINCT FROM $2` (terminal guard + real-transition guard so redelivery returns 0 rows).
- [ ] 1.2 Append `UpdateTaskStatus :execrows` to `api/queries/tasks.sql` — `SET status=$2, updated_at=now() WHERE id=$1 AND current_version=$3 AND status NOT IN ('succeeded','failed','cancelled') AND status IS DISTINCT FROM $2`.
- [ ] 1.3 Run `make sqlc`; confirm `UpdateVersionStatusParams` / `UpdateTaskStatusParams` are generated, both return `(int64, error)`, and the `Querier` interface gains both methods.

## 2. Domain layer — status mapping + ingest service

- [ ] 2.1 In `api/internal/domain/task/status.go` (or sibling), add **two** mapping functions (version and task status domains differ — version CHECK allows `cancelling`/`queued`, task CHECK does not):
  - `versionTargetStatus(kind string, payloadStatus string) (Status, bool)` — `kind=error`→`failed`; `kind=status`→identity over the 8 known version statuses (gated by validity); unknown→`(_, false)`.
  - `taskStatusFromVersion(Status) (Status, bool)` — `queued`→`pending`; `cancelling`→`(_, false)` (skip task update; never push `cancelling` into `tasks.status`); `pending`/`running`/`paused`/`cancelled`/`succeeded`/`failed`→1:1. Gate through the existing `taskStatuses`/`activeStatuses` maps.
- [ ] 2.2 Add an `IngestEvent` input type to `domain/task`: decoded envelope with `task_id`/`version_id`/`run_id` (string, parsed via `uuid.Parse`), `seq int64`, `kind string`, `payload json.RawMessage`, `ts`.
- [ ] 2.3 Create `api/internal/domain/task/event_sync.go` with `Service.IngestEvent(ctx, evt) (transitioned bool, err error)`: open tx via `s.Pool.BeginTx` + `defer Rollback`; call `q := s.Queries.WithTx(tx)`; `q.InsertTaskEvent(...)` (ids via `toPgUUID(uuid.Parse(...))`, `payload` as `[]byte`); resolve `versionTargetStatus`; if mapped, `q.UpdateVersionStatus(ctx, sqlc.UpdateVersionStatusParams{...})`; if `taskStatusFromVersion` maps, `q.UpdateTaskStatus(ctx, sqlc.UpdateTaskStatusParams{ID, Status, CurrentVersion: toPgUUID(versionID)})`; commit. Return `transitioned=true` when a CAS affected ≥1 row.
- [ ] 2.4 Branch on `kind` **first**: `kind=error`→target `failed` (do NOT read `payload.status`); `kind=status`→read `payload.status`, unknown/absent→persist only; all other kinds→persist only.
- [ ] 2.5 Unit-test the two mapping functions and the kind→action decision table in `event_sync_test.go` (pure, no DB) — include `cancelling`→version updates / task skipped, and `error`→failed.

## 3. Messaging — generic consumer

- [ ] 3.1 Create `api/internal/infrastructure/messaging/consumer.go`: a `Consumer` taking the `Connection`, queue name, prefetch, and a handler; opens a channel via `Connection.Channel()`, `Qos(prefetch,0,false)`, `Consume(queue, manual ack)`, dispatches deliveries. `Run(ctx)`/`Stop()` mirror the Relayer's `atomic.Bool stopped` + ctx shape.
- [ ] 3.2 Re-subscribe on `channel.NotifyClose`: loop calling `Connection.Channel()` with bounded backoff (it errors while the conn is down; retry until `watchLoop` reconnects) — same lazy re-open pattern as `publisher.go:62`.
- [ ] 3.3 Set `EventConsumerConnected` gauge to 1 while subscribed, 0 on disconnect.

## 4. Messaging — event-ingest handler

- [ ] 4.1 Create `api/internal/infrastructure/messaging/event_ingest.go`: define the inbound `taskEventEnvelope` struct (`task_id`,`version_id`,`run_id`,`seq`,`kind`,`payload json.RawMessage`,`ts`). **Do NOT reuse `messaging.Envelope`** (that wrapper is API→worker only). Dedupe is on the body's `(run_id, seq)`, not the AMQP header. Make the handler a plain func returning an ack/nack decision so it is unit-testable without a broker.
- [ ] 4.2 Add an `isRetryable(err) bool` classifier (messaging pkg): true for `pgconn.PgError` class `08*`/`53*` + codes `40001`/`40P01`, and `context.DeadlineExceeded`; false (→DLQ) for everything else including `23xxx`. Unclassifiable → false (DLQ), never infinite requeue.
- [ ] 4.3 Handler flow: decode fail / missing required field → `Nack(false,false)` (DLQ) + `EventIngestMalformedTotal`++. Else call `IngestEvent`: nil err → `Ack(false)`; `isRetryable` → `Nack(false,true)`; otherwise → `Nack(false,false)` (DLQ).
- [ ] 4.4 Increment `EventsIngestedTotal{kind}` on successful processing and `EventStatusTransitionsTotal` when `IngestEvent` reports a transition; structured log with `task_id`/`version_id`/`run_id`/`seq`/`kind`.

## 5. Observability

- [ ] 5.1 Add `EventsIngestedTotal` (counter, label `kind`), `EventStatusTransitionsTotal` (counter), `EventIngestMalformedTotal` (counter), `EventConsumerConnected` (gauge) to `observability/metrics.go`. Each must be (a) a struct field, (b) built in the `&Metrics{...}` literal, and (c) listed in the `reg.MustRegister(...)` block. Counter names keep the `_total` suffix per convention.

## 6. Wiring

- [ ] 6.1 In `api/cmd/api/main.go`, construct the event-ingest handler bound to the existing `domainSvc` (note: `domainSvc` is currently built *after* the relayer goroutine starts — construct the consumer below that, or move the relayer block down) and a `Consumer` over `messaging.QueueTaskEvents`; start it after `DeclareTopology` succeeds.
- [ ] 6.2 Add `EventConsumerPrefetch int \`env:"EVENT_CONSUMER_PREFETCH" envDefault:"16" yaml:"event_consumer_prefetch"\`` to `config.go` (no existing prefetch knob); update the defaults test in `config_test.go`. Don't hardcode the prefetch in main.go.
- [ ] 6.3 Graceful shutdown: `Stop()` the consumer in the same place the relayer is stopped — **before** `publisher.Close()` / `mqConn.Close()` — so the in-flight delivery isn't nacked into a closing channel.

## 7. Tests

- [ ] 7.1 Decide test home: the ingest integration tests exercise `Service.IngestEvent` against a real DB → add `internal/domain/task/event_sync_integration_test.go` (`//go:build integration`) reusing the container-bootstrap pattern from the existing `persistence`/`interfaces/http` suites (state which harness it reuses).
- [ ] 7.2 `TestIngestStatusRunning`: seed task+version with `current_version` set and version status `pending`; ingest `status:running`; assert version `running`, task `running`, one `task_events` row.
- [ ] 7.3 `TestIngestSucceededReleasesActive`: `status:succeeded` → version `succeeded`, task `succeeded`, `GetActiveVersionByTask` returns no row (generated `is_active` flipped).
- [ ] 7.4 `TestIngestErrorFails`: `kind=error` (payload `{code,message}`, no `status`) → version `failed`, task `failed`, error payload stored in the `task_events` row.
- [ ] 7.5 `TestTerminalGuard`: apply `succeeded`, then a late `running` → status stays `succeeded`, task unchanged, both event rows persisted, second CAS affected 0 rows (no transition metric).
- [ ] 7.6 `TestNonCurrentVersionDoesNotMoveTask`: event for a non-`current_version` version updates only that version, leaves `tasks.status` unchanged.
- [ ] 7.7 `TestDuplicateEventNoop`: redeliver same `(run_id, seq)` → exactly one `task_events` row, status unchanged, `transitioned=false` on the redelivery (validates `IS DISTINCT FROM`).
- [ ] 7.8 `TestUnknownStatusPersistOnly`: `status` event with unrecognised `payload.status` → event persisted, no transition.
- [ ] 7.9 Handler unit tests (no broker, fake delivery): malformed body → DLQ decision + malformed counter; `isRetryable` (e.g. 40001) → requeue decision; permanent (23514) → DLQ decision. Also a focused unit test of `isRetryable` over representative SQLSTATEs.

## 8. Verification & docs

- [ ] 8.1 `go vet ./...`, `golangci-lint run ./...` (with and without `integration` tag) clean.
- [ ] 8.2 `go test -race -count=1 ./...` and `make test-integration` green.
- [ ] 8.3 Update `api/README.md` — add an "事件消费 / 状态同步" section: the consumer, the version/task ownership boundary (`task_runs` stays worker-owned), the `error→failed` rule, and the new `EVENT_CONSUMER_PREFETCH` env var.
