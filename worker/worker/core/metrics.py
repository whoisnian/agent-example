"""Prometheus metrics registry and HTTP exposition.

The metric set follows the union of ``worker-bootstrap`` (Prometheus Metrics
Endpoint) and ``worker-messaging`` / ``worker-execution-runtime`` spec
requirements. Metrics are owned by a single ``CollectorRegistry`` so unit
tests can isolate per-test by passing their own registry.
"""

from __future__ import annotations

from dataclasses import dataclass

from prometheus_client import (
    CollectorRegistry,
    Counter,
    Gauge,
    Histogram,
    start_http_server,
)


@dataclass(slots=True)
class Metrics:
    """All Worker metrics. Construct once at startup; pass via DI."""

    # Consume / processing
    messages_consumed_total: Counter
    message_processing_seconds: Histogram
    in_flight: Gauge
    invalid_message_total: Counter
    forced_shutdown_total: Counter

    # Publishers
    event_publish_duration_seconds: Histogram
    cost_events_published_total: Counter
    cost_events_buffered: Gauge
    cost_events_dropped_total: Counter

    # Heartbeat
    heartbeat_failures_total: Counter

    # Control signals
    control_signals_total: Counter

    # Agent orchestration
    agent_runs_total: Counter
    agent_steps_total: Counter
    agent_step_duration_seconds: Histogram


def build_metrics(registry: CollectorRegistry | None = None) -> Metrics:
    """Create and register the worker metric set on the given registry.

    Defaults to a fresh ``CollectorRegistry`` so tests do not collide with the
    global default registry. Callers that want the default global behaviour
    must pass ``prometheus_client.REGISTRY`` explicitly.
    """
    reg = registry if registry is not None else CollectorRegistry()

    return Metrics(
        messages_consumed_total=Counter(
            "worker_messages_consumed_total",
            "Number of task.execute messages consumed by outcome.",
            labelnames=("outcome",),
            registry=reg,
        ),
        message_processing_seconds=Histogram(
            "worker_message_processing_seconds",
            "Wall-clock seconds spent processing a single task.execute message.",
            registry=reg,
        ),
        in_flight=Gauge(
            "worker_in_flight",
            "1 when a task is currently being processed, else 0.",
            registry=reg,
        ),
        invalid_message_total=Counter(
            "worker_invalid_message_total",
            "Number of poison messages routed to DLX (parse / schema failure).",
            registry=reg,
        ),
        forced_shutdown_total=Counter(
            "worker_forced_shutdown_total",
            "Number of times the worker exited because drain timeout was exceeded.",
            registry=reg,
        ),
        event_publish_duration_seconds=Histogram(
            "worker_event_publish_duration_seconds",
            "Seconds spent waiting for a task.events publisher confirm.",
            registry=reg,
        ),
        cost_events_published_total=Counter(
            "worker_cost_events_published_total",
            "Cost events successfully published, by kind.",
            labelnames=("kind",),
            registry=reg,
        ),
        cost_events_buffered=Gauge(
            "worker_cost_events_buffered",
            "Cost events currently buffered in memory awaiting MQ reconnect.",
            registry=reg,
        ),
        cost_events_dropped_total=Counter(
            "worker_cost_events_dropped_total",
            "Cost events dropped because the in-memory buffer was full.",
            registry=reg,
        ),
        heartbeat_failures_total=Counter(
            "worker_heartbeat_failures_total",
            "Times heartbeat updates failed (only the failure that triggered cancel).",
            registry=reg,
        ),
        control_signals_total=Counter(
            "worker_control_signals_total",
            "Control signals received (deduplicated across RMQ + Redis), by action.",
            labelnames=("action",),
            registry=reg,
        ),
        agent_runs_total=Counter(
            "worker_agent_runs_total",
            "Agent runs by task_type and outcome (success|error|cancelled).",
            labelnames=("task_type", "outcome"),
            registry=reg,
        ),
        agent_steps_total=Counter(
            "worker_agent_steps_total",
            "Completed agent steps by task_type.",
            labelnames=("task_type",),
            registry=reg,
        ),
        agent_step_duration_seconds=Histogram(
            "worker_agent_step_duration_seconds",
            "Wall-clock seconds per completed agent step.",
            registry=reg,
        ),
    )


def start_metrics_server(
    port: int,
    registry: CollectorRegistry,
) -> None:
    """Start the Prometheus HTTP exposition server.

    Returns once the server thread is listening. The server lifetime is tied
    to the process; ``prometheus_client`` does not expose a graceful stop, but
    the daemon thread terminates with the process.
    """
    start_http_server(port, registry=registry)
