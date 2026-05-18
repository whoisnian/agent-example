"""RabbitMQ connection supervisor and topology assertion.

We deliberately do NOT use ``aio_pika.connect_robust`` because it hides
reconnect state inside the library; instead we own the supervisor and emit
metrics / logs at each (re)connect (design D3).

Topology — the set of exchanges declared by the API service — is *asserted*
passively (passive=True). Queues consumed by the Worker (the execute lane
queue and the worker-specific control queue) are declared by us with the
expected arguments.
"""

from __future__ import annotations

import asyncio
import random
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Final

import aio_pika
import structlog


@dataclass(frozen=True)
class ExpectedExchange:
    name: str
    type: aio_pika.ExchangeType


#: Exchanges that MUST already exist (declared by ``init-api-scaffold``).
EXPECTED_EXCHANGES: Final[tuple[ExpectedExchange, ...]] = (
    ExpectedExchange("task.exchange", aio_pika.ExchangeType.TOPIC),
    ExpectedExchange("task.control", aio_pika.ExchangeType.DIRECT),
    ExpectedExchange("task.events", aio_pika.ExchangeType.TOPIC),
    ExpectedExchange("task.dlx", aio_pika.ExchangeType.DIRECT),
    ExpectedExchange("cost.exchange", aio_pika.ExchangeType.TOPIC),
)


class TopologyError(RuntimeError):
    """Raised when a required exchange is missing or has the wrong type."""


@dataclass
class ConnectionState:
    last_connected_at: datetime | None = None
    reconnect_count: int = 0


class MqConnection:
    """Owns the aio-pika connection and channel lifecycle.

    Reconnects are driven by ``ensure_connected`` (call sites await it before
    each operation that might race with a broker drop). Exponential backoff
    with jitter caps at 30 s.
    """

    INITIAL_BACKOFF = 0.5
    MAX_BACKOFF = 30.0

    def __init__(
        self,
        url: str,
        *,
        logger: structlog.stdlib.BoundLogger | None = None,
    ) -> None:
        self._url = url
        self._connection: aio_pika.abc.AbstractConnection | None = None
        self._channel: aio_pika.abc.AbstractChannel | None = None
        self._lock = asyncio.Lock()
        self._state = ConnectionState()
        self._log: structlog.stdlib.BoundLogger = logger or structlog.get_logger()

    @property
    def state(self) -> ConnectionState:
        return self._state

    async def connect(self) -> None:
        async with self._lock:
            await self._connect_unlocked()

    async def _connect_unlocked(self) -> None:
        backoff = self.INITIAL_BACKOFF
        attempt = 0
        while True:
            attempt += 1
            try:
                connection = await aio_pika.connect(self._url)
                channel = await connection.channel(publisher_confirms=True)
                await channel.set_qos(prefetch_count=1)
                self._connection = connection
                self._channel = channel
                self._state.last_connected_at = datetime.now(UTC)
                if attempt > 1:
                    self._state.reconnect_count += 1
                    self._log.warning(
                        "mq_reconnected",
                        attempts=attempt,
                        reconnect_count=self._state.reconnect_count,
                    )
                else:
                    self._log.info("mq_connected", url=self._sanitize_url())
                return
            except Exception as exc:  # noqa: BLE001 - intentionally broad
                jitter = random.uniform(0, backoff / 4)
                sleep_for = min(backoff + jitter, self.MAX_BACKOFF)
                self._log.warning(
                    "mq_connect_failed",
                    error=str(exc),
                    backoff_seconds=sleep_for,
                    attempt=attempt,
                )
                await asyncio.sleep(sleep_for)
                backoff = min(backoff * 2, self.MAX_BACKOFF)

    async def channel(self) -> aio_pika.abc.AbstractChannel:
        if self._channel is None or self._channel.is_closed:
            await self.connect()
        assert self._channel is not None  # noqa: S101
        return self._channel

    async def close(self) -> None:
        if self._connection is not None and not self._connection.is_closed:
            await self._connection.close()
        self._connection = None
        self._channel = None

    def _sanitize_url(self) -> str:
        # Strip password segment in case it's logged.
        if "@" in self._url and "://" in self._url:
            scheme, rest = self._url.split("://", 1)
            if "@" in rest:
                _, host = rest.split("@", 1)
                return f"{scheme}://<redacted>@{host}"
        return self._url


async def assert_topology(channel: aio_pika.abc.AbstractChannel) -> None:
    """Passively check that every required exchange exists with the right type.

    Raises :class:`TopologyError` on the first mismatch. The Worker is
    expected to ``exit(2)`` on this error before binding any consumer (spec:
    worker-messaging → "Topology Assertion on Startup").
    """
    for expected in EXPECTED_EXCHANGES:
        try:
            await channel.declare_exchange(
                expected.name,
                type=expected.type,
                durable=True,
                passive=True,
            )
        except aio_pika.exceptions.ChannelClosed as exc:
            raise TopologyError(
                f"required exchange {expected.name!r} ({expected.type.value}) missing or wrong type: {exc}"
            ) from exc


async def declare_worker_queues(
    channel: aio_pika.abc.AbstractChannel,
    *,
    lane: str,
    worker_id: str,
) -> tuple[aio_pika.abc.AbstractQueue, aio_pika.abc.AbstractQueue]:
    """Declare ``q.task.execute.<lane>`` and ``q.task.control.<worker_id>``.

    Returns ``(execute_queue, control_queue)``. Both are quorum queues; the
    control queue is bound auto-delete=True so it disappears when the worker
    disconnects.
    """
    execute_queue = await channel.declare_queue(
        f"q.task.execute.{lane}",
        durable=True,
        arguments={
            "x-queue-type": "quorum",
            "x-dead-letter-exchange": "task.dlx",
        },
    )
    task_exchange = await channel.declare_exchange(
        "task.exchange",
        type=aio_pika.ExchangeType.TOPIC,
        durable=True,
        passive=True,
    )
    await execute_queue.bind(task_exchange, routing_key=f"execute.*.{lane}")

    control_queue = await channel.declare_queue(
        f"q.task.control.{worker_id}",
        durable=False,
        auto_delete=True,
        arguments={
            "x-queue-type": "quorum",
        },
    )
    control_exchange = await channel.declare_exchange(
        "task.control",
        type=aio_pika.ExchangeType.DIRECT,
        durable=True,
        passive=True,
    )
    await control_queue.bind(control_exchange, routing_key=f"control.{worker_id}")

    return execute_queue, control_queue
