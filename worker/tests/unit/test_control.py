"""Unit tests for the control signal dispatcher and dedup."""

from __future__ import annotations

import json
from typing import Any
from unittest.mock import MagicMock
from uuid import uuid4

import structlog
from worker.core.control import ControlListener, _LruSet
from worker.core.metrics import build_metrics
from worker.core.run_context import CancelToken, PauseToken


def test_lru_set_dedup_and_eviction() -> None:
    s = _LruSet(capacity=3)
    assert s.add(("r", "cancel", "t1")) is True
    assert s.add(("r", "cancel", "t1")) is False
    s.add(("r", "cancel", "t2"))
    s.add(("r", "cancel", "t3"))
    s.add(("r", "cancel", "t4"))  # evicts t1
    assert s.add(("r", "cancel", "t1")) is True  # readmittable after eviction


def _make_ctx(run_id: Any) -> MagicMock:
    ctx = MagicMock()
    ctx.run_id = run_id
    ctx.cancel_token = CancelToken()
    ctx.pause_token = PauseToken()
    ctx.logger = structlog.get_logger()
    return ctx


def _make_listener(ctx: Any | None = None) -> ControlListener:
    mq = MagicMock()
    listener = ControlListener(
        worker_id="wk-1",
        mq=mq,
        redis_url=None,
        metrics=build_metrics(),
    )
    listener.current_run = ctx
    return listener


async def test_cancel_sets_token() -> None:
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    listener = _make_listener(ctx)
    body = json.dumps({"run_id": str(run_id), "action": "cancel", "ts": "t1"}).encode()
    await listener._dispatch_payload(body, source="rmq")
    assert ctx.cancel_token.is_set()


async def test_pause_then_resume_toggles_token() -> None:
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    listener = _make_listener(ctx)
    await listener._dispatch_payload(
        json.dumps({"run_id": str(run_id), "action": "pause", "ts": "t1"}).encode(),
        source="rmq",
    )
    assert ctx.pause_token.is_paused()
    await listener._dispatch_payload(
        json.dumps({"run_id": str(run_id), "action": "resume", "ts": "t2"}).encode(),
        source="redis",
    )
    assert not ctx.pause_token.is_paused()


async def test_duplicate_signal_dedup() -> None:
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    listener = _make_listener(ctx)
    body = json.dumps({"run_id": str(run_id), "action": "cancel", "ts": "t1"}).encode()
    await listener._dispatch_payload(body, source="rmq")
    # Same signal via redis must NOT double-increment the metric.
    samples_before = list(listener._metrics.control_signals_total.collect())
    await listener._dispatch_payload(body, source="redis")
    samples_after = list(listener._metrics.control_signals_total.collect())

    # collect() yields aggregate metric families; samples count remained the same.
    def _sum(samples: Any) -> float:
        total = 0.0
        for family in samples:
            for sample in family.samples:
                if sample.name.endswith("_total"):
                    total += sample.value
        return total

    assert _sum(samples_before) == _sum(samples_after)


async def test_signal_for_unknown_run_is_ignored() -> None:
    listener = _make_listener(None)
    body = json.dumps({"run_id": str(uuid4()), "action": "cancel", "ts": "t1"}).encode()
    await listener._dispatch_payload(body, source="rmq")
    # No exception, no metric change — implicit assertion.


async def test_unknown_action_is_logged_not_raised() -> None:
    listener = _make_listener(_make_ctx(uuid4()))
    body = json.dumps({"run_id": str(uuid4()), "action": "explode", "ts": "t1"}).encode()
    await listener._dispatch_payload(body, source="rmq")  # must not raise


async def test_malformed_payload_swallowed() -> None:
    listener = _make_listener(_make_ctx(uuid4()))
    await listener._dispatch_payload(b"not json", source="rmq")  # must not raise
