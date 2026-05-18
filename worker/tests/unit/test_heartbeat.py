"""Unit tests for heartbeat loop."""

from __future__ import annotations

import asyncio
from unittest.mock import AsyncMock, MagicMock
from uuid import uuid4

import structlog
from worker.core.heartbeat import heartbeat_loop
from worker.core.metrics import build_metrics
from worker.core.run_context import CancelToken


def _make_ctx() -> MagicMock:
    ctx = MagicMock()
    ctx.run_id = uuid4()
    ctx.cancel_token = CancelToken()
    ctx.logger = structlog.get_logger()
    return ctx


async def test_heartbeat_updates_until_cancel() -> None:
    ctx = _make_ctx()
    persistence = MagicMock()
    persistence.update_heartbeat = AsyncMock(return_value=True)
    metrics = build_metrics()
    task = asyncio.create_task(
        heartbeat_loop(
            ctx=ctx,
            worker_run_id=ctx.run_id,
            persistence=persistence,
            interval_seconds=0.05,
            metrics=metrics,
        )
    )
    await asyncio.sleep(0.18)
    ctx.cancel_token.set()
    await task
    assert persistence.update_heartbeat.await_count >= 2


async def test_three_failures_cancel_token_set() -> None:
    ctx = _make_ctx()
    persistence = MagicMock()
    persistence.update_heartbeat = AsyncMock(side_effect=ConnectionError("db down"))
    metrics = build_metrics()
    await heartbeat_loop(
        ctx=ctx,
        worker_run_id=ctx.run_id,
        persistence=persistence,
        interval_seconds=0.01,
        metrics=metrics,
    )
    assert ctx.cancel_token.is_set()


async def test_cas_lost_stops_loop() -> None:
    ctx = _make_ctx()
    persistence = MagicMock()
    persistence.update_heartbeat = AsyncMock(return_value=False)
    metrics = build_metrics()
    await heartbeat_loop(
        ctx=ctx,
        worker_run_id=ctx.run_id,
        persistence=persistence,
        interval_seconds=0.01,
        metrics=metrics,
    )
    assert ctx.cancel_token.is_set()
