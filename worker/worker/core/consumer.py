"""``task.execute`` consumer.

Owns the message → claim → dispatch → publish-events → ack/nack pipeline
described by ``worker-messaging`` and ``worker-execution-runtime``. The
heartbeat task and the control listener are siblings managed by the lifecycle
module (see ``worker.core.lifecycle``).
"""

from __future__ import annotations

import asyncio
import contextlib
import time
from typing import TYPE_CHECKING
from uuid import UUID

import aio_pika
import structlog
from pydantic import ValidationError

from worker.core.checkpoint import CheckpointStore
from worker.core.cost_meter import CostMeter
from worker.core.dispatcher import (
    AgentNotImplementedError,
    ExecutionDispatcher,
)
from worker.core.heartbeat import heartbeat_loop
from worker.core.logging import bind_run_context
from worker.core.messages import TaskExecuteMessage
from worker.core.persistence import ClaimOutcome
from worker.core.run_context import (
    CancelToken,
    PauseToken,
    RunContext,
    compute_oss_prefix,
)
from worker.core.tracing import get_tracer

if TYPE_CHECKING:
    from worker.core.control import ControlListener
    from worker.core.metrics import Metrics
    from worker.core.persistence import Persistence
    from worker.core.publisher import CostEventPublisher, EventPublisher
    from worker.core.storage import OssClient


_DEFAULT_TENANT_ID = "default-tenant"


class TaskConsumer:
    """Long-running consumer for ``q.task.execute.<lane>``."""

    def __init__(
        self,
        *,
        worker_id: str,
        lane: str,
        mq_channel: aio_pika.abc.AbstractChannel,
        queue: aio_pika.abc.AbstractQueue,
        persistence: Persistence,
        oss_client: OssClient,
        event_publisher: EventPublisher,
        cost_publisher: CostEventPublisher,
        dispatcher: ExecutionDispatcher,
        control_listener: ControlListener,
        metrics: Metrics,
        logger: structlog.stdlib.BoundLogger,
        heartbeat_interval: float,
        checkpoint_inline_bytes: int,
    ) -> None:
        self._worker_id = worker_id
        self._lane = lane
        self._channel = mq_channel
        self._queue = queue
        self._persistence = persistence
        self._oss = oss_client
        self._events = event_publisher
        self._costs = cost_publisher
        self._dispatcher = dispatcher
        self._control = control_listener
        self._metrics = metrics
        self._log = logger.bind(component="consumer", lane=lane)
        self._heartbeat_interval = heartbeat_interval
        self._checkpoint_inline_bytes = checkpoint_inline_bytes
        self._tracer = get_tracer("worker.consumer")
        self._stop = asyncio.Event()

    async def run(self) -> None:
        """Consume messages until ``stop()`` is called.

        Uses manual ack (``no_ack=False``); each in-flight message is fully
        handled before the next is taken from the queue (``prefetch=1`` set on
        the channel).
        """
        self._log.info("consumer_starting")
        async with self._queue.iterator(no_ack=False) as it:
            async for message in it:
                if self._stop.is_set():
                    await message.nack(requeue=True)
                    break
                await self._handle_message(message)

    def stop(self) -> None:
        self._stop.set()

    async def _handle_message(self, message: aio_pika.abc.AbstractIncomingMessage) -> None:
        start = time.monotonic()
        self._metrics.in_flight.set(1)
        try:
            try:
                parsed = TaskExecuteMessage.model_validate_json(message.body)
            except ValidationError as exc:
                self._log.warning("invalid_message_dlx", error=str(exc))
                self._metrics.invalid_message_total.inc()
                await message.nack(requeue=False)
                self._metrics.messages_consumed_total.labels(outcome="invalid").inc()
                return

            await self._process(parsed, message)
        finally:
            self._metrics.in_flight.set(0)
            self._metrics.message_processing_seconds.observe(time.monotonic() - start)

    async def _process(
        self,
        msg: TaskExecuteMessage,
        delivery: aio_pika.abc.AbstractIncomingMessage,
    ) -> None:
        # 1. Claim or skip via idempotency_key.
        from worker.core.persistence import RunRow  # local import to avoid cycle at top

        run_row = RunRow(
            run_id=msg.run_id,
            version_id=msg.version_id,
            attempt_no=msg.attempt_no,
            idempotency_key=msg.idempotency_key,
            worker_run_id=msg.run_id,  # scaffold: tie worker_run_id to run_id
        )
        claim = await self._persistence.claim_or_skip_run(run_row)

        if claim.outcome in {
            ClaimOutcome.ALREADY_SUCCEEDED,
            ClaimOutcome.ALREADY_FAILED,
            ClaimOutcome.ALREADY_CANCELLED,
        }:
            self._log.info(
                "duplicate_run_skipped",
                idempotency_key=msg.idempotency_key,
                outcome=claim.outcome.value,
            )
            await delivery.ack()
            self._metrics.messages_consumed_total.labels(outcome="duplicate").inc()
            return

        if claim.outcome == ClaimOutcome.RUNNING_BY_OTHER_RECENT:
            self._log.info(
                "owned_by_other_worker_nack",
                idempotency_key=msg.idempotency_key,
            )
            await delivery.nack(requeue=True)
            self._metrics.messages_consumed_total.labels(outcome="foreign_run").inc()
            return

        # fresh | running_stale_takeover → execute
        await self._execute(msg, delivery, claim_worker_run_id=run_row.worker_run_id)

    async def _execute(
        self,
        msg: TaskExecuteMessage,
        delivery: aio_pika.abc.AbstractIncomingMessage,
        *,
        claim_worker_run_id: UUID,
    ) -> None:
        traceparent = msg.traceparent or _read_traceparent_header(delivery)
        with self._tracer.start_as_current_span("worker.run") as span:
            trace_id_hex = f"{span.get_span_context().trace_id:032x}"
            tenant_id = msg.tenant_id or _DEFAULT_TENANT_ID
            ctx = RunContext(
                task_id=msg.task_id,
                version_id=msg.version_id,
                run_id=msg.run_id,
                attempt_no=msg.attempt_no,
                task_type=msg.task_type,
                worker_id=self._worker_id,
                tenant_id=tenant_id,
                oss_prefix=compute_oss_prefix(tenant_id, msg.task_id, msg.version_id),
                cancel_token=CancelToken(),
                pause_token=PauseToken(),
                cost_meter=None,  # type: ignore[arg-type]  # set below
                event_publisher=self._events,
                cost_publisher=self._costs,
                checkpoint_store=None,  # type: ignore[arg-type]
                oss_client=self._oss,
                logger=bind_run_context(
                    self._log, _LogShim(msg, self._worker_id, traceparent or "")
                ),
                trace_id=trace_id_hex,
                traceparent=traceparent,
                trace_span=span,
            )
            ctx.cost_meter = CostMeter(ctx)
            ctx.checkpoint_store = CheckpointStore(
                run_id=msg.run_id,
                oss_prefix=ctx.oss_prefix,
                persistence=self._persistence,
                oss_client=self._oss,
                inline_byte_limit=self._checkpoint_inline_bytes,
            )

            self._control.current_run = ctx
            terminal_status = "failed"
            try:
                await self._events.publish_event(
                    task_id=str(msg.task_id),
                    version_id=str(msg.version_id),
                    run_id=str(msg.run_id),
                    task_type=msg.task_type,
                    kind="status",
                    payload={"status": "running"},
                    seq=ctx.next_event_seq(),
                    traceparent=traceparent,
                )

                # Run heartbeat as a sibling task; dispatch is awaited inline
                # so its exception type (e.g. AgentNotImplementedError) is
                # preserved rather than wrapped in an ExceptionGroup.
                hb_task = asyncio.create_task(
                    heartbeat_loop(
                        ctx=ctx,
                        worker_run_id=claim_worker_run_id,
                        persistence=self._persistence,
                        interval_seconds=self._heartbeat_interval,
                        metrics=self._metrics,
                    ),
                    name="heartbeat",
                )
                dispatch_exc: BaseException | None = None
                try:
                    await self._dispatcher.dispatch(ctx, msg)
                except BaseException as exc:  # noqa: BLE001 - intentional catch-all
                    dispatch_exc = exc
                finally:
                    ctx.cancel_token.set()
                    with contextlib.suppress(BaseException):
                        await hb_task

                if isinstance(dispatch_exc, AgentNotImplementedError):
                    await self._publish_unimplemented(ctx, msg)
                    await delivery.nack(requeue=False)
                    self._metrics.messages_consumed_total.labels(outcome="unimplemented").inc()
                    return
                if isinstance(dispatch_exc, asyncio.CancelledError):
                    # Heartbeat watchdog cancelled the agent — requeue.
                    self._log.warning("dispatch_cancelled_by_runtime")
                    await delivery.nack(requeue=True)
                    self._metrics.messages_consumed_total.labels(outcome="cancelled").inc()
                    return
                if dispatch_exc is not None:
                    self._log.error("dispatch_error", error=str(dispatch_exc))
                    await self._publish_error(ctx, msg, code="internal", message=str(dispatch_exc))
                    await self._persistence.mark_run_terminal(
                        msg.run_id, status="failed", error={"code": "internal"}
                    )
                    await delivery.nack(requeue=False)
                    self._metrics.messages_consumed_total.labels(outcome="error").inc()
                    return

                # Success path — unreachable in scaffold (dispatcher always raises).
                terminal_status = "succeeded"
                await self._persistence.mark_run_terminal(msg.run_id, status="succeeded")
                await self._events.publish_event(
                    task_id=str(msg.task_id),
                    version_id=str(msg.version_id),
                    run_id=str(msg.run_id),
                    task_type=msg.task_type,
                    kind="status",
                    payload={"status": terminal_status},
                    seq=ctx.next_event_seq(),
                    traceparent=traceparent,
                )
                await delivery.ack()
                self._metrics.messages_consumed_total.labels(outcome="success").inc()
            finally:
                self._control.current_run = None

    async def _publish_unimplemented(self, ctx: RunContext, msg: TaskExecuteMessage) -> None:
        await self._events.publish_event(
            task_id=str(msg.task_id),
            version_id=str(msg.version_id),
            run_id=str(msg.run_id),
            task_type=msg.task_type,
            kind="error",
            payload={"code": "unimplemented", "message": f"no agent for {msg.task_type}"},
            seq=ctx.next_event_seq(),
            traceparent=ctx.traceparent,
        )
        await self._persistence.mark_run_terminal(
            msg.run_id, status="failed", error={"code": "unimplemented"}
        )

    async def _publish_error(
        self,
        ctx: RunContext,
        msg: TaskExecuteMessage,
        *,
        code: str,
        message: str,
    ) -> None:
        await self._events.publish_event(
            task_id=str(msg.task_id),
            version_id=str(msg.version_id),
            run_id=str(msg.run_id),
            task_type=msg.task_type,
            kind="error",
            payload={"code": code, "message": message},
            seq=ctx.next_event_seq(),
            traceparent=ctx.traceparent,
        )


class _LogShim:
    """Adapter to satisfy ``bind_run_context``'s ``RunContext`` typing.

    ``bind_run_context`` only reads a handful of attributes; we deliberately
    avoid passing a half-constructed ``RunContext`` (with ``trace_id`` derived
    from the span context) because the real ctx is built up in stages.
    """

    __slots__ = ("task_id", "run_id", "version_id", "step", "trace_id", "worker_id")

    def __init__(self, msg: TaskExecuteMessage, worker_id: str, trace_id: str) -> None:
        self.task_id = msg.task_id
        self.run_id = msg.run_id
        self.version_id = msg.version_id
        self.step = 0
        self.trace_id = trace_id
        self.worker_id = worker_id


def _read_traceparent_header(
    delivery: aio_pika.abc.AbstractIncomingMessage,
) -> str | None:
    headers = delivery.headers or {}
    value = headers.get("traceparent")
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="ignore")
    if isinstance(value, str):
        return value
    return None
