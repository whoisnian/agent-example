"""Unit tests for the control signal dispatcher, dedup, and binding lifecycle.

Covers worker-control-handling: payload shape (``issued_at``), status-event
acknowledgement emission, the cancel-during-pause race, the
``{action, outcome}`` metric, and the consumer's bind-before-claim /
unbind-after-release ordering (design D6/D8, reviewer S10/S11/S12).
"""

from __future__ import annotations

import asyncio
import json
from typing import Any
from unittest.mock import AsyncMock, MagicMock
from uuid import uuid4

import structlog
from worker.core.consumer import TaskConsumer
from worker.core.control import ControlListener, _LruSet
from worker.core.metrics import build_metrics
from worker.core.persistence import ClaimOutcome, ClaimResult
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
    ctx.task_id = uuid4()
    ctx.version_id = uuid4()
    ctx.task_type = "code"
    ctx.traceparent = None
    ctx.cancel_token = CancelToken()
    ctx.pause_token = PauseToken()
    ctx.logger = structlog.get_logger()
    ctx.event_publisher = AsyncMock()
    # Real monotonic seq counter so emission tests can assert seq values.
    counter = {"n": 0}

    def _next() -> int:
        counter["n"] += 1
        return counter["n"]

    ctx.next_event_seq.side_effect = _next
    return ctx


def _make_listener(ctx: Any | None = None) -> ControlListener:
    listener = ControlListener(
        worker_id="wk-1",
        mq=MagicMock(),
        redis_url=None,
        metrics=build_metrics(),
    )
    listener.current_run = ctx
    return listener


def _body(run_id: Any, action: str, *, issued_at: str, task_id: Any = "t-1") -> bytes:
    return json.dumps(
        {
            "task_id": str(task_id),
            "version_id": str(uuid4()),
            "run_id": str(run_id),
            "action": action,
            "reason": "manual",
            "issued_at": issued_at,
        }
    ).encode()


def _counter_value(listener: ControlListener, **labels: str) -> float:
    return listener._metrics.control_signals_total.labels(**labels)._value.get()


def _emit_failed_value(listener: ControlListener, action: str) -> float:
    return listener._metrics.control_emit_failed_total.labels(action=action)._value.get()


# --- 6.1 payload shape (issued_at) + dedup key --------------------------------


async def test_cancel_sets_token_new_payload_shape() -> None:
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    listener = _make_listener(ctx)
    await listener._dispatch_payload(
        _body(run_id, "cancel", issued_at="2026-06-01T00:00:00Z"), source="rmq"
    )
    assert ctx.cancel_token.is_set()
    assert _counter_value(listener, action="cancel", outcome="handled") == 1


async def test_pause_then_resume_toggles_token() -> None:
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    listener = _make_listener(ctx)
    await listener._dispatch_payload(_body(run_id, "pause", issued_at="t1"), source="rmq")
    assert ctx.pause_token.is_paused()
    await listener._dispatch_payload(_body(run_id, "resume", issued_at="t2"), source="redis")
    assert not ctx.pause_token.is_paused()


async def test_dedup_key_uses_issued_at() -> None:
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    listener = _make_listener(ctx)
    body = _body(run_id, "cancel", issued_at="same-issued-at")
    await listener._dispatch_payload(body, source="rmq")
    # Same (run_id, action, issued_at) via the slow channel is a dedup drop.
    await listener._dispatch_payload(body, source="redis")
    assert _counter_value(listener, action="cancel", outcome="handled") == 1
    assert _counter_value(listener, action="cancel", outcome="dedup_drop") == 1


async def test_signal_for_unknown_run_is_ignored() -> None:
    listener = _make_listener(None)
    await listener._dispatch_payload(_body(uuid4(), "cancel", issued_at="t1"), source="rmq")
    assert _counter_value(listener, action="cancel", outcome="unknown_run") == 1


async def test_unknown_action_counts_parse_error() -> None:
    listener = _make_listener(_make_ctx(uuid4()))
    await listener._dispatch_payload(_body(uuid4(), "explode", issued_at="t1"), source="rmq")
    assert _counter_value(listener, action="unknown", outcome="parse_error") == 1


async def test_malformed_payload_counts_parse_error() -> None:
    listener = _make_listener(_make_ctx(uuid4()))
    await listener._dispatch_payload(b"not json", source="rmq")  # must not raise
    assert _counter_value(listener, action="unknown", outcome="parse_error") == 1


# --- 6.2 cancel-during-pause race ---------------------------------------------


async def test_cancel_unblocks_paused_agent() -> None:
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    ctx.pause_token.set_paused()
    listener = _make_listener(ctx)

    waiter = asyncio.create_task(ctx.pause_token.wait_if_paused())
    await asyncio.sleep(0)  # let the waiter block
    assert not waiter.done()

    await listener._dispatch_payload(_body(run_id, "cancel", issued_at="t1"), source="rmq")

    await asyncio.wait_for(waiter, timeout=1.0)
    assert ctx.cancel_token.is_set()


async def test_cancel_sets_token_before_resuming_pause() -> None:
    """Reviewer S3: cancel_token MUST be set BEFORE pause_token.resume()."""
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    listener = _make_listener(ctx)

    order: list[str] = []
    real_set = ctx.cancel_token.set
    real_resume = ctx.pause_token.resume

    def spy_set() -> None:
        order.append("cancel_set")
        real_set()

    def spy_resume() -> None:
        order.append("pause_resume")
        real_resume()

    ctx.cancel_token.set = spy_set  # type: ignore[method-assign]
    ctx.pause_token.resume = spy_resume  # type: ignore[method-assign]

    await listener._dispatch_payload(_body(run_id, "cancel", issued_at="t1"), source="rmq")
    assert order == ["cancel_set", "pause_resume"]


# --- 6.3 status-event emission ------------------------------------------------


async def test_status_event_emission_triples_and_seq() -> None:
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    listener = _make_listener(ctx)

    await listener._dispatch_payload(_body(run_id, "pause", issued_at="t1"), source="rmq")
    await listener._dispatch_payload(_body(run_id, "resume", issued_at="t2"), source="rmq")
    await listener._dispatch_payload(_body(run_id, "cancel", issued_at="t3"), source="rmq")

    calls = ctx.event_publisher.publish_event.call_args_list
    assert len(calls) == 3
    emitted = [(c.kwargs["kind"], c.kwargs["payload"], c.kwargs["seq"]) for c in calls]
    assert emitted == [
        ("status", {"status": "paused"}, 1),
        ("status", {"status": "running"}, 2),
        ("status", {"status": "cancelling"}, 3),
    ]


async def test_resume_unblocks_before_emitting_running() -> None:
    """Reviewer S2: pause must be cleared BEFORE the running-status emit."""
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    ctx.pause_token.set_paused()
    listener = _make_listener(ctx)

    observed_paused_at_emit: list[bool] = []

    async def capture(**kwargs: Any) -> None:
        observed_paused_at_emit.append(ctx.pause_token.is_paused())

    ctx.event_publisher.publish_event = AsyncMock(side_effect=capture)

    await listener._dispatch_payload(_body(run_id, "resume", issued_at="t1"), source="rmq")
    assert observed_paused_at_emit == [False]


# --- 6.4 metric label shape ---------------------------------------------------


async def test_cross_run_bumps_unknown_run() -> None:
    listener = _make_listener(_make_ctx(uuid4()))  # current run is some other uuid
    other_run = uuid4()
    await listener._dispatch_payload(_body(other_run, "cancel", issued_at="t1"), source="rmq")
    assert _counter_value(listener, action="cancel", outcome="unknown_run") == 1
    assert _counter_value(listener, action="cancel", outcome="handled") == 0


# --- 6.5 consumer bind/unbind ordering (integration-flavor) -------------------


class _FakeAmqpQueue:
    def __init__(self, log: list[tuple[str, str]]) -> None:
        self._log = log

    async def bind(self, exchange: Any, routing_key: str) -> None:
        self._log.append(("bind", routing_key))

    async def unbind(self, exchange: Any, routing_key: str) -> None:
        self._log.append(("unbind", routing_key))


async def test_consumer_binds_before_claim_and_unbinds_after_release() -> None:
    log: list[tuple[str, str]] = []
    task_id = uuid4()

    listener = ControlListener(
        worker_id="wk-1",
        mq=MagicMock(),
        redis_url=None,
        metrics=build_metrics(),
        control_queue=_FakeAmqpQueue(log),  # type: ignore[arg-type]
        control_exchange=MagicMock(),
    )

    persistence = MagicMock()

    async def _claim(_run_row: Any) -> ClaimResult:
        log.append(("claim", ""))
        return ClaimResult(
            outcome=ClaimOutcome.FRESH,
            run_id=uuid4(),
            version_id=uuid4(),
            attempt_no=1,
            worker_run_id=uuid4(),
        )

    persistence.claim_or_skip_run = AsyncMock(side_effect=_claim)

    consumer = TaskConsumer(
        worker_id="wk-1",
        lane="default",
        mq_channel=MagicMock(),
        queue=MagicMock(),
        persistence=persistence,
        oss_client=MagicMock(),
        event_publisher=AsyncMock(),
        cost_publisher=AsyncMock(),
        dispatcher=MagicMock(),
        control_listener=listener,
        metrics=build_metrics(),
        logger=structlog.get_logger(),
        heartbeat_interval=30.0,
        checkpoint_inline_bytes=1024,
    )

    # Stub _execute to record the current_run set/clear ordering without driving
    # the full agent pipeline. Faithfully mirrors the real ordering: set
    # current_run before work, clear it in the finally before _process unbinds.
    async def fake_execute(_msg: Any, delivery: Any, *, claim_worker_run_id: Any) -> None:
        log.append(("current_run", "set"))
        listener.current_run = object()  # type: ignore[assignment]
        log.append(("execute", ""))
        listener.current_run = None
        log.append(("current_run", "clear"))
        await delivery.ack()

    consumer._execute = fake_execute  # type: ignore[method-assign]

    msg = MagicMock()
    msg.task_id = task_id
    msg.run_id = uuid4()
    msg.version_id = uuid4()
    msg.attempt_no = 1
    msg.idempotency_key = "idem-1"

    delivery = AsyncMock()
    await consumer._process(msg, delivery)

    assert log == [
        ("bind", f"task.{task_id}"),
        ("claim", ""),
        ("current_run", "set"),
        ("execute", ""),
        ("current_run", "clear"),
        ("unbind", f"task.{task_id}"),
    ]


async def test_consumer_bind_failure_nacks_without_claiming() -> None:
    listener = ControlListener(
        worker_id="wk-1",
        mq=MagicMock(),
        redis_url=None,
        metrics=build_metrics(),
        control_queue=MagicMock(),
        control_exchange=MagicMock(),
    )
    listener.bind_for = AsyncMock(side_effect=RuntimeError("broker recovering"))  # type: ignore[method-assign]

    persistence = MagicMock()
    persistence.claim_or_skip_run = AsyncMock()

    metrics = build_metrics()
    consumer = TaskConsumer(
        worker_id="wk-1",
        lane="default",
        mq_channel=MagicMock(),
        queue=MagicMock(),
        persistence=persistence,
        oss_client=MagicMock(),
        event_publisher=AsyncMock(),
        cost_publisher=AsyncMock(),
        dispatcher=MagicMock(),
        control_listener=listener,
        metrics=metrics,
        logger=structlog.get_logger(),
        heartbeat_interval=30.0,
        checkpoint_inline_bytes=1024,
    )

    msg = MagicMock()
    msg.task_id = uuid4()
    msg.run_id = uuid4()
    msg.version_id = uuid4()
    msg.attempt_no = 1
    msg.idempotency_key = "idem-1"

    delivery = AsyncMock()
    await consumer._process(msg, delivery)

    persistence.claim_or_skip_run.assert_not_called()
    delivery.nack.assert_awaited_once_with(requeue=True)
    assert metrics.messages_consumed_total.labels(outcome="bind_failed")._value.get() == 1


# --- 6.6 emit-failure-still-flips invariant -----------------------------------


async def _assert_emit_failure_still_flips(action: str, *, check) -> None:
    run_id = uuid4()
    ctx = _make_ctx(run_id)
    if action == "resume":
        ctx.pause_token.set_paused()
    ctx.event_publisher.publish_event = AsyncMock(side_effect=RuntimeError("mq down"))
    listener = _make_listener(ctx)

    with structlog.testing.capture_logs() as logs:
        await listener._dispatch_payload(_body(run_id, action, issued_at="t1"), source="rmq")

    check(ctx)
    assert _emit_failed_value(listener, action) == 1
    assert any(
        e.get("event") == "control_status_emit_failed" and e.get("log_level") == "warning"
        for e in logs
    )


async def test_emit_failure_still_cancels() -> None:
    await _assert_emit_failure_still_flips("cancel", check=lambda ctx: ctx.cancel_token.is_set())


async def test_emit_failure_still_pauses() -> None:
    await _assert_emit_failure_still_flips("pause", check=lambda ctx: ctx.pause_token.is_paused())


async def test_emit_failure_still_resumes() -> None:
    await _assert_emit_failure_still_flips(
        "resume", check=lambda ctx: not ctx.pause_token.is_paused()
    )
