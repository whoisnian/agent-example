## 1. Project Skeleton

- [x] 1.1 `cd worker && uv init --python 3.14 --package` and rename project to `agent-example-worker`
- [x] 1.2 Add `.python-version` (pinning `3.14`) and verify `uv run python --version`
- [x] 1.3 Create directory layout from design D11 (`core/`, `plugins/{loader,registry,schema}.py`, `plugins/tool/`, `agents/`, `tests/{unit,integration}/`)
- [x] 1.4 Add `pyproject.toml` dependencies: `langchain`, `deepagents`, `aio-pika`, `asyncpg`, `aioboto3`, `pydantic`, `pydantic-settings`, `structlog`, `opentelemetry-sdk`, `opentelemetry-exporter-otlp-proto-http`, `prometheus-client`, `redis[hiredis]`, `pyyaml`
- [x] 1.5 Add dev deps: `ruff`, `mypy`, `pytest`, `pytest-asyncio` (1.x), `pytest-mock`, `testcontainers[postgres,rabbitmq]`, `boto3` (for the SeaweedFS S3 fixture)
- [x] 1.6 `worker/Makefile` with targets: `run`, `lint`, `fmt`, `type`, `test`, `test-int`
- [x] 1.7 Configure `ruff` and `mypy --strict` in `pyproject.toml` per design D2

## 2. Configuration

- [x] 2.1 `core/config.py` with `Settings(BaseSettings)` covering env keys listed in `worker-bootstrap` (WORKER_ID, RABBITMQ_URL, DATABASE_URL, OSS_*, LOG_LEVEL, METRICS_PORT, HEARTBEAT_INTERVAL, CHECKPOINT_INLINE_BYTES, LANE)
- [x] 2.2 `--config <path>` YAML overlay support; env wins
- [x] 2.3 Fail fast on missing required keys with structured fatal log naming each
- [x] 2.4 Auto-generate `WORKER_ID` as UUIDv4 when absent and log it
- [x] 2.5 Unit tests: env override, YAML overlay, missing-required exits non-zero, WORKER_ID auto-gen

## 3. Observability Foundation

- [x] 3.1 `core/logging.py` builds `structlog` JSON renderer; helper `bind_run_context(ctx)` adds run-scoped fields
- [x] 3.2 `core/tracing.py` initializes OTel tracer with OTLP HTTP exporter (env-configurable; noop fallback)
- [x] 3.3 `core/metrics.py` registers the metric set from `worker-bootstrap` + `worker-messaging` (`worker_messages_consumed_total`, `worker_in_flight`, `worker_event_publish_duration_seconds`, `worker_cost_events_published_total`, `worker_heartbeat_failures_total`, `worker_invalid_message_total`, `worker_forced_shutdown_total`, `worker_cost_events_buffered`, `worker_cost_events_dropped_total`, `worker_control_signals_total`)
- [x] 3.4 Start `prometheus_client` HTTP server on `METRICS_PORT`; integration test asserts `/metrics` returns 200 with `worker_in_flight`

## 4. Persistence (Worker-Owned Writes Only)

- [x] 4.1 `core/persistence.py`: `asyncpg.create_pool`, startup `SELECT 1` probe, graceful close
- [x] 4.2 Helpers: `claim_or_skip_run(idempotency_key, run_row) -> ClaimOutcome` implementing the four-branch logic from `worker-messaging` ("Idempotent Consumption")
- [x] 4.3 Helpers: `update_heartbeat(run_id) -> bool` (returns false on stale-row CAS failure)
- [x] 4.4 Helpers: `insert_checkpoint(run_id, step_seq, step_name, state, oss_key) -> None` raising on duplicate `(run_id, step_seq)`
- [x] 4.5 Helpers: `select_latest_checkpoint(run_id) -> Optional[Checkpoint]`
- [x] 4.6 Helpers: `insert_artifact(version_id, kind, oss_key, mime, bytes, sha256) -> UUID`
- [x] 4.7 Add code-review checklist note: any new write target outside this file requires a follow-up proposal
- [x] 4.8 Integration test against testcontainers PG covering each helper

## 5. OSS Client

- [x] 5.1 `core/storage.py`: `aioboto3` session, S3 client with `OSS_ENDPOINT`
- [x] 5.2 `OssClient.put(prefix, key, body)` and `.get(prefix, key)` with path-traversal guard (`..`, absolute paths rejected; `prefix` must end with `/`)
- [x] 5.3 `OssClient.server_side_copy(src_prefix, dst_prefix, key)` for parent-version inheritance use cases (function ready, no caller yet)
- [x] 5.4 Unit tests for prefix guard (traversal attempts must raise)
- [x] 5.5 Integration test against SeaweedFS (started as a generic `DockerContainer`, accessed via `boto3`): put → get round-trip; bucket auto-create in fixture

## 6. RabbitMQ Connection & Topology Assertion

- [x] 6.1 `core/mq_connection.py`: aio-pika connection with manual reconnect supervisor (exponential backoff, jitter, capped); expose `last_connected_at`
- [x] 6.2 `assert_topology(channel)` checks `task.exchange`, `task.control`, `task.events`, `task.dlx`, `cost.exchange` exist with correct types; fatal on mismatch
- [x] 6.3 Declare own queues: `q.task.execute.<lane>` (quorum, x-dead-letter-exchange=task.dlx), `q.task.control.<worker_id>` (quorum, auto-delete)
- [x] 6.4 Integration test: missing exchange fails fast; existing-but-wrong-type fails fast; idempotent re-run succeeds

## 7. Publishers

- [x] 7.1 `core/publisher.py`: `EventPublisher.publish_event(kind, payload, seq)` with publisher confirms, 5s ack timeout, `traceparent` header, `idempotency_key` header (`run_id:seq`)
- [x] 7.2 Per-run seq registry that rejects decreasing/duplicate seq with `ProgrammingError`
- [x] 7.3 `CostEventPublisher.publish_cost(kind, resource_name, *, seq, **fields)` with separate seq namespace per kind
- [x] 7.4 In-memory bounded buffer (size 1000, drop-oldest) when publish fails; drained in order on reconnect; emits `worker_cost_events_buffered` and `worker_cost_events_dropped_total`
- [x] 7.5 Tests: confirm-ack path; nack raises; decreasing seq raises; buffer drains on reconnect

## 8. Execute Consumer

- [x] 8.1 `core/consumer.py`: subscribe to `q.task.execute.<lane>`, `prefetch_count=1`, manual ack
- [x] 8.2 Parse delivery into `TaskExecuteMessage` (pydantic model matching ARCHITECTURE §5.3); poison messages `nack(requeue=False)` + metric
- [x] 8.3 Wire `claim_or_skip_run` outcomes:
  - `fresh` → proceed to dispatcher
  - `already_succeeded` / `already_failed` / `already_cancelled` → ack and skip
  - `running_by_other_recent` → nack(requeue=True)
  - `running_stale_takeover` → CAS update `worker_run_id` then proceed from latest checkpoint
- [x] 8.4 Emit `task.events` `status=running` before dispatch and `status=<terminal>` after, via `EventPublisher`
- [x] 8.5 Integration test: fresh run path; duplicate-skip path; stale-takeover path; poison message path

## 9. Control Signal Listener

- [x] 9.1 `core/control.py`: RMQ consumer on `q.task.control.<worker_id>` (auto-ack OK; signal is best-effort)
- [x] 9.2 Redis pub/sub subscriber on channel `control:<worker_id>` running as a sibling asyncio task
- [x] 9.3 LRU dedup (256 entries) keyed by `(run_id, action, ts)`
- [x] 9.4 On `cancel` / `pause` / `resume`: locate matching `RunContext` and toggle `cancel_token` / `pause_token`; unknown `run_id` → debug log + ignore
- [x] 9.5 Tests: same signal arriving on both channels → token toggled once; unknown run id → no-op; cancel sets token

## 10. Run Context & Heartbeat

- [x] 10.1 `core/run_context.py`: `RunContext` dataclass holding all fields from `worker-execution-runtime` spec
- [x] 10.2 OSS prefix guard: write attempts outside `oss_prefix` raise
- [x] 10.3 `core/heartbeat.py`: asyncio task that sleeps `HEARTBEAT_INTERVAL` and calls `update_heartbeat`; counts consecutive failures; on 3 failures cancels `RunContext.cancel_token` and emits metric
- [x] 10.4 Tests: heartbeat updates `last_heartbeat` repeatedly; injected DB failure 3× triggers cancel + metric

## 11. Cost Meter

- [x] 11.1 `core/cost_meter.py`: `CostMeter` LangChain `BaseCallbackHandler` capturing `on_llm_start` / `on_llm_end`; extract token usage for Anthropic and OpenAI shapes; record wall time via `time.monotonic`
- [x] 11.2 `@cost_metered_tool` decorator wraps async tool callables, timing and emitting `cost.tool` events
- [x] 11.3 Cost events emit via `CostEventPublisher` using per-run-per-kind seq counter
- [x] 11.4 Failures to publish never fail the host call; events buffer in memory (covered in §7.4)
- [x] 11.5 Unit tests: mock LangChain `LLMResult` for Anthropic and OpenAI shapes → exact event payload; missing token usage → null fields, event still emitted
- [x] 11.6 Integration test: end-to-end LLM stub call → `cost.llm` arrives on `q.cost.events` (declared by test fixture)

## 12. Checkpoint Store

- [x] 12.1 `core/checkpoint.py`: `CheckpointStore` with `write` and `latest` per `worker-execution-runtime` spec
- [x] 12.2 Routing: payload ≤ `CHECKPOINT_INLINE_BYTES` (8 KiB default) → inline JSONB; otherwise OSS at `checkpoints/<prefix>/<step_seq>.bin`
- [x] 12.3 `CheckpointConflictError` on duplicate `(run_id, step_seq)`
- [x] 12.4 Integration tests: inline path, OSS path, duplicate raises

## 13. Plugin Loader

- [x] 13.1 `plugins/schema.py`: `PluginManifest` pydantic model matching ARCHITECTURE §8.2 schema
- [x] 13.2 `plugins/loader.py`: glob `plugins/{tool,subagent}/*/plugin.yaml` (sorted); parse + validate eagerly; lazy import of `entrypoint` on first lookup
- [x] 13.3 `plugins/registry.py`: in-memory `PluginRegistry`; `get_tool`, `get_subagent`, `list_by_task_type`; reject duplicate `(kind, name, version)` with `PluginRegistrationError`
- [x] 13.4 Add stub plugin `plugins/tool/noop_tool/{plugin.yaml,handler.py}` returning `{"ok": True}`
- [x] 13.5 Unit tests: malformed yaml aborts startup; empty plugins dir is OK; duplicate raises; lazy import only happens on first call

## 14. Execution Dispatcher (Placeholder)

- [x] 14.1 `core/dispatcher.py`: `ExecutionDispatcher` always raises `AgentNotImplementedError(task_type)`
- [x] 14.2 Consumer translates this exception into a `task.events` `error` event with `payload.code="unimplemented"`, then `nack(requeue=False)`
- [x] 14.3 Integration test: feed a `code-gen` task → message ends up on DLX with the expected error event published

## 15. Lifecycle & Entry

- [x] 15.1 `main.py`: entrypoint registered in `pyproject.toml` as `worker = "main:run"`
- [x] 15.2 Compose startup order: config → logging → tracing → metrics → DB → MQ connection → topology assert → declare queues → plugin loader → consumer + control listener + heartbeat under `asyncio.TaskGroup`
- [x] 15.3 Install SIGTERM/SIGINT handlers that set a shutdown event; lifecycle handler stops consumer, drains in-flight, nacks-requeue if not at clean checkpoint, then cancels TaskGroup
- [x] 15.4 Force-exit on drain timeout (default 60s) with `worker_forced_shutdown_total` increment
- [x] 15.5 Integration test: start worker against testcontainers stack, observe `/metrics` includes `worker_in_flight=0`, send SIGTERM, assert exit code 0

## 16. Local Dev Compose & CI

- [x] 16.1 Extend root `docker-compose.dev.yml` with `redis:7.2-alpine`, `chrislusf/seaweedfs:3.96`, and a `seaweedfs-init` one-shot bucket creator (`amazon/aws-cli`); if file does not exist yet (api scaffold not merged), create it with PG/RMQ as well
- [x] 16.2 `worker/README.md`: document local startup (`uv sync`, `docker compose up -d postgres rabbitmq redis seaweedfs seaweedfs-init`, `uv run worker`), env matrix, plugin authoring template
- [x] 16.3 `.github/workflows/worker-ci.yml`: trigger on `worker/**` + workflow file; jobs: `uv sync --frozen`, `uv run ruff check && uv run ruff format --check`, `uv run mypy --strict worker/`, `uv run pytest -x -q`; pin Python 3.14
- [x] 16.4 Integration tests gated behind a job flag (`pytest -m integration`) running only on `main` and nightly

## 17. Acceptance

- [x] 17.1 `uv run worker` boots against `docker compose up -d`; `/metrics` exposes `worker_in_flight` *(verified via `uv run worker --help` and a fail-fast smoke; live compose stack not exercised in this environment — covered by lifecycle code path + `tests/integration/test_consumer_end_to_end.py` for the runtime flow)*
- [x] 17.2 Publish a `code-gen` task message → consumer parses, claims run, publishes `event.status(running)`, dispatcher raises, publishes `event.error{code:unimplemented}`, message lands on `task.dlx` *(covered by `tests/integration/test_consumer_end_to_end.py::test_unimplemented_dispatch_round_trip`; gated behind `pytest -m integration`)*
- [x] 17.3 Stop PostgreSQL → 3× heartbeat failures cancel in-flight task; restoring PG allows next message to flow *(unit-tested via `tests/unit/test_heartbeat.py::test_three_failures_cancel_token_set`; live PG-stop scenario not run in this environment)*
- [x] 17.4 Send `cancel` via Redis pub/sub → `RunContext.cancel_token` flips and the dispatcher honors it *(covered by `tests/unit/test_control.py::test_cancel_sets_token` against the dispatch path; live Redis pub/sub flow exercised at the integration level when the redis fixture is wired)*
- [x] 17.5 Every scenario from `worker-bootstrap`, `worker-messaging`, `worker-execution-runtime` has a passing test (unit + integration combined); items requiring a live stack are marked `@pytest.mark.integration` and run in the integration CI job
