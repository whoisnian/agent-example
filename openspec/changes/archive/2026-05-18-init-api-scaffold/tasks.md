## 1. Project Skeleton

- [x] 1.1 `cd api && go mod init github.com/whoisnian/agent-example/api` (module path recorded in `api/README.md`)
- [x] 1.2 Create the directory layout from design D9 (`cmd/api`, `internal/{interfaces/http,application,domain,infrastructure/{persistence,messaging,observability,config},pkg}`, `migrations/`, `queries/`)
- [x] 1.3 Add `api/Makefile` with targets: `run`, `build`, `test`, `test-integration`, `lint`, `vet`, `sqlc`, `migrate-up`, `migrate-down`, `migrate-force`, `tidy`
- [x] 1.4 Add `api/.golangci.yml` with the lint set listed in design D10
- [x] 1.5 Add `.editorconfig` / `.gitignore` entries for Go build artifacts

## 2. Configuration

- [x] 2.1 Implement `internal/infrastructure/config/config.go` loading env vars (HTTP addr, DB DSN + pool params, RMQ URL, log level, drain timeout, OTLP endpoint, `DB_MIGRATE_ON_BOOT`) with `caarlos0/env`
- [x] 2.2 Support optional `--config <path>` YAML overlay; env wins over YAML
- [x] 2.3 Fail fast on missing required keys with fatal log naming each missing key (covers `api-bootstrap` "Configuration Loading")
- [x] 2.4 Unit test: env override, YAML overlay, missing-required produces non-zero exit and structured log

## 3. Observability Foundation

- [x] 3.1 `infrastructure/observability/logger.go`: build `slog` JSON handler with configurable level; helper to inject `trace_id`, `request_id`, `task_id` from context
- [x] 3.2 `infrastructure/observability/tracing.go`: init OTel tracer, OTLP exporter (env-configurable; log-only fallback), W3C propagator
- [x] 3.3 `infrastructure/observability/metrics.go`: build Prometheus registry; expose `http_requests_total`, `http_request_duration_seconds`, `http_panics_total`, plus runtime collectors
- [x] 3.4 Smoke test: process boots, `/metrics` returns 200 with at least the `http_requests_total` series declared (post-handler wiring)

## 4. HTTP Server & Middleware Chain

- [x] 4.1 `interfaces/http/server.go`: assemble Gin engine + middleware order: request_id → tracing → metrics → access-log → recovery → auth (no-op stub) → handler
- [x] 4.2 `interfaces/http/envelope.go`: response writer helpers for `{code, message, data, trace_id}` (success + error variants)
- [x] 4.3 `interfaces/http/errors.go`: domain-error → HTTP status + code mapping table per design D11
- [x] 4.4 `interfaces/http/recovery.go`: panic recovery middleware writes 500 envelope, logs stack with `trace_id`, increments `http_panics_total`
- [x] 4.5 `interfaces/http/health.go`: `GET /healthz` (always 200), `GET /readyz` (probes registered dependencies, returns 503 + failed list)
- [x] 4.6 `interfaces/http/server.go` exposes `/metrics` (exempt from envelope, no auth middleware) via `promhttp` handler
- [x] 4.7 Test: envelope shape on success & error; panic recovery returns 500; readiness flips to 503 when injected probe fails

## 5. Lifecycle & Entry

- [x] 5.1 `cmd/api/main.go`: parse flags, load config, init observability, init DB, init MQ, build HTTP server, register dependency probes for `/readyz`
- [x] 5.2 Trap SIGINT/SIGTERM; on shutdown: stop accepting new conns, drain in-flight requests up to `SHUTDOWN_DRAIN_TIMEOUT` (default 30s), then force-close
- [x] 5.3 Order shutdown: HTTP server → Outbox Relayer stop → MQ connection close → DB pool close → tracer flush
- [ ] 5.4 Test: integration test boots the binary, hits `/healthz`, sends SIGTERM, asserts exit code 0 and forced-close warning emitted when an in-flight request exceeds drain timeout — **DEFERRED**: requires bin build + docker; covered partially by unit tests in `interfaces/http/server_test.go`

## 6. Persistence Foundation

- [x] 6.1 `infrastructure/persistence/pgxpool.go`: build `pgxpool.Config` from `config`, open pool, run `SELECT 1` probe, expose `Close`
- [x] 6.2 `infrastructure/persistence/migrate.go`: wrap `golang-migrate` with `up/down/version/force` operations; expose CLI subcommands under `cmd/api migrate ...`
- [x] 6.3 `migrations/0001_init_outbox.up.sql` / `.down.sql`: create `outbox` table + `(status, next_retry_at)` index per `api-persistence` spec
- [x] 6.4 `sqlc.yaml` + `queries/outbox.sql` (scan-pending, mark-sent, increment-attempts-with-backoff, mark-failed). Note: outbox relayer uses raw pgx per design D2; sqlc-generated code dir is reserved for future business queries
- [x] 6.5 Register a DB probe with `/readyz` (acquires connection + `SELECT 1` with 1s timeout)
- [ ] 6.6 Integration test (testcontainers-postgres): migrations up→down→up is clean, `dirty=false`, `outbox` schema matches spec — **DEFERRED**

## 7. Messaging Foundation

- [x] 7.1 `infrastructure/messaging/connection.go`: dial RabbitMQ with auto-reconnect loop; expose `Channel()` factory; track `last_connected_at` for readiness
- [x] 7.2 `infrastructure/messaging/topology.go`: idempotently declare exchanges + queues + bindings per `api-messaging` spec; fail fast on type conflict
- [x] 7.3 `infrastructure/messaging/publisher.go`: typed envelope + `Publish(ctx, exchange, routingKey, msg)`; enable publisher confirms; block on ack with 5s timeout; inject `traceparent` header; emit `mq_publish_duration_seconds`, `mq_publish_failures_total`
- [x] 7.4 Register an MQ probe with `/readyz` that returns 503 only after sustained 10s disconnect
- [ ] 7.5 Integration test (testcontainers-rabbitmq): re-running topology is idempotent; conflicting existing type causes fatal exit; publisher confirms ack + nack paths — **DEFERRED**

## 8. Outbox Relayer

- [x] 8.1 `infrastructure/messaging/outbox_relayer.go`: tick loop (default 1s); per tick attempt `pg_try_advisory_lock(<id>)`; on success scan a batch (default 100) of pending rows
- [x] 8.2 Publish each row via `Publisher`; on ack mark `status='sent'`, `attempts+1`
- [x] 8.3 On failure increment attempts, set `next_retry_at = now() + backoff(attempts)` (base 2s, cap 5m, full jitter), keep `status='pending'`
- [x] 8.4 When `attempts` reaches `max_attempts` (default 10) set `status='failed'` and increment `outbox_failed_total`
- [x] 8.5 Metrics: `mq_outbox_pending`, `mq_outbox_published_total`, `outbox_failed_total`, `outbox_relayer_lock_owner` (gauge)
- [x] 8.6 Unit test with in-memory fake store covers state transitions, backoff math, and lock-skip behavior. Note: testcontainers-PG integration test (asserting real advisory-lock concurrency) is **DEFERRED**

## 9. Local Dev & CI

- [x] 9.1 `docker-compose.dev.yml` at repo root: postgres:16 (port 5432, healthcheck), rabbitmq:3.13-management (ports 5672/15672), optional jaeger via `profiles: [trace]`
- [x] 9.2 `api/README.md`: documents local startup (`docker compose up -d`, `make migrate-up`, `make run`), env var matrix, migration force procedure for dirty-state recovery
- [x] 9.3 `.github/workflows/api-ci.yml`: triggers on `api/**` + workflow file; jobs: vet, lint, test (-race), build; pinned Go 1.26 + golangci-lint v2.x
- [ ] 9.4 Verify CI green on this branch before requesting review — **PENDING**: requires push to a branch + CI run

## 10. Acceptance

- [ ] 10.1 `make run` boots against `docker-compose.dev.yml`; `curl /healthz` → 200; `curl /readyz` → 200; `curl /metrics` → exposes `http_requests_total` — **PENDING**: requires running stack to validate end-to-end
- [ ] 10.2 Stop PostgreSQL; `/readyz` flips to 503 within 1s and lists `postgres` as failed dependency — **PENDING**: requires running stack
- [ ] 10.3 Stop RabbitMQ; `/readyz` flips to 503 within ~10s (per sustained-disconnect rule) and lists `rabbitmq` — **PENDING**: requires running stack
- [ ] 10.4 Seed a row into `outbox`; observe Relayer publishes within one tick and marks it `sent` — **PENDING**: requires running stack
- [x] 10.5 All scenarios from `specs/api-bootstrap/spec.md`, `specs/api-persistence/spec.md`, `specs/api-messaging/spec.md` have corresponding unit tests passing. Some scenarios are partial-coverage (covered by unit-with-fakes, not testcontainers); those acceptance items are deferred above (5.4, 6.6, 7.5, 8.6-int, 9.4, 10.1-10.4)

---

## Apply Summary

**Completed:** 39/50 tasks. Service skeleton builds clean (`go build ./...`), `go vet` passes, all unit tests pass (`go test -race -count=1 ./...`).

**Deferred (require Docker / running stack / network):** 5.4, 6.6, 7.5 (testcontainers integration tests); 9.4 (CI run); 10.1–10.4 (end-to-end smoke against running compose).

These are explicitly testcontainers / live-stack items. Skeleton correctness is verified by unit tests with fakes (`internal/infrastructure/messaging/outbox_relayer_test.go`, `interfaces/http/server_test.go`, `infrastructure/observability/logger_test.go`, `infrastructure/config/config_test.go`).
