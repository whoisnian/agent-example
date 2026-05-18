## Context

`docs/ARCHITECTURE.md` already fixes the high-level shape of the API service (DDD layering, Outbox pattern, OTel-instrumented HTTP, RabbitMQ topology). This document records the concrete library and pattern choices needed to bring `api/` from an empty directory to a runnable skeleton — anything that would otherwise be re-decided per feature proposal.

Current state: `api/` contains only `README.md`. There is no Go module yet, no migration tooling, no Docker compose for local dev, no CI workflow targeting Go.

Constraints inherited from architecture:
- Go is the chosen language.
- PostgreSQL is the system of record; Outbox is the only sanctioned write→MQ path.
- Single-active Outbox Relayer per database.
- Health vs readiness must be split (k8s-style).
- Unified `{code, message, data, trace_id}` envelope for business endpoints.

## Goals / Non-Goals

**Goals:**
- Establish a Go service skeleton that builds, runs, exposes `/healthz`, `/readyz`, `/metrics`, and gracefully shuts down.
- Stand up PG connectivity, migration tooling, sqlc pipeline, and the `outbox` table.
- Stand up RabbitMQ topology, publisher confirms, and the Outbox Relayer loop.
- Pick libraries deliberately so subsequent feature proposals do not litigate them again.
- Provide a `docker-compose.dev.yml` that lets a contributor run the full local stack in one command.

**Non-Goals:**
- No business endpoints (`/tasks`, `/versions`, `/cost`, etc.).
- No authentication or authorization logic. The middleware chain reserves an `auth` slot but installs only a pass-through implementation.
- No WebSocket / Realtime Gateway; that is a separate service per ARCHITECTURE §3.2 and will be its own proposal.
- No worker-side queues (`q.task.execute.<lane>`, `q.task.control.<worker_id>`); declared lazily by the worker side, not by the API.
- No production deploy manifests (Helm/k8s). Local dev compose only.

## Decisions

### D1. HTTP framework: Gin

Chosen for stable middleware ecosystem, fast routing, and team familiarity. Alternatives considered:
- **Echo**: similar feature set; rejected because Gin has wider middleware adoption (`gin-contrib/*`) and our spec exposes nothing Echo-specific.
- **chi + stdlib `net/http`**: more idiomatic, but we would re-implement request ID, recovery, gzip, etc. For MVP velocity Gin wins.
- **gRPC + grpc-gateway**: overkill; clients are browser SPAs and a single Worker fleet.

If we later need streaming responses or low-allocation paths, the framework is encapsulated under `internal/interfaces/http/` and can be swapped without leaking into application/domain code.

### D2. Database driver: pgx v5 + pgxpool, queries via sqlc

`pgx` is the modern PG driver and integrates cleanly with sqlc. `database/sql` adds an unnecessary abstraction layer and weaker type support. Outbox Relayer is the single sanctioned consumer of raw `pgx` (batched scan + UPDATE), called out in `api-persistence` spec to avoid drift.

### D3. Migration tool: golang-migrate

Versioned SQL files, simple CLI, well-known dirty-state behavior. Alternatives:
- **goose**: similar; rejected for slightly weaker production tooling around forced versions.
- **Atlas**: more powerful schema-as-code, but adds a learning curve we don't need at MVP.
- **sqlc + automatic DDL**: sqlc is for queries, not migrations.

Migrations live under `api/migrations/` as paired `NNN_name.up.sql` / `NNN_name.down.sql` files.

### D4. RabbitMQ client: amqp091-go (official Go client)

The official maintained fork of `streadway/amqp`. Supports publisher confirms, connection auto-recovery (manual), and durable channels. Wrapped under `internal/infrastructure/messaging/` with our `Publisher` interface so application code never touches `amqp091.Channel.Publish`.

### D5. Logging: standard library `log/slog`

Go 1.26+ stdlib; structured, no third-party dep. We standardize fields and require correlation IDs to come from `context.Context`. Reject `zap`/`zerolog` to keep dependency surface small for the scaffold; if profiling shows hot-path overhead we can swap the handler implementation behind `slog`.

### D6. Observability: OpenTelemetry SDK + Prometheus client

- **Tracing**: `go.opentelemetry.io/otel` with HTTP propagator (`traceparent`). Exporter is configurable; defaults to OTLP (`localhost:4317`) in dev.
- **Metrics**: a Prometheus registry exposed at `/metrics`. We do not (yet) bridge to OTel metrics; the registry is exposed directly because Prometheus scraping is the MVP target and it avoids the OTel-metrics churn.

### D7. Configuration: env-only with optional YAML overlay

Env wins over YAML; YAML exists for local dev convenience. Loader is `github.com/caarlos0/env/v10` (or equivalent thin wrapper). No Viper — its global state is a known footgun.

### D8. Outbox Relayer concurrency model

A single goroutine inside the API process, gated by `pg_try_advisory_lock(<fixed_id>)`. Multiple API replicas elect a winner naturally via the lock; losers tick at idle. Rationale:
- No separate service to deploy at MVP.
- PG advisory lock is leader-election that survives crashes (auto-released on connection drop).
- When the loser sees the lock free, it picks up; no external coordinator (etcd/zk) needed.

If the Relayer later needs to outlive API lifecycle (e.g., when API has zero replicas during maintenance), it can be lifted into its own process — the spec already isolates it as a component.

### D9. Directory layout

```
api/
├── cmd/
│   └── api/main.go                    # entry, wires lifecycle + DI graph
├── internal/
│   ├── interfaces/
│   │   └── http/                      # gin engine, middleware, handlers (none yet)
│   ├── application/                   # use cases (empty in scaffold)
│   ├── domain/                        # entities, value objects (empty in scaffold)
│   ├── infrastructure/
│   │   ├── persistence/
│   │   │   ├── pgxpool.go
│   │   │   ├── migrate.go
│   │   │   └── sqlc/                  # generated; only outbox queries at scaffold time
│   │   ├── messaging/
│   │   │   ├── topology.go
│   │   │   ├── publisher.go
│   │   │   └── outbox_relayer.go
│   │   ├── observability/
│   │   │   ├── tracing.go
│   │   │   ├── metrics.go
│   │   │   └── logger.go
│   │   └── config/
│   │       └── config.go
│   └── pkg/                           # small cross-cutting helpers
├── migrations/
│   ├── 0001_init_outbox.up.sql
│   └── 0001_init_outbox.down.sql
├── queries/                           # sqlc input
│   └── outbox.sql
├── sqlc.yaml
├── Makefile
├── go.mod
├── go.sum
└── README.md
```

`internal/pkg/` deliberately separates project-wide helpers (errors, ids, contextkeys) from layer-specific code. Top-level `pkg/` is intentionally avoided in MVP — we don't yet have stable public APIs.

### D10. CI scope (this proposal)

GitHub Actions workflow `.github/workflows/api-ci.yml`:
- `go vet ./...`
- `golangci-lint run` (config at `api/.golangci.yml`, lint set: default + `gocritic`, `errorlint`, `gosec`)
- `go test ./... -race -count=1`
- `go build ./...`

Runs only when files under `api/**` or the workflow itself change.

### D11. Error mapping table (initial)

| Error kind | HTTP | code |
|---|---|---|
| validation / bad input | 400 | `invalid_argument` |
| auth missing/invalid | 401 | `unauthenticated` |
| forbidden | 403 | `permission_denied` |
| not found | 404 | `not_found` |
| conflict (e.g. active version exists) | 409 | `conflict` (or specific subcode like `active_version_exists`) |
| precondition failed | 412 | `failed_precondition` |
| rate limited | 429 | `resource_exhausted` |
| internal | 500 | `internal_error` |
| dependency down | 503 | `unavailable` |

Subcodes (e.g. `active_version_exists`) are introduced by the feature proposals that need them. The scaffold only defines the generic catalog and the envelope shape.

## Risks / Trade-offs

- **[Risk] Gin lock-in** → Mitigation: isolate router setup under `interfaces/http/server.go`; handlers depend on a thin context shim, not Gin types directly.
- **[Risk] Advisory-lock leader election can starve under flapping connections** → Mitigation: lock is acquired per Relayer tick (not per scan loop); a flapping replica simply tries again next tick. We log lock-skips at debug to keep noise down.
- **[Risk] Publisher-confirm blocking adds tail latency to outbox publishes** → Mitigation: Relayer runs out-of-band from request handlers; user latency is unaffected. We monitor `mq_publish_duration_seconds` and revisit if p99 exceeds 200ms.
- **[Risk] sqlc + pgx specifics leak into application layer via generated types** → Mitigation: repositories map sqlc types to domain types at the boundary; no sqlc type appears in `application/` or `domain/` packages. This is enforced via code review for the scaffold and will be lint-enforced later.
- **[Risk] golang-migrate dirty state requires manual intervention** → Mitigation: documented in `api/README.md` once written; `api migrate force <version>` provided. We accept this trade-off because it prevents silent half-applied schemas.
- **[Risk] slog is younger than zap/zerolog; some sinks may lag** → Mitigation: handler is constructed at one place (`observability/logger.go`); swap is local if needed.

## Migration Plan

This is the first code-bearing change for `api/`; there is no existing service to migrate from.

Rollback strategy: revert the commit / PR. Because no business state exists yet, rollback is risk-free. Future business proposals will define forward/back migration plans for their own schema additions.

## Open Questions

1. **OTLP exporter target in dev** — do we ship a Jaeger or Tempo container in `docker-compose.dev.yml`, or rely on a log-only exporter? **Tentative**: log-only exporter as default, opt-in Jaeger via compose profile.
2. **Should the Outbox Relayer live inside the API binary or a separate `cmd/relayer` from day one?** **Tentative**: inside API for MVP (per D8). Revisit if/when API HPA reaches zero replicas.
3. **Connection-pool sizing defaults** — `MaxConns=20` assumed; this should be revisited once we have load profiles. Out of scope for this proposal but flagged in spec.
4. **GitHub Actions Go version pin** — pinned to `1.26` (Feb 2026 release). `golangci-lint` v2.x line. Toolchain upgrade policy: bump in lockstep with module `go` directive minor versions.
