"""Unit tests for the registry-backed ExecutionDispatcher."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest
from worker.agents.base import AgentSpec
from worker.agents.registry import AgentRegistry
from worker.core.dispatcher import AgentNotImplementedError, ExecutionDispatcher
from worker.plugins.loader import load_plugins


class _RecordingAgent:
    def __init__(self, spec: AgentSpec, *, exc: BaseException | None = None) -> None:
        self._spec = spec
        self._exc = exc
        self.calls: list[tuple[Any, Any]] = []

    @property
    def task_type(self) -> str:
        return self._spec.task_type

    @property
    def spec(self) -> AgentSpec:
        return self._spec

    async def run(self, ctx: Any, message: Any) -> None:
        self.calls.append((ctx, message))
        if self._exc is not None:
            raise self._exc


def _msg(task_type: str) -> Any:
    return type("Msg", (), {"task_type": task_type})()


@pytest.fixture
def prompt_file(tmp_path: Path) -> Path:
    p = tmp_path / "system.md"
    p.write_text("prompt", encoding="utf-8")
    return p


def _registry_with(agent: _RecordingAgent) -> AgentRegistry:
    reg = AgentRegistry(load_plugins())
    reg.register(agent)
    return reg


async def test_unknown_task_type_raises() -> None:
    reg = AgentRegistry(load_plugins())
    dispatcher = ExecutionDispatcher(reg)
    with pytest.raises(AgentNotImplementedError) as info:
        await dispatcher.dispatch(ctx=object(), message=_msg("code-gen"))
    assert info.value.task_type == "code-gen"


async def test_registered_type_runs_agent(prompt_file: Path) -> None:
    agent = _RecordingAgent(
        AgentSpec(task_type="code-gen", model_key="code", system_prompt_path=prompt_file)
    )
    dispatcher = ExecutionDispatcher(_registry_with(agent))
    ctx, message = object(), _msg("code-gen")
    await dispatcher.dispatch(ctx=ctx, message=message)
    assert agent.calls == [(ctx, message)]


async def test_agent_exception_propagates(prompt_file: Path) -> None:
    boom = RuntimeError("kaboom")
    agent = _RecordingAgent(
        AgentSpec(task_type="code-gen", model_key="code", system_prompt_path=prompt_file),
        exc=boom,
    )
    dispatcher = ExecutionDispatcher(_registry_with(agent))
    with pytest.raises(RuntimeError, match="kaboom"):
        await dispatcher.dispatch(ctx=object(), message=_msg("code-gen"))
