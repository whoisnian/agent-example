"""Control signal listener — RMQ + Redis pub/sub dual-channel.

Receives ``pause`` / ``resume`` / ``cancel`` for the current run via two
independent channels (spec: worker-messaging → "Control Signal Listener" +
design D9). Whichever channel delivers first wins; duplicates from the slow
channel are deduplicated by ``(run_id, action, ts)`` via a small LRU.

The listener does not own the ``RunContext``; the consumer hands it (or
``None`` when idle) via the ``current_run`` attribute. This keeps the
listener decoupled from the message loop and lets it accept signals even
while a run is between checkpoints.
"""

from __future__ import annotations

import asyncio
import json
from collections import OrderedDict
from typing import TYPE_CHECKING, Any

import structlog

if TYPE_CHECKING:
    from worker.core.metrics import Metrics
    from worker.core.mq_connection import MqConnection
    from worker.core.run_context import RunContext


DEDUP_CAPACITY = 256


class _LruSet:
    """Tiny insertion-ordered LRU set."""

    def __init__(self, capacity: int) -> None:
        self._capacity = capacity
        self._items: OrderedDict[tuple[str, str, str], None] = OrderedDict()

    def __contains__(self, key: tuple[str, str, str]) -> bool:
        return key in self._items

    def add(self, key: tuple[str, str, str]) -> bool:
        """Return ``True`` if the key was new, ``False`` if duplicate."""
        if key in self._items:
            self._items.move_to_end(key)
            return False
        self._items[key] = None
        if len(self._items) > self._capacity:
            self._items.popitem(last=False)
        return True


class ControlListener:
    """Subscribes to both RMQ control queue and Redis pub/sub control channel."""

    def __init__(
        self,
        *,
        worker_id: str,
        mq: MqConnection,
        redis_url: str | None,
        metrics: Metrics,
        logger: structlog.stdlib.BoundLogger | None = None,
    ) -> None:
        self._worker_id = worker_id
        self._mq = mq
        self._redis_url = redis_url
        self._metrics = metrics
        self._log = logger or structlog.get_logger().bind(component="control")
        self._dedup = _LruSet(DEDUP_CAPACITY)
        self.current_run: RunContext | None = None

    async def run(self) -> None:
        """Start both subscribers concurrently. Returns when cancelled."""
        try:
            async with asyncio.TaskGroup() as tg:
                tg.create_task(self._run_rmq())
                if self._redis_url:
                    tg.create_task(self._run_redis())
        except asyncio.CancelledError:
            raise

    async def _run_rmq(self) -> None:
        channel = await self._mq.channel()
        queue = await channel.declare_queue(
            f"q.task.control.{self._worker_id}",
            durable=False,
            auto_delete=True,
            arguments={"x-queue-type": "quorum"},
        )
        # Best-effort delivery: auto-ack is safe because control signals are
        # idempotent at the application level (token set is idempotent).
        async with queue.iterator(no_ack=True) as it:
            async for message in it:
                await self._dispatch_payload(message.body, source="rmq")

    async def _run_redis(self) -> None:
        # Import inside method so unit tests that don't touch redis don't pull
        # the client into their import graph.
        import redis.asyncio as redis_async

        client: Any = (
            redis_async.from_url(self._redis_url)  # type: ignore[no-untyped-call]
            if self._redis_url
            else None
        )
        if client is None:  # pragma: no cover - guarded by caller
            return
        pubsub = client.pubsub()
        try:
            await pubsub.subscribe(f"control:{self._worker_id}")
            async for message in pubsub.listen():
                if message.get("type") != "message":
                    continue
                data = message["data"]
                if isinstance(data, bytes):
                    body: bytes = data
                else:
                    body = str(data).encode("utf-8")
                await self._dispatch_payload(body, source="redis")
        finally:
            await pubsub.close()
            await client.aclose()

    async def _dispatch_payload(self, body: bytes, *, source: str) -> None:
        try:
            payload = json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            self._log.warning("control_parse_failed", source=source, error=str(exc))
            return
        run_id = str(payload.get("run_id", ""))
        action = str(payload.get("action", ""))
        ts = str(payload.get("ts", ""))
        dedup_key = (run_id, action, ts)
        if not self._dedup.add(dedup_key):
            return  # duplicate

        if action not in {"pause", "resume", "cancel"}:
            self._log.warning("control_unknown_action", action=action, source=source)
            return

        ctx = self.current_run
        if ctx is None or str(ctx.run_id) != run_id:
            # Unknown / not-current run: ack/discard silently (spec).
            self._log.debug(
                "control_for_unknown_run",
                run_id=run_id,
                action=action,
                source=source,
            )
            return

        self._metrics.control_signals_total.labels(action=action).inc()
        if action == "cancel":
            ctx.cancel_token.set()
        elif action == "pause":
            ctx.pause_token.set_paused()
        elif action == "resume":
            ctx.pause_token.resume()


def _safe_json_dumps(payload: dict[str, Any]) -> bytes:
    return json.dumps(payload, separators=(",", ":"), default=str).encode("utf-8")


# Re-exported for tests / runtime
__all__ = [
    "ControlListener",
    "DEDUP_CAPACITY",
]
