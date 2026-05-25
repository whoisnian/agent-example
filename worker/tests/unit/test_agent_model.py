"""Unit tests for the model-factory test seam (design D3)."""

from __future__ import annotations

import asyncio
from typing import Any
from unittest.mock import AsyncMock, MagicMock
from uuid import uuid4

import pytest
import structlog
from worker.agents.model import ModelFactory, ProviderModelFactory, UnknownModelKeyError
from worker.core.cost_meter import CostMeter

from tests.support.fake_model import FakeModelFactory, scripted_model


def _make_ctx() -> Any:
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


def test_fake_factory_is_a_model_factory() -> None:
    factory = FakeModelFactory(model=scripted_model(["hi"]))
    assert isinstance(factory, ModelFactory)


def test_fake_factory_returns_same_model_for_any_key() -> None:
    model = scripted_model(["one", "two"])
    factory = FakeModelFactory(model=model)
    assert factory.get("code") is model
    assert factory.get("research") is model


def test_fake_factory_per_key() -> None:
    code = scripted_model(["c"])
    research = scripted_model(["r"])
    factory = FakeModelFactory(model_by_key={"code": code, "research": research})
    assert factory.get("code") is code
    assert factory.get("research") is research


def test_fake_factory_requires_exactly_one_source() -> None:
    with pytest.raises(ValueError):
        FakeModelFactory()
    with pytest.raises(ValueError):
        FakeModelFactory(model=scripted_model(["x"]), model_by_key={"code": scripted_model(["y"])})


async def test_fake_model_fires_cost_meter_callbacks() -> None:
    """Invoking the scripted model with CostMeter attached emits a cost.llm event."""
    ctx = _make_ctx()
    meter = CostMeter(ctx)
    model = scripted_model(["planned response"])

    result = await model.ainvoke("plan this", config={"callbacks": [meter]})
    assert result.content == "planned response"

    # CostMeter schedules the publish on the loop; let it run.
    await asyncio.sleep(0)
    await asyncio.sleep(0)
    assert ctx.cost_publisher.publish_cost.await_count == 1
    assert ctx.cost_publisher.publish_cost.await_args.kwargs["kind"] == "llm"


def test_provider_factory_unknown_key_raises() -> None:
    factory = ProviderModelFactory(model_by_key={"code": "claude-opus-4-7"})
    with pytest.raises(UnknownModelKeyError):
        factory.get("research")
