"""Control signal listener — RMQ + Redis pub/sub dual-channel.

Receives ``pause`` / ``resume`` / ``cancel`` for the current run via two
independent channels (spec: worker-control-handling + worker-messaging →
"Control Signal Listener" + design D9). Whichever channel delivers first wins;
duplicates from the slow channel are deduplicated by
``(run_id, action, issued_at)`` via a small LRU.

Dynamic-binding contract (design D1): the control queue
``q.task.control.<worker_id>`` is declared once at startup but is **not**
statically bound. When the consumer claims a run for task ``T`` it calls
``bind_for(T)`` (routing key ``task.<T>`` on the *topic* exchange
``task.control``) before attaching ``current_run``, and ``unbind_for(T)`` after
clearing it. The dispatcher then filters incoming deliveries by
``current_run.run_id`` so a stale message landing in the bind/unbind race window
is dropped harmlessly.

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

import aio_pika
import structlog

if TYPE_CHECKING:
    from worker.core.metrics import Metrics
    from worker.core.mq_connection import MqConnection
    from worker.core.run_context import RunContext

_VALID_ACTIONS = frozenset({"pause", "resume", "cancel"})


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
        control_queue: aio_pika.abc.AbstractQueue | None = None,
        control_exchange: aio_pika.abc.AbstractExchange | None = None,
        logger: structlog.stdlib.BoundLogger | None = None,
    ) -> None:
        self._worker_id = worker_id
        self._mq = mq
        self._redis_url = redis_url
        self._metrics = metrics
        # Declared once by ``declare_worker_queues``; bindings are dynamic per
        # claim (design D1). May be ``None`` in dispatch-only unit tests that
        # never call ``run`` / ``bind_for``.
        self._queue = control_queue
        self._exchange = control_exchange
        self._log = logger or structlog.get_logger().bind(component="control")
        self._dedup = _LruSet(DEDUP_CAPACITY)
        self.current_run: RunContext | None = None

    def attach(
        self,
        queue: aio_pika.abc.AbstractQueue,
        exchange: aio_pika.abc.AbstractExchange,
    ) -> None:
        """Late-bind the control queue + exchange handles (used by lifecycle)."""
        self._queue = queue
        self._exchange = exchange

    async def bind_for(self, task_id: Any) -> None:
        """Bind the control queue to ``task.<task_id>`` on the topic exchange.

        Called on the run-claim hot path BEFORE ``current_run`` is set (spec:
        worker-control-handling → "Per-Task Dynamic Binding"). Raises on broker
        failure so the consumer can nack-requeue the execute message.
        """
        assert self._queue is not None and self._exchange is not None  # noqa: S101
        await self._queue.bind(self._exchange, routing_key=f"task.{task_id}")

    async def unbind_for(self, task_id: Any) -> None:
        """Unbind ``task.<task_id>``; best-effort (see consumer's finally)."""
        assert self._queue is not None and self._exchange is not None  # noqa: S101
        await self._queue.unbind(self._exchange, routing_key=f"task.{task_id}")

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
        assert self._queue is not None  # noqa: S101 - lifecycle attaches before run
        # Best-effort delivery: auto-ack is safe because control signals are
        # idempotent at the application level (token set is idempotent). The
        # queue is declared (and its bindings managed) elsewhere — the listener
        # is a pure consumer + dispatcher here.
        async with self._queue.iterator(no_ack=True) as it:
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
        """Parse one control delivery and react. Bumps exactly one metric cell.

        Payload shape (from ``add-task-control-api``):
        ``{task_id, version_id?, run_id?, action, reason, issued_at}``. Unknown
        / extra fields are ignored; the dedup key is
        ``(run_id, action, issued_at)`` (design D3).
        """
        try:
            payload = json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            self._log.warning("control_parse_failed", source=source, error=str(exc))
            self._metrics.control_signals_total.labels(
                action="unknown", outcome="parse_error"
            ).inc()
            return
        run_id = str(payload.get("run_id", ""))
        action = str(payload.get("action", ""))
        issued_at = str(payload.get("issued_at", ""))

        if action not in _VALID_ACTIONS:
            self._log.warning("control_unknown_action", action=action, source=source)
            self._metrics.control_signals_total.labels(
                action="unknown", outcome="parse_error"
            ).inc()
            return

        dedup_key = (run_id, action, issued_at)
        if not self._dedup.add(dedup_key):
            self._metrics.control_signals_total.labels(action=action, outcome="dedup_drop").inc()
            return  # duplicate across the MQ + Redis dual-channel

        ctx = self.current_run
        if ctx is None or str(ctx.run_id) != run_id:
            # Unknown / not-current run: ack/discard silently (spec). Covers the
            # bind/unbind race window and HA cross-run tail messages.
            self._log.debug(
                "control_for_unknown_run",
                run_id=run_id,
                action=action,
                source=source,
            )
            self._metrics.control_signals_total.labels(action=action, outcome="unknown_run").inc()
            return

        await self._react(ctx, action)
        self._metrics.control_signals_total.labels(action=action, outcome="handled").inc()

    async def _react(self, ctx: RunContext, action: str) -> None:
        """Emit the acknowledgement status event and flip the in-memory token.

        Emit/flip ordering is contract (spec: worker-control-handling →
        "Token Flip + Cancel-During-Pause Race"). The token flip ALWAYS runs
        even if the status emit fails — ``_emit_status_event`` swallows.
        """
        if action == "pause":
            await self._emit_status_event(ctx, "paused", action="pause")
            ctx.pause_token.set_paused()
        elif action == "resume":
            # Unblock the agent BEFORE the front-end sees running, so subsequent
            # step events follow the status:running event in seq order.
            ctx.pause_token.resume()
            await self._emit_status_event(ctx, "running", action="resume")
        elif action == "cancel":
            await self._emit_status_event(ctx, "cancelling", action="cancel")
            # Set cancel BEFORE resuming the pause so a racing waker that wakes
            # between the two calls still observes the cancel-set state and the
            # next _check_boundary raises immediately (design D6).
            ctx.cancel_token.set()
            ctx.pause_token.resume()

    async def _emit_status_event(self, ctx: RunContext, status: str, *, action: str) -> None:
        """Publish ``kind=status`` ack; swallow + log WARN on failure (design D5)."""
        try:
            await ctx.event_publisher.publish_event(
                task_id=str(ctx.task_id),
                version_id=str(ctx.version_id),
                run_id=str(ctx.run_id),
                task_type=ctx.task_type,
                kind="status",
                payload={"status": status},
                seq=ctx.next_event_seq(),
                traceparent=ctx.traceparent,
            )
        except Exception as exc:  # noqa: BLE001 - never break the host loop
            self._log.warning(
                "control_status_emit_failed",
                action=action,
                status=status,
                run_id=str(ctx.run_id),
                error=str(exc),
            )
            self._metrics.control_emit_failed_total.labels(action=action).inc()


def _safe_json_dumps(payload: dict[str, Any]) -> bytes:
    return json.dumps(payload, separators=(",", ":"), default=str).encode("utf-8")


# Re-exported for tests / runtime
__all__ = [
    "ControlListener",
    "DEDUP_CAPACITY",
]
