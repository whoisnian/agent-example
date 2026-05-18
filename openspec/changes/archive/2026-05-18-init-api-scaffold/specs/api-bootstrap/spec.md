## ADDED Requirements

### Requirement: Service Process Lifecycle

The API service SHALL start an HTTP listener bound to a configured address, support graceful shutdown on SIGINT / SIGTERM, and complete in-flight requests within a configured drain timeout (default 30s) before exiting.

#### Scenario: Graceful shutdown drains in-flight requests
- **WHEN** the process receives SIGTERM while an HTTP request is still being handled
- **THEN** the service MUST stop accepting new connections, allow the in-flight request to complete (or hit drain timeout), and exit with code 0

#### Scenario: Drain timeout enforced
- **WHEN** in-flight requests exceed the configured drain timeout
- **THEN** the service MUST force-close remaining connections, emit a warning log including the count of forcibly closed connections, and exit with code 0

### Requirement: Configuration Loading

The service SHALL load configuration from environment variables (12-factor style), optionally overlaid by a single YAML file specified via `--config` flag. Required configuration keys missing at startup MUST cause the process to fail fast with a non-zero exit code and a log entry naming each missing key.

#### Scenario: Missing required config fails fast
- **WHEN** the service starts without the required `DATABASE_URL` environment variable
- **THEN** the process MUST exit with a non-zero code and log a fatal entry naming `DATABASE_URL` as missing, before any HTTP listener is opened

#### Scenario: Env overrides YAML
- **WHEN** both a YAML config file and the corresponding environment variable are present
- **THEN** the environment variable value MUST win

### Requirement: Structured Logging

The service SHALL emit JSON-structured logs via `slog`. Every log entry MUST include `ts`, `level`, `msg`, and (when available in context) `trace_id`, `request_id`, `task_id`. Log level SHALL be configurable via `LOG_LEVEL` (default `info`).

#### Scenario: Request log carries correlation IDs
- **WHEN** an HTTP request is processed
- **THEN** the access log entry MUST include `request_id` and `trace_id`, and any business log entry emitted within the request scope MUST inherit the same IDs

### Requirement: Distributed Tracing

The service SHALL initialize an OpenTelemetry tracer at startup, propagate W3C `traceparent` headers on inbound and outbound HTTP calls, and create a span per HTTP request named `HTTP <method> <route-template>`.

#### Scenario: Inbound trace context is honored
- **WHEN** an HTTP request arrives with a valid `traceparent` header
- **THEN** the span created for the request MUST be a child of the propagated trace context, and the same trace ID MUST appear on outbound calls made within that request

### Requirement: Prometheus Metrics Endpoint

The service SHALL expose Prometheus-format metrics at `GET /metrics` including, at minimum: `http_requests_total{route,method,status}`, `http_request_duration_seconds{route,method}` (histogram), and `process_*` runtime metrics.

#### Scenario: Metrics endpoint reachable
- **WHEN** a client issues `GET /metrics` against a running service
- **THEN** the response MUST return HTTP 200 with `Content-Type: text/plain; version=0.0.4` and contain at least one `http_requests_total` series

### Requirement: Health and Readiness Endpoints

The service SHALL expose `GET /healthz` (liveness, always returns 200 unless the process itself is unhealthy) and `GET /readyz` (readiness, returns 200 only when all configured dependencies — PostgreSQL, RabbitMQ — pass their probe).

#### Scenario: Readiness fails when DB is unreachable
- **WHEN** PostgreSQL is unreachable from the service
- **THEN** `GET /readyz` MUST return HTTP 503 with a JSON body listing the failed dependency name(s), while `GET /healthz` MUST continue returning 200

### Requirement: Unified Response Envelope

Every JSON response from a business API endpoint SHALL conform to the shape `{code, message, data, trace_id}`. `code` is `0` on success; non-zero values map to documented business error codes. Health, readiness, and metrics endpoints are exempt.

#### Scenario: Success response shape
- **WHEN** a business endpoint handles a request successfully
- **THEN** the response body MUST be `{"code": 0, "message": "ok", "data": <payload>, "trace_id": "<id>"}`

#### Scenario: Error response shape
- **WHEN** a business endpoint returns a domain error
- **THEN** the response body MUST be `{"code": "<error_code>", "message": "<human readable>", "data": null, "trace_id": "<id>"}` with the HTTP status mapped per the documented error catalog

### Requirement: Panic Recovery Middleware

The HTTP server SHALL recover from any panic that escapes a handler, log a stack trace with the request's `trace_id`, increment a `http_panics_total` counter, and return HTTP 500 with the unified error envelope (`code = "internal_error"`).

#### Scenario: Panic is converted to 500
- **WHEN** a handler panics with an unhandled error
- **THEN** the client MUST receive HTTP 500 with envelope `{"code":"internal_error",...,"trace_id":"<id>"}` and the server log MUST contain the stack trace tagged with the same `trace_id`
