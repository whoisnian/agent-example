"""Unit tests for the agent registry + spec validation (design D1)."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest
from worker.agents.base import AgentSpec
from worker.agents.registry import (
    AgentRegistrationError,
    AgentRegistry,
    AgentValidationError,
)
from worker.plugins.loader import load_plugins
from worker.plugins.registry import PluginRegistry


class _StubAgent:
    """Minimal Agent protocol implementation for tests."""

    def __init__(self, spec: AgentSpec) -> None:
        self._spec = spec

    @property
    def task_type(self) -> str:
        return self._spec.task_type

    @property
    def spec(self) -> AgentSpec:
        return self._spec

    async def run(self, ctx: Any, message: Any) -> None:  # pragma: no cover - not run here
        return None


@pytest.fixture
def plugins() -> PluginRegistry:
    # Loads the scaffold's noop tool (name="noop").
    return load_plugins()


@pytest.fixture
def prompt_file(tmp_path: Path) -> Path:
    p = tmp_path / "system.md"
    p.write_text("you are a test agent", encoding="utf-8")
    return p


def test_register_and_get(plugins: PluginRegistry, prompt_file: Path) -> None:
    reg = AgentRegistry(plugins)
    spec = AgentSpec(
        task_type="code-gen",
        model_key="code",
        system_prompt_path=prompt_file,
        tool_names=("noop",),
    )
    reg.register(_StubAgent(spec))
    assert "code-gen" in reg
    assert reg.get("code-gen") is not None
    assert reg.get("code-gen").task_type == "code-gen"


def test_get_unknown_returns_none(plugins: PluginRegistry) -> None:
    reg = AgentRegistry(plugins)
    assert reg.get("research") is None


def test_missing_prompt_file_raises(plugins: PluginRegistry, tmp_path: Path) -> None:
    reg = AgentRegistry(plugins)
    spec = AgentSpec(
        task_type="code-gen",
        model_key="code",
        system_prompt_path=tmp_path / "does-not-exist.md",
    )
    with pytest.raises(AgentValidationError, match="system prompt not found"):
        reg.register(_StubAgent(spec))


def test_unknown_tool_raises(plugins: PluginRegistry, prompt_file: Path) -> None:
    reg = AgentRegistry(plugins)
    spec = AgentSpec(
        task_type="code-gen",
        model_key="code",
        system_prompt_path=prompt_file,
        tool_names=("nonexistent_tool",),
    )
    with pytest.raises(AgentValidationError, match="not a registered plugin"):
        reg.register(_StubAgent(spec))


def test_unknown_subagent_raises(plugins: PluginRegistry, prompt_file: Path) -> None:
    reg = AgentRegistry(plugins)
    spec = AgentSpec(
        task_type="code-gen",
        model_key="code",
        system_prompt_path=prompt_file,
        subagent_names=("nonexistent_subagent",),
    )
    with pytest.raises(AgentValidationError, match="subagent .* not a registered plugin"):
        reg.register(_StubAgent(spec))


def test_duplicate_task_type_raises(plugins: PluginRegistry, prompt_file: Path) -> None:
    reg = AgentRegistry(plugins)
    spec = AgentSpec(task_type="code-gen", model_key="code", system_prompt_path=prompt_file)
    reg.register(_StubAgent(spec))
    with pytest.raises(AgentRegistrationError, match="duplicate agent"):
        reg.register(_StubAgent(spec))


def test_resolve_subagents_happy_path(plugins: PluginRegistry) -> None:
    from worker.agents.subagent import resolve_subagents

    resolved = resolve_subagents(plugins, ("planner", "executor", "critic"))
    assert set(resolved) == {"planner", "executor", "critic"}
    assert resolved["planner"].name == "planner"
    assert resolved["planner"].instruction  # non-empty role prompt


def test_resolve_subagents_unknown_raises(plugins: PluginRegistry) -> None:
    from worker.agents.subagent import resolve_subagents

    with pytest.raises(AgentValidationError, match="subagent 'nonexistent' is not a registered"):
        resolve_subagents(plugins, ("nonexistent",))


def test_build_agent_registry_injects_subagent_plugin_prompts(plugins: PluginRegistry) -> None:
    """build_agent_registry feeds the agents the plugin-sourced role instructions.

    Drift guard at the build boundary: the code-gen agent's `roles` must be a
    freshly built RoleInstructions (NOT the in-code DEFAULT fallback singleton),
    and its planner text must equal the bundled planner subagent's prompt.md.
    Equality alone cannot distinguish source (the default carries the same text),
    so we also assert object identity differs from DEFAULT_ROLE_INSTRUCTIONS.
    """
    from types import SimpleNamespace

    from worker.agents import build_agent_registry
    from worker.agents.base import LoopAgent
    from worker.agents.loop import DEFAULT_ROLE_INSTRUCTIONS
    from worker.plugins.loader import default_plugins_root

    from tests.support.fake_model import FakeModelFactory, scripted_model

    registry = build_agent_registry(
        plugins,
        FakeModelFactory(model=scripted_model([])),
        persistence=SimpleNamespace(),  # unused at build time
        settings=SimpleNamespace(max_step_retries=2),
    )
    agent = registry.get("code-gen")
    assert isinstance(agent, LoopAgent)
    assert agent.roles is not DEFAULT_ROLE_INSTRUCTIONS  # came from the plugins, not the fallback

    planner_prompt = (
        (default_plugins_root() / "subagent" / "planner" / "prompt.md")
        .read_text(encoding="utf-8")
        .strip()
    )
    assert agent.roles.planner == planner_prompt
