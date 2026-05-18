"""Unit tests for the publisher seq registry and buffer behaviour."""

from __future__ import annotations

from typing import Any
from unittest.mock import AsyncMock, MagicMock
from uuid import uuid4

import pytest
from worker.core.messages import CostEvent
from worker.core.metrics import build_metrics
from worker.core.publisher import (
    CostEventPublisher,
    EventPublisher,
    ProgrammingError,
    _SeqRegistry,
)


def test_seq_registry_rejects_non_increasing() -> None:
    reg = _SeqRegistry()
    reg.admit("run-1", "task", "status", 1)
    reg.admit("run-1", "task", "status", 2)
    with pytest.raises(ProgrammingError):
        reg.admit("run-1", "task", "status", 2)
    with pytest.raises(ProgrammingError):
        reg.admit("run-1", "task", "status", 1)


def test_seq_registry_independent_namespaces() -> None:
    reg = _SeqRegistry()
    reg.admit("run-1", "task", "status", 5)
    # Cost namespace is independent; same run id; same kind name allowed.
    reg.admit("run-1", "cost", "status", 1)


def _fake_mq(success: bool = True) -> tuple[Any, AsyncMock]:
    """Build a mock ``MqConnection`` whose channel/exchange/publish are async mocks."""
    mq = MagicMock()
    channel = MagicMock()
    exchange = MagicMock()

    publish_mock = AsyncMock()
    if not success:
        publish_mock.side_effect = ConnectionError("broker down")
    exchange.publish = publish_mock

    declare_exchange = AsyncMock(return_value=exchange)
    channel.declare_exchange = declare_exchange

    mq.channel = AsyncMock(return_value=channel)
    return mq, publish_mock


async def test_cost_publisher_buffers_on_failure() -> None:
    metrics = build_metrics()
    mq, _ = _fake_mq(success=False)
    pub = CostEventPublisher(mq, metrics=metrics, buffer_capacity=10)
    await pub.publish_cost(
        task_id=str(uuid4()),
        version_id=str(uuid4()),
        run_id=str(uuid4()),
        kind="llm",
        resource_name="claude-opus-4-7",
        seq=1,
        input_tokens=100,
        output_tokens=50,
        duration_ms=1234,
    )
    assert pub.buffered == 1


async def test_cost_publisher_drains_on_reconnect() -> None:
    metrics = build_metrics()
    mq, publish_mock = _fake_mq(success=False)
    pub = CostEventPublisher(mq, metrics=metrics, buffer_capacity=10)
    run_id = str(uuid4())
    task_id = str(uuid4())
    version_id = str(uuid4())
    for seq in range(1, 6):
        await pub.publish_cost(
            task_id=task_id,
            version_id=version_id,
            run_id=run_id,
            kind="llm",
            resource_name="claude-opus-4-7",
            seq=seq,
            input_tokens=10,
            output_tokens=5,
            duration_ms=100,
        )
    assert pub.buffered == 5

    # Flip publish to succeed and drain.
    publish_mock.side_effect = None
    publish_mock.return_value = None
    await pub.drain()
    assert pub.buffered == 0
    assert publish_mock.await_count >= 5


async def test_event_publisher_rejects_decreasing_seq() -> None:
    metrics = build_metrics()
    mq, publish_mock = _fake_mq(success=True)
    pub = EventPublisher(mq, metrics=metrics)
    common = {
        "task_id": str(uuid4()),
        "version_id": str(uuid4()),
        "run_id": str(uuid4()),
        "task_type": "code-gen",
    }
    await pub.publish_event(**common, kind="status", payload={"status": "running"}, seq=1)
    await pub.publish_event(**common, kind="status", payload={"status": "ok"}, seq=2)
    with pytest.raises(ProgrammingError):
        await pub.publish_event(**common, kind="status", payload={"status": "x"}, seq=1)


async def test_cost_publisher_drop_oldest_when_buffer_full() -> None:
    metrics = build_metrics()
    mq, _ = _fake_mq(success=False)
    pub = CostEventPublisher(mq, metrics=metrics, buffer_capacity=3)
    run_id = str(uuid4())
    task_id = str(uuid4())
    version_id = str(uuid4())
    for seq in range(1, 6):  # 5 events, capacity 3
        await pub.publish_cost(
            task_id=task_id,
            version_id=version_id,
            run_id=run_id,
            kind="llm",
            resource_name="claude-opus-4-7",
            seq=seq,
            duration_ms=10,
        )
    assert pub.buffered == 3
    # The two oldest should be dropped (seq 1, 2).
    seqs = [event.seq for event, _ in pub._buffer]
    assert seqs == [3, 4, 5]


def test_cost_event_model_smoke() -> None:
    e = CostEvent(
        task_id=uuid4(),
        version_id=uuid4(),
        run_id=uuid4(),
        seq=1,
        kind="llm",
        resource_name="claude-opus-4-7",
        input_tokens=10,
        output_tokens=5,
        duration_ms=100,
        occurred_at=__import__("datetime").datetime.now(__import__("datetime").timezone.utc),
    )
    assert e.kind == "llm"
