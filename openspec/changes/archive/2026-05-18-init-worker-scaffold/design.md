## Context

`docs/ARCHITECTURE.md §3.3` fixes the Worker shape (Python, deepagents, OSS-backed virtual FS, cost meter, checkpoint, plugin loader). What is unspecified at the architecture level: concrete Python toolchain, asyncio vs threaded model, how cost callbacks integrate with LangChain, how the plugin loader resolves entrypoints, and how Worker coexists with the API (event schema, table write boundary).

Current state: `worker/` contains only `README.md`. The `init-api-scaffold` proposal does not assume Worker exists; the two scaffolds may merge in any order. Where this proposal references MQ topology already declared by `init-api-scaffold`, we treat the exchange contract as authoritative and only declare worker-side queues here.

Constraints inherited from architecture:
- Worker writes only to: `task_runs.last_heartbeat`, `task_checkpoints`, `artifacts`, `cost_events` (via `cost.events` exchange — the actual INSERT happens in API-side Cost Service, but Worker emits the event).
- Wait — re-read §3.3: Worker emits `cost.events` MQ messages; Cost Service consumes and writes `cost_events`/`task_costs`. So Worker writes only `task_runs.last_heartbeat` and `task_checkpoints` and uploads to OSS, plus inserts `artifacts` rows (small metadata; large payloads are in OSS). This is the boundary.
- Topology of `task.exchange`, `task.events`, `task.control`, `task.dlx`, `cost.exchange` is declared by API; worker declares only its own queues.

## Goals / Non-Goals

**Goals:**
- Stand up a Python service skeleton that consumes one `task.execute` message end-to-end (parse → idempotency check → context build → dispatcher → DLX), exercising MQ / DB / OSS / metrics without any real agent.
- Lock in the Python toolchain (uv + ruff + mypy strict + pytest) and the async model so feature proposals don't re-litigate.
- Provide first-class hooks for cost capture (LangChain callback) and plugin registration so adding a real agent or tool is mechanical.
- Make the boundary between Worker-owned and API-owned writes auditable (lint / code review).

**Non-Goals:**
- Any real agent (`code_agent`, `research_agent`).
- Any non-stub plugin beyond `noop_tool`.
- Sandboxing (Docker / microVM). MVP runs everything inside the Worker process.
- Web/UI integration (separate proposal).
- Distributed locking across workers (none needed — RMQ delivery is the lock).
- Backpressure beyond `prefetch=1` (KEDA-based autoscaling is post-MVP).

## Decisions

### D1. Concurrency model: asyncio, prefetch=1, single in-flight task

Each Worker process handles **one** task at a time via asyncio. Rationale:
- Tasks are long (minutes–hours) and dominated by LLM I/O — asyncio fits well.
- `prefetch=1` provides natural load balancing across worker replicas; no need for in-process concurrency.
- Simplifies cancel/pause semantics: there is one `RunContext` per process.
- Heartbeat runs in a sibling asyncio task; control listener runs in another. The three communicate via in-memory `asyncio.Event` / `CancelToken`.

If a workload type (e.g. tiny research subtasks) ever benefits from in-process concurrency, we add it via a separate Worker lane (multiple processes), not multi-threading.

### D2. Toolchain: uv + Python 3.14 + ruff + mypy --strict + pytest

- **uv** chosen over poetry: ~10× faster install, lockfile is plain text, no global venv state. `uv sync --frozen` in CI.
- **Python 3.14** locked via `.python-version`. Asyncio improvements (`TaskGroup`, sub-interpreters concurrency) matter for orchestrating heartbeat + execution + control listener cleanly.
- **ruff** as the only lint/format tool (replaces black + isort + flake8 + pylint). Config in `pyproject.toml`.
- **mypy --strict** enabled — Worker has more typed-API surface than typical Python services and we want to catch envelope-shape drift at type level.
- **pytest** + `pytest-asyncio` (mode=auto) + `pytest-mock`. Integration tests use `testcontainers` for PG/RMQ; the SeaweedFS container is started as a generic `DockerContainer` and accessed via `boto3` directly (no vendor-specific testcontainers extra).

### D3. RMQ client: aio-pika

Native asyncio API, supports publisher confirms, exchange/queue declaration, channel-per-purpose patterns. Alternative `aiormq` is lower-level; `pika` is sync-only. Auto-reconnect is implemented manually with an exponential backoff supervisor — we do not rely on aio-pika's `robust_connection` because it hides reconnect state we want visible in metrics.

### D4. PG client: asyncpg directly (no ORM)

Only four write paths exist (`task_runs.last_heartbeat`, `task_checkpoints`, `artifacts`, plus a few read queries for idempotency / checkpoint resume). An ORM is overkill. We write SQL by hand in `core/persistence.py` with helpers; queries are short and reviewable.

We do **not** use sqlc here (it's Go-only). For typed-row safety we use `asyncpg` `Record` + dataclasses, validated by mypy.

### D5. OSS / S3 client: aioboto3

Standard for S3-compatible (OSS is S3-compatible at the SDK level). Endpoint URL set via `OSS_ENDPOINT`. We expose a thin wrapper `core/storage.py` enforcing path prefix discipline (every `put_object` is path-checked against `RunContext.oss_prefix`).

### D6. Cost meter integration with LangChain

LangChain exposes `BaseCallbackHandler` with `on_llm_start`, `on_llm_end`. We register our handler at the `RunContext` level (per-run callback), then attach it to every chain/agent constructed inside that context.

- `on_llm_end` receives `LLMResult` whose `llm_output` carries token usage for OpenAI; for Anthropic via `langchain_anthropic` the usage lives on `generation_info`. The cost meter abstracts both into a uniform `LLMUsage{model, input_tokens, output_tokens, cached_tokens, duration_ms}` record.
- Wall time is captured by recording `time.monotonic()` in `on_llm_start` and diffing in `on_llm_end` — we do not trust provider-reported latency.
- For tool calls we provide a `@cost_metered_tool` decorator that wraps the underlying coroutine, timing it.

If the upstream provider's token reporting is missing (e.g., streaming completion that didn't aggregate), the cost meter MUST emit an event with `input_tokens=null, output_tokens=null` and the `kind=llm` event still happens — Cost Service downstream will treat unknown tokens as zero but charge wall time. This degrades gracefully.

### D7. Idempotency mechanics

The MQ delivery's `idempotency_key` is the primary key for `task_runs` (per ARCHITECTURE §4.2 `idempotency_key TEXT NOT NULL UNIQUE`). On consume:
- Single SQL: `INSERT INTO task_runs (...) VALUES (...) ON CONFLICT (idempotency_key) DO NOTHING RETURNING id;`
- If insert returned a row → fresh run, execute.
- If insert returned nothing → SELECT the existing row to decide between skip / takeover / wait (see `worker-messaging` spec).

This avoids races: even if the same delivery is processed in parallel by two workers, only one wins the INSERT.

### D8. Heartbeat as a separate asyncio task

Inside `asyncio.TaskGroup`:
- execution task (the agent coroutine)
- heartbeat task (sleeps `HEARTBEAT_INTERVAL`, updates DB, repeats)
- control-listener task (consumes control queue + Redis pub/sub, sets tokens)

If any task raises, the TaskGroup cancels the others — clean shutdown semantics for free.

Heartbeat failure handling: 3 consecutive UPDATEs failing → cancel execution token → nack-requeue → exit message. This is more aggressive than waiting for the API-side Reaper because we want the broker to redeliver fast.

### D9. Control signal: dual-channel design

Both RMQ (`q.task.control.<worker_id>`) and Redis pub/sub (`control:<worker_id>`) are subscribed. RMQ provides reliability (queue survives broker reconnects), Redis provides low latency (sub-second).

Dedup uses an in-memory LRU keyed by `(run_id, action, ts)` with 256 entries. The signal is acted on at most once per token; if a second copy arrives after the first already set the token, it's a no-op (the token's idempotency is at the application level — see `worker-execution-runtime`).

Both channels remain best-effort: if Redis is down, RMQ still carries the signal (just slower). If RMQ control queue is down, Redis carries it. If both are down, the API-side Reaper will eventually time out the run.

### D10. Plugin loader: declarative yaml, lazy import

`plugin.yaml` is parsed eagerly at startup (so bad files fail fast and are surfaced before consumption begins). The entrypoint module is imported lazily on first use — startup remains fast even with many plugins, and import errors surface only when an unused plugin is touched.

Plugin schema validation: a `PluginManifest` pydantic model. Mismatches cause startup abort.

For `applies_to.task_types`, the runtime uses this only as metadata for now; actual routing of tasks to a particular plugin happens in agent code (future proposals). The scaffold's registry just exposes lookups.

### D11. Directory layout

```
worker/
├── pyproject.toml                  # uv-managed
├── uv.lock
├── .python-version
├── Makefile
├── README.md
├── core/
│   ├── __init__.py
│   ├── config.py                   # pydantic-settings
│   ├── lifecycle.py                # SIGTERM handling, TaskGroup wiring
│   ├── logging.py                  # structlog setup
│   ├── tracing.py                  # OTel init
│   ├── metrics.py                  # prometheus_client registry
│   ├── persistence.py              # asyncpg pool + worker-only writes
│   ├── storage.py                  # aioboto3 OSS client + prefix guard
│   ├── consumer.py                 # aio-pika consumer (task.execute)
│   ├── publisher.py                # EventPublisher + CostEventPublisher
│   ├── control.py                  # RMQ + Redis control listener
│   ├── heartbeat.py
│   ├── cost_meter.py               # LangChain callback + tool decorator
│   ├── checkpoint.py               # CheckpointStore
│   ├── run_context.py
│   └── dispatcher.py               # placeholder; always AgentNotImplementedError
├── plugins/
│   ├── __init__.py
│   ├── loader.py                   # scans plugin.yaml, registers
│   ├── registry.py                 # in-memory PluginRegistry
│   ├── schema.py                   # PluginManifest pydantic model
│   └── tool/
│       └── noop_tool/
│           ├── plugin.yaml
│           └── handler.py
├── agents/                          # empty; future proposals add code/research agents
│   └── __init__.py
├── main.py                          # entrypoint: `uv run worker`
└── tests/
    ├── unit/
    ├── integration/                # testcontainers (PG/RMQ/SeaweedFS-S3)
    └── conftest.py
```

### D12. CI scope (this proposal)

`.github/workflows/worker-ci.yml`:
- `uv sync --frozen`
- `uv run ruff check . && uv run ruff format --check .`
- `uv run mypy --strict worker/`
- `uv run pytest -x -q` (unit only on PRs; integration on main / nightly)
- Triggers on `worker/**` and the workflow file.

### D13. Local dev compose

The `docker-compose.dev.yml` introduced by `init-api-scaffold` is extended (or first-introduced here if that proposal lags) with:
- `redis:7-alpine` (port 6379) — for control fast-path
- `chrislusf/seaweedfs:3.96` (S3 API published as `:9000`) — open-source S3-compatible storage. Bucket bootstrapped via a one-shot `amazon/aws-cli` init container.
- An init job (`mc mb worker-bucket`) for bucket bootstrap

If `init-api-scaffold` lands first, this proposal adds only the `redis`, `seaweedfs`, and `seaweedfs-init` services to the existing file.

## Risks / Trade-offs

- **[Risk] LangChain version churn** → Mitigation: pin major lines (`langchain>=1.0,<2.0`, `deepagents>=0.6.1,<0.7.0`); cost-meter integration is centralized so a breaking change in callback shape touches one file.
- **[Risk] aio-pika auto-reconnect interplay with prefetch=1** → Mitigation: after reconnect, re-declare queue and resume consumption explicitly; supervisor logs every reconnect with elapsed downtime so we can detect flapping.
- **[Risk] Cost event buffer overflow during prolonged MQ outage** → Mitigation: bounded buffer (1000) with drop-oldest; emit `worker_cost_events_dropped_total` so downstream alerting catches systematic loss. Tasks themselves are unaffected.
- **[Risk] Idempotency `INSERT ... ON CONFLICT` may mask the takeover-vs-skip case** → Mitigation: a follow-up SELECT explicitly distinguishes succeeded / running-fresh / running-stale; logged and metric'd.
- **[Risk] Single in-flight task per process limits throughput at low-fanout deployments** → Mitigation: documented; horizontal scaling is the answer (more replicas), not in-process concurrency. KEDA scaling target for v1.
- **[Risk] mypy --strict drag on developer velocity for early agents** → Mitigation: agents/ subpackage may opt out via `# type: ignore` selectively; the discipline applies to `core/` and `plugins/` boundaries where shape matters.
- **[Risk] OSS prefix guard may produce false positives on path tricks (e.g., `..`)** → Mitigation: storage wrapper normalizes paths via `posixpath.normpath`, rejects `..`/absolute paths, and runs unit tests for traversal attempts.

## Migration Plan

First code change for `worker/`; no existing service. Rollback is `git revert`.

If this proposal merges before `init-api-scaffold`, the Worker will fail readiness at the topology assertion step (because exchanges don't exist) — that is the desired behavior. Operators should land both before deploying.

## Open Questions

1. **Heartbeat interval default** — 5s is conservative. Once we have load data, may lengthen to 10s to reduce write amplification on PG. Out of scope.
2. **Should `Redis` be required or optional?** — MVP makes it required (fast-path always present). If we hit deployment friction (Redis ops complexity), revisit: the system functionally works with RMQ-only signals.
3. **SeaweedFS vs LocalStack for local OSS** — chose SeaweedFS: open-source S3-compatible, actively maintained, single container, sub-second start. LocalStack supports more of AWS but is heavier; reconsider if we need broader AWS surface mocking.
4. **Where does `tenant_id` come from in scaffold tests?** — `RunContext` requires it for OSS prefix. We accept a `tenant_id` field on the test fixtures and assume the real message will carry it; finalizing the wire field is on `add-tenant-context` proposal.
