"""Unit tests for semantic title generation (add-semantic-task-title).

Covers the ``sanitize_title`` pure function and the ``TitleGenerator`` guards /
best-effort failure semantics, driven by the scripted fake model so the
``CostMeter``-style callback path is exercised with no network.
"""

from __future__ import annotations

import asyncio
import json
from types import SimpleNamespace
from typing import Any
from uuid import uuid4

from langchain_core.callbacks.base import BaseCallbackHandler
from prometheus_client import CollectorRegistry
from worker.core.messages import TaskExecuteMessage
from worker.core.metrics import build_metrics
from worker.core.run_context import CancelToken
from worker.core.title import TitleGenerator, sanitize_title

from tests.support.fake_model import FakeModelFactory, scripted_model

# --- sanitize_title ---------------------------------------------------------


def test_sanitize_passthrough_ascii() -> None:
    assert sanitize_title("Refactor the auth module") == "Refactor the auth module"


def test_sanitize_takes_first_non_empty_line() -> None:
    assert sanitize_title("\n\n  Build a music app  \nsecond line") == "Build a music app"


def test_sanitize_strips_wrapping_quotes_and_collapses_whitespace() -> None:
    assert sanitize_title('"Build   a\tmusic app"') == "Build a music app"
    assert sanitize_title("「重构  用户认证模块」") == "重构 用户认证模块"


def test_sanitize_all_whitespace_is_empty() -> None:
    assert sanitize_title("   \n\t  \n") == ""


def test_sanitize_truncates_long_ascii_within_runes() -> None:
    result = sanitize_title("a" * 100)
    assert result.endswith("…")
    assert len(result) <= 64
    assert len(result.encode("utf-8")) <= 200


def test_sanitize_truncates_long_cjk_within_runes_and_bytes() -> None:
    result = sanitize_title("汉" * 100)
    assert result.endswith("…")
    assert len(result) <= 64
    assert len(result.encode("utf-8")) <= 200


def test_sanitize_byte_limit_dominates_for_emoji() -> None:
    # 60 runes ≤ 64, but 4 bytes each = 240 bytes > 200 → byte-bound truncation.
    result = sanitize_title("🚀" * 60)
    assert result.endswith("…")
    assert len(result) <= 64
    assert len(result.encode("utf-8")) <= 200


def test_sanitize_exact_64_rune_cjk_passes_through() -> None:
    text = "汉" * 64  # 192 bytes — within both limits, no truncation.
    assert sanitize_title(text) == text


# --- TitleGenerator ----------------------------------------------------------


class RecordingHandler(BaseCallbackHandler):
    """Stands in for the run's CostMeter; records LLM-end callbacks."""

    raise_error = False
    run_inline = True

    def __init__(self) -> None:
        self.llm_ends: int = 0

    def on_llm_end(self, *args: Any, **kwargs: Any) -> None:
        self.llm_ends += 1


class FakeEventPublisher:
    def __init__(self, *, fail: bool = False) -> None:
        self.published: list[dict[str, Any]] = []
        self._fail = fail

    async def publish_event(self, **kwargs: Any) -> None:
        if self._fail:
            raise RuntimeError("mq down")
        self.published.append(kwargs)


class FakeCheckpointStore:
    def __init__(self, latest_val: Any = None) -> None:
        self._latest = latest_val

    async def latest(self) -> Any:
        return self._latest


class FakeLogger:
    def __init__(self) -> None:
        self.records: list[tuple[str, str, dict[str, Any]]] = []

    def bind(self, **kwargs: Any) -> FakeLogger:
        return self

    def info(self, event: str, **kwargs: Any) -> None:
        self.records.append(("info", event, kwargs))

    def warning(self, event: str, **kwargs: Any) -> None:
        self.records.append(("warning", event, kwargs))

    def events(self, level: str) -> list[str]:
        return [event for lvl, event, _ in self.records if lvl == level]


class FakeCtx:
    def __init__(
        self,
        *,
        latest_checkpoint: Any = None,
        publisher: FakeEventPublisher | None = None,
    ) -> None:
        self.task_id = uuid4()
        self.version_id = uuid4()
        self.run_id = uuid4()
        self.cancel_token = CancelToken()
        self.checkpoint_store = FakeCheckpointStore(latest_checkpoint)
        self.cost_meter = RecordingHandler()
        self.event_publisher = publisher if publisher is not None else FakeEventPublisher()
        self.logger = FakeLogger()
        self.traceparent = None
        self._seq = 0

    def next_event_seq(self) -> int:
        self._seq += 1
        return self._seq


def _message(*, gen_title: bool = True, task_type: str = "code-gen") -> TaskExecuteMessage:
    return TaskExecuteMessage.model_validate(
        {
            "msg_id": str(uuid4()),
            "idempotency_key": str(uuid4()),
            "task_id": str(uuid4()),
            "version_id": str(uuid4()),
            "run_id": str(uuid4()),
            "attempt_no": 1,
            "task_type": task_type,
            "prompt": "build a music app with playlists",
            "gen_title": gen_title,
        }
    )


class CountingFactory:
    """Wraps ``FakeModelFactory`` and counts ``get`` calls (LLM-call guard proof)."""

    def __init__(self, model: Any) -> None:
        self._inner = FakeModelFactory(model=model)
        self.gets: list[str] = []

    def get(self, model_key: str) -> Any:
        self.gets.append(model_key)
        return self._inner.get(model_key)


def _registry(task_type: str = "code-gen", model_key: str = "code") -> Any:
    agent = SimpleNamespace(spec=SimpleNamespace(model_key=model_key))
    return SimpleNamespace(get=lambda tt: agent if tt == task_type else None)


def _metrics() -> tuple[Any, CollectorRegistry]:
    reg = CollectorRegistry()
    return build_metrics(reg), reg


def _failures(reg: CollectorRegistry) -> float:
    value = reg.get_sample_value("worker_title_generation_failures_total")
    assert value is not None
    return value


def _generator(
    factory: Any,
    metrics: Any,
    *,
    title_model_key: str | None = None,
    timeout_seconds: float = 5.0,
) -> TitleGenerator:
    return TitleGenerator(
        model_factory=factory,
        agent_registry=_registry(),
        title_model_key=title_model_key,
        metrics=metrics,
        timeout_seconds=timeout_seconds,
    )


async def test_success_publishes_title_event_and_fires_cost_callback() -> None:
    metrics, reg = _metrics()
    factory = CountingFactory(scripted_model(['"Music app with playlists"']))
    ctx = FakeCtx()

    await _generator(factory, metrics).maybe_generate(ctx, _message())

    assert factory.gets == ["code"]  # fallback to the registered agent's model key
    [event] = ctx.event_publisher.published
    assert event["kind"] == "title"
    assert event["payload"] == {"title": "Music app with playlists"}
    assert event["seq"] == 1
    assert ctx.cost_meter.llm_ends == 1  # cost path exercised via callbacks
    assert _failures(reg) == 0


async def test_title_model_key_overrides_agent_key() -> None:
    metrics, _reg = _metrics()
    factory = CountingFactory(scripted_model(["Snappy title"]))
    ctx = FakeCtx()

    await _generator(factory, metrics, title_model_key="title").maybe_generate(ctx, _message())

    assert factory.gets == ["title"]


async def test_unflagged_message_makes_zero_calls() -> None:
    metrics, reg = _metrics()
    factory = CountingFactory(scripted_model(["unused"]))
    ctx = FakeCtx()

    await _generator(factory, metrics).maybe_generate(ctx, _message(gen_title=False))

    assert factory.gets == []
    assert ctx.event_publisher.published == []
    assert _failures(reg) == 0


async def test_run_with_checkpoint_skips_generation() -> None:
    metrics, reg = _metrics()
    factory = CountingFactory(scripted_model(["unused"]))
    ctx = FakeCtx(latest_checkpoint=object())  # redelivery / takeover resume

    await _generator(factory, metrics).maybe_generate(ctx, _message())

    assert factory.gets == []
    assert ctx.event_publisher.published == []
    assert _failures(reg) == 0  # skip, not failure
    assert "title_generation_skipped" in ctx.logger.events("info")


async def test_cancelled_run_skips_generation() -> None:
    metrics, reg = _metrics()
    factory = CountingFactory(scripted_model(["unused"]))
    ctx = FakeCtx()
    ctx.cancel_token.set()

    await _generator(factory, metrics).maybe_generate(ctx, _message())

    assert factory.gets == []
    assert ctx.event_publisher.published == []
    assert _failures(reg) == 0


async def test_unregistered_task_type_skips_generation() -> None:
    metrics, reg = _metrics()
    factory = CountingFactory(scripted_model(["unused"]))
    ctx = FakeCtx()

    await _generator(factory, metrics).maybe_generate(ctx, _message(task_type="unknown-type"))

    assert factory.gets == []
    assert ctx.event_publisher.published == []
    assert _failures(reg) == 0


async def test_llm_error_is_counted_and_does_not_raise() -> None:
    metrics, reg = _metrics()

    class RaisingModel:
        async def ainvoke(self, *args: Any, **kwargs: Any) -> Any:
            raise RuntimeError("provider down")

    factory = CountingFactory(RaisingModel())
    ctx = FakeCtx()

    await _generator(factory, metrics).maybe_generate(ctx, _message())

    assert ctx.event_publisher.published == []
    assert _failures(reg) == 1
    assert "title_generation_failed" in ctx.logger.events("warning")


async def test_timeout_is_counted_and_does_not_raise() -> None:
    metrics, reg = _metrics()

    class SlowModel:
        async def ainvoke(self, *args: Any, **kwargs: Any) -> Any:
            await asyncio.sleep(5)

    factory = CountingFactory(SlowModel())
    ctx = FakeCtx()

    await _generator(factory, metrics, timeout_seconds=0.01).maybe_generate(ctx, _message())

    assert ctx.event_publisher.published == []
    assert _failures(reg) == 1


async def test_empty_sanitized_output_suppresses_event_and_counts_failure() -> None:
    metrics, reg = _metrics()
    factory = CountingFactory(scripted_model(["   \n\t  "]))
    ctx = FakeCtx()

    await _generator(factory, metrics).maybe_generate(ctx, _message())

    assert ctx.event_publisher.published == []
    assert _failures(reg) == 1


async def test_publish_failure_is_counted_and_does_not_raise() -> None:
    metrics, reg = _metrics()
    factory = CountingFactory(scripted_model(["A fine title"]))
    ctx = FakeCtx(publisher=FakeEventPublisher(fail=True))

    await _generator(factory, metrics).maybe_generate(ctx, _message())

    assert _failures(reg) == 1


async def test_oversized_quoted_multiline_output_is_sanitized() -> None:
    metrics, _reg = _metrics()
    long_line = '"' + "汉" * 100 + '"\nsecond line'
    factory = CountingFactory(scripted_model([long_line]))
    ctx = FakeCtx()

    await _generator(factory, metrics).maybe_generate(ctx, _message())

    [event] = ctx.event_publisher.published
    title = event["payload"]["title"]
    assert title.endswith("…")
    assert not title.startswith('"')
    assert len(title) <= 64
    assert len(title.encode("utf-8")) <= 200


def test_payload_round_trips_through_json() -> None:
    # 防御性：标题载荷必须可序列化为事件 payload。
    assert json.loads(json.dumps({"title": "重构用户认证模块"})) == {"title": "重构用户认证模块"}
