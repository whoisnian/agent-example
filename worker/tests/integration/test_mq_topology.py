"""Integration tests for RMQ topology assertion against a live RabbitMQ."""

from __future__ import annotations

import aio_pika
import pytest
from worker.core.mq_connection import (
    MqConnection,
    TopologyError,
    assert_topology,
    declare_worker_queues,
)

pytestmark = pytest.mark.integration


async def _bootstrap_exchanges(channel: aio_pika.abc.AbstractChannel) -> None:
    """Declare the API-owned exchanges the Worker expects to find."""
    await channel.declare_exchange("task.exchange", type=aio_pika.ExchangeType.TOPIC, durable=True)
    await channel.declare_exchange("task.control", type=aio_pika.ExchangeType.DIRECT, durable=True)
    await channel.declare_exchange("task.events", type=aio_pika.ExchangeType.TOPIC, durable=True)
    await channel.declare_exchange("task.dlx", type=aio_pika.ExchangeType.DIRECT, durable=True)
    await channel.declare_exchange("cost.exchange", type=aio_pika.ExchangeType.TOPIC, durable=True)


async def test_topology_assert_passes_when_all_present(rmq_url: str) -> None:
    url = rmq_url
    mq = MqConnection(url)
    await mq.connect()
    channel = await mq.channel()
    await _bootstrap_exchanges(channel)
    await assert_topology(channel)
    await mq.close()


async def test_topology_assert_fails_when_exchange_missing(rmq_url: str) -> None:
    url = rmq_url
    mq = MqConnection(url)
    await mq.connect()
    channel = await mq.channel()
    # Declare some but not all — task.events is intentionally missing. The
    # session-scoped broker may already hold every exchange from earlier
    # tests, so delete task.events to make the missing case deterministic.
    await channel.declare_exchange("task.exchange", type=aio_pika.ExchangeType.TOPIC, durable=True)
    await channel.exchange_delete("task.events")
    with pytest.raises(TopologyError):
        await assert_topology(channel)
    await mq.close()


async def test_declare_worker_queues_idempotent(rmq_url: str) -> None:
    url = rmq_url
    mq = MqConnection(url)
    await mq.connect()
    channel = await mq.channel()
    await _bootstrap_exchanges(channel)
    exec_q, ctl_q, _ctl_x = await declare_worker_queues(
        channel, lane="default", worker_id="wk-test-1"
    )
    assert exec_q.name == "q.task.execute.default"
    assert ctl_q.name == "q.task.control.wk-test-1"
    # Re-declaring is fine (idempotent).
    exec_q2, ctl_q2, _ctl_x2 = await declare_worker_queues(
        channel, lane="default", worker_id="wk-test-1"
    )
    assert exec_q2.name == exec_q.name
    assert ctl_q2.name == ctl_q.name
    await mq.close()
