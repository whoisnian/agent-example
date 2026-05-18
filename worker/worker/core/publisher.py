"""Publishers for ``task.events`` and ``cost.events``.

Both use publisher confirms with a 5 s ack timeout. The cost publisher
features an in-memory drop-oldest buffer that is drained on broker reconnect
so that transient MQ outages do not surface to the agent code (spec:
worker-messaging → "Event Publisher", "Cost Event Publisher" + design D6).
"""

from __future__ import annotations

import asyncio
import json
from collections import deque
from datetime import UTC, datetime
from typing import TYPE_CHECKING, Any

import aio_pika
import structlog
from aio_pika.exceptions import DeliveryError

from worker.core.messages import CostEvent, TaskEvent
from worker.core.mq_connection import MqConnection

if TYPE_CHECKING:
    from worker.core.metrics import Metrics


PUBLISH_TIMEOUT_SECONDS = 5.0


class ProgrammingError(RuntimeError):
    """Raised when caller violates the publisher contract (e.g. decreasing seq)."""


class _SeqRegistry:
    """Tracks the last published seq per run (and per kind for cost events)."""

    def __init__(self) -> None:
        self._max: dict[tuple[str, str, str], int] = {}

    def admit(self, run_id: str, namespace: str, kind: str, seq: int) -> None:
        key = (run_id, namespace, kind)
        prev = self._max.get(key)
        if prev is not None and seq <= prev:
            raise ProgrammingError(
                f"non-increasing seq for run_id={run_id} namespace={namespace} kind={kind}: "
                f"previous={prev} attempted={seq}"
            )
        self._max[key] = seq


class EventPublisher:
    """Publishes ``task.events`` messages."""

    def __init__(
        self,
        mq: MqConnection,
        *,
        metrics: Metrics,
        logger: structlog.stdlib.BoundLogger | None = None,
    ) -> None:
        self._mq = mq
        self._metrics = metrics
        self._log = logger or structlog.get_logger().bind(component="event_publisher")
        self._seq = _SeqRegistry()

    async def publish_event(
        self,
        *,
        task_id: str,
        version_id: str,
        run_id: str,
        task_type: str,
        kind: str,
        payload: dict[str, Any],
        seq: int,
        traceparent: str | None = None,
    ) -> None:
        """Publish to ``task.events`` with publisher-confirm semantics."""
        self._seq.admit(run_id, "task", kind, seq)
        body = TaskEvent(
            task_id=task_id,  # type: ignore[arg-type]
            version_id=version_id,  # type: ignore[arg-type]
            run_id=run_id,  # type: ignore[arg-type]
            seq=seq,
            kind=kind,
            payload=payload,
            ts=datetime.now(UTC),
        ).model_dump_json()

        headers: dict[str, Any] = {"idempotency_key": f"{run_id}:{seq}"}
        if traceparent:
            headers["traceparent"] = traceparent

        with self._metrics.event_publish_duration_seconds.time():
            channel = await self._mq.channel()
            exchange = await channel.declare_exchange(
                "task.events", type=aio_pika.ExchangeType.TOPIC, durable=True, passive=True
            )
            message = aio_pika.Message(
                body=body.encode("utf-8"),
                content_type="application/json",
                headers=headers,
                message_id=f"{run_id}:{seq}",
            )
            try:
                await asyncio.wait_for(
                    exchange.publish(message, routing_key=f"event.{task_type}.{kind}"),
                    timeout=PUBLISH_TIMEOUT_SECONDS,
                )
            except (TimeoutError, DeliveryError) as exc:
                self._log.error("event_publish_failed", kind=kind, run_id=run_id, error=str(exc))
                raise


class CostEventPublisher:
    """Publishes ``cost.<kind>`` with an in-memory retry buffer.

    Publish failures (broker unreachable, ack timeout) are buffered up to
    ``buffer_capacity`` entries (drop-oldest); the buffer is drained in
    insertion order on the next successful publish.
    """

    def __init__(
        self,
        mq: MqConnection,
        *,
        metrics: Metrics,
        buffer_capacity: int = 1000,
        logger: structlog.stdlib.BoundLogger | None = None,
    ) -> None:
        self._mq = mq
        self._metrics = metrics
        self._log = logger or structlog.get_logger().bind(component="cost_publisher")
        self._seq = _SeqRegistry()
        self._buffer: deque[tuple[CostEvent, dict[str, Any]]] = deque(maxlen=buffer_capacity)
        self._buffer_capacity = buffer_capacity

    @property
    def buffered(self) -> int:
        return len(self._buffer)

    async def publish_cost(
        self,
        *,
        task_id: str,
        version_id: str,
        run_id: str,
        kind: str,
        resource_name: str,
        seq: int,
        input_tokens: int | None = None,
        output_tokens: int | None = None,
        cached_tokens: int | None = None,
        calls: int | None = None,
        duration_ms: int | None = None,
        traceparent: str | None = None,
    ) -> None:
        self._seq.admit(run_id, "cost", kind, seq)
        event = CostEvent(
            task_id=task_id,  # type: ignore[arg-type]
            version_id=version_id,  # type: ignore[arg-type]
            run_id=run_id,  # type: ignore[arg-type]
            seq=seq,
            kind=kind,
            resource_name=resource_name,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            cached_tokens=cached_tokens,
            calls=calls,
            duration_ms=duration_ms,
            occurred_at=datetime.now(UTC),
        )
        headers: dict[str, Any] = {"idempotency_key": f"{run_id}:{kind}:{seq}"}
        if traceparent:
            headers["traceparent"] = traceparent

        await self._send_or_buffer(event, headers)

    async def drain(self) -> None:
        """Attempt to publish buffered events, oldest first.

        Stops at the first publish failure and leaves the remaining entries
        in place for the next drain attempt.
        """
        while self._buffer:
            event, headers = self._buffer[0]
            try:
                await self._send_now(event, headers)
            except Exception:  # noqa: BLE001 - intentionally broad; will retry
                return
            self._buffer.popleft()
            self._metrics.cost_events_buffered.set(len(self._buffer))

    async def _send_or_buffer(self, event: CostEvent, headers: dict[str, Any]) -> None:
        # If anything is already buffered, append (preserves order).
        if self._buffer:
            self._buffer.append((event, headers))
            self._observe_buffer_growth()
            return
        try:
            await self._send_now(event, headers)
        except Exception as exc:  # noqa: BLE001 - intentionally broad
            self._log.warning(
                "cost_publish_failed_buffered",
                kind=event.kind,
                run_id=str(event.run_id),
                error=str(exc),
            )
            self._buffer.append((event, headers))
            self._observe_buffer_growth()

    def _observe_buffer_growth(self) -> None:
        self._metrics.cost_events_buffered.set(len(self._buffer))
        # If we hit maxlen the oldest was silently dropped — count it.
        if len(self._buffer) >= self._buffer_capacity:
            self._metrics.cost_events_dropped_total.inc()

    async def _send_now(self, event: CostEvent, headers: dict[str, Any]) -> None:
        channel = await self._mq.channel()
        exchange = await channel.declare_exchange(
            "cost.exchange", type=aio_pika.ExchangeType.TOPIC, durable=True, passive=True
        )
        message = aio_pika.Message(
            body=event.model_dump_json().encode("utf-8"),
            content_type="application/json",
            headers=headers,
            message_id=f"{event.run_id}:{event.kind}:{event.seq}",
        )
        await asyncio.wait_for(
            exchange.publish(message, routing_key=f"cost.{event.kind}"),
            timeout=PUBLISH_TIMEOUT_SECONDS,
        )
        self._metrics.cost_events_published_total.labels(kind=event.kind).inc()


def _to_json(payload: dict[str, Any]) -> str:
    return json.dumps(payload, separators=(",", ":"), default=str)
