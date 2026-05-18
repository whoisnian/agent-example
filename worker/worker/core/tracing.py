"""OpenTelemetry tracer setup.

Initializes a tracer provider with an OTLP HTTP exporter when configured;
falls back to a noop exporter when the endpoint env is unset. Inbound
``traceparent`` headers on consumed messages are used to make ``worker.run``
a child of the upstream trace (spec: worker-bootstrap → "Distributed
Tracing").
"""

from __future__ import annotations

from typing import Any

from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

_INITIALIZED = False


def setup_tracing(
    *,
    worker_id: str,
    otlp_endpoint: str | None,
    service_name: str = "worker",
) -> trace.Tracer:
    """Initialize the global tracer provider.

    When ``otlp_endpoint`` is None, a TracerProvider is still installed (so
    span context APIs work) but no exporter is attached — effectively a noop
    sink. This keeps call sites free of conditional logic.
    """
    global _INITIALIZED
    if _INITIALIZED:
        return trace.get_tracer(service_name)

    resource = Resource.create(
        {
            "service.name": service_name,
            "service.instance.id": worker_id,
        }
    )
    provider = TracerProvider(resource=resource)

    if otlp_endpoint:
        exporter = OTLPSpanExporter(endpoint=otlp_endpoint)
        provider.add_span_processor(BatchSpanProcessor(exporter))

    trace.set_tracer_provider(provider)
    _INITIALIZED = True
    return trace.get_tracer(service_name)


def get_tracer(name: str = "worker") -> trace.Tracer:
    """Return a tracer for the given instrumentation scope."""
    return trace.get_tracer(name)


def shutdown_tracing() -> None:
    """Flush and shutdown the tracer provider (called during drain)."""
    provider: Any = trace.get_tracer_provider()
    shutdown = getattr(provider, "shutdown", None)
    if callable(shutdown):
        shutdown()
