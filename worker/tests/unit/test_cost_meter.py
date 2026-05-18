"""Unit tests for cost meter token extraction and emission."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any
from unittest.mock import AsyncMock, MagicMock
from uuid import uuid4

import pytest
import structlog
from worker.core.cost_meter import (
    CostMeter,
    _extract_token_usage,
    cost_metered_tool,
)


def _make_ctx() -> Any:
    """Build a minimal RunContext-shaped stub."""
    ctx = MagicMock()
    ctx.task_id = uuid4()
    ctx.version_id = uuid4()
    ctx.run_id = uuid4()
    ctx.cost_seq_by_kind = {}
    ctx.traceparent = None
    ctx.logger = structlog.get_logger()

    def next_cost_seq(kind: str) -> int:
        ctx.cost_seq_by_kind[kind] = ctx.cost_seq_by_kind.get(kind, 0) + 1
        return ctx.cost_seq_by_kind[kind]

    ctx.next_cost_seq = next_cost_seq
    ctx.cost_publisher = MagicMock()
    ctx.cost_publisher.publish_cost = AsyncMock()
    return ctx


def test_extract_openai_shape() -> None:
    response = SimpleNamespace(
        llm_output={"token_usage": {"prompt_tokens": 12, "completion_tokens": 8}},
        generations=None,
    )
    usage = _extract_token_usage(response)
    assert usage.input_tokens == 12
    assert usage.output_tokens == 8
    assert usage.cached_tokens is None


def test_extract_anthropic_shape() -> None:
    gen = SimpleNamespace(
        generation_info={
            "usage": {"input_tokens": 30, "output_tokens": 5, "cache_read_input_tokens": 2}
        }
    )
    response = SimpleNamespace(llm_output={}, generations=[[gen]])
    usage = _extract_token_usage(response)
    assert usage.input_tokens == 30
    assert usage.output_tokens == 5
    assert usage.cached_tokens == 2


def test_extract_missing_returns_nones() -> None:
    response = SimpleNamespace(llm_output={}, generations=None)
    usage = _extract_token_usage(response)
    assert usage.input_tokens is None
    assert usage.output_tokens is None


async def test_cost_meter_emits_on_llm_end() -> None:
    ctx = _make_ctx()
    meter = CostMeter(ctx)
    run_id = uuid4()
    meter.on_llm_start(
        {"name": "ChatAnthropic"},
        ["hello"],
        run_id=run_id,
        invocation_params={"model_name": "claude-opus-4-7"},
    )
    response = SimpleNamespace(
        llm_output={"token_usage": {"prompt_tokens": 10, "completion_tokens": 3}},
        generations=None,
    )
    meter.on_llm_end(response, run_id=run_id)
    # The spawn pathway schedules the coroutine; give the loop a tick.
    import asyncio

    await asyncio.sleep(0)
    await asyncio.sleep(0)
    assert ctx.cost_publisher.publish_cost.await_count == 1
    call_kwargs = ctx.cost_publisher.publish_cost.await_args.kwargs
    assert call_kwargs["kind"] == "llm"
    assert call_kwargs["resource_name"] == "claude-opus-4-7"
    assert call_kwargs["input_tokens"] == 10
    assert call_kwargs["output_tokens"] == 3


async def test_cost_meter_emit_failure_does_not_propagate() -> None:
    ctx = _make_ctx()
    ctx.cost_publisher.publish_cost = AsyncMock(side_effect=ConnectionError("down"))
    meter = CostMeter(ctx)
    run_id = uuid4()
    meter.on_llm_start({"name": "x"}, [], run_id=run_id, invocation_params={"model_name": "m"})
    response = SimpleNamespace(llm_output={}, generations=None)
    # Must not raise.
    meter.on_llm_end(response, run_id=run_id)
    import asyncio

    await asyncio.sleep(0)


async def test_tool_decorator_emits_after_call() -> None:
    ctx = _make_ctx()
    meter = CostMeter(ctx)
    ctx.cost_meter = meter

    @cost_metered_tool("widget_search")
    async def widget_search(ctx_in: Any, query: str) -> str:
        return f"result for {query}"

    result = await widget_search(ctx, "hello")
    assert result == "result for hello"
    assert ctx.cost_publisher.publish_cost.await_count == 1
    call_kwargs = ctx.cost_publisher.publish_cost.await_args.kwargs
    assert call_kwargs["kind"] == "tool"
    assert call_kwargs["resource_name"] == "widget_search"
    assert call_kwargs["calls"] == 1


async def test_tool_decorator_emits_on_exception() -> None:
    ctx = _make_ctx()
    meter = CostMeter(ctx)
    ctx.cost_meter = meter

    @cost_metered_tool("flaky")
    async def flaky(ctx_in: Any) -> None:
        raise RuntimeError("boom")

    with pytest.raises(RuntimeError):
        await flaky(ctx)
    assert ctx.cost_publisher.publish_cost.await_count == 1
