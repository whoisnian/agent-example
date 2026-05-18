"""End-to-end integration test for the consumer pipeline.

Boots the API-owned exchanges, publishes one ``task.execute`` message, and
asserts the scaffold's dispatcher path: a ``status=running`` event is
published, then an ``error{code=unimplemented}`` event, and the message lands
on the DLX queue.
"""

from __future__ import annotations

import asyncio
import json
from typing import Any
from uuid import uuid4

import aio_pika
import pytest
import structlog
from worker.core.consumer import TaskConsumer
from worker.core.control import ControlListener
from worker.core.dispatcher import ExecutionDispatcher
from worker.core.metrics import build_metrics
from worker.core.mq_connection import (
    MqConnection,
    assert_topology,
    declare_worker_queues,
)
from worker.core.persistence import Persistence
from worker.core.publisher import CostEventPublisher, EventPublisher
from worker.core.storage import OssClient

pytestmark = pytest.mark.integration


async def _bootstrap_exchanges(channel: aio_pika.abc.AbstractChannel) -> None:
    await channel.declare_exchange("task.exchange", type=aio_pika.ExchangeType.TOPIC, durable=True)
    await channel.declare_exchange("task.control", type=aio_pika.ExchangeType.DIRECT, durable=True)
    await channel.declare_exchange("task.events", type=aio_pika.ExchangeType.TOPIC, durable=True)
    dlx = await channel.declare_exchange(
        "task.dlx", type=aio_pika.ExchangeType.DIRECT, durable=True
    )
    await channel.declare_exchange("cost.exchange", type=aio_pika.ExchangeType.TOPIC, durable=True)
    # DLX queue + binding so we can observe poison / unimplemented messages.
    dlq = await channel.declare_queue("q.task.dlx", durable=True)
    await dlq.bind(dlx, routing_key="#")


async def _drain_queue(
    queue: aio_pika.abc.AbstractQueue, count: int, timeout: float
) -> list[bytes]:
    bodies: list[bytes] = []
    end = asyncio.get_event_loop().time() + timeout
    async with queue.iterator(no_ack=True) as it:
        async for msg in it:
            bodies.append(msg.body)
            if len(bodies) >= count:
                return bodies
            if asyncio.get_event_loop().time() >= end:
                return bodies
    return bodies


async def test_unimplemented_dispatch_round_trip(
    rmq_container,  # type: ignore[no-untyped-def]
    pg_pool,  # type: ignore[no-untyped-def]
    minio_container,  # type: ignore[no-untyped-def]
) -> None:
    url = rmq_container.get_connection_url()
    mq = MqConnection(url)
    await mq.connect()
    channel = await mq.channel()
    await _bootstrap_exchanges(channel)
    await assert_topology(channel)

    worker_id = "wk-test"
    lane = "default"
    execute_q, _ctl_q = await declare_worker_queues(channel, lane=lane, worker_id=worker_id)

    persistence = Persistence(pg_pool, heartbeat_interval_seconds=5.0)
    endpoint = (
        f"http://{minio_container.get_container_host_ip()}:{minio_container.get_exposed_port(9000)}"
    )
    oss = OssClient(
        endpoint_url=endpoint,
        bucket="worker-bucket",
        access_key_id=minio_container.access_key,
        access_key_secret=minio_container.secret_key,
    )
    await oss.ensure_bucket()

    metrics = build_metrics()
    logger = structlog.get_logger().bind(worker_id=worker_id)
    events = EventPublisher(mq, metrics=metrics, logger=logger)
    costs = CostEventPublisher(mq, metrics=metrics, logger=logger)
    control = ControlListener(
        worker_id=worker_id, mq=mq, redis_url=None, metrics=metrics, logger=logger
    )

    consumer = TaskConsumer(
        worker_id=worker_id,
        lane=lane,
        mq_channel=channel,
        queue=execute_q,
        persistence=persistence,
        oss_client=oss,
        event_publisher=events,
        cost_publisher=costs,
        dispatcher=ExecutionDispatcher(),
        control_listener=control,
        metrics=metrics,
        logger=logger,
        heartbeat_interval=1.0,
        checkpoint_inline_bytes=8 * 1024,
    )

    # Subscribe to events + DLX so we can assert outputs.
    events_q = await channel.declare_queue("test.events.sink", auto_delete=True, durable=False)
    events_x = await channel.declare_exchange(
        "task.events", type=aio_pika.ExchangeType.TOPIC, durable=True, passive=True
    )
    await events_q.bind(events_x, routing_key="event.#")
    dlx_q = await channel.declare_queue("q.task.dlx", durable=True, passive=True)

    # Publish a task.execute message.
    task_id = uuid4()
    version_id = uuid4()
    run_id = uuid4()
    payload = {
        "msg_id": str(uuid4()),
        "idempotency_key": f"key-{uuid4()}",
        "task_id": str(task_id),
        "version_id": str(version_id),
        "run_id": str(run_id),
        "attempt_no": 1,
        "task_type": "code-gen",
        "prompt": "hello",
        "params": {},
        "tenant_id": "demo",
    }
    task_x = await channel.declare_exchange(
        "task.exchange", type=aio_pika.ExchangeType.TOPIC, durable=True, passive=True
    )
    await task_x.publish(
        aio_pika.Message(body=json.dumps(payload).encode("utf-8")),
        routing_key=f"execute.code-gen.{lane}",
    )

    consumer_task = asyncio.create_task(consumer.run())
    try:
        # Wait for both events to appear (running + error).
        bodies = await _drain_queue(events_q, count=2, timeout=15.0)
        kinds = [json.loads(b)["kind"] for b in bodies]
        assert "status" in kinds
        assert "error" in kinds
        error_body = next(json.loads(b) for b in bodies if json.loads(b)["kind"] == "error")
        assert error_body["payload"]["code"] == "unimplemented"

        # The original delivery should land on the DLX.
        dlx_bodies = await _drain_queue(dlx_q, count=1, timeout=15.0)
        assert dlx_bodies, "expected at least one message on task.dlx"
    finally:
        consumer.stop()
        consumer_task.cancel()
        with __import__("contextlib").suppress(Exception):
            await consumer_task
        await mq.close()


def _unused(_x: Any) -> None:
    """Placeholder to satisfy mypy strict on unused imports if removed later."""
