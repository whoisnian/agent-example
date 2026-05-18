# worker-bootstrap Specification

## Purpose
TBD - created by archiving change init-worker-scaffold. Update Purpose after archive.
## Requirements
### Requirement: Process Lifecycle and Graceful Shutdown

The Worker process SHALL start, register with the messaging layer, run until it receives `SIGINT` or `SIGTERM`, and shut down gracefully within a configurable drain timeout (default 60s, longer than API because tasks may need to write a final checkpoint).

Graceful shutdown SHALL: (1) stop pulling new messages from RabbitMQ, (2) allow the currently-processing message to either complete one more checkpoint and `ack`, or `nack(requeue=true)` cleanly, (3) close MQ / DB / OSS clients, (4) flush OTel trace exporter, (5) exit with code 0. Exceeding the drain timeout SHALL force exit with code 0 and emit a warning log naming the unfinished `run_id`.

#### Scenario: SIGTERM during idle worker
- **WHEN** the process receives SIGTERM with no message in-flight
- **THEN** the worker MUST close MQ/DB/OSS clients, flush traces, and exit with code 0 within 5 seconds

#### Scenario: SIGTERM mid-task triggers nack-requeue
- **WHEN** SIGTERM arrives while a task is mid-execution and not at a clean checkpoint boundary
- **THEN** the worker MUST `nack(requeue=true)` the current message, write any in-flight checkpoint progress that is already committed, and exit within drain timeout — the requeued message MUST become available for another worker

#### Scenario: Drain timeout exceeded
- **WHEN** the in-flight task does not finish within drain timeout
- **THEN** the process MUST force-exit with code 0, emit `worker_forced_shutdown_total` metric increment, and log a warning naming the unfinished `run_id`

### Requirement: Configuration Loading

The Worker SHALL load configuration via `pydantic-settings` from environment variables, optionally overlaid by a `--config` YAML file. Required keys missing at startup MUST cause `exit(2)` with a fatal log listing every missing key.

Required keys at minimum: `WORKER_ID` (UUID, may be auto-generated when absent), `RABBITMQ_URL`, `DATABASE_URL`, `OSS_ENDPOINT`, `OSS_BUCKET`, `OSS_ACCESS_KEY_ID`, `OSS_ACCESS_KEY_SECRET`.

#### Scenario: Missing required key fails fast
- **WHEN** the worker starts without `DATABASE_URL`
- **THEN** the process MUST exit non-zero before any MQ consumer is registered, and the fatal log MUST name `DATABASE_URL`

#### Scenario: WORKER_ID auto-generated when absent
- **WHEN** `WORKER_ID` is not provided
- **THEN** the worker MUST generate a UUIDv4, log it at info level, and use it for control-queue binding (`q.task.control.<worker_id>`)

### Requirement: Structured Logging

The Worker SHALL emit JSON logs via `structlog`. Every log entry MUST include `ts`, `level`, `event`, and (when in a task context) `worker_id`, `task_id`, `run_id`, `step`, `trace_id`. Log level SHALL be configurable via `LOG_LEVEL` (default `INFO`).

Logs emitted outside a task context (startup, shutdown, periodic heartbeats) MUST include `worker_id` but MAY omit task-scoped fields.

#### Scenario: Task-scoped logs carry full IDs
- **WHEN** any log statement is emitted from code running under a `RunContext`
- **THEN** the entry MUST include `worker_id`, `task_id`, `run_id`, `step`, and `trace_id`

### Requirement: Distributed Tracing

The Worker SHALL initialize an OpenTelemetry tracer at startup with OTLP HTTP exporter (env-configurable; falls back to a noop exporter when unset). For every consumed message, a root span named `worker.run` SHALL be created with `trace_id` extracted from the incoming message's `traceparent` header when present, otherwise newly generated. All LLM and tool calls SHALL be child spans named `llm.<model>` and `tool.<name>` respectively.

#### Scenario: Inbound trace context honored
- **WHEN** a `task.execute` message arrives with a valid `traceparent`
- **THEN** the `worker.run` span MUST be a child of that trace context, and the same trace ID MUST propagate to LLM/tool child spans

### Requirement: Prometheus Metrics Endpoint

The Worker SHALL expose Prometheus-format metrics on a dedicated port (default `9090`, env `METRICS_PORT`) at path `/metrics`. The initial metric set MUST include: `worker_messages_consumed_total{outcome}`, `worker_message_processing_seconds` (histogram), `worker_in_flight` (gauge), `worker_forced_shutdown_total`, plus default Python runtime metrics.

#### Scenario: Metrics endpoint reachable
- **WHEN** the metrics HTTP server is running
- **THEN** `GET :9090/metrics` MUST return HTTP 200 with `Content-Type: text/plain; version=0.0.4` and contain `worker_in_flight`

### Requirement: Plugin Registry Initialization

At startup, after configuration load and before MQ consumer registration, the Worker SHALL invoke the Plugin Loader (defined in `worker-execution-runtime`) to scan `worker/plugins/{tool,subagent}/<name>/plugin.yaml` and populate an in-memory registry. Failure to parse any `plugin.yaml` MUST cause `exit(2)` with a fatal log naming the offending file and parse error.

#### Scenario: Malformed plugin.yaml aborts startup
- **WHEN** any `plugin.yaml` under `worker/plugins/` fails to parse as valid YAML or does not satisfy the schema
- **THEN** startup MUST abort with non-zero exit code and a fatal log identifying the file

#### Scenario: Empty plugins directory is allowed
- **WHEN** `worker/plugins/` contains no plugin directories
- **THEN** the registry MUST initialize empty and the worker MUST continue startup normally

